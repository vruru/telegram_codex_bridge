package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type TokenHealth struct {
	APIBaseURL string `json:"api_base_url"`
	Valid      bool   `json:"valid"`
	BotID      int64  `json:"bot_id,omitempty"`
	Username   string `json:"username,omitempty"`
	Error      string `json:"error,omitempty"`
}

func ValidateBotToken(ctx context.Context, apiBaseURL, token string) TokenHealth {
	baseURL := strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}

	health := TokenHealth{
		APIBaseURL: baseURL,
	}

	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		health.Error = "telegram bot token is required"
		return health
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/bot%s/getMe", baseURL, trimmedToken), nil)
	if err != nil {
		health.Error = err.Error()
		return health
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		health.Error = err.Error()
		return health
	}
	defer resp.Body.Close()

	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		health.Error = fmt.Sprintf("decode telegram getMe response: %v", err)
		return health
	}

	if !envelope.OK {
		health.Error = strings.TrimSpace(envelope.Description)
		if health.Error == "" {
			health.Error = "telegram getMe returned ok=false"
		}
		return health
	}

	health.Valid = true
	health.BotID = envelope.Result.ID
	health.Username = envelope.Result.Username
	return health
}
