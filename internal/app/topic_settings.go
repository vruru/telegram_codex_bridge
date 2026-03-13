package app

import (
	"context"
	"fmt"
	"strings"

	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/store"
	"telegram-codex-bridge/internal/telegram"
)

const (
	settingsCallbackPrefix = "settings"
	settingsActionModel    = "model"
	settingsActionThink    = "think"
	settingsActionSpeed    = "speed"
	settingsValueDefault   = "default"
	settingsValueFast      = "fast"
)

func (a *App) handleModelCommand(ctx context.Context, msg telegram.IncomingUpdate, args string) error {
	if strings.TrimSpace(args) == "" {
		return a.presentModelMenu(ctx, msg)
	}

	loc := a.catalogForCommand(ctx, msg)
	catalog, preferences, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	selected := strings.TrimSpace(args)
	if _, ok := catalog.FindModel(selected); !ok {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("这个模型当前不可用。请用 `/model` 查看可选项。", "That model is not currently available. Use `/model` to see the available options."),
		})
	}

	preferences.Model = selected
	if preferences.ReasoningEffort != "" && !catalog.SupportsReasoningEffort(selected, preferences.ReasoningEffort) {
		preferences.ReasoningEffort = ""
	}
	if err := a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences); err != nil {
		return fmt.Errorf("save topic model preference: %w", err)
	}

	effective.Model = selected
	if effective.ReasoningEffort != "" && !catalog.SupportsReasoningEffort(selected, effective.ReasoningEffort) {
		effective.ReasoningEffort = catalog.DefaultReasoningForModel(selected)
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text: fmt.Sprintf(
			"%s %s",
			loc.T("当前话题模型已切换为", "This topic now uses"),
			selected,
		),
	})
}

func (a *App) handleThinkCommand(ctx context.Context, msg telegram.IncomingUpdate, args string) error {
	if strings.TrimSpace(args) == "" {
		return a.presentThinkMenu(ctx, msg)
	}

	loc := a.catalogForCommand(ctx, msg)
	catalog, preferences, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	requested := normalizeThinkArg(args)
	if requested == "" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("只支持 `/think default|none|minimal|low|medium|high|xhigh`。", "Supported values are `/think default|none|minimal|low|medium|high|xhigh`."),
		})
	}

	if requested == settingsValueDefault {
		preferences.ReasoningEffort = ""
	} else {
		if !catalog.SupportsReasoningEffort(effective.Model, requested) {
			return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
				ChatID:           msg.ChatID,
				TopicID:          msg.TopicID,
				ReplyToMessageID: msg.MessageID,
				Text: fmt.Sprintf(
					loc.T("当前模型 `%s` 不支持这个思考等级。请用 `/think` 查看可选项。", "The current model `%s` does not support that reasoning level. Use `/think` to see the available options."),
					effective.Model,
				),
			})
		}
		preferences.ReasoningEffort = requested
	}
	if err := a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences); err != nil {
		return fmt.Errorf("save topic reasoning preference: %w", err)
	}

	label := reasoningLabel(loc, effectiveReasoningEffort(catalog, preferences, effective.Model))
	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text: fmt.Sprintf(
			"%s %s",
			loc.T("当前话题思考等级已设置为", "This topic reasoning level is now set to"),
			label,
		),
	})
}

func (a *App) handleSpeedCommand(ctx context.Context, msg telegram.IncomingUpdate, args string) error {
	if strings.TrimSpace(args) == "" {
		return a.presentSpeedMenu(ctx, msg)
	}

	loc := a.catalogForCommand(ctx, msg)
	catalog, preferences, _, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	requested := normalizeSpeedArg(args)
	if requested == "" {
		return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
			ChatID:           msg.ChatID,
			TopicID:          msg.TopicID,
			ReplyToMessageID: msg.MessageID,
			Text:             loc.T("只支持 `/speed default` 或 `/speed fast`。", "Supported values are `/speed default` or `/speed fast`."),
		})
	}

	if requested == settingsValueDefault {
		preferences.ServiceTier = ""
	} else {
		preferences.ServiceTier = requested
	}
	if err := a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences); err != nil {
		return fmt.Errorf("save topic speed preference: %w", err)
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text: fmt.Sprintf(
			"%s %s",
			loc.T("当前话题速度模式已设置为", "This topic speed mode is now set to"),
			speedLabel(loc, effectiveServiceTier(preferences, catalog.DefaultServiceTier)),
		),
	})
}

