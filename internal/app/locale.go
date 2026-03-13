package app

import (
	"context"

	"telegram-codex-bridge/internal/i18n"
	"telegram-codex-bridge/internal/telegram"
)

func (a *App) catalogForCommand(ctx context.Context, msg telegram.IncomingUpdate) i18n.Catalog {
	return a.catalogForUpdate(ctx, msg, false)
}

func (a *App) catalogForMessage(ctx context.Context, msg telegram.IncomingUpdate) i18n.Catalog {
	return a.catalogForUpdate(ctx, msg, true)
}

func (a *App) catalogForPreference(language string) i18n.Catalog {
	return i18n.New(i18n.NormalizePreference(language))
}

func (a *App) catalogForUpdate(ctx context.Context, msg telegram.IncomingUpdate, preferMessage bool) i18n.Catalog {
	preference := ""
	if stored, ok, err := a.store.LoadLanguagePreference(ctx, msg.ChatID, msg.TopicID); err == nil && ok {
		preference = stored
	} else if err != nil {
		a.debugf("load language preference chat=%d topic=%d: %v", msg.ChatID, msg.TopicID, err)
	}

	return i18n.Resolve(msg.LanguageCode, msg.Text, preference, a.defaultLanguage, preferMessage)
}

func (a *App) effectiveLanguage(ctx context.Context, msg telegram.IncomingUpdate, preferMessage bool) string {
	return a.catalogForUpdate(ctx, msg, preferMessage).Code()
}
