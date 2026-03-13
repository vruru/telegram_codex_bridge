package macos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const (
	DefaultServiceLabel = "com.telegramcodex.bridge"
	DefaultAppBundleID  = "com.telegramcodex.bridge.status"
)

type Paths struct {
	ProjectRoot     string `json:"project_root"`
	BridgeBinary    string `json:"bridge_binary"`
	BridgeControl   string `json:"bridge_control"`
	EnvFile         string `json:"env_file"`
	StatePath       string `json:"state_path"`
	LogsDir         string `json:"logs_dir"`
	StdoutLog       string `json:"stdout_log"`
	StderrLog       string `json:"stderr_log"`
	LaunchAgentsDir string `json:"launch_agents_dir"`
	PlistPath       string `json:"plist_path"`
}

type Status struct {
	Label        string `json:"label"`
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	Running      bool   `json:"running"`
	AutoStart    bool   `json:"auto_start"`
	PID          int    `json:"pid,omitempty"`
	UnmanagedPID int    `json:"unmanaged_pid,omitempty"`
	LastExit     *int   `json:"last_exit,omitempty"`
	Description  string `json:"description,omitempty"`
	Paths        Paths  `json:"paths"`
}

type Service struct {
	label string
	uid   string
	paths Paths
}

func NewService(projectRoot string) (*Service, error) {
	paths, err := ResolvePaths(projectRoot)
	if err != nil {
		return nil, err
	}

	return &Service{
		label: DefaultServiceLabel,
		uid:   strconv.Itoa(os.Getuid()),
		paths: paths,
	}, nil
}

func ResolvePaths(projectRoot string) (Paths, error) {
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

	return Paths{
		ProjectRoot:     root,
		BridgeBinary:    filepath.Join(root, "bin", "telegram-codex-bridge"),
		BridgeControl:   filepath.Join(root, "bin", "telegram-codex-bridge"),
		EnvFile:         filepath.Join(root, ".env"),
		StatePath:       filepath.Join(root, "data", "bridge.db"),
		LogsDir:         filepath.Join(root, "data", "logs"),
		StdoutLog:       filepath.Join(root, "data", "logs", "bridge.stdout.log"),
		StderrLog:       filepath.Join(root, "data", "logs", "bridge.stderr.log"),
		LaunchAgentsDir: filepath.Join(home, "Library", "LaunchAgents"),
		PlistPath:       filepath.Join(home, "Library", "LaunchAgents", DefaultServiceLabel+".plist"),
	}, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	status := Status{
		Label: s.label,
		Paths: s.paths,
	}

	autoStart, err := s.readAutoStart()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	status.AutoStart = autoStart

	if _, err := os.Stat(s.paths.PlistPath); err == nil {
		status.Installed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("stat plist: %w", err)
	}

	stdout, stderr, err := s.runLaunchctl(ctx, "print", s.serviceTarget())
	if err != nil {
		if isServiceMissing(stderr) || isServiceMissing(stdout) {
			if unmanagedPID, ok := s.findUnmanagedPID(ctx); ok {
				status.Running = true
				status.UnmanagedPID = unmanagedPID
				status.Description = fmt.Sprintf("running outside launchd with pid %d", unmanagedPID)
				return status, nil
			}

			status.Description = "service not loaded"
			return status, nil
		}
		return status, fmt.Errorf("inspect launchd service: %w (%s)", err, strings.TrimSpace(stderr))
	}

	status.Loaded = true
	status.PID = parseKeyInt(stdout, `pid = (\d+)`)
	status.Running = status.PID > 0
	if lastExit, ok := parseOptionalInt(stdout, `last exit code = (\d+)`); ok {
		status.LastExit = &lastExit
	}
	if status.Running {
		status.Description = fmt.Sprintf("running with pid %d", status.PID)
	} else {
		status.Description = "loaded but idle"
	}

	return status, nil
}

func (s *Service) SetAutoStart(ctx context.Context, enabled bool) error {
	_ = ctx

	if err := s.writeLaunchAgent(enabled); err != nil {
		return err
	}

	return nil
}

func (s *Service) Start(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if status.Running && !status.Loaded && status.UnmanagedPID > 0 {
		return fmt.Errorf("bridge is already running outside launchd with pid %d", status.UnmanagedPID)
	}

	if !status.Installed {
		if err := s.writeLaunchAgent(false); err != nil {
			return err
		}
	}

	if !status.Loaded {
		if err := s.bootstrap(ctx); err != nil {
			return err
		}
	}

	return s.kickstart(ctx)
}

func (s *Service) Stop(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if !status.Loaded {
		return nil
	}
	return s.bootout(ctx)
}

