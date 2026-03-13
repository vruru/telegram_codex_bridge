package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

type UsageSnapshot struct {
	Available       bool         `json:"available"`
	Error           string       `json:"error,omitempty"`
	PlanType        string       `json:"plan_type,omitempty"`
	PrimaryWindow   *UsageWindow `json:"primary_window,omitempty"`
	SecondaryWindow *UsageWindow `json:"secondary_window,omitempty"`
	FetchedAt       int64        `json:"fetched_at,omitempty"`
}

type UsageWindow struct {
	UsedPercent        int   `json:"used_percent"`
	RemainingPercent   int   `json:"remaining_percent"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
	ResetAfterSeconds  int64 `json:"reset_after_seconds"`
	ResetAt            int64 `json:"reset_at"`
}

type usageAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type usageAPIResponse struct {
	PlanType  string             `json:"plan_type"`
	RateLimit usageRateLimitInfo `json:"rate_limit"`
}

type usageRateLimitInfo struct {
	PrimaryWindow   *usageWindowPayload `json:"primary_window"`
	SecondaryWindow *usageWindowPayload `json:"secondary_window"`
}

type usageWindowPayload struct {
	UsedPercent        int   `json:"used_percent"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
	ResetAfterSeconds  int64 `json:"reset_after_seconds"`
	ResetAt            int64 `json:"reset_at"`
}

func FetchUsageSnapshot(ctx context.Context) (UsageSnapshot, error) {
	auth, err := loadUsageAuth()
	if err != nil {
		return UsageSnapshot{Error: err.Error()}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return UsageSnapshot{Error: err.Error()}, fmt.Errorf("build usage request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "telegram-codex-bridge/1.0")
	if accountID := strings.TrimSpace(auth.Tokens.AccountID); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return UsageSnapshot{Error: err.Error()}, fmt.Errorf("fetch usage snapshot: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return UsageSnapshot{Error: err.Error()}, fmt.Errorf("read usage snapshot: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			message = "codex login expired; open Codex and sign in again"
		}
		err = fmt.Errorf("usage request failed: %s", message)
		return UsageSnapshot{Error: err.Error()}, err
	}

	var payload usageAPIResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageSnapshot{Error: err.Error()}, fmt.Errorf("decode usage snapshot: %w", err)
	}

	snapshot := UsageSnapshot{
		Available:       true,
		PlanType:        strings.TrimSpace(payload.PlanType),
		PrimaryWindow:   newUsageWindow(payload.RateLimit.PrimaryWindow),
		SecondaryWindow: newUsageWindow(payload.RateLimit.SecondaryWindow),
		FetchedAt:       time.Now().Unix(),
	}

	return snapshot, nil
}

func loadUsageAuth() (usageAuthFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return usageAuthFile{}, fmt.Errorf("resolve home directory: %w", err)
	}

	path := filepath.Join(home, ".codex", "auth.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return usageAuthFile{}, fmt.Errorf("read codex auth file: %w", err)
	}

	var auth usageAuthFile
	if err := json.Unmarshal(body, &auth); err != nil {
		return usageAuthFile{}, fmt.Errorf("decode codex auth file: %w", err)
	}

	if strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		return usageAuthFile{}, fmt.Errorf("codex auth file does not contain an access token")
	}

	return auth, nil
}

func newUsageWindow(payload *usageWindowPayload) *UsageWindow {
	if payload == nil {
		return nil
	}

	remaining := 100 - payload.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}

	return &UsageWindow{
		UsedPercent:        payload.UsedPercent,
		RemainingPercent:   remaining,
		LimitWindowSeconds: payload.LimitWindowSeconds,
		ResetAfterSeconds:  payload.ResetAfterSeconds,
		ResetAt:            payload.ResetAt,
	}
}
