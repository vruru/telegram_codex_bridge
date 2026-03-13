package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"telegram-codex-bridge/internal/config"
)

type RunMode string

const (
	RunModeStart  RunMode = "start"
	RunModeResume RunMode = "resume"
)

type StreamEventKind string

const (
	StreamEventThreadStarted StreamEventKind = "thread_started"
	StreamEventStatus        StreamEventKind = "status"
	StreamEventToolUse       StreamEventKind = "tool_use"
	StreamEventText          StreamEventKind = "text"
)

type StreamEvent struct {
	Kind      StreamEventKind
	SessionID string
	TurnID    string
	Text      string
	ToolName  string
}

type StreamHandler func(StreamEvent)

type ExecutionStats struct {
	Mode        RunMode
	StartedAt   time.Time
	CompletedAt time.Time
	Duration    time.Duration
}

type Thread struct {
	SessionID string
	Provider  string
	Name      string
	Workspace string
}

type RunSettings struct {
	Provider        string
	Model           string
	ReasoningEffort string
	ServiceTier     string
}

type StartThreadResult struct {
	Thread Thread
	Reply  string
	Stats  ExecutionStats
}

type StartThreadRequest struct {
	Name       string
	Prompt     string
	Workspace  string
	Settings   RunSettings
	ImagePaths []string
}

type ResumeThreadResult struct {
	SessionID string
	Provider  string
	Reply     string
	Stats     ExecutionStats
}

type ResumeThreadRequest struct {
	SessionID  string
	Message    string
	Workspace  string
	Settings   RunSettings
	ImagePaths []string
}

type CLIClient struct {
	mu  sync.RWMutex
	cfg config.CodexConfig
}

func NewCLIClient(cfg config.CodexConfig) *CLIClient {
	return &CLIClient{cfg: cfg}
}

func (c *CLIClient) Provider() string {
	return "codex"
}

func (c *CLIClient) WorkspaceRoot() string {
	return c.cfg.WorkspaceRoot
}

func (c *CLIClient) PermissionMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.PermissionMode
}

func (c *CLIClient) SetPermissionMode(mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.PermissionMode = normalizePermissionMode(mode)
}

func (c *CLIClient) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	_ = provider
	return c.SettingsCatalog()
}

func (c *CLIClient) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	args := c.commandArgs(workspace, "exec")
	args = c.applyRunSettings(args, req.Settings)
	args = c.applyImagePaths(args, req.ImagePaths)
	args = append(args,
		"--json",
		"--color",
		"never",
		"--skip-git-repo-check",
	)
	reply, sessionID, stats, err := c.run(ctx, RunModeStart, workspace, args, req.Prompt, handler)
	if err != nil {
		return StartThreadResult{}, err
	}

	if sessionID == "" {
		return StartThreadResult{}, fmt.Errorf("codex did not return a session id for new thread %q", req.Name)
	}

	return StartThreadResult{
		Thread: Thread{
			SessionID: sessionID,
			Provider:  c.Provider(),
			Name:      req.Name,
			Workspace: workspace,
		},
		Reply: reply,
		Stats: stats,
	}, nil
}

func (c *CLIClient) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	args := c.commandArgs(workspace, "exec", "resume")
	args = c.applyRunSettings(args, req.Settings)
	args = c.applyImagePaths(args, req.ImagePaths)
	args = append(args,
		"--json",
		"--skip-git-repo-check",
		req.SessionID,
	)
	reply, sessionID, stats, err := c.run(ctx, RunModeResume, workspace, args, req.Message, handler)
	if err != nil {
		return ResumeThreadResult{}, err
	}

	if sessionID == "" {
		sessionID = req.SessionID
	}

	return ResumeThreadResult{
		SessionID: sessionID,
		Provider:  c.Provider(),
		Reply:     reply,
		Stats:     stats,
	}, nil
}

func (c *CLIClient) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	_ = ctx
	_ = req
	return ErrSteeringUnsupported
}

func (c *CLIClient) ArchiveThread(ctx context.Context, sessionID string) error {
	_ = ctx
	_ = sessionID
	return nil
}

