package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"telegram-codex-bridge/internal/config"
)

type GeminiCLIClient struct {
	mu  sync.RWMutex
	cfg config.CodexConfig
}

func NewGeminiCLIClient(cfg config.CodexConfig) *GeminiCLIClient {
	return &GeminiCLIClient{cfg: cfg}
}

func (c *GeminiCLIClient) Provider() string {
	return "gemini"
}

func (c *GeminiCLIClient) WorkspaceRoot() string {
	return c.cfg.WorkspaceRoot
}

func (c *GeminiCLIClient) PermissionMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.PermissionMode
}

func (c *GeminiCLIClient) SetPermissionMode(mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.PermissionMode = normalizePermissionMode(mode)
}

func (c *GeminiCLIClient) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	reply, sessionID, stats, err := c.run(ctx, RunModeStart, workspace, req.Settings, req.Prompt, req.ImagePaths, "", handler)
	if err != nil {
		return StartThreadResult{}, err
	}
	if sessionID == "" {
		return StartThreadResult{}, fmt.Errorf("gemini did not return a session id for new thread %q", req.Name)
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

func (c *GeminiCLIClient) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	reply, sessionID, stats, err := c.run(ctx, RunModeResume, workspace, req.Settings, req.Message, req.ImagePaths, req.SessionID, handler)
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

func (c *GeminiCLIClient) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	_ = ctx
	_ = req
	return ErrSteeringUnsupported
}

func (c *GeminiCLIClient) ArchiveThread(ctx context.Context, sessionID string) error {
	_ = ctx
	_ = sessionID
	return nil
}

func (c *GeminiCLIClient) SettingsCatalog() (SettingsCatalog, error) {
	c.mu.RLock()
	models := append([]string(nil), c.cfg.GeminiModels...)
	defaultModel := strings.TrimSpace(c.cfg.GeminiDefault)
	c.mu.RUnlock()

	if len(models) == 0 && defaultModel != "" {
		models = append(models, defaultModel)
	}
	if len(models) == 0 {
		models = []string{"gemini-2.5-flash"}
	}
	if defaultModel == "" {
		defaultModel = models[0]
	}

	items := make([]ModelInfo, 0, len(models))
	for idx, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		items = append(items, ModelInfo{
			Slug:                  model,
			DisplayName:           model,
			DefaultReasoningLevel: "",
			Priority:              idx,
		})
	}

	return SettingsCatalog{
		Models:                 items,
		DefaultModel:           defaultModel,
		DefaultReasoningEffort: "",
		DefaultServiceTier:     "",
		ServiceTierOptions:     []string{""},
	}, nil
}

func (c *GeminiCLIClient) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	_ = provider
	return c.SettingsCatalog()
}

func (c *GeminiCLIClient) resolveWorkspace(workspace string) string {
	if strings.TrimSpace(workspace) != "" {
		return workspace
	}
	return c.cfg.WorkspaceRoot
}

func (c *GeminiCLIClient) run(
	ctx context.Context,
	mode RunMode,
	workspace string,
	settings RunSettings,
	prompt string,
	imagePaths []string,
	sessionID string,
	handler StreamHandler,
) (string, string, ExecutionStats, error) {
	stats := ExecutionStats{
		Mode:      mode,
		StartedAt: time.Now().UTC(),
	}

	finalPrompt := augmentPromptWithImagePaths(prompt, imagePaths)
	args := c.commandArgs(workspace, settings, sessionID)
	args = append(args, "-p", finalPrompt)

	c.mu.RLock()
	binaryPath := c.cfg.BinaryPath
	c.mu.RUnlock()

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = workspace

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", stats, fmt.Errorf("open gemini stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", stats, fmt.Errorf("open gemini stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", stats, fmt.Errorf("start gemini in %s: %w", workspace, err)
	}

	var stderrBuf bytes.Buffer
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&stderrBuf, stderrPipe)
		stderrDone <- copyErr
	}()

	state := newGeminiStreamState(handler)
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
		return "", "", stats, fmt.Errorf("scan gemini stdout: %w", err)
	}

	if stderrCopyErr := <-stderrDone; stderrCopyErr != nil {
		return "", "", stats, fmt.Errorf("read gemini stderr: %w", stderrCopyErr)
	}

	waitErr := cmd.Wait()
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)

	if waitErr != nil {
		return "", "", stats, fmt.Errorf(
			"run gemini in %s: %w (%s)",
			workspace,
			waitErr,
			summarizeFailure(stdoutBuf.String(), stderrBuf.String()),
		)
	}

	if state.failureText != "" {
		return "", "", stats, fmt.Errorf("gemini turn failed: %s", state.failureText)
	}

	return strings.TrimSpace(state.reply.String()), state.sessionID, stats, nil
}

