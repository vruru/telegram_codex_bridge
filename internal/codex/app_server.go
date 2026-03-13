package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"telegram-codex-bridge/internal/buildinfo"
	"telegram-codex-bridge/internal/config"
)

type AppServerClient struct {
	mu sync.RWMutex

	cfg             config.CodexConfig
	catalogFallback *CLIClient

	activeMu sync.RWMutex
	active   map[string]*appServerSession
}

func NewAppServerClient(cfg config.CodexConfig, catalogFallback *CLIClient) *AppServerClient {
	return &AppServerClient{
		cfg:             cfg,
		catalogFallback: catalogFallback,
		active:          make(map[string]*appServerSession),
	}
}

func (c *AppServerClient) WorkspaceRoot() string {
	return c.cfg.WorkspaceRoot
}

func (c *AppServerClient) Provider() string {
	return "codex"
}

func (c *AppServerClient) PermissionMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.PermissionMode
}

func (c *AppServerClient) SetPermissionMode(mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.PermissionMode = normalizePermissionMode(mode)
}

func (c *AppServerClient) SettingsCatalogFor(provider string) (SettingsCatalog, error) {
	_ = provider
	return c.SettingsCatalog()
}

func (c *AppServerClient) StartThread(ctx context.Context, req StartThreadRequest, handler StreamHandler) (StartThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	stats := ExecutionStats{
		Mode:      RunModeStart,
		StartedAt: time.Now().UTC(),
	}

	session, err := c.newSession(ctx, workspace)
	if err != nil {
		return StartThreadResult{}, err
	}
	defer session.Close()

	threadID, err := session.threadStart(ctx, workspace, req.Settings, c.PermissionMode())
	if err != nil {
		return StartThreadResult{}, err
	}
	c.registerActive(threadID, session)
	defer c.unregisterActive(threadID, session)

	if handler != nil {
		handler(StreamEvent{Kind: StreamEventThreadStarted, SessionID: threadID})
	}

	turnID, err := session.turnStart(ctx, threadID, req.Prompt, req.ImagePaths, workspace, req.Settings)
	if err != nil {
		return StartThreadResult{}, err
	}
	if handler != nil {
		handler(StreamEvent{
			Kind:      StreamEventStatus,
			SessionID: threadID,
			TurnID:    turnID,
			Text:      "Codex 已开始处理",
		})
	}

	reply, err := session.waitForTurnCompletion(ctx, threadID, handler)
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	if err != nil {
		return StartThreadResult{}, err
	}

	return StartThreadResult{
		Thread: Thread{
			SessionID: threadID,
			Provider:  c.Provider(),
			Name:      req.Name,
			Workspace: workspace,
		},
		Reply: reply,
		Stats: stats,
	}, nil
}

func (c *AppServerClient) ResumeThread(ctx context.Context, req ResumeThreadRequest, handler StreamHandler) (ResumeThreadResult, error) {
	workspace := c.resolveWorkspace(req.Workspace)
	stats := ExecutionStats{
		Mode:      RunModeResume,
		StartedAt: time.Now().UTC(),
	}

	session, err := c.newSession(ctx, workspace)
	if err != nil {
		return ResumeThreadResult{}, err
	}
	defer session.Close()

	threadID, err := session.threadResume(ctx, req.SessionID, workspace, req.Settings, c.PermissionMode())
	if err != nil {
		return ResumeThreadResult{}, err
	}
	c.registerActive(threadID, session)
	defer c.unregisterActive(threadID, session)

	if handler != nil {
		handler(StreamEvent{Kind: StreamEventThreadStarted, SessionID: threadID})
	}

	turnID, err := session.turnStart(ctx, threadID, req.Message, req.ImagePaths, workspace, req.Settings)
	if err != nil {
		return ResumeThreadResult{}, err
	}
	if handler != nil {
		handler(StreamEvent{
			Kind:      StreamEventStatus,
			SessionID: threadID,
			TurnID:    turnID,
			Text:      "Codex 已开始处理",
		})
	}

	reply, err := session.waitForTurnCompletion(ctx, threadID, handler)
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	if err != nil {
		return ResumeThreadResult{}, err
	}

	return ResumeThreadResult{
		SessionID: threadID,
		Provider:  c.Provider(),
		Reply:     reply,
		Stats:     stats,
	}, nil
}

