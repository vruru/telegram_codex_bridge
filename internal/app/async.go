package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/store"
	"telegram-codex-bridge/internal/telegram"
)

type queuedMessage struct {
	Update       telegram.IncomingUpdate
	AckMessageID int64
}

type topicWorker struct {
	jobs chan queuedMessage

	mu        sync.Mutex
	busy      bool
	followUps []queuedMessage
	active    *progressTracker
	current   queuedMessage

	activeSessionID string
	activeTurnID    string
}

type topicStats struct {
	TotalStartRuns     int64
	TotalResumeRuns    int64
	LastStartDuration  time.Duration
	LastResumeDuration time.Duration
	LastRunMode        codex.RunMode
	LastRunDuration    time.Duration
}

type executionResult struct {
	Reply            string
	SessionID        string
	Provider         string
	Stats            codex.ExecutionStats
	Artifacts        []generatedArtifact
	SkippedArtifacts []string
}

func (a *App) enqueueMessage(ctx context.Context, update telegram.IncomingUpdate) error {
	job := queuedMessage{
		Update: update,
	}

	worker := a.ensureWorker(ctx, update.ChatID, update.TopicID)
	followUp, queue := worker.begin(job)
	if followUp {
		sessionID, turnID := worker.activeTurn()
		next := newProgressTracker(ctx, a.bot, a.logger, update, 0, a.catalogForMessage(ctx, update))
		_, previousProgress := worker.promoteFollowUp(job, next)
		if previousProgress != nil {
			previousProgress.Handoff(ctx, "")
		}

		if sessionID != "" && turnID != "" {
			steerJob := job
			if len(update.Media) > 0 {
				prepared, err := a.prepareIncomingUpdate(ctx, update)
				if err == nil {
					steerJob.Update = prepared
				}
			}

			err := a.codex.SteerThread(ctx, codex.SteerThreadRequest{
				SessionID:  sessionID,
				TurnID:     turnID,
				Message:    resolvedPrompt(steerJob.Update),
				ImagePaths: append([]string(nil), steerJob.Update.PreparedImagePaths...),
			})
			if err == nil {
				return nil
			}
			if !errors.Is(err, codex.ErrSteeringUnsupported) && !errors.Is(err, codex.ErrActiveTurnUnavailable) {
				a.logger.Printf("steer follow-up chat=%d topic=%d failed, falling back to queued merge: %v", update.ChatID, update.TopicID, err)
			}
			job = steerJob
		}

		worker.enqueueFollowUp(job)
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case queue <- job:
		return nil
	}
}

func (a *App) ensureWorker(ctx context.Context, chatID, topicID int64) *topicWorker {
	key := topicKey(chatID, topicID)

	a.workersMu.Lock()
	defer a.workersMu.Unlock()

	if worker, ok := a.workers[key]; ok {
		return worker
	}

	worker := &topicWorker{
		jobs: make(chan queuedMessage, 1),
	}
	a.workers[key] = worker

	go a.runWorker(ctx, key, worker)
	return worker
}

func (a *App) runWorker(ctx context.Context, key string, worker *topicWorker) {
	defer func() {
		a.workersMu.Lock()
		delete(a.workers, key)
		a.workersMu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-worker.jobs:
			if !ok {
				return
			}
			a.processQueuedMessage(ctx, worker, job)
		}
	}
}

