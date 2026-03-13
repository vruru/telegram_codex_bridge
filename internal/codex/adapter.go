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
	WorkspaceRoot() string
	PermissionMode() string
	SetPermissionMode(mode string)
	SettingsCatalog() (SettingsCatalog, error)
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
	cli := NewCLIClient(cfg)

	switch cfg.AdapterMode {
	case "cli":
		return cli
	case "app-server":
		return NewFallbackClient(NewAppServerClient(cfg, cli), cli)
	default:
		return NewFallbackClient(NewAppServerClient(cfg, cli), cli)
	}
}

type FallbackClient struct {
	primary  BridgeClient
	fallback BridgeClient
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

func shouldFallback(err error) bool {
	return errors.Is(err, ErrAppServerUnavailable) || errors.Is(err, ErrSteeringUnsupported) || errors.Is(err, ErrActiveTurnUnavailable)
}
