package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteTopicStoreSaveTopicPreferencesWithProvider(t *testing.T) {
	store, err := NewSQLiteTopicStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}

	ctx := context.Background()
	preferences := TopicPreferences{
		Provider:        "gemini",
		Model:           "gemini-2.5-flash",
		ReasoningEffort: "high",
		ServiceTier:     "fast",
	}
	if err := store.SaveTopicPreferences(ctx, 100, 200, preferences); err != nil {
		t.Fatalf("save topic preferences: %v", err)
	}

	saved, found, err := store.LoadTopicPreferences(ctx, 100, 200)
	if err != nil {
		t.Fatalf("load topic preferences: %v", err)
	}
	if !found {
		t.Fatalf("expected topic preferences to be found")
	}
	if saved.Provider != preferences.Provider {
		t.Fatalf("expected provider %q, got %q", preferences.Provider, saved.Provider)
	}
	if saved.Model != preferences.Model {
		t.Fatalf("expected model %q, got %q", preferences.Model, saved.Model)
	}
	if saved.ReasoningEffort != preferences.ReasoningEffort {
		t.Fatalf("expected reasoning %q, got %q", preferences.ReasoningEffort, saved.ReasoningEffort)
	}
	if saved.ServiceTier != preferences.ServiceTier {
		t.Fatalf("expected service tier %q, got %q", preferences.ServiceTier, saved.ServiceTier)
	}
}
