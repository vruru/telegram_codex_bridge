package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"telegram-codex-bridge/internal/config"
)

type UpdateKind string

const (
	UpdateKindMessage              UpdateKind = "message"
	UpdateKindCallbackQuery        UpdateKind = "callback_query"
	UpdateKindTopicCreated         UpdateKind = "topic_created"
	UpdateKindTopicEdited          UpdateKind = "topic_edited"
	UpdateKindTopicClosed          UpdateKind = "topic_closed"
	UpdateKindTopicReopened        UpdateKind = "topic_reopened"
	UpdateKindGeneralTopicHidden   UpdateKind = "general_topic_hidden"
	UpdateKindGeneralTopicUnhidden UpdateKind = "general_topic_unhidden"
)

type IncomingUpdate struct {
	Kind               UpdateKind
	ChatID             int64
	TopicID            int64
	UserID             int64
	Username           string
	LanguageCode       string
	Text               string
	MessageID          int64
	ChatTitle          string
	TopicTitle         string
	CallbackID         string
	CallbackData       string
	Media              []IncomingMedia
	PreparedPrompt     string
	PreparedImagePaths []string
	PreparedWorkspace  string
	nextOffset         int64
}

type Handler func(context.Context, IncomingUpdate) error

type OffsetStore interface {
	LoadUpdateOffset(ctx context.Context) (int64, error)
	SaveUpdateOffset(ctx context.Context, offset int64) error
}

type Bot struct {
	cfg         config.TelegramConfig
	baseURL     string
	fileBaseURL string
	httpClient  *http.Client
	offsets     OffsetStore
	topicsMu    sync.RWMutex
	topicNames  map[string]string
}

type MediaKind string

const (
	MediaKindPhoto    MediaKind = "photo"
	MediaKindDocument MediaKind = "document"
	MediaKindVoice    MediaKind = "voice"
	MediaKindAudio    MediaKind = "audio"
)

type IncomingMedia struct {
	Kind         MediaKind
	FileID       string
	FileUniqueID string
	FileName     string
	MIMEType     string
	FileSize     int64
	Width        int
	Height       int
	Duration     int
}

type TelegramFile struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size"`
	FilePath     string `json:"file_path"`
}

type BotProfile struct {
	ID                      int64
	Username                string
	CanReadAllGroupMessages bool
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type APIError struct {
	Method      string
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s failed: %s (retry after %s)", e.Method, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("%s failed: %s", e.Method, e.Description)
}

func NewBot(cfg config.TelegramConfig, offsets OffsetStore) *Bot {
	apiBaseURL := strings.TrimRight(cfg.APIBaseURL, "/")
	return &Bot{
		cfg:         cfg,
		baseURL:     fmt.Sprintf("%s/bot%s", apiBaseURL, cfg.BotToken),
		fileBaseURL: fmt.Sprintf("%s/file/bot%s", apiBaseURL, cfg.BotToken),
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.PollTimeoutSeconds+10) * time.Second,
		},
		offsets:    offsets,
		topicNames: make(map[string]string),
	}
}

