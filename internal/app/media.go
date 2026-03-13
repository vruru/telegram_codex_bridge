package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/telegram"
	"telegram-codex-bridge/internal/transcribe"
)

const (
	maxReturnedArtifacts = 8
	maxReturnedFileSize  = 45 << 20
)

type preparedAttachment struct {
	Kind         telegram.MediaKind
	LocalPath    string
	RelativePath string
	MIMEType     string
	Transcript   string
}

type generatedArtifact struct {
	SendAs       string
	Path         string
	RelativePath string
	TooLarge     bool
}

type workspaceFileSnapshot map[string]workspaceFileInfo

type workspaceFileInfo struct {
	Size    int64
	ModTime time.Time
}

var markdownLocalLinkPattern = regexp.MustCompile(`\[(?P<label>[^\]]+)\]\((?P<path>/[^)\s]+)\)`)
var bareLocalPathPattern = regexp.MustCompile(`(?m)(^|[\s(])(?P<path>/Users/[^\s)]+)`)

func (a *App) prepareIncomingUpdate(ctx context.Context, msg telegram.IncomingUpdate) (telegram.IncomingUpdate, error) {
	if strings.TrimSpace(msg.PreparedPrompt) != "" && strings.TrimSpace(msg.PreparedWorkspace) != "" {
		return msg, nil
	}

	workspace, err := a.workspaceForUpdate(ctx, msg)
	if err != nil {
		return msg, err
	}

	msg.PreparedWorkspace = workspace
	msg.PreparedPrompt = strings.TrimSpace(msg.Text)
	msg.PreparedImagePaths = nil

	if len(msg.Media) == 0 {
		return msg, nil
	}

	attachments, err := a.downloadIncomingMedia(ctx, msg, workspace)
	if err != nil {
		return msg, err
	}

	imagePaths := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachmentKindIsImage(attachment.Kind, attachment.LocalPath, attachment.MIMEType) {
			imagePaths = append(imagePaths, attachment.LocalPath)
		}
	}

	msg.PreparedImagePaths = imagePaths
	msg.PreparedPrompt = buildAttachmentPrompt(strings.TrimSpace(msg.Text), attachments)
	return msg, nil
}

func (a *App) workspaceForUpdate(ctx context.Context, msg telegram.IncomingUpdate) (string, error) {
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return "", fmt.Errorf("lookup topic binding for workspace: %w", err)
	}
	if found && strings.TrimSpace(binding.Workspace) != "" {
		if err := os.MkdirAll(binding.Workspace, 0o755); err != nil {
			return "", fmt.Errorf("ensure topic workspace %s: %w", binding.Workspace, err)
		}
		return binding.Workspace, nil
	}
	return a.resolveTopicWorkspace(ctx, msg)
}

