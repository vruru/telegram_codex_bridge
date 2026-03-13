//go:build linux

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultServiceLabel = "com.telegramcodex.bridge"

type linuxManager struct {
	label string
	paths Paths
}

func newManager(projectRoot string) (Manager, error) {
	paths, err := resolveLinuxPaths(projectRoot)
	if err != nil {
		return nil, err
	}
	return &linuxManager{
		label: defaultServiceLabel,
		paths: paths,
	}, nil
}

func resolveLinuxPaths(projectRoot string) (Paths, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve executable: %w", err)
		}
		root = filepath.Dir(filepath.Dir(exe))
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve project root: %w", err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return Paths{}, fmt.Errorf("open project root: %w", err)
	}
	if !info.IsDir() {
		return Paths{}, fmt.Errorf("project root must be a directory: %s", root)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}

	systemdDir := filepath.Join(home, ".config", "systemd", "user")
	return Paths{
		ProjectRoot:     root,
		BridgeBinary:    filepath.Join(root, "bin", "telegram-codex-bridge"),
		BridgeControl:   filepath.Join(root, "bin", "telegram-codex-bridge"),
		EnvFile:         filepath.Join(root, ".env"),
		StatePath:       filepath.Join(root, "data", "bridge.db"),
		LogsDir:         filepath.Join(root, "data", "logs"),
		StdoutLog:       filepath.Join(root, "data", "logs", "bridge.stdout.log"),
		StderrLog:       filepath.Join(root, "data", "logs", "bridge.stderr.log"),
		LaunchAgentsDir: systemdDir,
		PlistPath:       filepath.Join(systemdDir, defaultServiceLabel+".service"),
	}, nil
}

func (s *linuxManager) Status(ctx context.Context) (Status, error) {
	status := Status{
		Label: s.label,
		Paths: s.paths,
	}

	if _, err := os.Stat(s.paths.PlistPath); err == nil {
		status.Installed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("stat systemd unit: %w", err)
	}

	props, err := s.showUnit(ctx)
	if err != nil {
		if unmanagedPID, ok := findUnmanagedPID(ctx, s.paths.BridgeBinary); ok {
			status.Running = true
			status.UnmanagedPID = unmanagedPID
			status.Description = fmt.Sprintf("running outside systemd with pid %d", unmanagedPID)
			return status, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			status.Description = "service not loaded"
			return status, nil
		}
		return status, err
	}

	loadState := props["LoadState"]
	activeState := props["ActiveState"]
	mainPID := parseSystemdInt(props["MainPID"])
	execMainStatus := parseSystemdInt(props["ExecMainStatus"])
	unitFileState := props["UnitFileState"]

	status.Loaded = loadState == "loaded"
	status.AutoStart = isEnabledUnitState(unitFileState)
	status.PID = mainPID
	status.Running = activeState == "active" && mainPID > 0
	if execMainStatus >= 0 {
		lastExit := execMainStatus
		status.LastExit = &lastExit
	}

	switch {
	case status.Running:
		status.Description = fmt.Sprintf("running with pid %d", status.PID)
	case status.Loaded:
		status.Description = "loaded but idle"
	default:
		status.Description = "service not loaded"
	}

	return status, nil
}

func (s *linuxManager) SetAutoStart(ctx context.Context, enabled bool) error {
	if err := s.writeUnit(); err != nil {
		return err
	}
	if err := s.daemonReload(ctx); err != nil {
		return err
	}
	if enabled {
		return s.systemctl(ctx, "enable", s.label)
	}
	return s.systemctl(ctx, "disable", s.label)
}

func (s *linuxManager) Start(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if status.Running && !status.Loaded && status.UnmanagedPID > 0 {
		return fmt.Errorf("bridge is already running outside systemd with pid %d", status.UnmanagedPID)
	}
	if err := s.writeUnit(); err != nil {
		return err
	}
	if err := s.daemonReload(ctx); err != nil {
		return err
	}
	return s.systemctl(ctx, "start", s.label)
}

