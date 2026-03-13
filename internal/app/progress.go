package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/telegram"
)

const typingActionInterval = 4 * time.Second

type progressTracker struct {
	ctx          context.Context
	bot          *telegram.Bot
	logger       *log.Logger
	update       telegram.IncomingUpdate
	ackMessageID int64
	loc          i18n.Catalog

	done     chan struct{}
	doneOnce sync.Once
}

func newProgressTracker(
	ctx context.Context,
	bot *telegram.Bot,
	logger *log.Logger,
	update telegram.IncomingUpdate,
	ackMessageID int64,
	loc i18n.Catalog,
) *progressTracker {
	tracker := &progressTracker{
		ctx:          ctx,
		bot:          bot,
		logger:       logger,
		update:       update,
		ackMessageID: ackMessageID,
		loc:          loc,
		done:         make(chan struct{}),
	}

	go tracker.typingLoop(ctx)
	return tracker
}

func (p *progressTracker) OnStreamEvent(event codex.StreamEvent) {
	_ = event
}

func (p *progressTracker) DeliverFinal(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	p.closeDone()
	if text == "" {
		return nil
	}

	p.deleteAckMessage(ctx)
	return p.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           p.update.ChatID,
		TopicID:          p.update.TopicID,
		ReplyToMessageID: p.update.MessageID,
		Text:             text,
	})
}

func (p *progressTracker) Finish(_ context.Context, _ codex.ExecutionStats) {
	p.closeDone()
}

func (p *progressTracker) Handoff(_ context.Context, _ string) {
	p.closeDone()
}

func (p *progressTracker) Fail(ctx context.Context, err error) {
	p.closeDone()
	p.deleteAckMessage(ctx)
	if sendErr := p.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           p.update.ChatID,
		TopicID:          p.update.TopicID,
		ReplyToMessageID: p.update.MessageID,
		Text:             fmt.Sprintf("%s: %s", p.loc.T("处理失败", "Request failed"), userVisibleError(err)),
	}); sendErr != nil {
		p.logger.Printf("send failed message chat=%d topic=%d: %v", p.update.ChatID, p.update.TopicID, sendErr)
	}
}

func (p *progressTracker) typingLoop(ctx context.Context) {
	if err := p.sendTyping(ctx); err != nil {
		p.logger.Printf("send typing action chat=%d topic=%d: %v", p.update.ChatID, p.update.TopicID, err)
	}

	ticker := time.NewTicker(typingActionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case <-ticker.C:
			if err := p.sendTyping(ctx); err != nil {
				p.logger.Printf("send typing action chat=%d topic=%d: %v", p.update.ChatID, p.update.TopicID, err)
			}
		}
	}
}

func (p *progressTracker) sendTyping(ctx context.Context) error {
	return p.bot.SendChatAction(ctx, p.update.ChatID, p.update.TopicID, "typing")
}

func (p *progressTracker) deleteAckMessage(ctx context.Context) {
	if p.ackMessageID == 0 {
		return
	}
	if err := p.bot.DeleteMessage(ctx, p.update.ChatID, p.ackMessageID); err != nil {
		p.logger.Printf("delete ack message chat=%d topic=%d message=%d: %v", p.update.ChatID, p.update.TopicID, p.ackMessageID, err)
	}
}

func (p *progressTracker) closeDone() {
	p.doneOnce.Do(func() {
		close(p.done)
	})
}
