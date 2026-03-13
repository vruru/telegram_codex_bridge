package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/telegram"
)

var fetchUsageSnapshot = codex.FetchUsageSnapshot

func (a *App) sendLimit(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	if a.currentProvider(ctx, msg) != "codex" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("当前 provider 不支持限额查询。这个命令目前只支持 Codex。", "Quota lookup is not available for the current provider. This command currently only supports Codex."),
		})
	}

	snapshot, err := fetchUsageSnapshot(ctx)
	if err != nil {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             fmt.Sprintf("%s: %s", loc.T("当前无法读取 Codex 限额", "Unable to read Codex quota right now"), userVisibleError(err)),
		})
	}

	title := loc.T("当前 Codex 限额", "Current Codex quota")
	if plan := strings.TrimSpace(snapshot.PlanType); plan != "" {
		title = fmt.Sprintf("%s (%s)", title, plan)
	}

	lines := []string{title}

	if snapshot.PrimaryWindow != nil {
		lines = append(lines, fmt.Sprintf(
			"%s: %s%%, %s %s",
			loc.T("5小时", "5-hour"),
			itoa(snapshot.PrimaryWindow.RemainingPercent),
			loc.T("重置时间", "resets at"),
			formatUsageResetTime(snapshot.PrimaryWindow.ResetAt, loc, true),
		))
	}

	if snapshot.SecondaryWindow != nil {
		lines = append(lines, fmt.Sprintf(
			"%s: %s%%, %s %s",
			loc.T("1周", "1-week"),
			itoa(snapshot.SecondaryWindow.RemainingPercent),
			loc.T("重置时间", "resets at"),
			formatUsageResetTime(snapshot.SecondaryWindow.ResetAt, loc, false),
		))
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             strings.Join(lines, "\n"),
	})
}

func (a *App) handleLanguageCommand(ctx context.Context, msg telegram.IncomingUpdate, args string) error {
	requested := i18n.NormalizePreference(strings.TrimSpace(args))
	if strings.TrimSpace(args) == "" {
		return a.sendLanguageStatus(ctx, msg)
	}

	if requested == i18n.PreferenceAuto && !isAutoValue(args) {
		loc := a.catalogForCommand(ctx, msg)
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("只支持 `/lang auto`、`/lang zh` 或 `/lang en`。", "Supported values are `/lang auto`, `/lang zh`, or `/lang en`."),
		})
	}

	stored := requested
	if requested == i18n.PreferenceAuto {
		stored = ""
	}
	if err := a.store.SaveLanguagePreference(ctx, msg.ChatID, msg.TopicID, stored); err != nil {
		return fmt.Errorf("save language preference: %w", err)
	}

	loc := a.catalogForPreference(requested)
	if requested == i18n.PreferenceAuto {
		loc = a.catalogForCommand(ctx, msg)
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             languageConfirmationText(ctx, a, msg, requested, loc),
	})
}

func (a *App) sendLanguageStatus(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	current := languagePreferenceLabel(loc, a.currentLanguagePreference(ctx, msg))
	text := fmt.Sprintf(
		"%s: %s\n%s",
		loc.T("当前语言", "Current language"),
		current,
		loc.T("可用命令：`/lang auto`、`/lang zh`、`/lang en`", "Available commands: `/lang auto`, `/lang zh`, `/lang en`"),
	)

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             text,
	})
}

func (a *App) currentLanguagePreference(ctx context.Context, msg telegram.IncomingUpdate) string {
	if stored, ok, err := a.store.LoadLanguagePreference(ctx, msg.ChatID, msg.TopicID); err == nil && ok {
		return i18n.NormalizePreference(stored)
	}
	if a.defaultLanguage != "" && a.defaultLanguage != i18n.PreferenceAuto {
		return a.defaultLanguage
	}
	return i18n.PreferenceAuto
}

func languageConfirmationText(ctx context.Context, a *App, msg telegram.IncomingUpdate, preference string, loc i18n.Catalog) string {
	if preference == i18n.PreferenceAuto {
		if a.defaultLanguage == i18n.PreferenceZH || a.defaultLanguage == i18n.PreferenceEN {
			return fmt.Sprintf(
				"%s %s",
				loc.T("当前话题已改为自动语言。默认回退语言是", "This topic now uses automatic language selection. The default fallback language is"),
				languagePreferenceLabel(loc, a.defaultLanguage),
			)
		}
		return loc.T("当前话题已改为自动语言。", "This topic now uses automatic language selection.")
	}

	return fmt.Sprintf(
		"%s %s",
		loc.T("当前话题语言已设置为", "This topic language is now set to"),
		languagePreferenceLabel(loc, preference),
	)
}

func languagePreferenceLabel(loc i18n.Catalog, preference string) string {
	switch i18n.NormalizePreference(preference) {
	case i18n.PreferenceZH:
		return loc.T("中文", "Chinese")
	case i18n.PreferenceEN:
		return loc.T("英文", "English")
	default:
		return loc.T("自动", "Auto")
	}
}

func isAutoValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "default", "system":
		return true
	default:
		return false
	}
}

func formatUsageResetTime(unix int64, loc i18n.Catalog, short bool) string {
	if unix <= 0 {
		return "-"
	}

	resetAt := time.Unix(unix, 0).Local()
	if short {
		return resetAt.Format("15:04")
	}
	if loc.Code() == i18n.PreferenceZH {
		return resetAt.Format("1月2日 15:04")
	}
	return resetAt.Format("Jan 2 15:04")
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