func (a *App) processQueuedMessage(ctx context.Context, worker *topicWorker, job queuedMessage) {
	releaseWakeLock := func() {}
	if a.power != nil {
		releaseWakeLock = a.power.Acquire(ctx)
	}
	defer releaseWakeLock()

	progress := worker.ensureActiveProgress(ctx, a.bot, a.logger, job)
	current := job
	var (
		collectedArtifacts []generatedArtifact
		collectedSkipped   []string
	)
	for {
		prepared, err := a.prepareIncomingUpdate(ctx, current.Update)
		if err != nil {
			a.logger.Printf("failed to prepare queued message chat=%d topic=%d: %v", current.Update.ChatID, current.Update.TopicID, err)
			active := worker.activeProgress()
			if active == nil {
				active = progress
			}
			active.Fail(ctx, err)
			a.failPendingFollowUps(ctx, worker.releasePending(), err)
			return
		}
		current.Update = prepared

		streamHandler := func(event codex.StreamEvent) {
			worker.updateActiveTurn(event)
			progress.OnStreamEvent(event)
		}
		result, err := a.executeUpdate(ctx, current.Update, streamHandler)
		if err != nil {
			a.logger.Printf("failed to process queued message chat=%d topic=%d: %v", current.Update.ChatID, current.Update.TopicID, err)
			active := worker.activeProgress()
			if active == nil {
				active = progress
			}
			active.Fail(ctx, err)
			a.failPendingFollowUps(ctx, worker.releasePending(), err)
			return
		}
		collectedArtifacts = mergeGeneratedArtifacts(collectedArtifacts, result.Artifacts)
		collectedSkipped = mergeUniqueStrings(collectedSkipped, result.SkippedArtifacts)
		worker.clearActiveTurn()

		followUps := worker.takeFollowUpsOrMarkIdle()
		if len(followUps) == 0 {
			loc := a.catalogForMessage(ctx, current.Update)
			active := worker.activeProgress()
			if active == nil {
				active = progress
			}
			if err := active.DeliverFinal(ctx, ensureReplyText(result.Reply, result.SessionID, loc)); err != nil {
				a.logger.Printf("deliver final reply chat=%d topic=%d: %v", current.Update.ChatID, current.Update.TopicID, err)
			}
			a.sendGeneratedArtifacts(ctx, current.Update, collectedArtifacts, collectedSkipped)
			active.Finish(ctx, result.Stats)
			worker.finishRun(active)
			return
		}

		latest := followUps[len(followUps)-1]
		preparedFollowUps := make([]telegram.IncomingUpdate, 0, len(followUps))
		for _, followUp := range followUps {
			preparedFollowUp, prepErr := a.prepareIncomingUpdate(ctx, followUp.Update)
			if prepErr != nil {
				a.logger.Printf("failed to prepare follow-up chat=%d topic=%d: %v", followUp.Update.ChatID, followUp.Update.TopicID, prepErr)
				active := worker.activeProgress()
				if active == nil {
					active = progress
				}
				active.Fail(ctx, prepErr)
				a.failPendingFollowUps(ctx, worker.releasePending(), prepErr)
				return
			}
			preparedFollowUps = append(preparedFollowUps, preparedFollowUp)
		}
		latestPrepared := preparedFollowUps[len(preparedFollowUps)-1]
		progress = worker.activeProgress()
		if progress == nil || progress.update.MessageID != latestPrepared.MessageID {
			progress = newProgressTracker(ctx, a.bot, a.logger, latestPrepared, latest.AckMessageID, a.catalogForMessage(ctx, latestPrepared))
			_, previousProgress := worker.promoteFollowUp(latest, progress)
			if previousProgress != nil && previousProgress != progress {
				previousProgress.Handoff(ctx, "")
			}
		}

		current = queuedMessage{
			Update: telegram.IncomingUpdate{
				Kind:               telegram.UpdateKindMessage,
				ChatID:             latestPrepared.ChatID,
				TopicID:            latestPrepared.TopicID,
				UserID:             latestPrepared.UserID,
				Username:           latestPrepared.Username,
				LanguageCode:       latestPrepared.LanguageCode,
				Text:               a.mergeFollowUpDisplayText(preparedFollowUps),
				MessageID:          latestPrepared.MessageID,
				ChatTitle:          latestPrepared.ChatTitle,
				TopicTitle:         latestPrepared.TopicTitle,
				PreparedPrompt:     a.mergeFollowUpPrompt(ctx, preparedFollowUps),
				PreparedImagePaths: mergeFollowUpImagePaths(preparedFollowUps),
				PreparedWorkspace:  latestPrepared.PreparedWorkspace,
			},
			AckMessageID: progress.ackMessageID,
		}
	}
}