func (c *AppServerClient) SteerThread(ctx context.Context, req SteerThreadRequest) error {
	session := c.lookupActive(req.SessionID)
	if session == nil {
		return ErrActiveTurnUnavailable
	}

	turnID := strings.TrimSpace(req.TurnID)
	if turnID == "" {
		turnID = session.TurnID()
	}
	if turnID == "" {
		return ErrActiveTurnUnavailable
	}

	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}

		if err := session.turnSteer(ctx, req.SessionID, turnID, req.Message, req.ImagePaths); err != nil {
			lastErr = err
			if strings.Contains(strings.ToLower(err.Error()), "no active turn") {
				continue
			}
			return err
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return ErrActiveTurnUnavailable
}

func (c *AppServerClient) ArchiveThread(ctx context.Context, sessionID string) error {
	workspace := c.resolveWorkspace("")
	session, err := c.newSession(ctx, workspace)
	if err != nil {
		return err
	}
	defer session.Close()

	if err := session.request(ctx, "thread/archive", map[string]any{
		"threadId": sessionID,
	}, nil); err != nil {
		return err
	}
	return nil
}

func (c *AppServerClient) SettingsCatalog() (SettingsCatalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	workspace := c.resolveWorkspace("")
	session, err := c.newSession(ctx, workspace)
	if err != nil {
		if c.catalogFallback != nil {
			return c.catalogFallback.SettingsCatalog()
		}
		return SettingsCatalog{}, err
	}
	defer session.Close()

	var response struct {
		Data []struct {
			ID                       string `json:"id"`
			Model                    string `json:"model"`
			DisplayName              string `json:"displayName"`
			Description              string `json:"description"`
			IsDefault                bool   `json:"isDefault"`
			Hidden                   bool   `json:"hidden"`
			DefaultReasoningEffort   string `json:"defaultReasoningEffort"`
			SupportedReasoningEffort []struct {
				Description string `json:"description"`
				Effort      string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := session.request(ctx, "model/list", map[string]any{
		"includeHidden": false,
		"limit":         100,
	}, &response); err != nil {
		if c.catalogFallback != nil {
			return c.catalogFallback.SettingsCatalog()
		}
		return SettingsCatalog{}, err
	}

	configPath := configPathForHome()
	configValues, err := readCodexConfigValues(configPath)
	if err != nil {
		return SettingsCatalog{}, err
	}

	models := make([]ModelInfo, 0, len(response.Data))
	defaultModel := strings.TrimSpace(configValues["model"])
	for _, item := range response.Data {
		if item.Hidden {
			continue
		}

		slug := strings.TrimSpace(item.Model)
		if slug == "" {
			slug = strings.TrimSpace(item.ID)
		}
		if slug == "" {
			continue
		}

		levels := make([]ModelReasoningLevel, 0, len(item.SupportedReasoningEffort))
		for _, level := range item.SupportedReasoningEffort {
			effort := normalizeReasoningEffort(level.Effort)
			if effort == "" {
				continue
			}
			levels = append(levels, ModelReasoningLevel{
				Effort:      effort,
				Description: strings.TrimSpace(level.Description),
			})
		}

		models = append(models, ModelInfo{
			Slug:                     slug,
			DisplayName:              emptyFallback(strings.TrimSpace(item.DisplayName), slug),
			Description:              strings.TrimSpace(item.Description),
			DefaultReasoningLevel:    normalizeReasoningEffort(item.DefaultReasoningEffort),
			SupportedReasoningLevels: levels,
		})
		if defaultModel == "" && item.IsDefault {
			defaultModel = slug
		}
	}

	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0].Slug
	}

	defaultReasoning := normalizeReasoningEffort(configValues["model_reasoning_effort"])
	if defaultReasoning == "" {
		defaultReasoning = defaultReasoningForModel(models, defaultModel)
	}
	if defaultReasoning == "" {
		defaultReasoning = "medium"
	}

	defaultServiceTier := normalizeServiceTier(configValues["service_tier"])
	serviceTierOptions := []string{"", "fast"}
	if defaultServiceTier == "flex" {
		serviceTierOptions = append(serviceTierOptions, "flex")
	}

	return SettingsCatalog{
		Models:                 models,
		DefaultModel:           defaultModel,
		DefaultReasoningEffort: defaultReasoning,
		DefaultServiceTier:     defaultServiceTier,
		ServiceTierOptions:     serviceTierOptions,
	}, nil
}

func (c *AppServerClient) resolveWorkspace(workspace string) string {
	if strings.TrimSpace(workspace) != "" {
		return workspace
	}
	return c.cfg.WorkspaceRoot
}

func (c *AppServerClient) registerActive(threadID string, session *appServerSession) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	c.active[threadID] = session
}

