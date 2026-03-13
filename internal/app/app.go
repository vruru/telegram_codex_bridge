package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"telegram-codex-bridge/internal/buildinfo"
	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/power"
	"telegram-codex-bridge/internal/store"
	"telegram-codex-bridge/internal/telegram"
)

type App struct {
	logger *log.Logger
	bot    *telegram.Bot
	codex  codex.BridgeClient
	store  store.TopicStore
	power  *power.Manager

	defaultLanguage string
	envPath         string
	debug           bool

	workersMu sync.Mutex
	workers   map[string]*topicWorker

	statsMu sync.RWMutex
	stats   map[string]topicStats
}

func New(logger *log.Logger, bot *telegram.Bot, codexClient codex.BridgeClient, topicStore store.TopicStore, powerManager *power.Manager, defaultLanguage, envPath string, debug bool) *App {
	return &App{
		logger:          logger,
		bot:             bot,
		codex:           codexClient,
		store:           topicStore,
		power:           powerManager,
		defaultLanguage: i18n.NormalizePreference(defaultLanguage),
		envPath:         strings.TrimSpace(envPath),
		debug:           debug,
		workers:         make(map[string]*topicWorker),
		stats:           make(map[string]topicStats),
	}
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Printf("starting Telegram Codex bridge")
	go a.bootstrapTelegram(ctx)
	return a.bot.Run(ctx, a.handleUpdate)
}

