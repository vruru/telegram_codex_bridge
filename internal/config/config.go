package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Telegram TelegramConfig
	Codex    CodexConfig
	Store    StoreConfig
	Log      LogConfig
	Power    PowerConfig
	Language string
	EnvPath  string
}

type TelegramConfig struct {
	BotToken           string
	AllowedUserIDs     []int64
	AllowedChatIDs     []int64
	APIBaseURL         string
	PollTimeoutSeconds int
}

type CodexConfig struct {
	BinaryPath     string
	Provider       string
	AdapterMode    string
	WorkspaceRoot  string
	PermissionMode string
	GeminiModels   []string
	GeminiDefault  string
}

type StoreConfig struct {
	StatePath string
}

type LogConfig struct {
	FilePath   string
	Level      string
	MaxSizeMB  int
	MaxBackups int
}

type PowerConfig struct {
	PreventSleep bool
}

func LoadFromEnv() (Config, error) {
	if err := loadDotEnvIfPresent(".env"); err != nil {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("resolve working directory: %w", err)
	}

	cfg := Config{
		Telegram: TelegramConfig{
			BotToken:           strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
			AllowedUserIDs:     parseInt64List(os.Getenv("TELEGRAM_ALLOWED_USER_IDS")),
			AllowedChatIDs:     parseInt64List(os.Getenv("TELEGRAM_ALLOWED_CHAT_IDS")),
			APIBaseURL:         defaultString(strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE_URL")), "https://api.telegram.org"),
			PollTimeoutSeconds: defaultInt(strings.TrimSpace(os.Getenv("TELEGRAM_POLL_TIMEOUT_SECONDS")), 30),
		},
		Codex: CodexConfig{
			BinaryPath:     defaultString(strings.TrimSpace(os.Getenv("CODEX_BIN")), "codex"),
			Provider:       normalizeProvider(strings.TrimSpace(os.Getenv("CODEX_PROVIDER"))),
			AdapterMode:    normalizeAdapterMode(strings.TrimSpace(os.Getenv("CODEX_ADAPTER"))),
			WorkspaceRoot:  defaultString(strings.TrimSpace(os.Getenv("CODEX_WORKSPACE_ROOT")), wd),
			PermissionMode: normalizePermissionMode(strings.TrimSpace(os.Getenv("CODEX_PERMISSION_MODE"))),
			GeminiModels:   parseStringList(defaultString(strings.TrimSpace(os.Getenv("GEMINI_MODELS")), "gemini-2.5-flash,gemini-2.5-flash-lite,gemini-2.5-pro,gemini-3-flash-preview,gemini-3.1-pro-preview")),
			GeminiDefault:  defaultString(strings.TrimSpace(os.Getenv("GEMINI_DEFAULT_MODEL")), "gemini-2.5-flash"),
		},
		Store: StoreConfig{
			StatePath: defaultString(strings.TrimSpace(os.Getenv("BRIDGE_STATE_PATH")), filepath.Join(wd, "data", "bridge.db")),
		},
		Log: LogConfig{
			FilePath:   defaultString(strings.TrimSpace(os.Getenv("BRIDGE_LOG_PATH")), filepath.Join(wd, "data", "logs", "bridge.stdout.log")),
			Level:      normalizeLogLevel(strings.TrimSpace(os.Getenv("BRIDGE_LOG_LEVEL"))),
			MaxSizeMB:  defaultInt(strings.TrimSpace(os.Getenv("BRIDGE_LOG_MAX_SIZE_MB")), 20),
			MaxBackups: defaultInt(strings.TrimSpace(os.Getenv("BRIDGE_LOG_MAX_BACKUPS")), 5),
		},
		Power: PowerConfig{
			PreventSleep: defaultBool(strings.TrimSpace(os.Getenv("BRIDGE_PREVENT_SLEEP")), true),
		},
		Language: normalizeLanguagePreference(strings.TrimSpace(os.Getenv("BRIDGE_LANGUAGE"))),
		EnvPath:  filepath.Join(wd, ".env"),
	}

	if strings.TrimSpace(os.Getenv("CODEX_BIN")) == "" && cfg.Codex.Provider == "gemini" {
		cfg.Codex.BinaryPath = "gemini"
	}

	if cfg.Telegram.BotToken == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}

	info, err := os.Stat(cfg.Codex.WorkspaceRoot)
	if err != nil {
		return Config{}, fmt.Errorf("CODEX_WORKSPACE_ROOT is not accessible: %w", err)
	}
	if !info.IsDir() {
		return Config{}, errors.New("CODEX_WORKSPACE_ROOT must point to a directory")
	}

	return cfg, nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}

	return parsed
}

func defaultBool(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseInt64List(raw string) []int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			continue
		}
		values = append(values, value)
	}

	return values
}

func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		values = append(values, part)
	}
	return values
}

func normalizeProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gemini", "gemini-cli", "gemini_cli":
		return "gemini"
	default:
		return "codex"
	}
}

func normalizeLanguagePreference(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "zh", "zh-cn", "zh-hans", "zh-hant", "cn", "中文", "chinese":
		return "zh"
	case "en", "en-us", "en-gb", "english":
		return "en"
	default:
		return "auto"
	}
}

func normalizePermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "full", "full-access", "danger", "danger-full-access":
		return "full-access"
	default:
		return "default"
	}
}

func normalizeAdapterMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cli":
		return "cli"
	case "app-server", "appserver", "server":
		return "app-server"
	default:
		return "auto"
	}
}

func normalizeLogLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return "debug"
	default:
		return "info"
	}
}

func ReadEnvValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
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
		return strings.TrimSpace(value), true, nil
	}

	if err := scanner.Err(); err != nil {
		return "", false, err
	}

	return "", false, nil
}

func UpsertEnvValue(path, key, value string) error {
	lines := []string{}
	if body, err := os.ReadFile(path); err == nil {
		text := string(body)
		lines = strings.Split(text, "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	updated := false
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		currentKey, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(currentKey) != key {
			continue
		}
		lines[idx] = key + "=" + value
		updated = true
	}

	if !updated {
		lines = append(lines, key+"="+value)
	}

	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func loadDotEnvIfPresent(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}

		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from .env: %w", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}