func (b *Bot) Run(ctx context.Context, handler Handler) error {
	offset, err := b.loadStoredOffset()
	if err != nil {
		return fmt.Errorf("load telegram update offset: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		messages, nextOffset, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}

		lastSavedOffset := offset
		for _, message := range messages {
			if err := handler(ctx, message); err != nil {
				return err
			}

			if message.nextOffset > offset {
				offset = message.nextOffset
			}
			if offset > lastSavedOffset {
				if err := b.saveStoredOffset(offset); err == nil {
					lastSavedOffset = offset
				}
			}
		}

		if nextOffset > offset {
			offset = nextOffset
		}
		if offset > lastSavedOffset {
			if err := b.saveStoredOffset(offset); err != nil {
				// Offset persistence should not take the whole bridge down.
				// We'll keep processing with the in-memory offset and retry next loop.
				continue
			}
		}
	}
}

func (b *Bot) loadStoredOffset() (int64, error) {
	if b.offsets == nil {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return b.offsets.LoadUpdateOffset(ctx)
}

func (b *Bot) saveStoredOffset(offset int64) error {
	if b.offsets == nil || offset == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return b.offsets.SaveUpdateOffset(ctx, offset)
}

func (b *Bot) SyncCommands(ctx context.Context) error {
	defaultCommands := []BotCommand{
		{Command: "help", Description: "Show help"},
		{Command: "where", Description: "Show chat/topic/user identifiers"},
		{Command: "version", Description: "Show the current bridge version"},
		{Command: "status", Description: "Show the current Codex thread status"},
		{Command: "limit", Description: "Show the current Codex quota"},
		{Command: "lang", Description: "Show or set the language"},
		{Command: "model", Description: "Show or set the model for this topic"},
		{Command: "think", Description: "Show or set the reasoning level"},
		{Command: "speed", Description: "Show or set the speed mode"},
		{Command: "permission", Description: "Show or set Codex permissions"},
		{Command: "threads", Description: "List bound threads in this chat"},
		{Command: "new", Description: "Start a new thread for this topic"},
		{Command: "archive", Description: "Archive the current topic binding"},
		{Command: "delete", Description: "Delete the current forum topic"},
	}
	zhCommands := []BotCommand{
		{Command: "help", Description: "查看帮助"},
		{Command: "where", Description: "查看 chat/topic/user 标识"},
		{Command: "version", Description: "查看当前 Bridge 版本"},
		{Command: "status", Description: "查看当前 Codex 线程状态"},
		{Command: "limit", Description: "查看当前 Codex 限额"},
		{Command: "lang", Description: "查看或设置当前语言"},
		{Command: "model", Description: "查看或设置当前话题模型"},
		{Command: "think", Description: "查看或设置思考等级"},
		{Command: "speed", Description: "查看或设置速度模式"},
		{Command: "permission", Description: "查看或设置 Codex 权限"},
		{Command: "threads", Description: "查看当前 chat 的绑定线程"},
		{Command: "new", Description: "为当前话题新建线程"},
		{Command: "archive", Description: "归档当前话题绑定"},
		{Command: "delete", Description: "删除当前 forum 话题"},
	}

	if err := b.setCommands(ctx, defaultCommands, ""); err != nil {
		return err
	}
	return b.setCommands(ctx, zhCommands, "zh")
}

func (b *Bot) GetProfile(ctx context.Context) (BotProfile, error) {
	var result struct {
		ID                      int64  `json:"id"`
		Username                string `json:"username"`
		CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
	}

	if err := b.call(ctx, "getMe", nil, &result); err != nil {
		return BotProfile{}, err
	}

	return BotProfile{
		ID:                      result.ID,
		Username:                result.Username,
		CanReadAllGroupMessages: result.CanReadAllGroupMessages,
	}, nil
}

type OutgoingMessage struct {
	ChatID           int64
	TopicID          int64
	ReplyToMessageID int64
	Text             string
	InlineKeyboard   [][]InlineButton
}

type SentMessage struct {
	MessageID int64
}

type OutgoingFile struct {
	ChatID           int64
	TopicID          int64
	ReplyToMessageID int64
	FilePath         string
	Caption          string
}

type EditMessage struct {
	ChatID         int64
	MessageID      int64
	Text           string
	InlineKeyboard [][]InlineButton
}

type ChatAction struct {
	ChatID  int64
	TopicID int64
	Action  string
}

type InlineButton struct {
	Text         string
	CallbackData string
}

func (b *Bot) SendMessage(ctx context.Context, msg OutgoingMessage) error {
	_, err := b.SendMessageDetailed(ctx, msg)
	return err
}

func (b *Bot) SendMessageDetailed(ctx context.Context, msg OutgoingMessage) (SentMessage, error) {
	chunks := SplitTextForTelegram(msg.Text)
	if len(chunks) == 0 {
		return SentMessage{}, nil
	}

	var first SentMessage
	for i, chunk := range chunks {
		request := sendMessageRequest{
			ChatID:           msg.ChatID,
			Text:             chunk,
			ReplyToMessageID: msg.ReplyToMessageID,
		}
		if msg.TopicID > 0 {
			request.MessageThreadID = msg.TopicID
		}
		if len(msg.InlineKeyboard) > 0 && i == len(chunks)-1 {
			request.ReplyMarkup = inlineKeyboardMarkup{InlineKeyboard: toTelegramInlineKeyboard(msg.InlineKeyboard)}
		}

		var response telegramSentMessage
		if err := b.call(ctx, "sendMessage", request, &response); err != nil {
			return SentMessage{}, err
		}
		if first.MessageID == 0 {
			first.MessageID = response.MessageID
		}
	}

	return first, nil
}

func (b *Bot) EditMessageText(ctx context.Context, msg EditMessage) error {
	chunks := SplitTextForTelegram(msg.Text)
	if len(chunks) == 0 || msg.MessageID == 0 {
		return nil
	}

	err := b.call(ctx, "editMessageText", editMessageTextRequest{
		ChatID:      msg.ChatID,
		MessageID:   msg.MessageID,
		Text:        chunks[0],
		ReplyMarkup: optionalInlineKeyboard(msg.InlineKeyboard),
	}, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	if chatID == 0 || messageID == 0 {
		return nil
	}
	return b.call(ctx, "deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}, nil)
}

func (b *Bot) SendChatAction(ctx context.Context, chatID, topicID int64, action string) error {
	action = strings.TrimSpace(action)
	if chatID == 0 || action == "" {
		return nil
	}

	request := chatActionRequest{
		ChatID: chatID,
		Action: action,
	}
	if topicID > 0 {
		request.MessageThreadID = topicID
	}
	return b.call(ctx, "sendChatAction", request, nil)
}

func (b *Bot) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	callbackID = strings.TrimSpace(callbackID)
	if callbackID == "" {
		return nil
	}

	request := map[string]any{
		"callback_query_id": callbackID,
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		request["text"] = truncateForTelegram(trimmed)
	}
	return b.call(ctx, "answerCallbackQuery", request, nil)
}

func (b *Bot) GetFile(ctx context.Context, fileID string) (TelegramFile, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return TelegramFile{}, fmt.Errorf("file id is required")
	}

	var result TelegramFile
	if err := b.call(ctx, "getFile", map[string]any{"file_id": fileID}, &result); err != nil {
		return TelegramFile{}, err
	}
	return result, nil
}

func (b *Bot) DownloadFile(ctx context.Context, fileID, destPath string) (TelegramFile, error) {
	fileInfo, err := b.GetFile(ctx, fileID)
	if err != nil {
		return TelegramFile{}, err
	}
	if strings.TrimSpace(fileInfo.FilePath) == "" {
		return TelegramFile{}, fmt.Errorf("telegram did not return a file path for %s", fileID)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return TelegramFile{}, fmt.Errorf("create download directory: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.fileBaseURL+"/"+strings.TrimLeft(fileInfo.FilePath, "/"), nil)
	if err != nil {
		return TelegramFile{}, fmt.Errorf("build download request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return TelegramFile{}, fmt.Errorf("download telegram file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TelegramFile{}, fmt.Errorf("download telegram file failed: http %d", resp.StatusCode)
	}

	file, err := os.Create(destPath)
	if err != nil {
		return TelegramFile{}, fmt.Errorf("create destination file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return TelegramFile{}, fmt.Errorf("write destination file: %w", err)
	}

	return fileInfo, nil
}

func (b *Bot) SendPhotoFile(ctx context.Context, msg OutgoingFile) error {
	return b.sendFileUpload(ctx, "sendPhoto", "photo", msg)
}

func (b *Bot) SendDocumentFile(ctx context.Context, msg OutgoingFile) error {
	return b.sendFileUpload(ctx, "sendDocument", "document", msg)
}

func (b *Bot) SendAudioFile(ctx context.Context, msg OutgoingFile) error {
	return b.sendFileUpload(ctx, "sendAudio", "audio", msg)
}

func (b *Bot) SendVoiceFile(ctx context.Context, msg OutgoingFile) error {
	return b.sendFileUpload(ctx, "sendVoice", "voice", msg)
}

func (b *Bot) DeleteForumTopic(ctx context.Context, chatID, topicID int64) error {
	if topicID == 0 {
		return fmt.Errorf("delete forum topic requires a non-zero topic id")
	}

	return b.call(ctx, "deleteForumTopic", topicControlRequest{
		ChatID:          chatID,
		MessageThreadID: topicID,
	}, nil)
}

func (b *Bot) IsAllowedUser(userID int64) bool {
	if len(b.cfg.AllowedUserIDs) > 0 && !containsID(b.cfg.AllowedUserIDs, userID) {
		return false
	}
	return true
}

func (b *Bot) IsAllowedChat(chatID int64) bool {
	if chatID > 0 {
		// Private chats are authorized by user allowlist; group allowlist only applies to shared chats.
		return true
	}

	if len(b.cfg.AllowedChatIDs) > 0 && !containsID(b.cfg.AllowedChatIDs, chatID) {
		return false
	}
	return true
}

func containsID(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]IncomingUpdate, int64, error) {
	request := getUpdatesRequest{
		Offset:         offset,
		Timeout:        b.cfg.PollTimeoutSeconds,
		AllowedUpdates: []string{"message", "callback_query"},
	}

	var updates []update
	if err := b.call(ctx, "getUpdates", request, &updates); err != nil {
		return nil, offset, err
	}

	nextOffset := offset
	messages := make([]IncomingUpdate, 0, len(updates))
	for _, update := range updates {
		if update.UpdateID >= nextOffset {
			nextOffset = update.UpdateID + 1
		}

		if update.Message == nil {
			if update.CallbackQuery == nil {
				continue
			}
			normalized := b.normalizeCallbackQuery(update.CallbackQuery)
			if normalized.Kind == "" {
				continue
			}
			normalized.nextOffset = update.UpdateID + 1
			messages = append(messages, normalized)
			continue
		}

		normalized := b.normalizeMessage(update.Message)
		if len(normalized) == 0 {
			continue
		}
		for i := range normalized {
			normalized[i].nextOffset = update.UpdateID + 1
		}
		messages = append(messages, normalized...)
	}

	return messages, nextOffset, nil
}

func (b *Bot) call(ctx context.Context, method string, payload any, result any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal %s request: %w", method, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/"+url.PathEscape(method), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close()

	var envelope apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}

	if !envelope.OK {
		return newAPIError(method, envelope.Description, envelope.Parameters.RetryAfter)
	}

	if result == nil {
		return nil
	}

	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}

	return nil
}

func (b *Bot) setCommands(ctx context.Context, commands []BotCommand, languageCode string) error {
	request := map[string]any{
		"commands": commands,
	}
	if strings.TrimSpace(languageCode) != "" {
		request["language_code"] = strings.TrimSpace(languageCode)
	}

	if err := b.call(ctx, "setMyCommands", request, nil); err != nil {
		return fmt.Errorf("set bot commands for language %q: %w", languageCode, err)
	}
	return nil
}

func (b *Bot) normalizeMessage(message *telegramMessage) []IncomingUpdate {
	if message == nil {
		return nil
	}

	base := IncomingUpdate{
		ChatID:     message.Chat.ID,
		TopicID:    message.MessageThreadID,
		MessageID:  message.MessageID,
		ChatTitle:  normalizeChatTitle(message.Chat),
		TopicTitle: b.topicName(message.Chat.ID, message.MessageThreadID),
	}
	if message.From != nil {
		base.UserID = message.From.ID
		base.Username = normalizeUsername(message.From)
		base.LanguageCode = strings.TrimSpace(message.From.LanguageCode)
	}

	if message.ForumTopicCreated != nil {
		title := strings.TrimSpace(message.ForumTopicCreated.Name)
		b.setTopicName(message.Chat.ID, message.MessageThreadID, title)
		base.Kind = UpdateKindTopicCreated
		base.TopicTitle = title
		return []IncomingUpdate{base}
	}

	if message.ForumTopicEdited != nil {
		if title := strings.TrimSpace(message.ForumTopicEdited.Name); title != "" {
			b.setTopicName(message.Chat.ID, message.MessageThreadID, title)
			base.TopicTitle = title
		}
		base.Kind = UpdateKindTopicEdited
		return []IncomingUpdate{base}
	}

	if message.ForumTopicClosed != nil {
		base.Kind = UpdateKindTopicClosed
		return []IncomingUpdate{base}
	}

	if message.ForumTopicReopened != nil {
		base.Kind = UpdateKindTopicReopened
		return []IncomingUpdate{base}
	}

	if message.GeneralForumTopicHidden != nil {
		base.Kind = UpdateKindGeneralTopicHidden
		return []IncomingUpdate{base}
	}

	if message.GeneralForumTopicUnhidden != nil {
		base.Kind = UpdateKindGeneralTopicUnhidden
		return []IncomingUpdate{base}
	}

	if message.From == nil || message.From.IsBot {
		return nil
	}

	base.Media = extractIncomingMedia(message)
	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}
	if text == "" && len(base.Media) == 0 {
		return nil
	}

	base.Kind = UpdateKindMessage
	base.Text = text
	return []IncomingUpdate{base}
}

func (b *Bot) normalizeCallbackQuery(query *telegramCallbackQuery) IncomingUpdate {
	if query == nil || query.Message == nil || query.From == nil || query.From.IsBot {
		return IncomingUpdate{}
	}

	message := query.Message
	return IncomingUpdate{
		Kind:         UpdateKindCallbackQuery,
		ChatID:       message.Chat.ID,
		TopicID:      message.MessageThreadID,
		UserID:       query.From.ID,
		Username:     normalizeUsername(query.From),
		LanguageCode: strings.TrimSpace(query.From.LanguageCode),
		MessageID:    message.MessageID,
		ChatTitle:    normalizeChatTitle(message.Chat),
		TopicTitle:   b.topicName(message.Chat.ID, message.MessageThreadID),
		CallbackID:   strings.TrimSpace(query.ID),
		CallbackData: strings.TrimSpace(query.Data),
	}
}

func normalizeUsername(user *telegramUser) string {
	if user == nil {
		return ""
	}
	if user.Username != "" {
		return user.Username
	}

	name := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	return name
}

func normalizeChatTitle(chat telegramChat) string {
	if chat.Title != "" {
		return chat.Title
	}
	return chat.Type
}

func truncateForTelegram(text string) string {
	chunks := SplitTextForTelegram(text)
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func SplitTextForTelegram(text string) []string {
	const maxTextLength = 4000
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+maxTextLength-1)/maxTextLength)
	for len(runes) > 0 {
		if len(runes) <= maxTextLength {
			chunks = append(chunks, string(runes))
			break
		}

		splitAt := preferredTelegramSplit(runes, maxTextLength)
		chunk := strings.TrimSpace(string(runes[:splitAt]))
		if chunk == "" {
			splitAt = maxTextLength
			chunk = string(runes[:splitAt])
		}
		chunks = append(chunks, chunk)
		runes = trimLeadingSpaceRunes(runes[splitAt:])
	}
	return chunks
}

func preferredTelegramSplit(runes []rune, max int) int {
	if len(runes) <= max {
		return len(runes)
	}

	floor := max - 500
	if floor < 1 {
		floor = 1
	}

	for i := max; i > floor+1; i-- {
		if runes[i-1] == '\n' && runes[i-2] == '\n' {
			return i
		}
	}
	for i := max; i > floor; i-- {
		if runes[i-1] == '\n' {
			return i
		}
	}
	for i := max; i > floor; i-- {
		if unicode.IsSpace(runes[i-1]) {
			return i
		}
	}
	return max
}

func trimLeadingSpaceRunes(runes []rune) []rune {
	start := 0
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	return runes[start:]
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type sendMessageRequest struct {
	ChatID           int64  `json:"chat_id"`
	MessageThreadID  int64  `json:"message_thread_id,omitempty"`
	Text             string `json:"text"`
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
	ReplyMarkup      any    `json:"reply_markup,omitempty"`
}

type editMessageTextRequest struct {
	ChatID      int64  `json:"chat_id"`
	MessageID   int64  `json:"message_id"`
	Text        string `json:"text"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type chatActionRequest struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
	Action          string `json:"action"`
}

type topicControlRequest struct {
	ChatID          int64 `json:"chat_id"`
	MessageThreadID int64 `json:"message_thread_id"`
}

type update struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramMessage struct {
	MessageID                 int64                      `json:"message_id"`
	MessageThreadID           int64                      `json:"message_thread_id"`
	Text                      string                     `json:"text"`
	Caption                   string                     `json:"caption"`
	Photo                     []telegramPhotoSize        `json:"photo"`
	Document                  *telegramDocument          `json:"document"`
	Voice                     *telegramVoice             `json:"voice"`
	Audio                     *telegramAudio             `json:"audio"`
	Chat                      telegramChat               `json:"chat"`
	From                      *telegramUser              `json:"from"`
	ForumTopicCreated         *telegramForumTopicCreated `json:"forum_topic_created"`
	ForumTopicEdited          *telegramForumTopicEdited  `json:"forum_topic_edited"`
	ForumTopicClosed          *struct{}                  `json:"forum_topic_closed"`
	ForumTopicReopened        *struct{}                  `json:"forum_topic_reopened"`
	GeneralForumTopicHidden   *struct{}                  `json:"general_forum_topic_hidden"`
	GeneralForumTopicUnhidden *struct{}                  `json:"general_forum_topic_unhidden"`
}

type telegramSentMessage struct {
	MessageID int64 `json:"message_id"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    *telegramUser    `json:"from"`
	Message *telegramMessage `json:"message"`
	Data    string           `json:"data"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type telegramUser struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	LanguageCode string `json:"language_code"`
}

type telegramForumTopicCreated struct {
	Name string `json:"name"`
}

type telegramForumTopicEdited struct {
	Name string `json:"name"`
}

type telegramPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type telegramDocument struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MIMEType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type telegramVoice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	MIMEType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Duration     int    `json:"duration"`
}

type telegramAudio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MIMEType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Duration     int    `json:"duration"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]telegramInlineButton `json:"inline_keyboard"`
}

type telegramInlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

func optionalInlineKeyboard(rows [][]InlineButton) any {
	if len(rows) == 0 {
		return nil
	}
	return inlineKeyboardMarkup{InlineKeyboard: toTelegramInlineKeyboard(rows)}
}

func toTelegramInlineKeyboard(rows [][]InlineButton) [][]telegramInlineButton {
	keyboard := make([][]telegramInlineButton, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		buttons := make([]telegramInlineButton, 0, len(row))
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" || strings.TrimSpace(button.CallbackData) == "" {
				continue
			}
			buttons = append(buttons, telegramInlineButton{
				Text:         strings.TrimSpace(button.Text),
				CallbackData: strings.TrimSpace(button.CallbackData),
			})
		}
		if len(buttons) > 0 {
			keyboard = append(keyboard, buttons)
		}
	}
	return keyboard
}

