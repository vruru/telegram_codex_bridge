# Architecture

The Telegram Codex Bridge connects a Telegram bot to a local coding agent (primarily Codex, with optional Gemini support). The system is designed as a single-process Go application that manages multiple independent conversation topics, each mapped to a persistent session.

## Data Persistence

All persistent state is stored in a SQLite database (default: `root/data/bridge.db`). The schema consists of four tables. [internal/store/sqlite.go](../internal/store/sqlite.go) is the schema source; [internal/store/store.go](../internal/store/store.go) defines the store interface and data types:

### `topic_bindings`

Maps Telegram chats/topics to backend sessions. Key fields:

- `chat_id`: Telegram chat identifier.
- `topic_id`: Telegram forum topic identifier, or `0` for chats without a forum topic.
- `session_id`: Unique identifier for the backend conversation thread.
- `provider`: The backend provider associated with this session.
- `topic_title`: Cached title of the Telegram topic.
- `workspace_root`: The specific filesystem directory assigned to this topic for file operations.
- `archived_at`: Nullable archive timestamp. `/delete` also archives the binding; it does not remove the database row or add a separate deletion marker.
- `created_at`, `updated_at`: Standard lifecycle timestamps.

### `language_preferences`

Stores language settings. Key fields:

- `chat_id`: Telegram chat identifier.
- `topic_id`: Telegram topic identifier. Together with `chat_id`, this forms the key for per-topic language preferences.
- `language`: Response language for the specific chat/topic.
- `updated_at`: Last preference update timestamp.

### `topic_preferences`

Stores per-topic configuration overrides.

- `chat_id`: Telegram chat identifier.
- `topic_id`: Telegram topic identifier.
- `model`: Selected model name.
- `provider`: Force a specific provider for this topic.
- `reasoning_effort`: Configuration for models supporting reasoning effort.
- `service_tier`: Preferred service tier for backend turns.
- `updated_at`: Last preference update timestamp.

### `bridge_state`

Simple key-value store for internal bridge state.

- `key`: State identifier.
- `value`: Serialized state data.

The `telegram_update_offset` key stores the Bot API polling cursor so it can be restored after restart. SQLite uses WAL mode and initializes the schema on startup, including missing provider columns in older databases.

## Core Components

### Routing and Adapters

The provider router owns separate Codex and Gemini clients and remembers the provider associated with each session. Bindings persist that provider so resumed threads and archive operations use the appropriate client after a restart.

Codex prefers a locally spawned `app-server` over loopback WebSocket JSON-RPC. Thread start/resume and model catalogs use that adapter, with `codex exec` / `codex exec resume` available when app-server operations return a supported fallback error. Both the automatic and explicit app-server adapter selections retain the CLI fallback. Active app-server turns support `turn/steer`; the CLI cannot steer, so the application queues and merges follow-ups when steering is unavailable.

Gemini uses its separately installed and authenticated CLI. Automatic Codex-to-Gemini fallback applies only to fresh thread creation with specific quota, rate-limit, capacity, or login errors. Existing threads are not migrated on resume failure. `/provider` stores a topic preference and marks a binding from the previous provider as archived; the next normal message starts a fresh thread.

### Topic Concurrency

- **Serialization**: Operations within a single topic (`chat_id` + `topic_id`) are serialized to maintain context integrity.
- **Independence**: Different topics run independently. A busy topic does not block others.
- **Steering**: New messages use `turn/steer` when a Codex app-server turn is active. When steering is unavailable, follow-ups are queued and merged for later processing.

### Workspace Management

- Each topic is assigned a `workspace_root` directory under the global `CODEX_WORKSPACE_ROOT`.
- Directories are generated on-demand and reused.
- `/new` command reuses the existing workspace directory (does not delete content).
- The Codex CLI adapter grants additional shared-root access with `--add-dir`. The app-server adapter passes the topic directory as its working directory and applies its sandbox settings. Workspace folders are not strict isolation boundaries.

### Media Handling

- **Inbox**: Incoming files are stored in `<workspace>/.telegram/inbox/<message-id>`.
- **Backend input**: Photos and image documents are attached as native Codex image inputs. Other attachments are supplied as file paths with caption context.
- **Transcripts**: When local Whisper and ffmpeg are available, voice/audio transcripts are stored in `<workspace>/.telegram/transcripts/<message-id>` and included in the prompt. Failed transcription is logged; the original file input is still processed.
- **Outbound**: Generated images, audio, and documents are sent directly to Telegram. Final text responses are sent after completion, with typing indicators shown during processing.

## Session Lifecycle

1. **Creation**: The first normal message in a chat/topic creates a `topic_bindings` row. Topic create/edit events update the title of an existing binding. Close/reopen/hide/unhide events are observed without changing archive state.
2. **Usage**: Messages are routed to the bound provider/session.
3. **Archival**:
   - `/archive`: Sets `archived_at` on the binding and attempts to archive the backend thread. The binding remains stored.
   - `/delete`: Available only in a forum topic. Archives the local binding first, then calls the Telegram topic deletion API. The archived binding remains stored even if topic deletion succeeds.
4. **Retrieval**: `/threads` lists current **stored** bindings for the chat, not full backend history.

## Configuration Hierarchy

1. **Environment Variables**: Loaded from the current working directory's `.env` file (simple `KEY=VALUE` parser). Existing exported shell variables override `.env` values. No shell evaluation or quote stripping.
2. **CLI Arguments**: Limited. `--project-root` affects **management** commands (like `status`, `start`), not the `serve` command (which uses CWD).
3. **Runtime Commands**: Commands like `/provider`, `/model`, `/lang` update per-topic state. `/permission` updates the global `CODEX_PERMISSION_MODE` in `.env` for future executions. Runtime preferences are stored in SQLite except `/permission` which is global in `.env`.
