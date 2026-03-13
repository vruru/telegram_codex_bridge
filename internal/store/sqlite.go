package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteTopicStore struct {
	db *sql.DB
}

const updateOffsetStateKey = "telegram_update_offset"

func NewSQLiteTopicStore(path string) (*SQLiteTopicStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteTopicStore{db: db}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteTopicStore) SaveBinding(ctx context.Context, binding TopicBinding) error {
	now := time.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now

	var archivedAt any
	if binding.ArchivedAt != nil {
		archivedAt = binding.ArchivedAt.UTC().Format(time.RFC3339Nano)
	}

	_, err := s.db.ExecContext(ctx, `
		insert into topic_bindings (
			chat_id,
			topic_id,
			session_id,
			provider,
			topic_title,
			workspace_root,
			archived_at,
			created_at,
			updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(chat_id, topic_id) do update set
			session_id = excluded.session_id,
			provider = excluded.provider,
			topic_title = excluded.topic_title,
			workspace_root = excluded.workspace_root,
			archived_at = excluded.archived_at,
			updated_at = excluded.updated_at
	`,
		binding.ChatID,
		binding.TopicID,
		binding.SessionID,
		normalizeStoredProvider(binding.Provider),
		binding.TopicTitle,
		binding.Workspace,
		archivedAt,
		binding.CreatedAt.Format(time.RFC3339Nano),
		binding.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save binding: %w", err)
	}

	return nil
}

func (s *SQLiteTopicStore) LookupBinding(ctx context.Context, chatID, topicID int64) (TopicBinding, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		select
			chat_id,
			topic_id,
			session_id,
			provider,
			topic_title,
			workspace_root,
			archived_at,
			created_at,
			updated_at
		from topic_bindings
		where chat_id = ? and topic_id = ?
	`,
		chatID,
		topicID,
	)

	binding, err := scanBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TopicBinding{}, false, nil
	}
	if err != nil {
		return TopicBinding{}, false, fmt.Errorf("lookup binding: %w", err)
	}

	return binding, true, nil
}

func (s *SQLiteTopicStore) ListBindingsByChat(ctx context.Context, chatID int64) ([]TopicBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		select
			chat_id,
			topic_id,
			session_id,
			provider,
			topic_title,
			workspace_root,
			archived_at,
			created_at,
			updated_at
		from topic_bindings
		where chat_id = ?
		order by updated_at desc
	`, chatID)
	if err != nil {
		return nil, fmt.Errorf("list bindings by chat: %w", err)
	}
	defer rows.Close()

	var bindings []TopicBinding
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bindings by chat: %w", err)
	}

	return bindings, nil
}

func (s *SQLiteTopicStore) ArchiveBinding(ctx context.Context, chatID, topicID int64, archivedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		update topic_bindings
		set archived_at = ?, updated_at = ?
		where chat_id = ? and topic_id = ?
	`,
		archivedAt.UTC().Format(time.RFC3339Nano),
		archivedAt.UTC().Format(time.RFC3339Nano),
		chatID,
		topicID,
	)
	if err != nil {
		return fmt.Errorf("archive binding: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("archive binding rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("binding not found for chat=%d topic=%d", chatID, topicID)
	}

	return nil
}

func (s *SQLiteTopicStore) LoadTopicPreferences(ctx context.Context, chatID, topicID int64) (TopicPreferences, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		select
			model,
			provider,
			reasoning_effort,
			service_tier,
			updated_at
		from topic_preferences
		where chat_id = ? and topic_id = ?
	`, chatID, topicID)

	var (
		preferences TopicPreferences
		updatedAt   string
	)
	err := row.Scan(&preferences.Model, &preferences.Provider, &preferences.ReasoningEffort, &preferences.ServiceTier, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TopicPreferences{}, false, nil
	}
	if err != nil {
		return TopicPreferences{}, false, fmt.Errorf("load topic preferences: %w", err)
	}

	if updatedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, updatedAt)
		if parseErr != nil {
			return TopicPreferences{}, false, fmt.Errorf("parse topic preferences updated_at: %w", parseErr)
		}
		preferences.UpdatedAt = parsed
	}

	return preferences, true, nil
}

func (s *SQLiteTopicStore) SaveTopicPreferences(ctx context.Context, chatID, topicID int64, preferences TopicPreferences) error {
	if preferences.Provider == "" && preferences.Model == "" && preferences.ReasoningEffort == "" && preferences.ServiceTier == "" {
		_, err := s.db.ExecContext(ctx, `
			delete from topic_preferences
			where chat_id = ? and topic_id = ?
		`, chatID, topicID)
		if err != nil {
			return fmt.Errorf("delete topic preferences: %w", err)
		}
		return nil
	}

	now := time.Now().UTC()
	preferences.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into topic_preferences (
			chat_id,
			topic_id,
			model,
			provider,
			reasoning_effort,
			service_tier,
			updated_at
		) values (?, ?, ?, ?, ?, ?, ?)
		on conflict(chat_id, topic_id) do update set
			model = excluded.model,
			provider = excluded.provider,
			reasoning_effort = excluded.reasoning_effort,
			service_tier = excluded.service_tier,
			updated_at = excluded.updated_at
	`, chatID, topicID, preferences.Model, preferences.Provider, preferences.ReasoningEffort, preferences.ServiceTier, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save topic preferences: %w", err)
	}

	return nil
}

func (s *SQLiteTopicStore) LoadLanguagePreference(ctx context.Context, chatID, topicID int64) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		select language
		from language_preferences
		where chat_id = ? and topic_id = ?
	`, chatID, topicID)

	var language string
	err := row.Scan(&language)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load language preference: %w", err)
	}

	return language, true, nil
}