func (a *App) bootstrapTelegram(ctx context.Context) {
	go a.retryUntilSuccess(ctx, 8*time.Second, "telegram bot profile check", func(runCtx context.Context) (bool, error) {
		profile, err := a.bot.GetProfile(runCtx)
		if err != nil {
			return false, err
		}
		if !profile.CanReadAllGroupMessages {
			a.logger.Printf(
				"telegram bot @%s cannot read all group messages; disable BotFather privacy mode or group topics will only receive commands/mentions",
				profile.Username,
			)
		}
		return true, nil
	})

	go a.retryUntilSuccess(ctx, 8*time.Second, "telegram bot command sync", func(runCtx context.Context) (bool, error) {
		if err := a.bot.SyncCommands(runCtx); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (a *App) retryUntilSuccess(
	ctx context.Context,
	attemptTimeout time.Duration,
	label string,
	run func(context.Context) (bool, error),
) {
	backoffs := []time.Duration{0, 5 * time.Second, 15 * time.Second, 30 * time.Second, 1 * time.Minute}

	for attempt := 0; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if attempt < len(backoffs) && backoffs[attempt] > 0 {
			timer := time.NewTimer(backoffs[attempt])
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		runCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		done, err := run(runCtx)
		cancel()
		if err != nil {
			a.logger.Printf("%s failed: %v", label, err)
			continue
		}
		if done {
			return
		}
	}
}

func (a *App) handleUpdate(ctx context.Context, update telegram.IncomingUpdate) error {
	a.debugf(
		"received update kind=%s chat=%d topic=%d user=%d text=%q",
		update.Kind,
		update.ChatID,
		update.TopicID,
		update.UserID,
		shortText(update.Text, 80),
	)

	switch update.Kind {
	case telegram.UpdateKindMessage:
		if !a.bot.IsAllowedChat(update.ChatID) || !a.bot.IsAllowedUser(update.UserID) {
			a.debugf("rejected message from user=%d chat=%d", update.UserID, update.ChatID)
			return nil
		}

		text := strings.TrimSpace(update.Text)
		if text == "" && len(update.Media) == 0 {
			return nil
		}

		if len(update.Media) == 0 {
			if handled, err := a.handleCommand(ctx, update, text); handled {
				if err != nil {
					loc := a.catalogForCommand(ctx, update)
					a.logger.Printf("failed to handle command chat=%d topic=%d: %v", update.ChatID, update.TopicID, err)
					_ = a.bot.SendMessage(ctx, telegram.OutgoingMessage{
						ChatID:           update.ChatID,
						TopicID:          update.TopicID,
						ReplyToMessageID: update.MessageID,
						Text:             fmt.Sprintf("%s: %s", loc.T("处理这条消息时出错了", "Failed to handle this message"), userVisibleError(err)),
					})
				}
				return nil
			}
		}

		if err := a.enqueueMessage(ctx, update); err != nil {
			loc := a.catalogForMessage(ctx, update)
			a.logger.Printf("failed to handle message chat=%d topic=%d: %v", update.ChatID, update.TopicID, err)
			_ = a.bot.SendMessage(ctx, telegram.OutgoingMessage{
				ChatID:           update.ChatID,
				TopicID:          update.TopicID,
				ReplyToMessageID: update.MessageID,
				Text:             fmt.Sprintf("%s: %s", loc.T("处理这条消息时出错了", "Failed to handle this message"), userVisibleError(err)),
			})
		}
	case telegram.UpdateKindCallbackQuery:
		if !a.bot.IsAllowedChat(update.ChatID) || !a.bot.IsAllowedUser(update.UserID) {
			a.debugf("rejected callback from user=%d chat=%d", update.UserID, update.ChatID)
			return nil
		}
		if err := a.handleSettingsCallback(ctx, update); err != nil {
			loc := a.catalogForCommand(ctx, update)
			a.logger.Printf("failed to handle callback chat=%d topic=%d: %v", update.ChatID, update.TopicID, err)
			_ = a.bot.AnswerCallbackQuery(ctx, update.CallbackID, fmt.Sprintf("%s: %s", loc.T("处理失败", "Request failed"), userVisibleError(err)))
		}
	case telegram.UpdateKindTopicCreated, telegram.UpdateKindTopicEdited:
		if !a.bot.IsAllowedChat(update.ChatID) {
			return nil
		}
		return a.syncTopicMetadata(ctx, update)
	case telegram.UpdateKindTopicClosed, telegram.UpdateKindTopicReopened, telegram.UpdateKindGeneralTopicHidden, telegram.UpdateKindGeneralTopicUnhidden:
		if !a.bot.IsAllowedChat(update.ChatID) {
			return nil
		}
		a.debugf("observed topic lifecycle event kind=%s chat=%d topic=%d", update.Kind, update.ChatID, update.TopicID)
		return nil
	}

	return nil
}

func (a *App) debugf(format string, args ...any) {
	if a == nil || !a.debug || a.logger == nil {
		return
	}
	a.logger.Printf(format, args...)
}

func (a *App) dispatch(ctx context.Context, msg telegram.IncomingUpdate) error {
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return fmt.Errorf("lookup topic binding: %w", err)
	}

	if found && binding.ArchivedAt != nil {
		found = false
		binding = store.TopicBinding{}
	}

	if !found || strings.TrimSpace(binding.SessionID) == "" {
		_, err := a.createThread(ctx, msg, nil)
		return err
	}

	_, err = a.continueThread(ctx, msg, binding, nil)
	return err
}

func (a *App) handleCommand(ctx context.Context, msg telegram.IncomingUpdate, text string) (bool, error) {
	command, args, ok := parseCommand(text)
	if !ok {
		return false, nil
	}

	switch command {
	case "start", "help":
		loc := a.catalogForCommand(ctx, msg)
		return true, a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             helpText(loc),
		})
	case "where":
		loc := a.catalogForCommand(ctx, msg)
		return true, a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text: fmt.Sprintf(
				"%s=%d\n%s=%d\n%s=%d\n%s=%s",
				loc.T("chat_id", "chat_id"),
				msg.ChatID,
				loc.T("topic_id", "topic_id"),
				msg.TopicID,
				loc.T("user_id", "user_id"),
				msg.UserID,
				loc.T("username", "username"),
				emptyFallback(msg.Username, "<none>"),
			),
		})
	case "version":
		loc := a.catalogForCommand(ctx, msg)
		return true, a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text: fmt.Sprintf(
				"%s: %s",
				loc.T("版本", "Version"),
				buildinfo.DisplayVersion(),
			),
		})
	case "status":
		return true, a.sendStatus(ctx, msg)
	case "limit":
		return true, a.sendLimit(ctx, msg)
	case "lang":
		return true, a.handleLanguageCommand(ctx, msg, args)
	case "model":
		return true, a.handleModelCommand(ctx, msg, args)
	case "think":
		return true, a.handleThinkCommand(ctx, msg, args)
	case "speed":
		return true, a.handleSpeedCommand(ctx, msg, args)
	case "permission":
		return true, a.handlePermissionCommand(ctx, msg, args)
	case "threads":
		return true, a.listThreads(ctx, msg)
	case "archive":
		return true, a.archiveCurrentBinding(ctx, msg)
	case "delete":
		return true, a.deleteCurrentTopic(ctx, msg)
	case "new":
		return true, a.resetCurrentBinding(ctx, msg, args)
	default:
		return false, nil
	}
}