func (c *CLIClient) resolveWorkspace(workspace string) string {
	if strings.TrimSpace(workspace) != "" {
		return workspace
	}
	return c.cfg.WorkspaceRoot
}

func (c *CLIClient) commandArgs(workspace string, args ...string) []string {
	c.mu.RLock()
	permissionMode := normalizePermissionMode(c.cfg.PermissionMode)
	root := strings.TrimSpace(c.cfg.WorkspaceRoot)
	c.mu.RUnlock()

	finalArgs := []string{
		"-C",
		workspace,
	}
	if sharedRoot := sharedWorkspaceRoot(root, workspace); sharedRoot != "" {
		finalArgs = append(finalArgs, "--add-dir", sharedRoot)
	}
	switch permissionMode {
	case "full-access":
		finalArgs = append(finalArgs, "--dangerously-bypass-approvals-and-sandbox")
	}
	finalArgs = append(finalArgs, args...)
	return finalArgs
}

func (c *CLIClient) applyRunSettings(args []string, settings RunSettings) []string {
	if model := strings.TrimSpace(settings.Model); model != "" {
		args = append(args, "-m", model)
	}
	if effort := normalizeReasoningEffort(settings.ReasoningEffort); effort != "" {
		args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort="%s"`, effort))
	}
	if tier := normalizeServiceTier(settings.ServiceTier); tier != "" {
		args = append(args, "-c", fmt.Sprintf(`service_tier="%s"`, tier))
	}
	return args
}

func (c *CLIClient) applyImagePaths(args []string, imagePaths []string) []string {
	for _, imagePath := range imagePaths {
		imagePath = strings.TrimSpace(imagePath)
		if imagePath == "" {
			continue
		}
		args = append(args, "-i", imagePath)
	}
	return args
}

func sharedWorkspaceRoot(root, workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if root == "" || workspace == "" {
		return ""
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	if rootAbs == workspaceAbs {
		return ""
	}

	rel, err := filepath.Rel(rootAbs, workspaceAbs)
	if err != nil {
		return ""
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rootAbs
}

func normalizePermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "full", "full-access", "danger", "danger-full-access":
		return "full-access"
	default:
		return "default"
	}
}

func (c *CLIClient) run(
	ctx context.Context,
	mode RunMode,
	workspace string,
	args []string,
	prompt string,
	handler StreamHandler,
) (string, string, ExecutionStats, error) {
	stats := ExecutionStats{
		Mode:      mode,
		StartedAt: time.Now().UTC(),
	}

	lastMessageFile, err := os.CreateTemp("", "telegram-codex-bridge-*.txt")
	if err != nil {
		return "", "", stats, fmt.Errorf("create output file: %w", err)
	}
	lastMessagePath := lastMessageFile.Name()
	if err := lastMessageFile.Close(); err != nil {
		return "", "", stats, fmt.Errorf("close output file: %w", err)
	}
	defer os.Remove(lastMessagePath)

	finalArgs := append([]string{}, args...)
	finalArgs = append(finalArgs, "-o", lastMessagePath, prompt)

	cmd := exec.CommandContext(ctx, c.cfg.BinaryPath, finalArgs...)
	cmd.Dir = workspace

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", stats, fmt.Errorf("open codex stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", stats, fmt.Errorf("open codex stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", stats, fmt.Errorf("start codex in %s: %w", workspace, err)
	}

	var stderrBuf bytes.Buffer
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&stderrBuf, stderrPipe)
		stderrDone <- copyErr
	}()

	state := newStreamState(handler)
	var stdoutBuf bytes.Buffer

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		stdoutBuf.WriteString(line)
		stdoutBuf.WriteByte('\n')
		state.consumeLine(line)
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		return "", "", stats, fmt.Errorf("scan codex stdout: %w", err)
	}

	stderrCopyErr := <-stderrDone
	if stderrCopyErr != nil {
		return "", "", stats, fmt.Errorf("read codex stderr: %w", stderrCopyErr)
	}

	waitErr := cmd.Wait()
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)

	if waitErr != nil {
		return "", "", stats, fmt.Errorf(
			"run codex in %s: %w (%s)",
			workspace,
			waitErr,
			summarizeFailure(stdoutBuf.String(), stderrBuf.String()),
		)
	}

	if state.failureText != "" {
		return "", "", stats, fmt.Errorf("codex turn failed: %s", state.failureText)
	}

	reply, err := os.ReadFile(filepath.Clean(lastMessagePath))
	if err != nil {
		return "", "", stats, fmt.Errorf("read codex last message: %w", err)
	}

	replyText := strings.TrimSpace(string(reply))
	if replyText == "" {
		replyText = strings.TrimSpace(state.finalText)
	}

	return replyText, state.sessionID, stats, nil
}

type streamState struct {
	handler     StreamHandler
	sessionID   string
	finalText   string
	failureText string
}

func newStreamState(handler StreamHandler) *streamState {
	return &streamState{handler: handler}
}

func (s *streamState) consumeLine(line string) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return
	}

	eventType, _ := payload["type"].(string)
	switch eventType {
	case "thread.started":
		if sessionID, _ := payload["thread_id"].(string); strings.TrimSpace(sessionID) != "" {
			s.sessionID = strings.TrimSpace(sessionID)
			s.emit(StreamEvent{
				Kind:      StreamEventThreadStarted,
				SessionID: s.sessionID,
			})
		}
	case "turn.started":
		s.emit(StreamEvent{
			Kind: StreamEventStatus,
			Text: "Codex 已开始处理",
		})
	case "turn.failed":
		s.failureText = extractFailure(payload)
		if s.failureText == "" {
			s.failureText = "unknown failure"
		}
	case "item.started", "item.updated", "item.completed":
		s.consumeItemEvent(eventType, payload)
	}

	if s.sessionID == "" {
		if fallback := findStringField(payload, "session_id", "thread_id"); fallback != "" {
			s.sessionID = fallback
		}
	}
}

func (s *streamState) consumeItemEvent(eventType string, payload map[string]any) {
	item, _ := payload["item"].(map[string]any)
	if len(item) == 0 {
		return
	}

	itemType, _ := item["type"].(string)
	switch itemType {
	case "agent_message":
		if eventType != "item.completed" {
			return
		}
		text, _ := item["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		s.finalText = text
		s.emit(StreamEvent{
			Kind: StreamEventText,
			Text: text,
		})
	case "reasoning":
		if eventType == "item.started" {
			s.emit(StreamEvent{
				Kind: StreamEventStatus,
				Text: "Codex 正在分析上下文",
			})
		}
	default:
		if eventType == "item.started" {
			if toolName := toolNameForItemType(itemType, item); toolName != "" {
				s.emit(StreamEvent{
					Kind:     StreamEventToolUse,
					ToolName: toolName,
					Text:     "Codex 正在使用 " + toolName,
				})
			}
		}
	}
}

func (s *streamState) emit(event StreamEvent) {
	if s.handler == nil {
		return
	}
	s.handler(event)
}

func toolNameForItemType(itemType string, item map[string]any) string {
	switch itemType {
	case "mcp_tool_call":
		if name, _ := item["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		if name, _ := item["tool_name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return "MCP"
	case "command_execution":
		return "Bash"
	case "file_change":
		return "Edit"
	case "web_search":
		return "WebSearch"
	case "todo_list":
		return "TodoWrite"
	default:
		return ""
	}
}

func extractFailure(payload map[string]any) string {
	errorPayload, _ := payload["error"].(map[string]any)
	if message, _ := errorPayload["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if message, _ := payload["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	return ""
}

func findStringField(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if raw, ok := typed[key]; ok {
				if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
		for _, nested := range typed {
			if found := findStringField(nested, keys...); found != "" {
				return found
			}
		}
	case []any:
		for _, nested := range typed {
			if found := findStringField(nested, keys...); found != "" {
				return found
			}
		}
	}

	return ""
}

func summarizeFailure(stdout, stderr string) string {
	combined := strings.TrimSpace(stderr)
	if combined == "" {
		combined = strings.TrimSpace(stdout)
	}
	if combined == "" {
		return "no CLI output"
	}
	if len(combined) > 500 {
		return combined[:500] + "..."
	}
	return combined
}
