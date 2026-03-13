package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ModelReasoningLevel struct {
	Effort      string
	Description string
}

type ModelInfo struct {
	Slug                     string
	DisplayName              string
	Description              string
	DefaultReasoningLevel    string
	SupportedReasoningLevels []ModelReasoningLevel
	Priority                 int
}

type SettingsCatalog struct {
	Models                 []ModelInfo
	DefaultModel           string
	DefaultReasoningEffort string
	DefaultServiceTier     string
	ServiceTierOptions     []string
}

func (c SettingsCatalog) FindModel(slug string) (ModelInfo, bool) {
	return modelBySlug(c.Models, slug)
}

func (c SettingsCatalog) SupportsReasoningEffort(model, effort string) bool {
	effort = normalizeReasoningEffort(effort)
	if effort == "" {
		return false
	}

	info, ok := c.FindModel(model)
	if !ok {
		return false
	}
	if len(info.SupportedReasoningLevels) == 0 {
		return false
	}
	for _, level := range info.SupportedReasoningLevels {
		if level.Effort == effort {
			return true
		}
	}
	return false
}

func (c SettingsCatalog) DefaultReasoningForModel(model string) string {
	if defaultEffort := defaultReasoningForModel(c.Models, model); defaultEffort != "" {
		return defaultEffort
	}
	return c.DefaultReasoningEffort
}

func (c *CLIClient) SettingsCatalog() (SettingsCatalog, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return SettingsCatalog{}, fmt.Errorf("resolve home directory: %w", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	modelsPath := filepath.Join(home, ".codex", "models_cache.json")

	configValues, err := readCodexConfigValues(configPath)
	if err != nil {
		return SettingsCatalog{}, err
	}

	models, err := readModelsCache(modelsPath)
	if err != nil {
		return SettingsCatalog{}, err
	}

	defaultModel := strings.TrimSpace(configValues["model"])
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
	if defaultServiceTier != "" && defaultServiceTier != "fast" {
		serviceTierOptions = append(serviceTierOptions, defaultServiceTier)
	}

	return SettingsCatalog{
		Models:                 models,
		DefaultModel:           defaultModel,
		DefaultReasoningEffort: defaultReasoning,
		DefaultServiceTier:     defaultServiceTier,
		ServiceTierOptions:     serviceTierOptions,
	}, nil
}

func readCodexConfigValues(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open codex config: %w", err)
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = parseTOMLScalar(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan codex config: %w", err)
	}
	return values, nil
}

func parseTOMLScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.Trim(raw, `"'`)
	return strings.TrimSpace(raw)
}

func readModelsCache(path string) ([]ModelInfo, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read models cache: %w", err)
	}

	var payload struct {
		Models []struct {
			Slug                  string `json:"slug"`
			DisplayName           string `json:"display_name"`
			Description           string `json:"description"`
			DefaultReasoningLevel string `json:"default_reasoning_level"`
			Priority              int    `json:"priority"`
			Visibility            string `json:"visibility"`
			SupportedLevels       []struct {
				Effort      string `json:"effort"`
				Description string `json:"description"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models cache: %w", err)
	}

	models := make([]ModelInfo, 0, len(payload.Models))
	for _, model := range payload.Models {
		if strings.TrimSpace(model.Visibility) != "" && strings.TrimSpace(model.Visibility) != "list" {
			continue
		}

		levels := make([]ModelReasoningLevel, 0, len(model.SupportedLevels))
		for _, level := range model.SupportedLevels {
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
			Slug:                     strings.TrimSpace(model.Slug),
			DisplayName:              emptyFallback(strings.TrimSpace(model.DisplayName), strings.TrimSpace(model.Slug)),
			Description:              strings.TrimSpace(model.Description),
			DefaultReasoningLevel:    normalizeReasoningEffort(model.DefaultReasoningLevel),
			SupportedReasoningLevels: levels,
			Priority:                 model.Priority,
		})
	}

	sort.Slice(models, func(i, j int) bool {
		if models[i].Priority != models[j].Priority {
			return models[i].Priority < models[j].Priority
		}
		return models[i].DisplayName < models[j].DisplayName
	})

	return models, nil
}

func normalizeReasoningEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return "none"
	case "minimal", "min":
		return "minimal"
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "very-high", "very_high", "extra-high", "extra_high":
		return "xhigh"
	default:
		return ""
	}
}

func normalizeServiceTier(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fast":
		return "fast"
	case "flex":
		return "flex"
	default:
		return ""
	}
}

func defaultReasoningForModel(models []ModelInfo, model string) string {
	for _, candidate := range models {
		if candidate.Slug == model {
			return candidate.DefaultReasoningLevel
		}
	}
	return ""
}

func modelBySlug(models []ModelInfo, slug string) (ModelInfo, bool) {
	for _, model := range models {
		if model.Slug == slug {
			return model, true
		}
	}
	return ModelInfo{}, false
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