func (a *App) startThread(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForMessage(ctx, msg)
	result, err := a.createThread(ctx, msg, nil)
	if err != nil {
		return err
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             ensureReplyText(result.Reply, result.Thread.SessionID, loc),
	})
}

func (a *App) resumeThread(ctx context.Context, msg telegram.IncomingUpdate, binding store.TopicBinding) error {
	loc := a.catalogForMessage(ctx, msg)
	result, err := a.continueThread(ctx, msg, binding, nil)
	if err != nil {
		return err
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             ensureReplyText(result.Reply, result.SessionID, loc),
	})
}

func defaultThreadName(msg telegram.IncomingUpdate) string {
	title := topicTitleOrDefault(msg)
	if msg.TopicID > 0 {
		return fmt.Sprintf("%s [%d/%d]", title, msg.ChatID, msg.TopicID)
	}
	return fmt.Sprintf("%s [%d]", title, msg.ChatID)
}

func ensureReplyText(reply, sessionID string, loc i18n.Catalog) string {
	reply = sanitizeReplyLocalPaths(reply)
	reply = strings.TrimSpace(reply)
	if reply != "" {
		return reply
	}
	return fmt.Sprintf("%s `%s`", loc.T("Codex 已处理完成，但没有返回文本内容。会话：", "Codex finished without returning text. Session:"), sessionID)
}

func userVisibleError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func helpText(loc i18n.Catalog) string {
	return strings.TrimSpace(loc.T(`
可用命令：
/help - 查看帮助
/where - 查看当前 chat/topic/user 标识
/version - 查看当前 Bridge 版本
/status - 查看当前话题绑定的 Codex 会话
/limit - 查看当前 Codex 限额信息
/lang [auto|zh|en] - 查看或设置当前话题的语言
/model [模型名] - 查看或设置当前话题模型
/think [default|none|minimal|low|medium|high|xhigh] - 查看或设置思考等级
/speed [default|fast] - 查看或设置速度模式
/permission [default|full-access] - 查看或设置 Codex 执行权限
/threads - 查看当前 chat 下已经绑定的线程
/new [提示词] - 断开旧线程；带提示词时立即新建线程
/archive - 归档当前话题的线程绑定
/delete - 删除当前 Telegram 话题，并归档它的线程绑定
`, `
Available commands:
/help - Show help
/where - Show the current chat/topic/user identifiers
/version - Show the current bridge version
/status - Show the Codex session bound to this topic
/limit - Show the current Codex quota usage
/lang [auto|zh|en] - Show or set the language for this topic
/model [model] - Show or set the model for this topic
/think [default|none|minimal|low|medium|high|xhigh] - Show or set the reasoning level
/speed [default|fast] - Show or set the speed mode
/permission [default|full-access] - Show or set the Codex execution permission
/threads - List the threads already bound in this chat
/new [prompt] - Disconnect the old thread; start a new one immediately if a prompt is provided
/archive - Archive the current topic-thread binding
/delete - Delete the current Telegram topic and archive its thread binding
`))
}

func shortText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}