func (a *App) downloadIncomingMedia(ctx context.Context, msg telegram.IncomingUpdate, workspace string) ([]preparedAttachment, error) {
	dir := filepath.Join(workspace, ".telegram", "inbox", fmt.Sprintf("%d", msg.MessageID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create media inbox: %w", err)
	}

	attachments := make([]preparedAttachment, 0, len(msg.Media))
	for index, media := range msg.Media {
		localPath, err := a.downloadSingleMedia(ctx, media, dir, index)
		if err != nil {
			return nil, err
		}

		relativePath, relErr := filepath.Rel(workspace, localPath)
		if relErr != nil {
			relativePath = filepath.Base(localPath)
		}

		attachment := preparedAttachment{
			Kind:         media.Kind,
			LocalPath:    localPath,
			RelativePath: filepath.ToSlash(relativePath),
			MIMEType:     strings.TrimSpace(media.MIMEType),
		}
		if transcript, transcriptErr := a.transcribeAudioAttachment(ctx, msg, attachment, workspace); transcriptErr != nil {
			a.logger.Printf("local Whisper transcription unavailable for %s: %v", localPath, transcriptErr)
		} else {
			attachment.Transcript = transcript
		}

		attachments = append(attachments, attachment)
	}

	return attachments, nil
}

func (a *App) downloadSingleMedia(ctx context.Context, media telegram.IncomingMedia, dir string, index int) (string, error) {
	fileName := sanitizedIncomingFileName(media, index)
	destPath := filepath.Join(dir, fileName)
	if _, err := a.bot.DownloadFile(ctx, media.FileID, destPath); err != nil {
		return "", fmt.Errorf("download telegram %s: %w", media.Kind, err)
	}
	return destPath, nil
}

func buildAttachmentPrompt(originalText string, attachments []preparedAttachment) string {
	lines := []string{
		"The user sent attachment(s) for this turn.",
	}
	if strings.TrimSpace(originalText) != "" {
		lines = append(lines, "", "User caption or message:", originalText)
	} else {
		lines = append(lines, "", "No additional caption was provided.")
	}

	lines = append(lines, "", "Saved attachments:")
	for idx, attachment := range attachments {
		lines = append(lines, fmt.Sprintf("%d. %s: %s", idx+1, attachmentLabel(attachment), attachment.RelativePath))
	}
	if transcriptLines := attachmentTranscriptLines(attachments); len(transcriptLines) > 0 {
		lines = append(lines, "", "Local Whisper transcripts:")
		lines = append(lines, transcriptLines...)
	}

	lines = append(lines, "", "Use these saved files as part of your answer. Image files are also attached as image inputs whenever possible.")
	return strings.Join(lines, "\n")
}

func attachmentTranscriptLines(attachments []preparedAttachment) []string {
	lines := make([]string, 0)
	index := 0
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Transcript) == "" {
			continue
		}
		index++
		lines = append(lines, fmt.Sprintf("%d. %s transcript: %s", index, attachmentLabel(attachment), truncateTranscriptForPrompt(attachment.Transcript)))
	}
	return lines
}

func truncateTranscriptForPrompt(text string) string {
	text = strings.TrimSpace(text)
	const maxTranscriptChars = 6000
	if len(text) <= maxTranscriptChars {
		return text
	}
	return strings.TrimSpace(text[:maxTranscriptChars]) + " ..."
}

func attachmentLabel(attachment preparedAttachment) string {
	switch attachment.Kind {
	case telegram.MediaKindPhoto:
		return "Photo"
	case telegram.MediaKindVoice:
		return "Voice note"
	case telegram.MediaKindAudio:
		return "Audio file"
	default:
		if attachmentKindIsImage(attachment.Kind, attachment.LocalPath, attachment.MIMEType) {
			return "Image document"
		}
		return "Document"
	}
}

func sanitizedIncomingFileName(media telegram.IncomingMedia, index int) string {
	name := sanitizeWorkspaceName(strings.TrimSuffix(strings.TrimSpace(media.FileName), filepath.Ext(strings.TrimSpace(media.FileName))))
	if name == "" {
		name = fmt.Sprintf("%s-%d", media.Kind, index+1)
	}

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(media.FileName)))
	if ext == "" {
		ext = defaultExtensionForMedia(media)
	}
	if ext == "" {
		ext = ".bin"
	}
	return name + ext
}

func defaultExtensionForMedia(media telegram.IncomingMedia) string {
	switch media.Kind {
	case telegram.MediaKindPhoto:
		return ".jpg"
	case telegram.MediaKindVoice:
		return ".ogg"
	case telegram.MediaKindAudio:
		if strings.Contains(strings.ToLower(media.MIMEType), "mpeg") {
			return ".mp3"
		}
		if strings.Contains(strings.ToLower(media.MIMEType), "wav") {
			return ".wav"
		}
		return ".m4a"
	case telegram.MediaKindDocument:
		if strings.Contains(strings.ToLower(media.MIMEType), "pdf") {
			return ".pdf"
		}
	}
	return ""
}