func (a *App) handleSettingsCallback(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	action, value, ok := parseSettingsCallback(msg.CallbackData)
	if !ok {
		return a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, loc.T("这个按钮已经失效，请重新发送命令。", "This button is no longer valid. Please send the command again."))
	}

	switch action {
	case settingsActionModel:
		if err := a.applyModelSelection(ctx, msg, value); err != nil {
			_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, userVisibleError(err))
			return err
		}
		_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, loc.T("模型已更新", "Model updated"))
		return a.presentModelMenu(ctx, msg)
	case settingsActionThink:
		if err := a.applyThinkSelection(ctx, msg, value); err != nil {
			_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, userVisibleError(err))
			return err
		}
		_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, loc.T("思考等级已更新", "Reasoning level updated"))
		return a.presentThinkMenu(ctx, msg)
	case settingsActionSpeed:
		if err := a.applySpeedSelection(ctx, msg, value); err != nil {
			_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, userVisibleError(err))
			return err
		}
		_ = a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, loc.T("速度模式已更新", "Speed mode updated"))
		return a.presentSpeedMenu(ctx, msg)
	default:
		return a.bot.AnswerCallbackQuery(ctx, msg.CallbackID, loc.T("这个按钮已经失效，请重新发送命令。", "This button is no longer valid. Please send the command again."))
	}
}

func (a *App) applyModelSelection(ctx context.Context, msg telegram.IncomingUpdate, value string) error {
	catalog, preferences, _, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}
	if _, ok := catalog.FindModel(value); !ok {
		return fmt.Errorf("model %q is not available", value)
	}

	preferences.Model = value
	if preferences.ReasoningEffort != "" && !catalog.SupportsReasoningEffort(value, preferences.ReasoningEffort) {
		preferences.ReasoningEffort = ""
	}
	return a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences)
}

func (a *App) applyThinkSelection(ctx context.Context, msg telegram.IncomingUpdate, value string) error {
	catalog, preferences, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	value = normalizeThinkArg(value)
	if value == "" {
		return fmt.Errorf("unsupported reasoning level")
	}
	if value == settingsValueDefault {
		preferences.ReasoningEffort = ""
		return a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences)
	}
	if !catalog.SupportsReasoningEffort(effective.Model, value) {
		return fmt.Errorf("model %q does not support reasoning level %q", effective.Model, value)
	}

	preferences.ReasoningEffort = value
	return a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences)
}

func (a *App) applySpeedSelection(ctx context.Context, msg telegram.IncomingUpdate, value string) error {
	_, preferences, _, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	value = normalizeSpeedArg(value)
	if value == "" {
		return fmt.Errorf("unsupported speed mode")
	}
	if value == settingsValueDefault {
		preferences.ServiceTier = ""
	} else {
		preferences.ServiceTier = value
	}
	return a.store.SaveTopicPreferences(ctx, msg.ChatID, msg.TopicID, preferences)
}

func (a *App) presentModelMenu(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	catalog, _, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	rows := make([][]telegram.InlineButton, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		label := model.DisplayName
		if model.Slug == effective.Model {
			label = "☑️ " + label
		}
		rows = append(rows, []telegram.InlineButton{{
			Text:         label,
			CallbackData: settingsCallbackData(settingsActionModel, model.Slug),
		}})
	}

	return a.presentSettingsMessage(ctx, msg, loc.T("选择当前话题使用的模型。", "Choose the model for this topic."), rows)
}

func (a *App) presentThinkMenu(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	catalog, preferences, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	model, ok := catalog.FindModel(effective.Model)
	if !ok {
		return fmt.Errorf("current model %q is not available", effective.Model)
	}

	current := effective.ReasoningEffort
	rows := [][]telegram.InlineButton{
		{{
			Text:         checkedLabel(preferences.ReasoningEffort == "", loc.T("默认", "Default")),
			CallbackData: settingsCallbackData(settingsActionThink, settingsValueDefault),
		}},
	}
	for _, level := range model.SupportedReasoningLevels {
		rows = append(rows, []telegram.InlineButton{{
			Text:         checkedLabel(level.Effort == current && preferences.ReasoningEffort != "", reasoningLabel(loc, level.Effort)),
			CallbackData: settingsCallbackData(settingsActionThink, level.Effort),
		}})
	}

	text := fmt.Sprintf(
		"%s\n%s: %s",
		loc.T("选择当前话题的思考等级。", "Choose the reasoning level for this topic."),
		loc.T("当前模型", "Current model"),
		effective.Model,
	)
	return a.presentSettingsMessage(ctx, msg, text, rows)
}

func (a *App) presentSpeedMenu(ctx context.Context, msg telegram.IncomingUpdate) error {
	loc := a.catalogForCommand(ctx, msg)
	_, _, effective, err := a.topicSettings(ctx, msg)
	if err != nil {
		return err
	}

	current := effective.ServiceTier
	rows := [][]telegram.InlineButton{
		{{
			Text:         checkedLabel(current == "", loc.T("默认", "Default")),
			CallbackData: settingsCallbackData(settingsActionSpeed, settingsValueDefault),
		}},
		{{
			Text:         checkedLabel(current == settingsValueFast, speedLabel(loc, settingsValueFast)),
			CallbackData: settingsCallbackData(settingsActionSpeed, settingsValueFast),
		}},
	}

	text := loc.T(
		"选择当前话题的速度模式。当前机器已确认可用的是默认模式和 Fast。",
		"Choose the speed mode for this topic. This machine currently confirms Default and Fast.",
	)
	return a.presentSettingsMessage(ctx, msg, text, rows)
}