func (a *App) sendStatus(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return fmt.Errorf("lookup topic binding: %w", err)
	}

	if !found {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("当前话题还没有绑定 Codex 线程。发送一条普通消息后会自动新建。", "This topic does not have a bound Codex thread yet. Send a normal message to create one automatically."),
		})
	}

	status := loc.T("运行中", "active")
	if binding.ArchivedAt != nil {
		status = loc.T("已归档", "archived")
	}

	_, preferences, effectiveSettings, err := a.topicSettings(ctx, msg)
	if err != nil {
		return fmt.Errorf("resolve topic settings: %w", err)
	}

	statsText := ""
	if stats, ok := a.topicStats(msg.ChatID, msg.TopicID); ok {
		statsText = fmt.Sprintf(
			"\n%s=%s\n%s=%s\n%s=%d\n%s=%d\n%s=%s\n%s=%s",
			loc.T("最近一次运行", "last_run"),
			emptyFallback(string(stats.LastRunMode), "<none>"),
			loc.T("最近一次耗时", "last_duration"),
			formatDuration(stats.LastRunDuration),
			loc.T("新线程次数", "start_runs"),
			stats.TotalStartRuns,
			loc.T("续线程次数", "resume_runs"),
			stats.TotalResumeRuns,
			loc.T("最近一次新线程耗时", "last_start_duration"),
			formatDuration(stats.LastStartDuration),
			loc.T("最近一次续线程耗时", "last_resume_duration"),
			formatDuration(stats.LastResumeDuration),
		)
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text: fmt.Sprintf(
			"%s=%s\n%s=%s\n%s=%s\n%s=%s\n%s=%s\n%s=%s\n%s=%s\n%s=%s",
			loc.T("状态", "status"),
			status,
			loc.T("会话", "session_id"),
			emptyFallback(binding.SessionID, "<none>"),
			loc.T("工作目录", "workspace"),
			emptyFallback(binding.Workspace, "<none>"),
			loc.T("模型", "model"),
			emptyFallback(effectiveSettings.Model, "<none>"),
			loc.T("思考等级", "reasoning"),
			reasoningLabel(loc, effectiveSettings.ReasoningEffort),
			loc.T("速度模式", "speed"),
			speedLabel(loc, effectiveServiceTier(preferences, effectiveSettings.ServiceTier)),
			loc.T("权限模式", "permission_mode"),
			a.codex.PermissionMode(),
			loc.T("话题标题", "topic_title"),
			emptyFallback(binding.TopicTitle, "<none>"),
		) + statsText,
	})
}

func (a *App) archiveCurrentBinding(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	changed, err := a.archiveBinding(ctx, msg)
	if err != nil {
		return err
	}
	if !changed {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("当前话题还没有绑定线程，不需要归档。", "This topic does not have a bound thread, so there is nothing to archive."),
		})
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             loc.T("当前话题已归档。下一次如果要继续，请用 `/new` 新开线程。", "This topic is archived. Use `/new` next time if you want to start a fresh thread."),
	})
}

func (a *App) archiveBinding(ctx context.Context, msg telegram.IncomingUpdate) (bool, error) {
	loc := a.catalogForCommand(ctx, msg)
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return false, fmt.Errorf("lookup topic binding: %w", err)
	}

	if !found {
		return false, nil
	}

	if binding.ArchivedAt != nil {
		return false, a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("当前话题已经归档。", "This topic is already archived."),
		})
	}

	now := time.Now().UTC()
	if err := a.store.ArchiveBinding(ctx, msg.ChatID, msg.TopicID, now); err != nil {
		return false, fmt.Errorf("archive topic binding: %w", err)
	}

	if binding.SessionID != "" {
		if err := a.codex.ArchiveThread(ctx, binding.SessionID); err != nil {
			a.logger.Printf("archive codex thread %s: %v", binding.SessionID, err)
		}
	}

	return true, nil
}

func (a *App) resetCurrentBinding(ctx context.Context, msg telegram.IncomingUpdate, prompt string) error {
	loc := a.catalogForCommand(ctx, msg)
	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return fmt.Errorf("lookup topic binding: %w", err)
	}

	if found && binding.ArchivedAt == nil {
		now := time.Now().UTC()
		if err := a.store.ArchiveBinding(ctx, msg.ChatID, msg.TopicID, now); err != nil {
			return fmt.Errorf("archive topic binding before reset: %w", err)
		}
		if binding.SessionID != "" {
			if err := a.codex.ArchiveThread(ctx, binding.SessionID); err != nil {
				a.logger.Printf("archive codex thread %s before reset: %v", binding.SessionID, err)
			}
		}
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("旧线程已断开。发送下一条普通消息后会自动创建新线程。", "The previous thread has been disconnected. Send your next normal message to create a new thread automatically."),
		})
	}

	msg.Text = prompt
	return a.startThread(ctx, msg)
}