func (s *linuxManager) Stop(ctx context.Context) error {
	return s.systemctl(ctx, "stop", s.label)
}

func (s *linuxManager) Restart(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if status.Running && !status.Loaded && status.UnmanagedPID > 0 {
		return fmt.Errorf("bridge is already running outside systemd with pid %d", status.UnmanagedPID)
	}
	if err := s.writeUnit(); err != nil {
		return err
	}
	if err := s.daemonReload(ctx); err != nil {
		return err
	}
	return s.systemctl(ctx, "restart", s.label)
}

func (s *linuxManager) Remove(ctx context.Context) error {
	_ = s.systemctl(ctx, "disable", "--now", s.label)
	if err := os.Remove(s.paths.PlistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	return s.daemonReload(ctx)
}

func (s *linuxManager) StopUnmanaged(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if status.UnmanagedPID == 0 {
		return nil
	}

	process, err := os.FindProcess(status.UnmanagedPID)
	if err != nil {
		return fmt.Errorf("find unmanaged process: %w", err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("stop unmanaged process %d: %w", status.UnmanagedPID, err)
	}
	return nil
}

func (s *linuxManager) Paths() Paths {
	return s.paths
}

func (s *linuxManager) writeUnit() error {
	if err := os.MkdirAll(s.paths.LaunchAgentsDir, 0o755); err != nil {
		return fmt.Errorf("create systemd user directory: %w", err)
	}
	if err := os.MkdirAll(s.paths.LogsDir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	if _, err := os.Stat(s.paths.BridgeBinary); err != nil {
		return fmt.Errorf("bridge binary is missing: %w", err)
	}

	content := renderSystemdUnit(systemdUnitSpec{
		Label:      s.label,
		Program:    s.paths.BridgeBinary,
		WorkingDir: s.paths.ProjectRoot,
		EnvFile:    s.paths.EnvFile,
		StdoutLog:  s.paths.StdoutLog,
		StderrLog:  s.paths.StderrLog,
	})
	if err := os.WriteFile(s.paths.PlistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	return nil
}

func (s *linuxManager) daemonReload(ctx context.Context) error {
	return s.systemctl(ctx, "daemon-reload")
}

func (s *linuxManager) showUnit(ctx context.Context) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "show", s.label,
		"--property=LoadState,ActiveState,SubState,MainPID,UnitFileState,ExecMainStatus")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			combined := strings.TrimSpace(string(append(exitErr.Stderr, output...)))
			if strings.Contains(combined, "not-found") || strings.Contains(combined, "could not be found") || strings.Contains(combined, "Unit "+s.label+" could not be found") {
				return nil, os.ErrNotExist
			}
		}
		return nil, fmt.Errorf("inspect systemd service: %w", err)
	}

	props := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		props[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return props, nil
}

func (s *linuxManager) systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		trimmed := strings.TrimSpace(stderr.String())
		if trimmed != "" {
			return fmt.Errorf("%w (%s)", err, trimmed)
		}
		return err
	}
	return nil
}

func renderSystemdUnit(spec systemdUnitSpec) string {
	return fmt.Sprintf(`[Unit]
Description=Telegram Codex Bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
Environment=PATH=%s
EnvironmentFile=-%s
ExecStart=%s
Restart=always
RestartSec=3
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, spec.WorkingDir, systemdPathEnv(), spec.EnvFile, spec.Program, spec.StdoutLog, spec.StderrLog)
}

type systemdUnitSpec struct {
	Label      string
	Program    string
	WorkingDir string
	EnvFile    string
	StdoutLog  string
	StderrLog  string
}

func systemdPathEnv() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/usr/local/bin:/usr/bin:/bin"
	}
	return strings.Join([]string{
		filepath.Join(home, ".local", "bin"),
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}, ":")
}

func isEnabledUnitState(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "enabled") || strings.HasPrefix(value, "linked")
}

func parseSystemdInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}