func (b *Bot) topicName(chatID, topicID int64) string {
	if topicID == 0 {
		return ""
	}

	b.topicsMu.RLock()
	defer b.topicsMu.RUnlock()
	return b.topicNames[topicKey(chatID, topicID)]
}

func (b *Bot) setTopicName(chatID, topicID int64, title string) {
	if topicID == 0 || strings.TrimSpace(title) == "" {
		return
	}

	b.topicsMu.Lock()
	defer b.topicsMu.Unlock()
	b.topicNames[topicKey(chatID, topicID)] = title
}

func topicKey(chatID, topicID int64) string {
	return fmt.Sprintf("%d:%d", chatID, topicID)
}

func extractIncomingMedia(message *telegramMessage) []IncomingMedia {
	if message == nil {
		return nil
	}

	var media []IncomingMedia
	if photo := bestPhoto(message.Photo); photo != nil {
		media = append(media, IncomingMedia{
			Kind:         MediaKindPhoto,
			FileID:       strings.TrimSpace(photo.FileID),
			FileUniqueID: strings.TrimSpace(photo.FileUniqueID),
			FileName:     "photo.jpg",
			MIMEType:     "image/jpeg",
			FileSize:     photo.FileSize,
			Width:        photo.Width,
			Height:       photo.Height,
		})
	}
	if message.Document != nil && strings.TrimSpace(message.Document.FileID) != "" {
		media = append(media, IncomingMedia{
			Kind:         MediaKindDocument,
			FileID:       strings.TrimSpace(message.Document.FileID),
			FileUniqueID: strings.TrimSpace(message.Document.FileUniqueID),
			FileName:     strings.TrimSpace(message.Document.FileName),
			MIMEType:     strings.TrimSpace(message.Document.MIMEType),
			FileSize:     message.Document.FileSize,
		})
	}
	if message.Voice != nil && strings.TrimSpace(message.Voice.FileID) != "" {
		media = append(media, IncomingMedia{
			Kind:         MediaKindVoice,
			FileID:       strings.TrimSpace(message.Voice.FileID),
			FileUniqueID: strings.TrimSpace(message.Voice.FileUniqueID),
			FileName:     "voice.ogg",
			MIMEType:     strings.TrimSpace(message.Voice.MIMEType),
			FileSize:     message.Voice.FileSize,
			Duration:     message.Voice.Duration,
		})
	}
	if message.Audio != nil && strings.TrimSpace(message.Audio.FileID) != "" {
		media = append(media, IncomingMedia{
			Kind:         MediaKindAudio,
			FileID:       strings.TrimSpace(message.Audio.FileID),
			FileUniqueID: strings.TrimSpace(message.Audio.FileUniqueID),
			FileName:     strings.TrimSpace(message.Audio.FileName),
			MIMEType:     strings.TrimSpace(message.Audio.MIMEType),
			FileSize:     message.Audio.FileSize,
			Duration:     message.Audio.Duration,
		})
	}
	return media
}

