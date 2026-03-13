package codex

import (
	"context"
	"errors"

	"telegram-codex-bridge/internal/config"
)

var (
	ErrAppServerUnavailable  = errors.New("codex app-server unavailable")
	ErrSteeringUnsupported   = errors.New("codex steering unsupported")
	ErrActiveTurnUnavailable = errors.New("codex active turn unavailable")
)

type BridgeClient interface {
	Provider() string
	WorkspaceRoot() string
	PermissionMode() string
	SetPermissionMode(mode string)
	SettingsCatalog() (SettingsCatalog, error)
	SettingsCatalogFor(provider string) (SettingsCatalog, error)
	StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error)
	ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error)
	SteerThread(ctx context.Context, req SteerThreadRequest) error
	ArchiveThread(ctx context.Context, sessionID string) error
}

type SteerThreadRequest struct {
	SessionID  string
	TurnID     string
	Message    string
	ImagePaths []string
}

func NewClient(cfg config.CodexConfig) BridgeClient {
	return NewProviderRouter(cfg)
}

type FallbackClient struct {
	primary  BridgeClient
	fallback BridgeClient
}

type sessionProviderRememberer interface {
	RememberSessionProvider(sessionID, provider string)
}

func NewFallbackClient(primary, fallback BridgeClient) *FallbackClient {
	return &FallbackClient{
		primary:  primary,
		fallback: fallback,
	}
}

func (c *FallbackClient) WorkspaceRoot() string {
	return c.primary.WorkspaceRoot()
}

func (c *FallbackClient) Provider() string {
	return c.primary.Provider()
}

func (c *FallbackClient) PermissionMode() string {
	return c.primary.PermissionMode()
}

func (c *FallbackClient) SetPermissionMode(mode string) {
	c.primary.SetPermissionMode(mode)
	if c.fallback != nil && c.fallback != c.primary {
		c.fallback.SetPermissionMode(mode)
	}
}

func (c *FallbackClient) SettingsCatalog() (SettingsCatalog, error) {
	result, err := c.primary.SettingsCatalog()
	if shouldFallback(err) && c.fallback != nil {
		return c.fallback.SettingsCatalog()
	}
	return result, err
}

func (c *FallbackClient) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	_ = provider
	return c.SettingsCatalog()
}

func (c *FallbackClient) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	result, err := c.primary.StartThread(ctx, req, handler)
	if shouldFallback(err) && c.fallback != nil {
		return c.fallback.StartThread(ctx, req, handler)
	}
	return result, err
}

func (c *FallbackClient) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	result, err := c.primary.ResumeThread(ctx, req, handler)
	if shouldFallback(err) && c.fallback != nil {
		return c.fallback.ResumeThread(ctx, req, handler)
	}
	return result, err
}

func (c *FallbackClient) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	err := c.primary.SteerThread(ctx, req)
	if shouldFallback(err) && c.fallback != nil {
		return c.fallback.SteerThread(ctx, req)
	}
	return err
}

func (c *FallbackClient) ArchiveThread(ctx context.Context, sessionID string) error {
	err := c.primary.ArchiveThread(ctx, sessionID)
	if shouldFallback(err) && c.fallback != nil {
		return c.fallback.ArchiveThread(ctx, sessionID)
	}
	return err
}

func (c *FallbackClient) RememberSessionProvider(sessionID, provider string) {
	if rememberer, ok := c.primary.(sessionProviderRememberer); ok {
		rememberer.RememberSessionProvider(sessionID, provider)
	}
	if rememberer, ok := c.fallback.(sessionProviderRememberer); ok {
		rememberer.RememberSessionProvider(sessionID, provider)
	}
}

func RememberSessionProvider(client BridgeClient, sessionID, provider string) {
	rememberer, ok := client.(sessionProviderRememberer)
	if !ok {
		return
	}
	rememberer.RememberSessionProvider(sessionID, provider)
}

func shouldFallback(err error) bool {
	return errors.Is(err, ErrAppServerUnavailable) || errors.Is(err, ErrSteeringUnsupported) || errors.Is(err, ErrActiveTurnUnavailable)
}