func (a *App) executeUpdate(ctx context.Context, msg telegram.IncomingUpdate, streamHandler codex.StreamHandler) (executionResult, error) {
	msg, err := a.prepareIncomingUpdate(ctx, msg)
	if err != nil {
		return executionResult{}, err
	}

	binding, found, err := a.store.LookupBinding(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return executionResult{}, fmt.Errorf("lookup topic binding: %w", err)
	}

	if found && binding.ArchivedAt != nil {
		// After /new or /archive, the next normal message should start a fresh
		// thread instead of erroring on the archived binding.
		found = false
		binding = store.TopicBinding{}
	}

	beforeSnapshot, err := snapshotWorkspaceFiles(msg.PreparedWorkspace)
	if err != nil {
		return executionResult{}, err
	}

	_, _, settings, err := a.topicSettings(ctx, msg)
	if err != nil {
		return executionResult{}, fmt.Errorf("resolve topic settings: %w", err)
	}
	if found && binding.Provider != "" && normalizeProviderArg(binding.Provider) != settings.Provider {
		found = false
		binding = store.TopicBinding{}
	}

	if !found || binding.SessionID == "" {
		result, err := a.createThread(ctx, msg, streamHandler)
		if err != nil {
			return executionResult{}, err
		}
		artifacts, skipped, artifactErr := detectGeneratedArtifacts(msg.PreparedWorkspace, beforeSnapshot)
		if artifactErr != nil {
			return executionResult{}, artifactErr
		}
		return executionResult{
			Reply:            result.Reply,
			SessionID:        result.Thread.SessionID,
			Provider:         result.Thread.Provider,
			Stats:            result.Stats,
			Artifacts:        mergeGeneratedArtifactsWithReply(msg.PreparedWorkspace, result.Reply, artifacts),
			SkippedArtifacts: skipped,
		}, nil
	}

	result, err := a.continueThread(ctx, msg, binding, streamHandler)
	if err != nil {
		return executionResult{}, err
	}
	artifacts, skipped, artifactErr := detectGeneratedArtifacts(msg.PreparedWorkspace, beforeSnapshot)
	if artifactErr != nil {
		return executionResult{}, artifactErr
	}
	return executionResult{
		Reply:            result.Reply,
		SessionID:        result.SessionID,
		Provider:         result.Provider,
		Stats:            result.Stats,
		Artifacts:        mergeGeneratedArtifactsWithReply(msg.PreparedWorkspace, result.Reply, artifacts),
		SkippedArtifacts: skipped,
	}, nil
}

func (a *App) createThread(ctx context.Context, msg telegram.IncomingUpdate, streamHandler codex.StreamHandler) (codex.StartThreadResult, error) {
	workspace := strings.TrimSpace(msg.PreparedWorkspace)
	var err error
	if workspace == "" {
		workspace, err = a.resolveTopicWorkspace(ctx, msg)
		if err != nil {
			return codex.StartThreadResult{}, err
		}
	}
	_, _, settings, err := a.topicSettings(ctx, msg)
	if err != nil {
		return codex.StartThreadResult{}, fmt.Errorf("resolve topic settings: %w", err)
	}
	a.debugf("selected workspace chat=%d topic=%d workspace=%q", msg.ChatID, msg.TopicID, workspace)

	result, err := a.codex.StartThread(ctx, codex.StartThreadRequest{
		Name:       defaultThreadName(msg),
		Prompt:     resolvedPrompt(msg),
		Workspace:  workspace,
		Settings:   settings,
		ImagePaths: append([]string(nil), msg.PreparedImagePaths...),
	}, streamHandler)
	if err != nil {
		return codex.StartThreadResult{}, fmt.Errorf("start codex thread: %w", err)
	}

	if err := a.store.SaveBinding(ctx, store.TopicBinding{
		ChatID:     msg.ChatID,
		TopicID:    msg.TopicID,
		SessionID:  result.Thread.SessionID,
		Provider:   result.Thread.Provider,
		TopicTitle: topicTitleOrDefault(msg),
		Workspace:  result.Thread.Workspace,
	}); err != nil {
		return codex.StartThreadResult{}, fmt.Errorf("save topic binding: %w", err)
	}

	a.recordStats(msg.ChatID, msg.TopicID, result.Stats)
	return result, nil
}

