package codex

import (
	"context"
	"errors"
	"testing"
)

type stubBridgeClient struct {
	provider string

	startCalls   int
	startResult  StartThreadResult
	startErr     error
	resumeCalls  int
	resumeResult ResumeThreadResult
	resumeErr    error
	archiveCalls []string
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

func (c *stubBridgeClient) SettingsCatalog() (SettingsCatalog, error) {
	return SettingsCatalog{}, nil
}

func (c *stubBridgeClient) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	_ = provider
	return SettingsCatalog{}, nil
}

func (c *stubBridgeClient) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	_ = ctx
	_ = req
	_ = handler
	c.startCalls++
	if c.startErr != nil {
		return StartThreadResult{}, c.startErr
	}
	return c.startResult, nil
}

func (c *stubBridgeClient) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	_ = ctx
	_ = req
	_ = handler
	c.resumeCalls++
	if c.resumeErr != nil {
		return ResumeThreadResult{}, c.resumeErr
	}
	return c.resumeResult, nil
}

func (c *stubBridgeClient) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	_ = ctx
	_ = req
	return nil
}

func (c *stubBridgeClient) ArchiveThread(ctx context.Context, sessionID string) error {
	_ = ctx
	c.archiveCalls = append(c.archiveCalls, sessionID)
	return nil
}

func TestProviderRouterResumeDoesNotSilentlyFallbackToGemini(t *testing.T) {
	t.Parallel()

	codexClient := &stubBridgeClient{
		provider:  "codex",
		resumeErr: errors.New("quota exceeded"),
	}
	geminiClient := &stubBridgeClient{
		provider: "gemini",
		startResult: StartThreadResult{
			Thread: Thread{
				SessionID: "gemini-session",
				Provider:  "gemini",
			},
		},
	}
	router := &ProviderRouter{
		defaultProvider:  "codex",
		clients:          map[string]BridgeClient{"codex": codexClient, "gemini": geminiClient},
		sessionProviders: make(map[string]string),
	}

	_, err := router.ResumeThread(context.Background(), ResumeThreadRequest{
		SessionID: "codex-session",
		Message:   "hello",
		Settings:  RunSettings{Provider: "codex"},
	}, nil)
	if err == nil {
		t.Fatalf("expected resume error, got nil")
	}
	if geminiClient.startCalls != 0 {
		t.Fatalf("expected no Gemini fallback start, got %d call(s)", geminiClient.startCalls)
	}
}

func TestProviderRouterStartStillFallsBackForFreshThread(t *testing.T) {
	t.Parallel()

	codexClient := &stubBridgeClient{
		provider: "codex",
		startErr: errors.New("rate limit"),
	}
	geminiClient := &stubBridgeClient{
		provider: "gemini",
		startResult: StartThreadResult{
			Thread: Thread{
				SessionID: "gemini-session",
				Provider:  "gemini",
			},
			Reply: "hello from gemini",
		},
	}
	router := &ProviderRouter{
		defaultProvider:  "codex",
		clients:          map[string]BridgeClient{"codex": codexClient, "gemini": geminiClient},
		sessionProviders: make(map[string]string),
	}

	result, err := router.StartThread(context.Background(), StartThreadRequest{
		Prompt:   "hello",
		Settings: RunSettings{Provider: "codex"},
	}, nil)
	if err != nil {
		t.Fatalf("expected fallback start to succeed, got %v", err)
	}
	if result.Thread.Provider != "gemini" {
		t.Fatalf("expected Gemini provider, got %q", result.Thread.Provider)
	}
	if geminiClient.startCalls != 1 {
		t.Fatalf("expected Gemini start to be called once, got %d", geminiClient.startCalls)
	}
}

func TestProviderRouterArchiveUsesRememberedSessionProvider(t *testing.T) {
	t.Parallel()

	codexClient := &stubBridgeClient{provider: "codex"}
	geminiClient := &stubBridgeClient{provider: "gemini"}
	router := &ProviderRouter{
		defaultProvider:  "codex",
		clients:          map[string]BridgeClient{"codex": codexClient, "gemini": geminiClient},
		sessionProviders: make(map[string]string),
	}

	RememberSessionProvider(router, "gemini-session", "gemini")
	if err := router.ArchiveThread(context.Background(), "gemini-session"); err != nil {
		t.Fatalf("archive thread: %v", err)
	}

	if len(geminiClient.archiveCalls) != 1 || geminiClient.archiveCalls[0] != "gemini-session" {
		t.Fatalf("expected Gemini archive call for remembered session, got %#v", geminiClient.archiveCalls)
	}
	if len(codexClient.archiveCalls) != 0 {
		t.Fatalf("expected no Codex archive call, got %#v", codexClient.archiveCalls)
	}
}