func (c *AppServerClient) unregisterActive(threadID string, session *appServerSession) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	if current, ok := c.active[threadID]; ok && current == session {
		delete(c.active, threadID)
	}
}

func (c *AppServerClient) lookupActive(threadID string) *appServerSession {
	c.activeMu.RLock()
	defer c.activeMu.RUnlock()
	return c.active[threadID]
}

func (c *AppServerClient) newSession(ctx context.Context, workspace string) (*appServerSession, error) {
	port, err := freeTCPPort()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve localhost port: %v", ErrAppServerUnavailable, err)
	}

	listenURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	cmd := exec.CommandContext(ctx, c.cfg.BinaryPath, "app-server", "--listen", listenURL)
	cmd.Dir = workspace

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start app-server: %v", ErrAppServerUnavailable, err)
	}

	conn, err := dialAppServer(ctx, listenURL)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = stdout.WriteString("")
		_ = cmd.Wait()
		return nil, fmt.Errorf("%w: connect app-server: %v (%s)", ErrAppServerUnavailable, err, summarizeFailure(stdout.String(), stderr.String()))
	}

	session := newAppServerSession(cmd, conn, &stdout, &stderr)
	if err := session.initialize(ctx); err != nil {
		session.Close()
		return nil, fmt.Errorf("%w: initialize app-server: %v", ErrAppServerUnavailable, err)
	}
	return session, nil
}

