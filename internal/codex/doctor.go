package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Health struct {
	ConfiguredBinary string `json:"configured_binary"`
	ResolvedBinary   string `json:"resolved_binary,omitempty"`
	AppDetected      bool   `json:"app_detected"`
	Found            bool   `json:"found"`
	Version          string `json:"version,omitempty"`
	LoggedIn         bool   `json:"logged_in"`
	LoginStatus      string `json:"login_status,omitempty"`
	Ready            bool   `json:"ready"`
	Error            string `json:"error,omitempty"`
}

func CheckHealth(ctx context.Context, binary string) Health {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}

	health := Health{
		ConfiguredBinary: binary,
	}

	resolved, err := resolveBinary(binary)
	if err != nil {
		health.Error = fmt.Sprintf("codex binary not found: %v", err)
		return health
	}

	health.ResolvedBinary = resolved
	health.AppDetected = strings.Contains(resolved, "/Codex.app/")
	health.Found = true

	if version, err := runTrimmed(ctx, resolved, "--version"); err == nil {
		health.Version = version
	}

	loginStatus, err := runTrimmed(ctx, resolved, "login", "status")
	if err != nil {
		health.LoginStatus = chooseNonEmpty(loginStatus, err.Error())
		health.Error = chooseNonEmpty(health.Error, "codex is installed but not logged in")
		return health
	}

	health.LoggedIn = true
	health.LoginStatus = loginStatus
	health.Ready = true
	return health
}

func resolveBinary(binary string) (string, error) {
	trimmed := strings.TrimSpace(binary)
	if trimmed == "" {
		trimmed = "codex"
	}

	if strings.Contains(trimmed, "/") {
		if isExecutableFile(trimmed) {
			return trimmed, nil
		}
		return "", fmt.Errorf("%s is not executable", trimmed)
	}

	if resolved, err := exec.LookPath(trimmed); err == nil {
		return resolved, nil
	}

	for _, candidate := range codexCandidates() {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", trimmed)
}

func codexCandidates() []string {
	var candidates []string

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex"),
		)
	}

	candidates = append(candidates,
		"/Applications/Codex.app/Contents/Resources/codex",
	)

	return candidates
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode().Perm()&0o111 != 0
}

func runTrimmed(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if err != nil {
		return output, errors.New(strings.TrimSpace(chooseNonEmpty(stderr.String(), err.Error())))
	}
	return output, nil
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
