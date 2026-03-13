package codex

import (
	"context"
	"strings"
	"sync"

	"telegram-codex-bridge/internal/config"
)

type ProviderRouter struct {
	defaultProvider string
	clients         map[string]BridgeClient

	mu               sync.RWMutex
	sessionProviders map[string]string
}

func NewProviderRouter(cfg config.CodexConfig) BridgeClient {
	router := &ProviderRouter{
		defaultProvider:  normalizeProviderName(cfg.Provider),
		clients:          make(map[string]BridgeClient),
		sessionProviders: make(map[string]string),
	}

	codexCfg := cfg
	codexCfg.Provider = "codex"
	if strings.TrimSpace(codexCfg.BinaryPath) == "" || cfg.Provider == "gemini" {
		codexCfg.BinaryPath = "codex"
	}
	codexCLI := NewCLIClient(codexCfg)
	switch codexCfg.AdapterMode {
	case "cli":
		router.clients["codex"] = codexCLI
	case "app-server", "appserver", "server":
		router.clients["codex"] = NewFallbackClient(NewAppServerClient(codexCfg, codexCLI), codexCLI)
	default:
		router.clients["codex"] = NewFallbackClient(NewAppServerClient(codexCfg, codexCLI), codexCLI)
	}

	geminiCfg := cfg
	geminiCfg.Provider = "gemini"
	if strings.TrimSpace(geminiCfg.BinaryPath) == "" || cfg.Provider == "codex" {
		geminiCfg.BinaryPath = "gemini"
	}
	router.clients["gemini"] = NewGeminiCLIClient(geminiCfg)

	return router
}

func (r *ProviderRouter) Provider() string {
	return r.defaultProvider
}

func (r *ProviderRouter) WorkspaceRoot() string {
	return r.clientFor(r.defaultProvider).WorkspaceRoot()
}

func (r *ProviderRouter) PermissionMode() string {
	return r.clientFor(r.defaultProvider).PermissionMode()
}

func (r *ProviderRouter) SetPermissionMode(mode string) {
	for _, client := range r.clients {
		client.SetPermissionMode(mode)
	}
}

func (r *ProviderRouter) SettingsCatalog() (SettingsCatalog, error) {
	return r.SettingsCatalogFor(r.defaultProvider)
}

func (r *ProviderRouter) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	return r.clientFor(provider).SettingsCatalog()
}

func (r *ProviderRouter) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	provider := normalizeProviderName(req.Settings.Provider)
	req.Settings.Provider = provider
	result, err := r.clientFor(provider).StartThread(ctx, req, handler)
	if err == nil {
		r.remember(result.Thread.SessionID, provider)
		return result, nil
	}

	if provider == "codex" && shouldProviderFallback(err) {
		fallbackReq := req
		fallbackReq.Settings.Provider = "gemini"
		fallbackResult, fallbackErr := r.clientFor("gemini").StartThread(ctx, fallbackReq, handler)
		if fallbackErr == nil {
			r.remember(fallbackResult.Thread.SessionID, "gemini")
			return fallbackResult, nil
		}
	}

	return StartThreadResult{}, err
}

func (r *ProviderRouter) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	provider := normalizeProviderName(req.Settings.Provider)
	req.Settings.Provider = provider
	result, err := r.clientFor(provider).ResumeThread(ctx, req, handler)
	if err == nil {
		r.remember(result.SessionID, provider)
		return result, nil
	}
	return ResumeThreadResult{}, err
}

func (r *ProviderRouter) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	return r.clientFor(r.providerForSession(req.SessionID)).SteerThread(ctx, req)
}

func (r *ProviderRouter) ArchiveThread(ctx context.Context, sessionID string) error {
	return r.clientFor(r.providerForSession(sessionID)).ArchiveThread(ctx, sessionID)
}

func (r *ProviderRouter) RememberSessionProvider(sessionID, provider string) {
	r.remember(sessionID, provider)
}

func (r *ProviderRouter) remember(sessionID, provider string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionProviders[sessionID] = normalizeProviderName(provider)
}

func (r *ProviderRouter) providerForSession(sessionID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if provider, ok := r.sessionProviders[strings.TrimSpace(sessionID)]; ok {
		return provider
	}
	return r.defaultProvider
}

func (r *ProviderRouter) clientFor(provider string) BridgeClient {
	provider = normalizeProviderName(provider)
	if client, ok := r.clients[provider]; ok {
		return client
	}
	return r.clients[r.defaultProvider]
}

func shouldProviderFallback(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "quota") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "429") ||
		strings.Contains(text, "resource_exhausted") ||
		strings.Contains(text, "capacity") ||
		strings.Contains(text, "login expired") ||
		strings.Contains(text, "not logged in")
}

func normalizeProviderName(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gemini", "gemini-cli", "gemini_cli":
		return "gemini"
	default:
		return "codex"
	}
}
