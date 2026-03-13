package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type TopicBinding struct {
	ChatID     int64
	TopicID    int64
	SessionID  string
	TopicTitle string
	Workspace  string
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TopicPreferences struct {
	Model           string
	ReasoningEffort string
	ServiceTier     string
	UpdatedAt       time.Time
}

type TopicStore interface {
	SaveBinding(ctx context.Context, binding TopicBinding) error
	LookupBinding(ctx context.Context, chatID, topicID int64) (TopicBinding, bool, error)
	ListBindingsByChat(ctx context.Context, chatID int64) ([]TopicBinding, error)
	ArchiveBinding(ctx context.Context, chatID, topicID int64, archivedAt time.Time) error
	LoadTopicPreferences(ctx context.Context, chatID, topicID int64) (TopicPreferences, bool, error)
	SaveTopicPreferences(ctx context.Context, chatID, topicID int64, preferences TopicPreferences) error
	LoadLanguagePreference(ctx context.Context, chatID, topicID int64) (string, bool, error)
	SaveLanguagePreference(ctx context.Context, chatID, topicID int64, language string) error
	LoadUpdateOffset(ctx context.Context) (int64, error)
	SaveUpdateOffset(ctx context.Context, offset int64) error
}

type MemoryTopicStore struct {
	mu              sync.RWMutex
	bindings        map[string]TopicBinding
	preferencesByID map[string]TopicPreferences
	languageByTopic map[string]string
	offset          int64
}

func NewMemoryTopicStore() *MemoryTopicStore {
	return &MemoryTopicStore{
		bindings:        make(map[string]TopicBinding),
		preferencesByID: make(map[string]TopicPreferences),
		languageByTopic: make(map[string]string),
	}
}

func (s *MemoryTopicStore) SaveBinding(ctx context.Context, binding TopicBinding) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	s.bindings[key(binding.ChatID, binding.TopicID)] = binding
	return nil
}

func (s *MemoryTopicStore) LookupBinding(ctx context.Context, chatID, topicID int64) (TopicBinding, bool, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	binding, ok := s.bindings[key(chatID, topicID)]
	return binding, ok, nil
}

func (s *MemoryTopicStore) ListBindingsByChat(ctx context.Context, chatID int64) ([]TopicBinding, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]TopicBinding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		if binding.ChatID == chatID {
			result = append(result, binding)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})

	return result, nil
}

func (s *MemoryTopicStore) ArchiveBinding(ctx context.Context, chatID, topicID int64, archivedAt time.Time) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(chatID, topicID)
	binding, ok := s.bindings[k]
	if !ok {
		return fmt.Errorf("binding not found for chat=%d topic=%d", chatID, topicID)
	}

	binding.ArchivedAt = &archivedAt
	binding.UpdatedAt = archivedAt.UTC()
	s.bindings[k] = binding
	return nil
}

func (s *MemoryTopicStore) LoadTopicPreferences(ctx context.Context, chatID, topicID int64) (TopicPreferences, bool, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	preferences, ok := s.preferencesByID[key(chatID, topicID)]
	return preferences, ok, nil
}

func (s *MemoryTopicStore) SaveTopicPreferences(ctx context.Context, chatID, topicID int64, preferences TopicPreferences) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(chatID, topicID)
	if preferences.Model == "" && preferences.ReasoningEffort == "" && preferences.ServiceTier == "" {
		delete(s.preferencesByID, k)
		return nil
	}

	preferences.UpdatedAt = time.Now().UTC()
	s.preferencesByID[k] = preferences
	return nil
}

func (s *MemoryTopicStore) LoadLanguagePreference(ctx context.Context, chatID, topicID int64) (string, bool, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	language, ok := s.languageByTopic[key(chatID, topicID)]
	return language, ok, nil
}

func (s *MemoryTopicStore) SaveLanguagePreference(ctx context.Context, chatID, topicID int64, language string) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(chatID, topicID)
	if language == "" {
		delete(s.languageByTopic, k)
		return nil
	}

	s.languageByTopic[k] = language
	return nil
}

func (s *MemoryTopicStore) LoadUpdateOffset(ctx context.Context) (int64, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.offset, nil
}

func (s *MemoryTopicStore) SaveUpdateOffset(ctx context.Context, offset int64) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	s.offset = offset
	return nil
}

func key(chatID, topicID int64) string {
	return fmt.Sprintf("%d:%d", chatID, topicID)
}