func (a *App) continueThread(
	ctx context.Context,
	msg telegram.IncomingUpdate,
	binding store.TopicBinding,
	streamHandler codex.StreamHandler,
) (codex.ResumeThreadResult, error) {
	_, _, settings, err := a.topicSettings(ctx, msg)
	if err != nil {
		return codex.ResumeThreadResult{}, fmt.Errorf("resolve topic settings: %w", err)
	}
	workspace := binding.Workspace
	if strings.TrimSpace(msg.PreparedWorkspace) != "" {
		workspace = msg.PreparedWorkspace
	}
	result, err := a.codex.ResumeThread(ctx, codex.ResumeThreadRequest{
		SessionID:  binding.SessionID,
		Message:    resolvedPrompt(msg),
		Workspace:  workspace,
		Settings:   settings,
		ImagePaths: append([]string(nil), msg.PreparedImagePaths...),
	}, streamHandler)
	if err != nil {
		return codex.ResumeThreadResult{}, fmt.Errorf("resume codex thread %s: %w", binding.SessionID, err)
	}

	binding.SessionID = result.SessionID
	binding.Provider = result.Provider
	if err := a.store.SaveBinding(ctx, binding); err != nil {
		return codex.ResumeThreadResult{}, fmt.Errorf("refresh topic binding: %w", err)
	}

	a.recordStats(msg.ChatID, msg.TopicID, result.Stats)
	return result, nil
}

func (a *App) recordStats(chatID, topicID int64, stats codex.ExecutionStats) {
	key := topicKey(chatID, topicID)

	a.statsMu.Lock()
	defer a.statsMu.Unlock()

	current := a.stats[key]
	current.LastRunMode = stats.Mode
	current.LastRunDuration = stats.Duration
	switch stats.Mode {
	case codex.RunModeStart:
		current.TotalStartRuns++
		current.LastStartDuration = stats.Duration
	case codex.RunModeResume:
		current.TotalResumeRuns++
		current.LastResumeDuration = stats.Duration
	}
	a.stats[key] = current
}

func (a *App) topicStats(chatID, topicID int64) (topicStats, bool) {
	key := topicKey(chatID, topicID)

	a.statsMu.RLock()
	defer a.statsMu.RUnlock()

	stats, ok := a.stats[key]
	return stats, ok
}

func topicKey(chatID, topicID int64) string {
	return fmt.Sprintf("%d:%d", chatID, topicID)
}

func (w *topicWorker) begin(job queuedMessage) (bool, chan queuedMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.busy {
		return true, nil
	}

	w.busy = true
	w.active = nil
	w.current = job
	return false, w.jobs
}

func (w *topicWorker) enqueueFollowUp(job queuedMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.followUps = append(w.followUps, job)
}

func (w *topicWorker) ensureActiveProgress(
	ctx context.Context,
	bot *telegram.Bot,
	logger *log.Logger,
	job queuedMessage,
) *progressTracker {
	w.mu.Lock()
	if w.active != nil {
		active := w.active
		w.mu.Unlock()
		return active
	}
	w.mu.Unlock()

	progress := newProgressTracker(ctx, bot, logger, job.Update, job.AckMessageID, catalogForMessage(job.Update))

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active == nil {
		w.active = progress
		w.current = job
		return progress
	}

	progress.closeDone()
	return w.active
}

func (w *topicWorker) takeFollowUpsOrMarkIdle() []queuedMessage {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.followUps) == 0 {
		w.busy = false
		w.activeSessionID = ""
		w.activeTurnID = ""
		return nil
	}

	followUps := append([]queuedMessage(nil), w.followUps...)
	w.followUps = nil
	return followUps
}