func parseCommand(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", "", false
	}

	name := strings.TrimPrefix(fields[0], "/")
	if name == "" {
		return "", "", false
	}

	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}

	switch strings.ToLower(name) {
	case "start", "help", "where", "version", "status", "limit", "lang", "model", "think", "speed", "permission", "threads", "new", "archive", "delete":
	default:
		return "", "", false
	}

	args := ""
	if len(fields) > 1 {
		args = strings.TrimSpace(text[len(fields[0]):])
	}

	return strings.ToLower(name), args, true
}

func emptyFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func topicTitleOrDefault(msg telegram.IncomingUpdate) string {
	if title := strings.TrimSpace(msg.TopicTitle); title != "" {
		return title
	}
	if msg.TopicID > 0 {
		return fmt.Sprintf("telegram-topic-%d", msg.TopicID)
	}
	if title := strings.TrimSpace(msg.ChatTitle); title != "" {
		return title
	}
	return "telegram-chat"
}

func (a *App) syncTopicMetadata(ctx context.Context, update telegram.IncomingUpdate) error {
	if update.TopicID == 0 || strings.TrimSpace(update.TopicTitle) == "" {
		return nil
	}

	binding, found, err := a.store.LookupBinding(ctx, update.ChatID, update.TopicID)
	if err != nil {
		return fmt.Errorf("lookup binding for topic metadata: %w", err)
	}
	if !found {
		a.logger.Printf("observed topic metadata chat=%d topic=%d title=%q without active binding", update.ChatID, update.TopicID, update.TopicTitle)
		return nil
	}

	if strings.TrimSpace(binding.TopicTitle) == strings.TrimSpace(update.TopicTitle) {
		return nil
	}

	binding.TopicTitle = update.TopicTitle
	if err := a.store.SaveBinding(ctx, binding); err != nil {
		return fmt.Errorf("save updated topic metadata: %w", err)
	}

	a.logger.Printf("updated topic title chat=%d topic=%d title=%q", update.ChatID, update.TopicID, update.TopicTitle)
	return nil
}

func (a *App) deleteCurrentTopic(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	if msg.TopicID == 0 {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("只有 forum topic 里才能使用 `/delete`。", "`/delete` is only available inside a forum topic."),
		})
	}

	if _, err := a.archiveBinding(ctx, msg); err != nil {
		return err
	}

	if err := a.bot.DeleteForumTopic(ctx, msg.ChatID, msg.TopicID); err != nil {
		return fmt.Errorf("delete telegram forum topic: %w", err)
	}

	return nil
}

func (a *App) listThreads(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	bindings, err := a.store.ListBindingsByChat(ctx, msg.ChatID)
	if err != nil {
		return fmt.Errorf("list bindings by chat: %w", err)
	}

	if len(bindings) == 0 {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("当前 chat 还没有任何已绑定线程。", "There are no bound threads in this chat yet."),
		})
	}

	lines := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		status := loc.T("运行中", "active")
		if binding.ArchivedAt != nil {
			status = loc.T("已归档", "archived")
		}
		lines = append(lines, fmt.Sprintf(
			"- topic_id=%d | %s | %s | %s",
			binding.TopicID,
			status,
			emptyFallback(binding.TopicTitle, "<no title>"),
			shortSession(binding.SessionID),
		))
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             loc.T("当前 chat 的线程：\n", "Threads in this chat:\n") + strings.Join(lines, "\n"),
	})
}

func shortSession(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) <= 12 {
		return emptyFallback(sessionID, "<none>")
	}
	return sessionID[:12]
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "<none>"
	}

	seconds := duration.Round(time.Second)
	if seconds < time.Second {
		seconds = time.Second
	}
	return seconds.String()
}
