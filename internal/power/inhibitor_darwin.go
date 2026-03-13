//go:build darwin

package power

import (
	"context"
	"fmt"
	"os/exec"
)

func startInhibitorProcess(ctx context.Context) (func(), error) {
	cmd := exec.CommandContext(ctx, "caffeinate", "-i", "-s")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start caffeinate: %w", err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}, nil
}