func (a *App) presentSettingsMessage(ctx context.Context, msg telegram.IncomingUpdate, text string, rows [][]telegram.InlineButton) error {
	if msg.Kind == telegram.UpdateKindCallbackQuery {
		return a.bot.EditMessageText(ctx, telegram.EditMessage{
			ChatID:         msg.ChatID,
			MessageID:      msg.MessageID,
			Text:           text,
			InlineKeyboard: rows,
		})
	}

	return a.bot.SendMessage(ctx, telegram.OutgoingMessage{
		ChatID:           msg.ChatID,
		TopicID:          msg.TopicID,
		ReplyToMessageID: msg.MessageID,
		Text:             text,
		InlineKeyboard:   rows,
	})
}

func (a *App) topicSettings(ctx context.Context, msg telegram.IncomingUpdate) (codex.SettingsCatalog, store.TopicPreferences, codex.RunSettings, error) {
	catalog, err := a.codex.SettingsCatalog()
	if err != nil {
		return codex.SettingsCatalog{}, store.TopicPreferences{}, codex.RunSettings{}, err
	}

	preferences, _, err := a.store.LoadTopicPreferences(ctx, msg.ChatID, msg.TopicID)
	if err != nil {
		return codex.SettingsCatalog{}, store.TopicPreferences{}, codex.RunSettings{}, fmt.Errorf("load topic preferences: %w", err)
	}
	preferences = sanitizeTopicPreferences(catalog, preferences)

	model := preferences.Model
	if model == "" {
		model = catalog.DefaultModel
	}
	if model == "" && len(catalog.Models) > 0 {
		model = catalog.Models[0].Slug
	}

	return catalog, preferences, codex.RunSettings{
		Model:           model,
		ReasoningEffort: effectiveReasoningEffort(catalog, preferences, model),
		ServiceTier:     effectiveServiceTier(preferences, catalog.DefaultServiceTier),
	}, nil
}

func sanitizeTopicPreferences(catalog codex.SettingsCatalog, preferences store.TopicPreferences) store.TopicPreferences {
	if preferences.Model != "" {
		if _, ok := catalog.FindModel(preferences.Model); !ok {
			preferences.Model = ""
		}
	}

	model := preferences.Model
	if model == "" {
		model = catalog.DefaultModel
	}
	if preferences.ReasoningEffort != "" && !catalog.SupportsReasoningEffort(model, preferences.ReasoningEffort) {
		preferences.ReasoningEffort = ""
	}

	if preferences.ServiceTier != "" && preferences.ServiceTier != settingsValueFast {
		preferences.ServiceTier = ""
	}

	return preferences
}

func effectiveReasoningEffort(catalog codex.SettingsCatalog, preferences store.TopicPreferences, model string) string {
	if catalog.SupportsReasoningEffort(model, preferences.ReasoningEffort) {
		return preferences.ReasoningEffort
	}
	if effort := catalog.DefaultReasoningForModel(model); effort != "" {
		return effort
	}
	return catalog.DefaultReasoningEffort
}

func effectiveServiceTier(preferences store.TopicPreferences, defaultTier string) string {
	if preferences.ServiceTier != "" {
		return preferences.ServiceTier
	}
	return defaultTier
}

func parseSettingsCallback(raw string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 || parts[0] != settingsCallbackPrefix {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func settingsCallbackData(action, value string) string {
	return settingsCallbackPrefix + ":" + action + ":" + value
}

func normalizeThinkArg(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case settingsValueDefault, "auto":
		return settingsValueDefault
	case "none":
		return "none"
	case "minimal", "min":
		return "minimal"
	case "low", "l":
		return "low"
	case "medium", "med", "m":
		return "medium"
	case "high", "h":
		return "high"
	case "xhigh", "xh", "ultra", "very-high", "extra-high":
		return "xhigh"
	default:
		return ""
	}
}

func normalizeSpeedArg(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case settingsValueDefault, "auto":
		return settingsValueDefault
	case settingsValueFast:
		return settingsValueFast
	default:
		return ""
	}
}

func checkedLabel(selected bool, label string) string {
	if selected {
		return "☑️ " + label
	}
	return label
}

func reasoningLabel(loc interface{ T(string, string) string }, effort string) string {
	switch effort {
	case "none":
		return loc.T("无", "None")
	case "minimal":
		return loc.T("极低", "Minimal")
	case "low":
		return loc.T("低", "Low")
	case "medium":
		return loc.T("中", "Medium")
	case "high":
		return loc.T("高", "High")
	case "xhigh":
		return loc.T("超高", "Extra High")
	default:
		return loc.T("默认", "Default")
	}
}

func speedLabel(loc interface{ T(string, string) string }, value string) string {
	switch value {
	case settingsValueFast:
		return "Fast"
	default:
		return loc.T("默认", "Default")
	}
}