func attachmentKindIsImage(kind telegram.MediaKind, path, mimeType string) bool {
	if kind == telegram.MediaKindPhoto {
		return true
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func (a *App) transcribeAudioAttachment(
	ctx context.Context,
	msg telegram.IncomingUpdate,
	attachment preparedAttachment,
	workspace string,
) (string, error) {
	switch attachment.Kind {
	case telegram.MediaKindVoice, telegram.MediaKindAudio:
	default:
		return "", nil
	}

	transcriptsDir := filepath.Join(workspace, ".telegram", "transcripts", fmt.Sprintf("%d", msg.MessageID))
	result, err := transcribe.Transcribe(ctx, a.runtimeProjectRoot(), attachment.LocalPath, transcriptsDir, msg.LanguageCode)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

func (a *App) runtimeProjectRoot() string {
	if strings.TrimSpace(a.envPath) != "" {
		return filepath.Dir(a.envPath)
	}
	return ""
}

func snapshotWorkspaceFiles(root string) (workspaceFileSnapshot, error) {
	snapshot := make(workspaceFileSnapshot)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshot[path] = workspaceFileInfo{
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot workspace %s: %w", root, err)
	}
	return snapshot, nil
}

func detectGeneratedArtifacts(root string, before workspaceFileSnapshot) ([]generatedArtifact, []string, error) {
	after, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return nil, nil, err
	}

	artifacts := make([]generatedArtifact, 0)
	skipped := make([]string, 0)
	for path, info := range after {
		beforeInfo, existed := before[path]
		if existed && beforeInfo.Size == info.Size && beforeInfo.ModTime.Equal(info.ModTime) {
			continue
		}

		artifact, ok := classifyGeneratedArtifact(root, path, info.Size)
		if !ok {
			continue
		}
		if artifact.TooLarge {
			skipped = append(skipped, artifact.RelativePath)
			continue
		}
		artifacts = append(artifacts, artifact)
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].RelativePath < artifacts[j].RelativePath
	})
	if len(artifacts) > maxReturnedArtifacts {
		for _, artifact := range artifacts[maxReturnedArtifacts:] {
			skipped = append(skipped, artifact.RelativePath)
		}
		artifacts = artifacts[:maxReturnedArtifacts]
	}
	return artifacts, skipped, nil
}

func classifyGeneratedArtifact(root, path string, size int64) (generatedArtifact, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return generatedArtifact{}, false
	}
	rel = filepath.ToSlash(rel)
	ext := strings.ToLower(filepath.Ext(path))
	sendAs := ""
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		sendAs = "photo"
	case ".ogg", ".opus":
		sendAs = "voice"
	case ".mp3", ".m4a", ".wav", ".aac", ".flac", ".oga":
		sendAs = "audio"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".md", ".csv", ".tsv", ".json", ".zip", ".7z", ".rar":
		sendAs = "document"
	default:
		return generatedArtifact{}, false
	}

	return generatedArtifact{
		SendAs:       sendAs,
		Path:         path,
		RelativePath: rel,
		TooLarge:     size > maxReturnedFileSize,
	}, true
}

func mergeGeneratedArtifacts(sets ...[]generatedArtifact) []generatedArtifact {
	byPath := make(map[string]generatedArtifact)
	for _, set := range sets {
		for _, artifact := range set {
			byPath[artifact.Path] = artifact
		}
	}

	merged := make([]generatedArtifact, 0, len(byPath))
	for _, artifact := range byPath {
		merged = append(merged, artifact)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].RelativePath < merged[j].RelativePath
	})
	return merged
}

func mergeGeneratedArtifactsWithReply(root, reply string, sets ...[]generatedArtifact) []generatedArtifact {
	merged := mergeGeneratedArtifacts(sets...)
	replyArtifacts := generatedArtifactsFromReply(root, reply)
	if len(replyArtifacts) == 0 {
		return merged
	}
	return mergeGeneratedArtifacts(merged, replyArtifacts)
}

func generatedArtifactsFromReply(root, reply string) []generatedArtifact {
	reply = strings.TrimSpace(reply)
	root = strings.TrimSpace(root)
	if reply == "" || root == "" {
		return nil
	}

	seen := make(map[string]generatedArtifact)
	addPath := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || !filepath.IsAbs(candidate) {
			return
		}

		clean := filepath.Clean(candidate)
		relative, err := filepath.Rel(root, clean)
		if err != nil || strings.HasPrefix(relative, "..") {
			return
		}

		info, err := os.Stat(clean)
		if err != nil || info.IsDir() {
			return
		}

		artifact, ok := classifyGeneratedArtifact(root, clean, info.Size())
		if !ok || artifact.TooLarge {
			return
		}
		seen[artifact.Path] = artifact
	}

	for _, match := range markdownLocalLinkPattern.FindAllStringSubmatch(reply, -1) {
		for i, name := range markdownLocalLinkPattern.SubexpNames() {
			if name == "path" {
				addPath(match[i])
				break
			}
		}
	}

	for _, match := range bareLocalPathPattern.FindAllStringSubmatch(reply, -1) {
		for i, name := range bareLocalPathPattern.SubexpNames() {
			if name == "path" {
				addPath(match[i])
				break
			}
		}
	}

	artifacts := make([]generatedArtifact, 0, len(seen))
	for _, artifact := range seen {
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].RelativePath < artifacts[j].RelativePath
	})
	return artifacts
}

