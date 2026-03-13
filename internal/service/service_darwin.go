//go:build darwin

package service

import (
	"context"

	"telegram-codex-bridge/internal/macos"
)

type darwinManager struct {
	inner *macos.Service
}

func newManager(projectRoot string) (Manager, error) {
	inner, err := macos.NewService(projectRoot)
	if err != nil {
		return nil, err
	}
	return &darwinManager{inner: inner}, nil
}

func (m *darwinManager) Status(ctx context.Context) (Status, error) {
	status, err := m.inner.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	return convertStatus(status), nil
}

func (m *darwinManager) SetAutoStart(ctx context.Context, enabled bool) error {
	return m.inner.SetAutoStart(ctx, enabled)
}

func (m *darwinManager) Start(ctx context.Context) error {
	return m.inner.Start(ctx)
}

func (m *darwinManager) Stop(ctx context.Context) error {
	return m.inner.Stop(ctx)
}

func (m *darwinManager) Restart(ctx context.Context) error {
	return m.inner.Restart(ctx)
}

func (m *darwinManager) Remove(ctx context.Context) error {
	return m.inner.Remove(ctx)
}

func (m *darwinManager) StopUnmanaged(ctx context.Context) error {
	return m.inner.StopUnmanaged(ctx)
}

func (m *darwinManager) Paths() Paths {
	return convertPaths(m.inner.Paths())
}

func convertPaths(paths macos.Paths) Paths {
	return Paths{
		ProjectRoot:     paths.ProjectRoot,
		BridgeBinary:    paths.BridgeBinary,
		BridgeControl:   paths.BridgeControl,
		EnvFile:         paths.EnvFile,
		StatePath:       paths.StatePath,
		LogsDir:         paths.LogsDir,
		StdoutLog:       paths.StdoutLog,
		StderrLog:       paths.StderrLog,
		LaunchAgentsDir: paths.LaunchAgentsDir,
		PlistPath:       paths.PlistPath,
	}
}

func convertStatus(status macos.Status) Status {
	return Status{
		Label:        status.Label,
		Installed:    status.Installed,
		Loaded:       status.Loaded,
		Running:      status.Running,
		AutoStart:    status.AutoStart,
		PID:          status.PID,
		UnmanagedPID: status.UnmanagedPID,
		LastExit:     status.LastExit,
		Description:  status.Description,
		Paths:        convertPaths(status.Paths),
	}
}
