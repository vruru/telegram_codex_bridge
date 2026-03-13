//go:build linux

package power

import (
	"context"
	"fmt"
	"os/exec"
)

func startInhibitorProcess(ctx context.Context) (func(), error) {
	cmd := exec.CommandContext(
		ctx,
		"systemd-inhibit",
		"--what=sleep",
		"--why=telegram-codex-bridge active Codex task",
		"--mode=block",
		"sh",
		"-c",
		"while :; do sleep 3600; done",
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start systemd-inhibit: %w", err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}, nil
}