func dialAppServer(ctx context.Context, listenURL string) (*websocket.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		conn, _, err := websocket.Dial(ctx, listenURL, &websocket.DialOptions{
			HTTPClient: &http.Client{Timeout: 2 * time.Second},
		})
		if err == nil {
			// App-server notifications can include aggregated tool output and can
			// easily exceed the websocket package's default 32 KiB read cap.
			// This is a local trusted connection, so disabling the cap avoids
			// aborting otherwise healthy turns with "message too big".
			conn.SetReadLimit(-1)
			return conn, nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func freeTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

type appServerSession struct {
	cmd    *exec.Cmd
	conn   *websocket.Conn
	stdout *bytes.Buffer
	stderr *bytes.Buffer

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse

	events chan rpcNotification
	done   chan struct{}

	closeOnce sync.Once
	readerErr atomic.Pointer[error]
	nextID    atomic.Int64

	mu        sync.RWMutex
	threadID  string
	turnID    string
	textSoFar string
	reasoning string
	plan      string
}

func newAppServerSession(cmd *exec.Cmd, conn *websocket.Conn, stdout, stderr *bytes.Buffer) *appServerSession {
	s := &appServerSession{
		cmd:     cmd,
		conn:    conn,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[string]chan rpcResponse),
		events:  make(chan rpcNotification, 128),
		done:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

func (s *appServerSession) initialize(ctx context.Context) error {
	var response struct {
		UserAgent string `json:"userAgent"`
	}
	if err := s.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "telegram-codex-bridge",
			"version": buildinfo.DisplayVersion(),
			"title":   "Telegram Codex Bridge",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}, &response); err != nil {
		return err
	}
	return s.notify(ctx, "initialized", map[string]any{})
}

func (s *appServerSession) threadStart(ctx context.Context, workspace string, settings RunSettings, permissionMode string) (string, error) {
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{
		"cwd":            workspace,
		"approvalPolicy": "never",
		"sandbox":        sandboxMode(permissionMode),
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		params["model"] = model
	}
	if tier := normalizeServiceTier(settings.ServiceTier); tier != "" {
		params["serviceTier"] = tier
	}
	if err := s.request(ctx, "thread/start", params, &response); err != nil {
		return "", err
	}
	s.setThreadID(response.Thread.ID)
	return response.Thread.ID, nil
}

func (s *appServerSession) threadResume(ctx context.Context, threadID, workspace string, settings RunSettings, permissionMode string) (string, error) {
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{
		"threadId":       threadID,
		"cwd":            workspace,
		"approvalPolicy": "never",
		"sandbox":        sandboxMode(permissionMode),
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		params["model"] = model
	}
	if tier := normalizeServiceTier(settings.ServiceTier); tier != "" {
		params["serviceTier"] = tier
	}
	if err := s.request(ctx, "thread/resume", params, &response); err != nil {
		return "", err
	}
	s.setThreadID(response.Thread.ID)
	return response.Thread.ID, nil
}

func (s *appServerSession) turnStart(ctx context.Context, threadID, prompt string, imagePaths []string, workspace string, settings RunSettings) (string, error) {
	var response struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{
		"threadId":       threadID,
		"input":          buildUserInput(prompt, imagePaths),
		"cwd":            workspace,
		"approvalPolicy": "never",
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		params["model"] = model
	}
	if effort := normalizeReasoningEffort(settings.ReasoningEffort); effort != "" {
		params["effort"] = effort
	}
	if tier := normalizeServiceTier(settings.ServiceTier); tier != "" {
		params["serviceTier"] = tier
	}
	if err := s.request(ctx, "turn/start", params, &response); err != nil {
		return "", err
	}
	s.setTurnID(response.Turn.ID)
	return response.Turn.ID, nil
}

func (s *appServerSession) turnSteer(ctx context.Context, threadID, turnID, prompt string, imagePaths []string) error {
	return s.request(ctx, "turn/steer", map[string]any{
		"threadId":       threadID,
		"expectedTurnId": turnID,
		"input":          buildUserInput(prompt, imagePaths),
	}, nil)
}

func (s *appServerSession) waitForTurnCompletion(ctx context.Context, threadID string, handler StreamHandler) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.done:
			if err := s.ReaderError(); err != nil {
				return "", err
			}
			return "", ErrAppServerUnavailable
		case note := <-s.events:
			switch note.Method {
			case "thread/started":
				var payload struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					s.setThreadID(payload.Thread.ID)
					if handler != nil {
						handler(StreamEvent{Kind: StreamEventThreadStarted, SessionID: payload.Thread.ID})
					}
				}
			case "turn/started":
				var payload struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ID string `json:"id"`
					} `json:"turn"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					s.setTurnID(payload.Turn.ID)
					if handler != nil {
						handler(StreamEvent{
							Kind:      StreamEventStatus,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    payload.Turn.ID,
							Text:      "Codex 已开始处理",
						})
					}
				}
			case "item/started":
				var payload struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Item     struct {
						Type string `json:"type"`
					} `json:"item"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil && handler != nil {
					if text, toolName := startedItemStatus(payload.Item.Type); text != "" {
						handler(StreamEvent{
							Kind:      StreamEventToolUse,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    emptyFallback(payload.TurnID, s.TurnID()),
							ToolName:  toolName,
							Text:      text,
						})
					}
				}
			case "item/agentMessage/delta":
				var payload struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Delta    string `json:"delta"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					text := s.appendText(payload.Delta)
					if handler != nil {
						handler(StreamEvent{
							Kind:      StreamEventText,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    emptyFallback(payload.TurnID, s.TurnID()),
							Text:      text,
						})
					}
				}
			case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
				var payload struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Delta    string `json:"delta"`
					Text     string `json:"text"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					summary := s.appendReasoning(emptyFallback(payload.Delta, payload.Text))
					if handler != nil && strings.TrimSpace(summary) != "" {
						handler(StreamEvent{
							Kind:      StreamEventStatus,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    emptyFallback(payload.TurnID, s.TurnID()),
							Text:      "Codex 正在思考: " + summary,
						})
					}
				}
			case "item/reasoning/summaryPartAdded":
				var payload struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Text     string `json:"text"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					summary := s.appendReasoning(payload.Text)
					if handler != nil && strings.TrimSpace(summary) != "" {
						handler(StreamEvent{
							Kind:      StreamEventStatus,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    emptyFallback(payload.TurnID, s.TurnID()),
							Text:      "Codex 正在思考: " + summary,
						})
					}
				}
			case "turn/plan/updated", "item/plan/delta":
				var payload struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Delta    string `json:"delta"`
					Text     string `json:"text"`
				}
				if err := json.Unmarshal(note.Params, &payload); err == nil {
					plan := s.appendPlan(emptyFallback(payload.Delta, payload.Text))
					if handler != nil {
						text := "Codex 正在更新计划"
						if strings.TrimSpace(plan) != "" {
							text += ": " + plan
						}
						handler(StreamEvent{
							Kind:      StreamEventStatus,
							SessionID: emptyFallback(payload.ThreadID, s.ThreadID()),
							TurnID:    emptyFallback(payload.TurnID, s.TurnID()),
							Text:      text,
						})
					}
				}
			case "item/commandExecution/outputDelta":
				if handler != nil {
					handler(StreamEvent{
						Kind:      StreamEventToolUse,
						SessionID: s.ThreadID(),
						TurnID:    s.TurnID(),
						ToolName:  "Bash",
						Text:      "Codex 正在运行命令",
					})
				}
			case "item/fileChange/outputDelta":
				if handler != nil {
					handler(StreamEvent{
						Kind:      StreamEventToolUse,
						SessionID: s.ThreadID(),
						TurnID:    s.TurnID(),
						ToolName:  "Edit",
						Text:      "Codex 正在修改文件",
					})
				}
			case "item/mcpToolCall/progress":
				if handler != nil {
					handler(StreamEvent{
						Kind:      StreamEventToolUse,
						SessionID: s.ThreadID(),
						TurnID:    s.TurnID(),
						ToolName:  "MCP",
						Text:      "Codex 正在调用工具",
					})
				}
			case "turn/completed":
				var payload struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ID     string `json:"id"`
						Status string `json:"status"`
						Error  any    `json:"error"`
					} `json:"turn"`
				}
				if err := json.Unmarshal(note.Params, &payload); err != nil {
					return "", err
				}
				if strings.EqualFold(payload.Turn.Status, "failed") {
					return "", fmt.Errorf("app-server turn failed")
				}

				reply := strings.TrimSpace(s.Text())
				if reply == "" {
					fallbackReply, err := s.threadReadLatestReply(ctx, threadID)
					if err == nil {
						reply = fallbackReply
					}
				}
				return reply, nil
			}
		}
	}
}

func (s *appServerSession) threadReadLatestReply(ctx context.Context, threadID string) (string, error) {
	var response struct {
		Thread struct {
			Turns []struct {
				Items []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := s.request(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
	}, &response); err != nil {
		return "", err
	}

	for i := len(response.Thread.Turns) - 1; i >= 0; i-- {
		items := response.Thread.Turns[i].Items
		for j := len(items) - 1; j >= 0; j-- {
			if items[j].Type == "agentMessage" && strings.TrimSpace(items[j].Text) != "" {
				return strings.TrimSpace(items[j].Text), nil
			}
		}
	}
	return "", nil
}

func (s *appServerSession) request(ctx context.Context, method string, params any, out any) error {
	id := fmt.Sprintf("%d", s.nextID.Add(1))
	replyCh := make(chan rpcResponse, 1)

	s.pendingMu.Lock()
	s.pending[id] = replyCh
	s.pendingMu.Unlock()

	if err := s.write(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply := <-replyCh:
		if reply.Err != nil {
			return reply.Err
		}
		if out != nil && len(reply.Result) > 0 {
			if err := json.Unmarshal(reply.Result, out); err != nil {
				return err
			}
		}
		return nil
	case <-s.done:
		if err := s.ReaderError(); err != nil {
			return err
		}
		return ErrAppServerUnavailable
	}
}

func (s *appServerSession) notify(ctx context.Context, method string, params any) error {
	return s.write(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *appServerSession) write(ctx context.Context, payload any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return wsjson.Write(ctx, s.conn, payload)
}

func (s *appServerSession) readLoop() {
	defer close(s.done)

	for {
		var payload map[string]json.RawMessage
		if err := wsjson.Read(context.Background(), s.conn, &payload); err != nil {
			s.failAllPending(err)
			return
		}

		method := decodeJSONString(payload["method"])
		id := canonicalID(payload["id"])

		switch {
		case method != "" && id != "":
			_ = s.respondError(id, -32601, "server requests are not supported by telegram-codex-bridge yet")
		case method != "":
			select {
			case s.events <- rpcNotification{Method: method, Params: payload["params"]}:
			default:
			}
		case id != "":
			response := rpcResponse{
				Result: payload["result"],
			}
			if rawErr, ok := payload["error"]; ok && len(rawErr) > 0 && string(rawErr) != "null" {
				var rpcErr struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(rawErr, &rpcErr); err == nil {
					response.Err = fmt.Errorf("app-server rpc error %d: %s", rpcErr.Code, rpcErr.Message)
				} else {
					response.Err = fmt.Errorf("app-server rpc error")
				}
			}
			s.pendingMu.Lock()
			replyCh := s.pending[id]
			delete(s.pending, id)
			s.pendingMu.Unlock()
			if replyCh != nil {
				replyCh <- response
			}
		}
	}
}

func (s *appServerSession) respondError(id string, code int, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.write(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (s *appServerSession) failAllPending(err error) {
	err = normalizeAppServerReadError(err)
	s.readerErr.Store(&err)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, replyCh := range s.pending {
		replyCh <- rpcResponse{Err: err}
		delete(s.pending, id)
	}
}

func (s *appServerSession) ReaderError() error {
	ptr := s.readerErr.Load()
	if ptr == nil {
		return nil
	}
	return normalizeAppServerReadError(*ptr)
}

func (s *appServerSession) Close() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close(websocket.StatusNormalClosure, "bye")
		done := make(chan struct{})
		go func() {
			_ = s.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			_ = s.cmd.Process.Kill()
			<-done
		}
	})
}

func (s *appServerSession) setThreadID(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = threadID
}

func (s *appServerSession) ThreadID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.threadID
}

func (s *appServerSession) setTurnID(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnID = turnID
}

func (s *appServerSession) TurnID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turnID
}

func (s *appServerSession) appendText(delta string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textSoFar += delta
	return s.textSoFar
}

func (s *appServerSession) Text() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.textSoFar
}

func (s *appServerSession) appendReasoning(delta string) string {
	delta = compactStatusDelta(delta)
	if delta == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasoning += delta
	s.reasoning = trimStatusPreview(s.reasoning, 280)
	return s.reasoning
}

func (s *appServerSession) appendPlan(delta string) string {
	delta = compactStatusDelta(delta)
	if delta == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan += delta
	s.plan = trimStatusPreview(s.plan, 220)
	return s.plan
}

func compactStatusDelta(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func trimStatusPreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit-1]) + "…"
}

func normalizeAppServerReadError(err error) error {
	if err == nil {
		return nil
	}
	// Any websocket read failure means this app-server session is no longer
	// trustworthy. Surface it as app-server unavailability so the bridge can
	// fall back to the CLI path instead of exposing transport noise to users.
	return fmt.Errorf("%w: %v", ErrAppServerUnavailable, err)
}

func startedItemStatus(itemType string) (text, toolName string) {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "reasoning":
		return "Codex 正在思考", "Reasoning"
	case "commandexecution":
		return "Codex 正在运行命令", "Bash"
	case "filechange":
		return "Codex 正在修改文件", "Edit"
	case "mcptoolcall":
		return "Codex 正在调用工具", "MCP"
	default:
		return "", ""
	}
}

func buildUserInput(prompt string, imagePaths []string) []map[string]any {
	input := make([]map[string]any, 0, 1+len(imagePaths))
	if strings.TrimSpace(prompt) != "" {
		input = append(input, map[string]any{
			"type": "text",
			"text": prompt,
		})
	}
	for _, imagePath := range imagePaths {
		imagePath = strings.TrimSpace(imagePath)
		if imagePath == "" {
			continue
		}
		input = append(input, map[string]any{
			"type": "localImage",
			"path": imagePath,
		})
	}
	return input
}

func sandboxMode(permissionMode string) string {
	switch normalizePermissionMode(permissionMode) {
	case "full-access":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

func configPathForHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

type rpcNotification struct {
	Method string
	Params json.RawMessage
}

type rpcResponse struct {
	Result json.RawMessage
	Err    error
}

func canonicalID(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	text = strings.Trim(text, `"`)
	return text
}

func decodeJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}
