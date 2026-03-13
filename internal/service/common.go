package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func findUnmanagedPID(ctx context.Context, bridgeBinary string) (int, bool) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=")
	output, err := cmd.Output()
	if err != nil {
		return 0, false
	}

	target := filepath.Clean(bridgeBinary)
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
		if filepath.Clean(exe) == target {
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
