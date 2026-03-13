package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"telegram-codex-bridge/internal/buildinfo"
	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/config"
	"telegram-codex-bridge/internal/service"
	"telegram-codex-bridge/internal/telegram"
	"telegram-codex-bridge/internal/transcribe"
)

func IsCommand(raw []string) bool {
	if len(raw) == 0 {
		return false
	}

	switch strings.TrimSpace(raw[0]) {
	case "status", "version", "limits", "codex", "telegram-check", "whisper-status", "install-whisper", "paths", "start", "stop", "restart", "set-autostart", "stop-unmanaged", "remove", "help":
		return true
	default:
		return false
	}
}

func Run(raw []string) error {
	command, args := parseCommand(raw)
	svc, err := service.New(args.projectRoot)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(command))
	defer cancel()

	switch command {
	case "status":
		status, err := svc.Status(ctx)
		if err != nil {
			return err
		}
		codexCfg := configuredCodexConfig(status.Paths.ProjectRoot)
		snapshot := combinedStatus{
			Status:         status,
			Version:        buildinfo.DisplayVersion(),
			Codex:          codex.CheckHealth(ctx, codexCfg),
			Provider:       codexCfg.Provider,
			PermissionMode: configuredPermissionMode(status.Paths.ProjectRoot),
		}
		if args.json {
			return writeJSON(snapshot)
		}
		return printStatus(snapshot)
	case "version":
		if args.json {
			return writeJSON(map[string]string{
				"version": buildinfo.DisplayVersion(),
				"commit":  strings.TrimSpace(buildinfo.Commit),
			})
		}
		fmt.Printf("version=%s\n", buildinfo.DisplayVersion())
		if commit := strings.TrimSpace(buildinfo.Commit); commit != "" && commit != "unknown" {
			fmt.Printf("commit=%s\n", commit)
		}
		return nil
	case "limits":
		if configuredProvider(svc.Paths().ProjectRoot) != "codex" {
			return fmt.Errorf("limits are only available for the codex provider")
		}
		limits, err := codex.FetchUsageSnapshot(ctx)
		if err != nil {
			if args.json {
				return writeJSON(limits)
			}
			return printLimits(limits)
		}
		if args.json {
			return writeJSON(limits)
		}
		return printLimits(limits)
	case "codex":
		health := codex.CheckHealth(ctx, configuredCodexConfig(svc.Paths().ProjectRoot))
		if args.json {
			return writeJSON(health)
		}
		return printCodex(health)
	case "telegram-check":
		health := telegram.ValidateBotToken(ctx, "https://api.telegram.org", args.token)
		if args.json {
			return writeJSON(health)
		}
		return printTelegramHealth(health)
	case "whisper-status":
		status := transcribe.Status(ctx, svc.Paths().ProjectRoot)
		if args.json {
			return writeJSON(status)
		}
		return printWhisperStatus(status)
	case "install-whisper":
		result, err := transcribe.Install(ctx, svc.Paths().ProjectRoot)
		if args.json {
			if err != nil {
				return writeJSON(result)
			}
			return writeJSON(result)
		}
		if err != nil {
			if strings.TrimSpace(result.Output) != "" {
				return fmt.Errorf("%s", strings.TrimSpace(result.Output))
			}
			return err
		}
		return printWhisperStatus(result.Status)
	case "paths":
		if args.json {
			return writeJSON(svc.Paths())
		}
		return printPaths(svc.Paths())
	case "start":
		return svc.Start(ctx)
	case "stop":
		return svc.Stop(ctx)
	case "restart":
		return svc.Restart(ctx)
	case "set-autostart":
		if args.value == "" {
			return fmt.Errorf("set-autostart requires on or off")
		}
		return svc.SetAutoStart(ctx, args.value == "on")
	case "stop-unmanaged":
		return svc.StopUnmanaged(ctx)
	case "remove":
		return svc.Remove(ctx)
	case "help":
		fallthrough
	default:
		printUsage()
		return nil
	}
}

func commandTimeout(command string) time.Duration {
	switch strings.TrimSpace(command) {
	case "install-whisper":
		return 45 * time.Minute
	default:
		return 20 * time.Second
	}
}

type commandArgs struct {
	projectRoot string
	json        bool
	value       string
	token       string
}

type combinedStatus struct {
	service.Status
	Version        string       `json:"version"`
	Codex          codex.Health `json:"codex"`
	Provider       string       `json:"provider"`
	PermissionMode string       `json:"permission_mode"`
}

