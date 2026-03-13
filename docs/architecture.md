# Architecture Notes

## Core model

- Telegram chat or forum topic is the user-facing conversation surface
- Bridge storage owns the mapping from Telegram topic to Codex session
- Codex stays the execution engine, reached through `app-server` first and CLI second

## Why this shape

- We avoid coupling business logic to Codex internal SQLite tables
- We can treat Telegram topic lifecycle as the source of truth
- We keep room to swap long polling for webhooks later without rewriting routing

## Main components

### Telegram transport

- Receives updates from the Bot API
- Normalizes chat, topic, user, and message data
- Enforces whitelist checks early

### Routing layer

- Resolves `chat_id + topic_id` to a Codex session id
- Creates a new session when no mapping exists
- Serializes work per topic so one thread is not resumed concurrently

### Codex adapter

- Starts and resumes turns through `codex app-server` over WebSocket JSON-RPC
- Uses official `turn/steer` for in-flight follow-up messages when a turn is active
- Falls back to `codex exec` / `codex exec resume` if `app-server` is unavailable
- Uses `model/list` to populate model and reasoning menus when available

### Storage

- Persists topic bindings and archive state
- Stores thread labels, workspace roots, and timestamps
- Keeps a soft-delete marker for deleted Telegram topics

## First persistence schema

```sql
create table topic_bindings (
  chat_id integer not null,
  topic_id integer not null,
  codex_session_id text not null,
  topic_title text not null,
  workspace_root text not null,
  archived_at text,
  created_at text not null,
  updated_at text not null,
  primary key (chat_id, topic_id)
);
```

## Operational notes

- Run the bridge under `launchd` or `systemd`
- Persist Telegram update offsets
- Add backoff for Codex adapter startup failures
- Never route untrusted users or chats into Codex execution