func (s *SQLiteTopicStore) SaveLanguagePreference(ctx context.Context, chatID, topicID int64, language string) error {
	if strings.TrimSpace(language) == "" {
		_, err := s.db.ExecContext(ctx, `
			delete from language_preferences
			where chat_id = ? and topic_id = ?
		`, chatID, topicID)
		if err != nil {
			return fmt.Errorf("delete language preference: %w", err)
		}
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		insert into language_preferences (
			chat_id,
			topic_id,
			language,
			updated_at
		) values (?, ?, ?, ?)
		on conflict(chat_id, topic_id) do update set
			language = excluded.language,
			updated_at = excluded.updated_at
	`, chatID, topicID, language, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save language preference: %w", err)
	}

	return nil
}

func (s *SQLiteTopicStore) LoadUpdateOffset(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx, `
		select value
		from bridge_state
		where key = ?
	`, updateOffsetStateKey)

	var raw string
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load update offset: %w", err)
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse update offset: %w", err)
	}

	return parsed, nil
}

func (s *SQLiteTopicStore) SaveUpdateOffset(ctx context.Context, offset int64) error {
	_, err := s.db.ExecContext(ctx, `
		insert into bridge_state (key, value)
		values (?, ?)
		on conflict(key) do update set
			value = excluded.value
	`, updateOffsetStateKey, strconv.FormatInt(offset, 10))
	if err != nil {
		return fmt.Errorf("save update offset: %w", err)
	}

	return nil
}

func (s *SQLiteTopicStore) configure() error {
	// Keep SQLite predictable for concurrent bot polling + worker goroutines.
	_, err := s.db.Exec(`
		pragma journal_mode = wal;
		pragma busy_timeout = 5000;
		pragma synchronous = normal;
	`)
	if err != nil {
		return fmt.Errorf("configure sqlite pragmas: %w", err)
	}

	return nil
}

func (s *SQLiteTopicStore) initSchema() error {
	_, err := s.db.Exec(`
		create table if not exists topic_bindings (
			chat_id integer not null,
			topic_id integer not null,
			session_id text not null,
			provider text not null default 'codex',
			topic_title text not null,
			workspace_root text not null,
			archived_at text,
			created_at text not null,
			updated_at text not null,
			primary key (chat_id, topic_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("initialize topic_bindings schema: %w", err)
	}
	if err := s.ensureColumn("topic_bindings", "provider", "text not null default 'codex'"); err != nil {
		return fmt.Errorf("initialize topic_bindings.provider schema: %w", err)
	}

	_, err = s.db.Exec(`
		create table if not exists language_preferences (
			chat_id integer not null,
			topic_id integer not null,
			language text not null,
			updated_at text not null,
			primary key (chat_id, topic_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("initialize language_preferences schema: %w", err)
	}

	_, err = s.db.Exec(`
		create table if not exists topic_preferences (
			chat_id integer not null,
			topic_id integer not null,
			model text not null default '',
			provider text not null default '',
			reasoning_effort text not null default '',
			service_tier text not null default '',
			updated_at text not null,
			primary key (chat_id, topic_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("initialize topic_preferences schema: %w", err)
	}
	if err := s.ensureColumn("topic_preferences", "provider", "text not null default ''"); err != nil {
		return fmt.Errorf("initialize topic_preferences.provider schema: %w", err)
	}

	_, err = s.db.Exec(`
		create table if not exists bridge_state (
			key text primary key,
			value text not null
		)
	`)
	if err != nil {
		return fmt.Errorf("initialize bridge_state schema: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBinding(scanner rowScanner) (TopicBinding, error) {
	var (
		binding       TopicBinding
		archivedAtRaw sql.NullString
		createdAtRaw  string
		updatedAtRaw  string
	)

	err := scanner.Scan(
		&binding.ChatID,
		&binding.TopicID,
		&binding.SessionID,
		&binding.Provider,
		&binding.TopicTitle,
		&binding.Workspace,
		&archivedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if err != nil {
		return TopicBinding{}, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return TopicBinding{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtRaw)
	if err != nil {
		return TopicBinding{}, fmt.Errorf("parse updated_at: %w", err)
	}
	binding.CreatedAt = createdAt
	binding.UpdatedAt = updatedAt

	if archivedAtRaw.Valid {
		archivedAt, err := time.Parse(time.RFC3339Nano, archivedAtRaw.String)
		if err != nil {
			return TopicBinding{}, fmt.Errorf("parse archived_at: %w", err)
		}
		binding.ArchivedAt = &archivedAt
	}

	binding.Provider = normalizeStoredProvider(binding.Provider)
	return binding, nil
}

func (s *SQLiteTopicStore) ensureColumn(table, column, columnDef string) error {
	exists, err := s.hasColumn(table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = s.db.Exec(fmt.Sprintf("alter table %s add column %s %s", table, column, columnDef))
	return err
}

func (s *SQLiteTopicStore) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("pragma table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultV   any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryKey); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func normalizeStoredProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gemini":
		return "gemini"
	default:
		return "codex"
	}
}
