package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAttachmentPromptListsAttachments(t *testing.T) {
	t.Parallel()

	prompt := buildAttachmentPrompt("请看这两个附件", []preparedAttachment{
		{
			Kind:         "photo",
			RelativePath: ".telegram/inbox/100/photo.jpg",
		},
		{
			Kind:         "voice",
			RelativePath: ".telegram/inbox/100/voice.ogg",
		},
	})

	for _, want := range []string{
		"The user sent attachment(s) for this turn.",
		"User caption or message:",
		"请看这两个附件",
		"1. Photo: .telegram/inbox/100/photo.jpg",
		"2. Voice note: .telegram/inbox/100/voice.ogg",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildAttachmentPromptIncludesWhisperTranscript(t *testing.T) {
	t.Parallel()

	prompt := buildAttachmentPrompt("", []preparedAttachment{
		{
			Kind:         "voice",
			RelativePath: ".telegram/inbox/101/voice.ogg",
			Transcript:   "请帮我总结这个仓库的结构。",
		},
	})

	for _, want := range []string{
		"Local Whisper transcripts:",
		"1. Voice note transcript: 请帮我总结这个仓库的结构。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDetectGeneratedArtifactsFiltersShareableOutputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}

	files := []string{
		filepath.Join(root, "render.png"),
		filepath.Join(root, "speech.mp3"),
		filepath.Join(root, "report.pdf"),
		filepath.Join(root, "main.go"),
		filepath.Join(root, ".telegram", "inbox", "1", "photo.jpg"),
	}
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	artifacts, skipped, err := detectGeneratedArtifacts(root, before)
	if err != nil {
		t.Fatalf("detect artifacts: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped artifacts, got %v", skipped)
	}
	if len(artifacts) != 4 {
		t.Fatalf("expected 4 artifacts, got %d (%v)", len(artifacts), artifacts)
	}

	got := map[string]string{}
	for _, artifact := range artifacts {
		got[artifact.RelativePath] = artifact.SendAs
	}

	want := map[string]string{
		"render.png":                  "photo",
		"speech.mp3":                  "audio",
		"report.pdf":                  "document",
		".telegram/inbox/1/photo.jpg": "photo",
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Fatalf("expected %s => %s, got %q", path, kind, got[path])
		}
	}
	if _, ok := got["main.go"]; ok {
		t.Fatalf("source file should not be treated as artifact")
	}
}

func TestDetectGeneratedArtifactsFindsModifiedInboxAttachment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".telegram", "inbox", "66", "photo_italy.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatalf("write before file: %v", err)
	}

	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}

	if err := os.WriteFile(path, []byte("after-change"), 0o644); err != nil {
		t.Fatalf("write after file: %v", err)
	}

	artifacts, skipped, err := detectGeneratedArtifacts(root, before)
	if err != nil {
		t.Fatalf("detect artifacts: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped artifacts, got %v", skipped)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d (%v)", len(artifacts), artifacts)
	}
	if artifacts[0].RelativePath != ".telegram/inbox/66/photo_italy.png" {
		t.Fatalf("unexpected artifact path: %s", artifacts[0].RelativePath)
	}
	if artifacts[0].SendAs != "photo" {
		t.Fatalf("unexpected artifact send type: %s", artifacts[0].SendAs)
	}
}

func TestGeneratedArtifactsFromReplyFindsLocalLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	imagePath := filepath.Join(root, ".telegram", "inbox", "66", "photo_italy_v5.jpg")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", imagePath, err)
	}
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatalf("write %s: %v", imagePath, err)
	}

	reply := "文件在 [photo_italy_v5.jpg](" + imagePath + ")。"
	artifacts := generatedArtifactsFromReply(root, reply)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d (%v)", len(artifacts), artifacts)
	}
	if artifacts[0].RelativePath != ".telegram/inbox/66/photo_italy_v5.jpg" {
		t.Fatalf("unexpected artifact path: %s", artifacts[0].RelativePath)
	}
	if artifacts[0].SendAs != "photo" {
		t.Fatalf("unexpected artifact type: %s", artifacts[0].SendAs)
	}
}

func TestSanitizeReplyLocalPathsStripsAbsolutePaths(t *testing.T) {
	t.Parallel()

	reply := "文件在 [photo_italy_v5.jpg](/Users/example/telegram_codex_bridge/private/.telegram/inbox/66/photo_italy_v5.jpg)。\n另一个路径是 /Users/example/telegram_codex_bridge/private/render.png"
	got := sanitizeReplyLocalPaths(reply)

	if strings.Contains(got, "/Users/") {
		t.Fatalf("expected local paths to be removed, got %q", got)
	}
	for _, want := range []string{"photo_italy_v5.jpg", "render.png"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in sanitized reply, got %q", want, got)
		}
	}
}