func sanitizeReplyLocalPaths(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return reply
	}

	reply = markdownLocalLinkPattern.ReplaceAllStringFunc(reply, func(match string) string {
		submatch := markdownLocalLinkPattern.FindStringSubmatch(match)
		label := ""
		path := ""
		for i, name := range markdownLocalLinkPattern.SubexpNames() {
			switch name {
			case "label":
				label = submatch[i]
			case "path":
				path = submatch[i]
			}
		}
		label = strings.TrimSpace(label)
		if label != "" && filepath.IsAbs(path) {
			return label
		}
		return match
	})

	reply = bareLocalPathPattern.ReplaceAllStringFunc(reply, func(match string) string {
		submatch := bareLocalPathPattern.FindStringSubmatch(match)
		path := ""
		for i, name := range bareLocalPathPattern.SubexpNames() {
			if name == "path" {
				path = submatch[i]
				break
			}
		}
		if path == "" {
			return match
		}
		prefix := strings.TrimSuffix(match, path)
		return prefix + filepath.Base(path)
	})

	return strings.TrimSpace(reply)
}

func (a *App) sendGeneratedArtifacts(ctx context.Context, msg telegram.IncomingUpdate, artifacts []generatedArtifact, skipped []string) {
	if len(artifacts) == 0 && len(skipped) == 0 {
		return
	}

	loc := a.catalogForMessage(ctx, msg)
	if len(artifacts) > 0 {
		a.debugf("sending %d generated artifact(s) chat=%d topic=%d", len(artifacts), msg.ChatID, msg.TopicID)
	}
	for _, artifact := range artifacts {
		caption := artifactCaption(loc, artifact)
		outgoing := telegram.OutgoingFile{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			FilePath:         artifact.Path,
			Caption:          caption,
		}

		var err error
		switch artifact.SendAs {
		case "photo":
			err = a.bot.SendPhotoFile(ctx, outgoing)
			if err != nil {
				a.logger.Printf("sendPhoto failed for %s, falling back to document: %v", artifact.Path, err)
				err = a.bot.SendDocumentFile(ctx, outgoing)
			}
		case "voice":
			err = a.bot.SendVoiceFile(ctx, outgoing)
			if err != nil {
				a.logger.Printf("sendVoice failed for %s, falling back to audio: %v", artifact.Path, err)
				err = a.bot.SendAudioFile(ctx, outgoing)
			}
		case "audio":
			err = a.bot.SendAudioFile(ctx, outgoing)
		default:
			err = a.bot.SendDocumentFile(ctx, outgoing)
		}
		if err != nil {
			a.logger.Printf("send generated artifact %s: %v", artifact.Path, err)
		}
	}

	if len(skipped) > 0 {
		lines := []string{
			loc.T("有些生成的附件没有自动回传：", "Some generated attachments were not sent back automatically:"),
		}
		for _, rel := range skipped {
			lines = append(lines, "- "+rel)
		}
		_ = a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             strings.Join(lines, "\n"),
		})
	}
}

func artifactCaption(loc i18n.Catalog, artifact generatedArtifact) string {
	switch artifact.SendAs {
	case "photo":
		return ""
	case "voice", "audio":
		return fmt.Sprintf("%s: %s", loc.T("Codex 输出音频", "Codex output audio"), artifact.RelativePath)
	default:
		return fmt.Sprintf("%s: %s", loc.T("Codex 输出文件", "Codex output file"), artifact.RelativePath)
	}
}