func (c *GeminiCLIClient) commandArgs(workspace string, settings RunSettings, sessionID string) []string {
	c.mu.RLock()
	permissionMode := normalizePermissionMode(c.cfg.PermissionMode)
	root := strings.TrimSpace(c.cfg.WorkspaceRoot)
	defaultModel := strings.TrimSpace(c.cfg.GeminiDefault)
	c.mu.RUnlock()

	args := []string{"--output-format", "stream-json"}
	if sharedRoot := sharedWorkspaceRoot(root, workspace); sharedRoot != "" {
		args = append(args, "--include-directories", sharedRoot)
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		args = append(args, "-m", model)
	} else if defaultModel != "" {
		args = append(args, "-m", defaultModel)
	}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(sessionID))
	}

	switch permissionMode {
	case "full-access":
		args = append(args, "--approval-mode", "yolo", "--sandbox=false")
	default:
		args = append(args, "--approval-mode", "default", "--sandbox=true")
	}

	return args
}

func augmentPromptWithImagePaths(prompt string, imagePaths []string) string {
	lines := make([]string, 0, len(imagePaths))
	for _, imagePath := range imagePaths {
		imagePath = strings.TrimSpace(imagePath)
		if imagePath == "" {
			continue
		}
		lines = append(lines, "- "+filepath.Clean(imagePath))
	}
	if len(lines) == 0 {
		return prompt
	}

	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(prompt))
	builder.WriteString("\n\nAttached local image files are available in the workspace:\n")
	builder.WriteString(strings.Join(lines, "\n"))
	builder.WriteString("\nInspect them directly if they are relevant to the task.")
	return builder.String()
}

type geminiStreamState struct {
	handler     StreamHandler
	sessionID   string
	reply       strings.Builder
	failureText string
}

func newGeminiStreamState(handler StreamHandler) *geminiStreamState {
	return &geminiStreamState{handler: handler}
}

func (s *geminiStreamState) consumeLine(line string) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return
	}

	eventType, _ := payload["type"].(string)
	switch eventType {
	case "init":
		if sessionID := findStringField(payload, "session_id"); sessionID != "" {
			s.sessionID = sessionID
			s.emit(StreamEvent{
				Kind:      StreamEventThreadStarted,
				SessionID: sessionID,
			})
		}
		s.emit(StreamEvent{
			Kind: StreamEventStatus,
			Text: "Gemini 已开始处理",
		})
	case "message":
		role, _ := payload["role"].(string)
		if role != "assistant" {
			return
		}
		content, _ := payload["content"].(string)
		if strings.TrimSpace(content) == "" {
			return
		}
		s.reply.WriteString(content)
		s.emit(StreamEvent{
			Kind: StreamEventText,
			Text: content,
		})
	case "tool_use":
		toolName, _ := payload["tool_name"].(string)
		if strings.TrimSpace(toolName) == "" {
			toolName = "Tool"
		}
		s.emit(StreamEvent{
			Kind:     StreamEventToolUse,
			ToolName: strings.TrimSpace(toolName),
			Text:     "Gemini 正在使用 " + strings.TrimSpace(toolName),
		})
	case "result":
		if status, _ := payload["status"].(string); strings.TrimSpace(status) != "success" {
			s.failureText = chooseNonEmpty(findStringField(payload, "error", "message"), "unknown failure")
		}
	case "error":
		s.failureText = chooseNonEmpty(findStringField(payload, "error", "message"), "unknown failure")
	}

	if s.sessionID == "" {
		if fallback := findStringField(payload, "session_id"); fallback != "" {
			s.sessionID = fallback
		}
	}
}

func (s *geminiStreamState) emit(event StreamEvent) {
	if s.handler == nil {
		return
	}
	s.handler(event)
}