func bestPhoto(photos []telegramPhotoSize) *telegramPhotoSize {
	if len(photos) == 0 {
		return nil
	}
	best := photos[0]
	for _, candidate := range photos[1:] {
		if candidate.FileSize > best.FileSize {
			best = candidate
			continue
		}
		if candidate.Width*candidate.Height > best.Width*best.Height {
			best = candidate
		}
	}
	return &best
}

func (b *Bot) sendFileUpload(ctx context.Context, method, fieldName string, msg OutgoingFile) error {
	filePath := strings.TrimSpace(msg.FilePath)
	if filePath == "" {
		return fmt.Errorf("%s requires a file path", method)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s upload file: %w", method, err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writeField := func(key, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return writer.WriteField(key, value)
	}

	if err := writeField("chat_id", strconv.FormatInt(msg.ChatID, 10)); err != nil {
		return fmt.Errorf("write %s chat_id: %w", method, err)
	}
	if msg.TopicID > 0 {
		if err := writeField("message_thread_id", strconv.FormatInt(msg.TopicID, 10)); err != nil {
			return fmt.Errorf("write %s topic id: %w", method, err)
		}
	}
	if msg.ReplyToMessageID > 0 {
		if err := writeField("reply_to_message_id", strconv.FormatInt(msg.ReplyToMessageID, 10)); err != nil {
			return fmt.Errorf("write %s reply id: %w", method, err)
		}
	}
	if caption := truncateCaption(msg.Caption); caption != "" {
		if err := writeField("caption", caption); err != nil {
			return fmt.Errorf("write %s caption: %w", method, err)
		}
	}

	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create %s form file: %w", method, err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy %s file contents: %w", method, err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close %s multipart writer: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/"+url.PathEscape(method), &body)
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close()

	var envelope apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if !envelope.OK {
		return newAPIError(method, envelope.Description, envelope.Parameters.RetryAfter)
	}
	return nil
}

func newAPIError(method, description string, retryAfter int) error {
	err := &APIError{
		Method:      method,
		Description: description,
	}
	if retryAfter > 0 {
		err.RetryAfter = time.Duration(retryAfter) * time.Second
	}
	return err
}

func truncateCaption(text string) string {
	text = strings.TrimSpace(text)
	const maxCaptionLength = 900
	if len(text) <= maxCaptionLength {
		return text
	}
	return text[:maxCaptionLength-1] + "…"
}