func (w *topicWorker) releasePending() []queuedMessage {
	w.mu.Lock()
	defer w.mu.Unlock()

	followUps := append([]queuedMessage(nil), w.followUps...)
	w.followUps = nil
	w.busy = false
	w.active = nil
	w.current = queuedMessage{}
	w.activeSessionID = ""
	w.activeTurnID = ""
	return followUps
}

func (w *topicWorker) promoteFollowUp(job queuedMessage, next *progressTracker) (queuedMessage, *progressTracker) {
	w.mu.Lock()
	defer w.mu.Unlock()

	previousJob := w.current
	previousProgress := w.active
	w.current = job
	w.active = next
	return previousJob, previousProgress
}

func (w *topicWorker) activeProgress() *progressTracker {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.active
}

func (w *topicWorker) finishRun(active *progressTracker) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.active == active {
		w.active = nil
	}
	w.current = queuedMessage{}
	w.activeSessionID = ""
	w.activeTurnID = ""
}

func (w *topicWorker) updateActiveTurn(event codex.StreamEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(event.SessionID) != "" {
		w.activeSessionID = strings.TrimSpace(event.SessionID)
	}
	if strings.TrimSpace(event.TurnID) != "" {
		w.activeTurnID = strings.TrimSpace(event.TurnID)
	}
}

func (w *topicWorker) clearActiveTurn() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.activeSessionID = ""
	w.activeTurnID = ""
}

func (w *topicWorker) activeTurn() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.activeSessionID, w.activeTurnID
}

func (a *App) mergeFollowUpPrompt(ctx context.Context, messages []telegram.IncomingUpdate) string {
	if len(messages) == 1 {
		return resolvedPrompt(messages[0])
	}

	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(resolvedPrompt(message))
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. %s", len(parts)+1, text))
	}
	if len(parts) == 0 {
		return ""
	}

	loc := a.catalogForMessage(ctx, messages[len(messages)-1])
	return loc.T(
		"你处理上一条消息时，用户又补充了这些要求。请继续当前任务，并把下面所有补充都考虑进去：\n",
		"While you were still working, the user added these follow-up requirements. Continue the current task and account for all of them:\n",
	) + strings.Join(parts, "\n")
}

func (a *App) mergeFollowUpDisplayText(messages []telegram.IncomingUpdate) string {
	if len(messages) == 1 {
		return messages[0].Text
	}

	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. %s", len(parts)+1, text))
	}
	return strings.Join(parts, "\n")
}

func mergeFollowUpImagePaths(messages []telegram.IncomingUpdate) []string {
	var imagePaths []string
	for _, message := range messages {
		imagePaths = append(imagePaths, message.PreparedImagePaths...)
	}
	return uniqueStrings(imagePaths)
}

func resolvedPrompt(msg telegram.IncomingUpdate) string {
	if strings.TrimSpace(msg.PreparedPrompt) != "" {
		return msg.PreparedPrompt
	}
	return strings.TrimSpace(msg.Text)
}

func mergeUniqueStrings(existing, next []string) []string {
	return uniqueStrings(append(append([]string(nil), existing...), next...))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (a *App) failPendingFollowUps(ctx context.Context, messages []queuedMessage, err error) {
	for _, message := range messages {
		loc := a.catalogForMessage(ctx, message.Update)
		text := fmt.Sprintf("%s: %s", loc.T("当前运行失败了，请重新发送这条补充要求", "The current run failed. Please resend this follow-up"), userVisibleError(err))
		if message.AckMessageID != 0 {
			_ = a.bot.EditMessageText(ctx, telegram.EditMessage{
				ChatID:    message.Update.ChatID,
				MessageID: message.AckMessageID,
				Text:      text,
			})
			continue
		}
		_ = a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           message.Update.ChatID,
			TopicID:          message.Update.TopicID,
			ReplyToMessageID: message.Update.MessageID,
			Text:             text,
		})
	}
}

func catalogForMessage(update telegram.IncomingUpdate) i18n.Catalog {
	return i18n.Resolve(update.LanguageCode, update.Text, "", i18n.PreferenceAuto, true)
}