func parseCommand(raw []string) (string, commandArgs) {
	args := commandArgs{}
	command := "help"

	if len(raw) > 0 {
		command = strings.TrimSpace(raw[0])
		raw = raw[1:]
	}

	for i := 0; i < len(raw); i++ {
		part := raw[i]
		switch {
		case part == "--json":
			args.json = true
		case part == "--project-root" && i+1 < len(raw):
			i++
			args.projectRoot = raw[i]
		case strings.HasPrefix(part, "--project-root="):
			args.projectRoot = strings.TrimSpace(strings.TrimPrefix(part, "--project-root="))
		case part == "--token" && i+1 < len(raw):
			i++
			args.token = raw[i]
		case strings.HasPrefix(part, "--token="):
			args.token = strings.TrimSpace(strings.TrimPrefix(part, "--token="))
		default:
			if command == "set-autostart" && args.value == "" {
				args.value = strings.ToLower(strings.TrimSpace(part))
			}
		}
	}

	return command, args
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printStatus(status combinedStatus) error {
	fmt.Printf("label=%s\n", status.Label)
	if status.Version != "" {
		fmt.Printf("version=%s\n", status.Version)
	}
	fmt.Printf("installed=%t\n", status.Installed)
	fmt.Printf("loaded=%t\n", status.Loaded)
	fmt.Printf("running=%t\n", status.Running)
	fmt.Printf("auto_start=%t\n", status.AutoStart)
	if status.PID > 0 {
		fmt.Printf("pid=%d\n", status.PID)
	}
	if status.LastExit != nil {
		fmt.Printf("last_exit=%d\n", *status.LastExit)
	}
	if status.Description != "" {
		fmt.Printf("description=%s\n", status.Description)
	}
	fmt.Printf("project_root=%s\n", status.Paths.ProjectRoot)
	fmt.Printf("provider=%s\n", status.Provider)
	if runtime.GOOS == "linux" {
		fmt.Printf("systemd_user_dir=%s\n", status.Paths.LaunchAgentsDir)
		fmt.Printf("systemd_unit=%s\n", status.Paths.PlistPath)
	} else {
		fmt.Printf("plist=%s\n", status.Paths.PlistPath)
	}
	fmt.Printf("stdout_log=%s\n", status.Paths.StdoutLog)
	fmt.Printf("stderr_log=%s\n", status.Paths.StderrLog)
	fmt.Printf("codex_found=%t\n", status.Codex.Found)
	fmt.Printf("codex_ready=%t\n", status.Codex.Ready)
	if status.Codex.ResolvedBinary != "" {
		fmt.Printf("codex_binary=%s\n", status.Codex.ResolvedBinary)
	}
	if status.Codex.Version != "" {
		fmt.Printf("codex_version=%s\n", status.Codex.Version)
	}
	if status.Codex.LoginStatus != "" {
		fmt.Printf("codex_login=%s\n", status.Codex.LoginStatus)
	}
	fmt.Printf("permission_mode=%s\n", status.PermissionMode)
	return nil
}

func printPaths(paths service.Paths) error {
	fmt.Printf("project_root=%s\n", paths.ProjectRoot)
	fmt.Printf("bridge_binary=%s\n", paths.BridgeBinary)
	fmt.Printf("bridge_control=%s\n", paths.BridgeControl)
	fmt.Printf("env_file=%s\n", paths.EnvFile)
	fmt.Printf("state_path=%s\n", paths.StatePath)
	fmt.Printf("stdout_log=%s\n", paths.StdoutLog)
	fmt.Printf("stderr_log=%s\n", paths.StderrLog)
	if runtime.GOOS == "linux" {
		fmt.Printf("systemd_user_dir=%s\n", paths.LaunchAgentsDir)
		fmt.Printf("systemd_unit=%s\n", paths.PlistPath)
		return nil
	}
	fmt.Printf("plist=%s\n", paths.PlistPath)
	return nil
}

func printCodex(health codex.Health) error {
	fmt.Printf("configured_binary=%s\n", health.ConfiguredBinary)
	fmt.Printf("found=%t\n", health.Found)
	fmt.Printf("ready=%t\n", health.Ready)
	if health.ResolvedBinary != "" {
		fmt.Printf("resolved_binary=%s\n", health.ResolvedBinary)
	}
	if health.Version != "" {
		fmt.Printf("version=%s\n", health.Version)
	}
	if health.LoginStatus != "" {
		fmt.Printf("login_status=%s\n", health.LoginStatus)
	}
	if health.Error != "" {
		fmt.Printf("error=%s\n", health.Error)
	}
	return nil
}

func printLimits(limits codex.UsageSnapshot) error {
	fmt.Printf("available=%t\n", limits.Available)
	if limits.PlanType != "" {
		fmt.Printf("plan_type=%s\n", limits.PlanType)
	}
	if limits.PrimaryWindow != nil {
		fmt.Printf("primary_remaining_percent=%d\n", limits.PrimaryWindow.RemainingPercent)
		fmt.Printf("primary_reset_at=%d\n", limits.PrimaryWindow.ResetAt)
	}
	if limits.SecondaryWindow != nil {
		fmt.Printf("secondary_remaining_percent=%d\n", limits.SecondaryWindow.RemainingPercent)
		fmt.Printf("secondary_reset_at=%d\n", limits.SecondaryWindow.ResetAt)
	}
	if limits.Error != "" {
		fmt.Printf("error=%s\n", limits.Error)
	}
	return nil
}

func printTelegramHealth(health telegram.TokenHealth) error {
	fmt.Printf("valid=%t\n", health.Valid)
	fmt.Printf("api_base_url=%s\n", health.APIBaseURL)
	if health.BotID > 0 {
		fmt.Printf("bot_id=%d\n", health.BotID)
	}
	if health.Username != "" {
		fmt.Printf("username=%s\n", health.Username)
	}
	if health.Error != "" {
		fmt.Printf("error=%s\n", health.Error)
	}
	return nil
}

func printWhisperStatus(status transcribe.WhisperStatus) error {
	fmt.Printf("available=%t\n", status.Available)
	fmt.Printf("installed=%t\n", status.Installed)
	if status.PythonPath != "" {
		fmt.Printf("python_path=%s\n", status.PythonPath)
	}
	if status.WhisperPath != "" {
		fmt.Printf("whisper_path=%s\n", status.WhisperPath)
	}
	if status.FFmpegPath != "" {
		fmt.Printf("ffmpeg_path=%s\n", status.FFmpegPath)
	}
	if status.Version != "" {
		fmt.Printf("version=%s\n", status.Version)
	}
	if status.Source != "" {
		fmt.Printf("source=%s\n", status.Source)
	}
	if status.InstallRoot != "" {
		fmt.Printf("install_root=%s\n", status.InstallRoot)
	}
	if status.Model != "" {
		fmt.Printf("model=%s\n", status.Model)
	}
	if status.Error != "" {
		fmt.Printf("error=%s\n", status.Error)
	}
	return nil
}

func userVisibleCodexHealth(health codex.Health) string {
	if health.Error != "" {
		return health.Error
	}
	if health.LoginStatus != "" {
		return health.LoginStatus
	}
	return "unknown codex health error"
}

func printUsage() {
	fmt.Println(strings.TrimSpace(`
usage:
  telegram-codex-bridge status [--json] [--project-root PATH]
  telegram-codex-bridge version [--json]
  telegram-codex-bridge limits [--json]
  telegram-codex-bridge codex [--json] [--project-root PATH]
  telegram-codex-bridge telegram-check --token TOKEN [--json]
  telegram-codex-bridge whisper-status [--json] [--project-root PATH]
  telegram-codex-bridge install-whisper [--json] [--project-root PATH]
  telegram-codex-bridge paths [--json] [--project-root PATH]
  telegram-codex-bridge start [--project-root PATH]
  telegram-codex-bridge stop [--project-root PATH]
  telegram-codex-bridge restart [--project-root PATH]
  telegram-codex-bridge set-autostart on|off [--project-root PATH]
  telegram-codex-bridge stop-unmanaged [--project-root PATH]
  telegram-codex-bridge remove [--project-root PATH]
  telegram-codex-bridge serve
`))
}

func configuredCodexBinary(projectRoot string) string {
	if value, ok := configuredEnvValue(projectRoot, "CODEX_BIN"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if configuredProvider(projectRoot) == "gemini" {
		return "gemini"
	}
	return "codex"
}

func configuredProvider(projectRoot string) string {
	if value, ok := configuredEnvValue(projectRoot, "CODEX_PROVIDER"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "gemini", "gemini-cli", "gemini_cli":
			return "gemini"
		}
	}
	return "codex"
}

func configuredCodexConfig(projectRoot string) config.CodexConfig {
	return config.CodexConfig{
		BinaryPath:     configuredCodexBinary(projectRoot),
		Provider:       configuredProvider(projectRoot),
		PermissionMode: configuredPermissionMode(projectRoot),
		WorkspaceRoot:  configuredWorkspaceRoot(projectRoot),
	}
}

func configuredPermissionMode(projectRoot string) string {
	if value, ok := configuredEnvValue(projectRoot, "CODEX_PERMISSION_MODE"); ok {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "full" || value == "full-access" || value == "danger" || value == "danger-full-access" {
			return "full-access"
		}
	}
	return "default"
}

func configuredWorkspaceRoot(projectRoot string) string {
	if value, ok := configuredEnvValue(projectRoot, "CODEX_WORKSPACE_ROOT"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return projectRoot
}

func configuredEnvValue(projectRoot, key string) (string, bool) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", false
	}

	path := filepath.Join(projectRoot, ".env")
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		currentKey, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(currentKey) != key {
			continue
		}

		value = strings.TrimSpace(value)
		return value, value != ""
	}

	return "", false
}