func (s *Service) Restart(ctx context.Context) error {
	status, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if status.Running && !status.Loaded && status.UnmanagedPID > 0 {
		return fmt.Errorf("bridge is already running outside launchd with pid %d", status.UnmanagedPID)
	}

	if !status.Installed {
		if err := s.writeLaunchAgent(false); err != nil {
			return err
		}
	}

	if !status.Loaded {
		if err := s.bootstrap(ctx); err != nil {
			return err
		}
	}

	return s.kickstart(ctx)
}

func (s *Service) Remove(ctx context.Context) error {
	_ = s.bootout(ctx)
	if err := os.Remove(s.paths.PlistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

func (s *Service) StopUnmanaged(ctx context.Context) error {
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

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop unmanaged process %d: %w", status.UnmanagedPID, err)
	}

	return nil
}

func (s *Service) Paths() Paths {
	return s.paths
}

func (s *Service) StatusJSON(ctx context.Context) ([]byte, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(status, "", "  ")
}

func (s *Service) writeLaunchAgent(autoStart bool) error {
	if err := os.MkdirAll(s.paths.LaunchAgentsDir, 0o755); err != nil {
		return fmt.Errorf("create launch agents directory: %w", err)
	}
	if err := os.MkdirAll(s.paths.LogsDir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	if _, err := os.Stat(s.paths.BridgeBinary); err != nil {
		return fmt.Errorf("bridge binary is missing: %w", err)
	}

	content := renderLaunchAgent(launchAgentSpec{
		Label:                       s.label,
		Program:                     s.paths.BridgeBinary,
		WorkingDir:                  s.paths.ProjectRoot,
		StdoutLog:                   s.paths.StdoutLog,
		StderrLog:                   s.paths.StderrLog,
		RunAtLoad:                   autoStart,
		KeepAlive:                   true,
		ProcessType:                 "Interactive",
		AssociatedBundleIdentifiers: []string{DefaultAppBundleID},
	})

	if err := os.WriteFile(s.paths.PlistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	return nil
}

func (s *Service) readAutoStart() (bool, error) {
	content, err := os.ReadFile(s.paths.PlistPath)
	if err != nil {
		return false, err
	}

	switch {
	case bytes.Contains(content, []byte("<key>RunAtLoad</key>\n\t<true/>")):
		return true, nil
	case bytes.Contains(content, []byte("<key>RunAtLoad</key>\n\t<false/>")):
		return false, nil
	default:
		return false, nil
	}
}

func (s *Service) bootstrap(ctx context.Context) error {
	_, stderr, err := s.runLaunchctl(ctx, "bootstrap", s.domainTarget(), s.paths.PlistPath)
	if err != nil {
		if strings.Contains(stderr, "already bootstrapped") {
			return nil
		}
		return fmt.Errorf("bootstrap launch agent: %w (%s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *Service) bootout(ctx context.Context) error {
	_, stderr, err := s.runLaunchctl(ctx, "bootout", s.serviceTarget())
	if err != nil {
		if isServiceMissing(stderr) {
			return nil
		}
		return fmt.Errorf("bootout launch agent: %w (%s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *Service) kickstart(ctx context.Context) error {
	_, stderr, err := s.runLaunchctl(ctx, "kickstart", "-k", s.serviceTarget())
	if err != nil {
		return fmt.Errorf("kickstart launch agent: %w (%s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *Service) runLaunchctl(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (s *Service) domainTarget() string {
	return "gui/" + s.uid
}

func (s *Service) serviceTarget() string {
	return s.domainTarget() + "/" + s.label
}

func parseKeyInt(source, pattern string) int {
	value, ok := parseOptionalInt(source, pattern)
	if !ok {
		return 0
	}
	return value
}

func parseOptionalInt(source, pattern string) (int, bool) {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(source)
	if len(matches) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

func isServiceMissing(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.Contains(trimmed, "Could not find service") || strings.Contains(trimmed, "service not loaded")
}

func (s *Service) findUnmanagedPID(ctx context.Context) (int, bool) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=")
	output, err := cmd.Output()
	if err != nil {
		return 0, false
	}

	target := filepath.Clean(s.paths.BridgeBinary)
	for _, rawLine := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if command == "" {
			continue
		}
		exe := firstCommandToken(command)
		if exe == "" {
			continue
		}
		exe = filepath.Clean(exe)
		if exe == target {
			return pid, true
		}
	}

	return 0, false
}

func firstCommandToken(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.HasPrefix(command, "\"") {
		end := strings.Index(command[1:], "\"")
		if end >= 0 {
			return command[1 : end+1]
		}
		return strings.Trim(command, "\"")
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
