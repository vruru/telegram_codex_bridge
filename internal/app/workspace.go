package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"telegram-codex-bridge/internal/telegram"
)

const maxWorkspaceNameRunes = 64

func (a *App) resolveTopicWorkspace(ctx context.Context, msg telegram.IncomingUpdate) (string, error) {
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return "", fmt.Errorf("lookup topic binding for workspace: %w", err)
	}
	if found && strings.TrimSpace(binding.Workspace) != "" {
		if err := os.MkdirAll(binding.Workspace, 0o755); err != nil {
			return "", fmt.Errorf("ensure existing topic workspace %s: %w", binding.Workspace, err)
		}
		return binding.Workspace, nil
	}

	root := strings.TrimSpace(a.codex.WorkspaceRoot())
	if root == "" {
		return "", fmt.Errorf("codex workspace root is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("ensure workspace root %s: %w", root, err)
	}

	workspace, err := uniqueWorkspacePath(root, workspaceBaseName(msg))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", fmt.Errorf("create topic workspace %s: %w", workspace, err)
	}
	return workspace, nil
}

func workspaceBaseName(msg telegram.IncomingUpdate) string {
	candidates := []string{
		strings.TrimSpace(msg.TopicTitle),
		strings.TrimSpace(msg.ChatTitle),
		strings.TrimSpace(msg.Username),
		firstPromptLine(msg.Text),
	}
	for _, candidate := range candidates {
		if name := sanitizeWorkspaceName(candidate); name != "" {
			return name
		}
	}

	if msg.TopicID > 0 {
		return fmt.Sprintf("topic-%d", msg.TopicID)
	}
	if msg.ChatID > 0 {
		return fmt.Sprintf("private-%d", msg.ChatID)
	}
	return fmt.Sprintf("chat-%d", -msg.ChatID)
}

func uniqueWorkspacePath(root, baseName string) (string, error) {
	baseName = sanitizeWorkspaceName(baseName)
	if baseName == "" {
		baseName = "telegram-workspace"
	}

	for i := 0; i < 1000; i++ {
		candidateName := baseName
		if i > 0 {
			candidateName = fmt.Sprintf("%s-%d", baseName, i+1)
		}
		candidatePath := filepath.Join(root, candidateName)
		_, err := os.Stat(candidatePath)
		if err == nil {
			continue
		}
		if os.IsNotExist(err) {
			return candidatePath, nil
		}
		return "", fmt.Errorf("check workspace path %s: %w", candidatePath, err)
	}

	return "", fmt.Errorf("could not allocate unique workspace under %s for %s", root, baseName)
}

func sanitizeWorkspaceName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(raw))
	lastWasDash := false
	count := 0

	for _, r := range raw {
		if count >= maxWorkspaceNameRunes {
			break
		}

		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastWasDash = false
			count++
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if builder.Len() == 0 || lastWasDash {
				continue
			}
			builder.WriteByte('-')
			lastWasDash = true
			count++
		default:
			if builder.Len() == 0 || lastWasDash {
				continue
			}
			builder.WriteByte('-')
			lastWasDash = true
			count++
		}
	}

	return strings.Trim(builder.String(), "-")
}

func firstPromptLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if line, _, ok := strings.Cut(text, "\n"); ok {
		return strings.TrimSpace(line)
	}
	return text
}
