package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/config"
	"telegram-codex-bridge/internal/store"
	"telegram-codex-bridge/internal/telegram"
)

type stubBridgeClient struct {
	provider string
}

func (c *stubBridgeClient) Provider() string {
	return c.provider
}

func (c *stubBridgeClient) WorkspaceRoot() string {
	return ""
}

func (c *stubBridgeClient) PermissionMode() string {
	return "default"
}

func (c *stubBridgeClient) SetPermissionMode(mode string) {
	_ = mode
}

func (c *stubBridgeClient) SettingsCatalog() (codex.SettingsCatalog, error) {
	return codex.SettingsCatalog{}, nil
}

func (c *stubBridgeClient) SettingsCatalogFor(provider string) (codex.SettingsCatalog, error) {
	_ = provider
	return codex.SettingsCatalog{}, nil
}

func (c *stubBridgeClient) StartThread(ctx context.Context, req codex.StartThreadRequest, handler codex.StreamHandler) (codex.StartThreadResult, error) {
	_ = ctx
	_ = req
	_ = handler
	return codex.StartThreadResult{}, nil
}

func (c *stubBridgeClient) ResumeThread(ctx context.Context, req codex.ResumeThreadRequest, handler codex.StreamHandler) (codex.ResumeThreadResult, error) {
	_ = ctx
	_ = req
	_ = handler
	return codex.ResumeThreadResult{}, nil
}

func (c *stubBridgeClient) SteerThread(ctx context.Context, req codex.SteerThreadRequest) error {
	_ = ctx
	_ = req
	return nil
}

func (c *stubBridgeClient) ArchiveThread(ctx context.Context, sessionID string) error {
	_ = ctx
	_ = sessionID
	return nil
}

func TestSendLimitUsesCurrentTopicProvider(t *testing.T) {
	originalFetch := fetchUsageSnapshot
	defer func() {
		fetchUsageSnapshot = originalFetch
	}()

	var (
		mu         sync.Mutex
		sentTexts  []string
		fetchCalls int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		defer r.Body.Close()

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		text, _ := payload["text"].(string)
		mu.Lock()
		sentTexts = append(sentTexts, text)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()

	fetchUsageSnapshot = func(ctx context.Context) (codex.UsageSnapshot, error) {
		_ = ctx
		fetchCalls++
		return codex.UsageSnapshot{
			Available: true,
			PlanType:  "pro",
			PrimaryWindow: &codex.UsageWindow{
				RemainingPercent: 88,
				ResetAt:          1741804140,
			},
		}, nil
	}

	bot := telegram.NewBot(config.TelegramConfig{
		BotToken:           "token",
		APIBaseURL:         server.URL,
		PollTimeoutSeconds: 1,
	}, nil)
	topicStore := store.NewMemoryTopicStore()
	app := New(log.New(io.Discard, "", 0), bot, &stubBridgeClient{provider: "gemini"}, topicStore, nil, "en", "", false)

	if err := topicStore.SaveTopicPreferences(context.Background(), 100, 200, store.TopicPreferences{Provider: "codex"}); err != nil {
		t.Fatalf("save topic preference: %v", err)
	}

	err := app.sendLimit(context.Background(), telegram.IncomingUpdate{
		ChatID:       100,
		TopicID:      200,
		MessageID:    300,
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("send limit: %v", err)
	}

	if fetchCalls != 1 {
		t.Fatalf("expected quota lookup to run once, got %d", fetchCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sentTexts) != 1 {
		t.Fatalf("expected one message, got %d", len(sentTexts))
	}
	if !strings.Contains(sentTexts[0], "Current Codex quota") {
		t.Fatalf("expected Codex quota message, got %q", sentTexts[0])
	}
}

func TestSendLimitRejectsGeminiTopicEvenWhenDefaultProviderIsCodex(t *testing.T) {
	originalFetch := fetchUsageSnapshot
	defer func() {
		fetchUsageSnapshot = originalFetch
	}()

	var (
		mu         sync.Mutex
		sentTexts  []string
		fetchCalls int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		defer r.Body.Close()

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		text, _ := payload["text"].(string)
		mu.Lock()
		sentTexts = append(sentTexts, text)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()

	fetchUsageSnapshot = func(ctx context.Context) (codex.UsageSnapshot, error) {
		_ = ctx
		fetchCalls++
		return codex.UsageSnapshot{}, nil
	}

	bot := telegram.NewBot(config.TelegramConfig{
		BotToken:           "token",
		APIBaseURL:         server.URL,
		PollTimeoutSeconds: 1,
	}, nil)
	topicStore := store.NewMemoryTopicStore()
	app := New(log.New(io.Discard, "", 0), bot, &stubBridgeClient{provider: "codex"}, topicStore, nil, "en", "", false)

	if err := topicStore.SaveTopicPreferences(context.Background(), 100, 200, store.TopicPreferences{Provider: "gemini"}); err != nil {
		t.Fatalf("save topic preference: %v", err)
	}

	err := app.sendLimit(context.Background(), telegram.IncomingUpdate{
		ChatID:       100,
		TopicID:      200,
		MessageID:    300,
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("send limit: %v", err)
	}

	if fetchCalls != 0 {
		t.Fatalf("expected quota lookup not to run, got %d", fetchCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sentTexts) != 1 {
		t.Fatalf("expected one message, got %d", len(sentTexts))
	}
	if !strings.Contains(sentTexts[0], "Quota lookup is not available for the current provider") {
		t.Fatalf("expected Gemini unsupported message, got %q", sentTexts[0])
	}
}
