package app

import (
	"os"
	"path/filepath"
	"testing"

	"telegram-codex-bridge/internal/telegram"
)

func TestWorkspaceBaseNamePrefersTopicTitle(t *testing.T) {
	t.Parallel()

	msg := telegram.IncomingUpdate{
		TopicTitle: "新 项目 Alpha",
		Text:       "忽略这行",
	}

	if got := workspaceBaseName(msg); got != "新-项目-alpha" {
		t.Fatalf("workspaceBaseName() = %q, want %q", got, "新-项目-alpha")
	}
}

func TestWorkspaceBaseNameFallsBackToPrompt(t *testing.T) {
	t.Parallel()

	msg := telegram.IncomingUpdate{
		Text: "Build Project Mercury\nwith a second line",
	}

	if got := workspaceBaseName(msg); got != "build-project-mercury" {
		t.Fatalf("workspaceBaseName() = %q, want %q", got, "build-project-mercury")
	}
}

func TestUniqueWorkspacePathAddsSuffixWhenNeeded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := filepath.Join(root, "alpha")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", first, err)
	}

	got, err := uniqueWorkspacePath(root, "alpha")
	if err != nil {
		t.Fatalf("uniqueWorkspacePath(): %v", err)
	}

	want := filepath.Join(root, "alpha-2")
	if got != want {
		t.Fatalf("uniqueWorkspacePath() = %q, want %q", got, want)
	}
}

func TestSanitizeWorkspaceNameTrimsAndCollapsesSeparators(t *testing.T) {
	t.Parallel()

	got := sanitizeWorkspaceName("  Demo///Project___2026  ")
	if got != "demo-project-2026" {
		t.Fatalf("sanitizeWorkspaceName() = %q, want %q", got, "demo-project-2026")
	}
}
