package app

import (
	"context"
	"fmt"
	"strings"

	"telegram-codex-bridge/internal/config"
	"telegram-codex-bridge/internal/telegram"
)

func (a *App) handlePermissionCommand(ctx context.Context, msg telegram.IncomingUpdate, args string) error {
	loc := a.catalogForCommand(ctx, msg)
	requested := normalizePermissionArg(args)
	if strings.TrimSpace(args) == "" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text: fmt.Sprintf(
				"%s: %s\n%s",
				loc.T("当前 Codex 权限", "Current Codex permission"),
				permissionLabel(loc, a.codex.PermissionMode()),
				loc.T("可用命令：`/permission default`、`/permission full-access`", "Available commands: `/permission default`, `/permission full-access`"),
			),
		})
	}

	if requested == "" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("只支持 `/permission default` 或 `/permission full-access`。", "Supported values are `/permission default` or `/permission full-access`."),
		})
	}

	if err := config.UpsertEnvValue(a.resolvedEnvPath(), "CODEX_PERMISSION_MODE", requested); err != nil {
		return fmt.Errorf("update CODEX_PERMISSION_MODE: %w", err)
	}
	a.codex.SetPermissionMode(requested)

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text: fmt.Sprintf(
			"%s %s",
			loc.T("当前 Codex 权限已设置为", "Codex permission is now set to"),
			permissionLabel(loc, requested),
		),
	})
}

func normalizePermissionArg(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "default", "normal":
		return "default"
	case "full", "full-access", "danger", "danger-full-access":
		return "full-access"
	default:
		return ""
	}
}

func permissionLabel(loc interface{ T(string, string) string }, mode string) string {
	switch mode {
	case "full-access":
		return loc.T("完全访问", "Full Access")
	default:
		return loc.T("默认", "Default")
	}
}

func (a *App) resolvedEnvPath() string {
	if strings.TrimSpace(a.envPath) != "" {
		return a.envPath
	}
	return ".env"
}
