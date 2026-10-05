# Telegram Codex Bridge

`telegram-codex-bridge` is a local Go service that turns Telegram private chats and forum topics into a conversational front end for coding-agent threads. Codex is the primary execution backend; Gemini CLI is an optional alternate provider. A native Swift menu bar app provides setup and service controls on macOS.

## Features

- Telegram Bot API long polling, with user and group allowlists and a persisted update offset.
- Persistent routing from each chat/topic to a backend session and a reusable workspace directory.
- Codex app-server integration, CLI fallback, and follow-up steering during active turns.
- Topic-level provider, model, reasoning, service-tier, and Chinese/English language controls.
- Incoming photos, documents, voice notes, and audio files; optional local Whisper transcription.
- Final text replies, typing indicators, and automatic return of generated images, audio, and common document files.
- Unified service management for macOS `launchd` and Linux `systemd --user`.
- macOS menu bar setup, quota display, restart, logs, autostart toggle, and Whisper installation controls.

## Architecture

```text
Telegram Bot API <-> internal/telegram <-> internal/app topic workers
                                             |
                                             +-> internal/store (SQLite)
                                             +-> internal/codex provider router
                                             |     +-> Codex app-server / Codex CLI
                                             |     +-> Gemini CLI
                                             +-> workspace files / optional Whisper
```

Routing is keyed by `chat_id + topic_id`; chats without a forum topic use `topic_id=0`. The first ordinary message creates a backend thread and a workspace subdirectory under `CODEX_WORKSPACE_ROOT`. Later messages resume that thread. `/new` replaces the binding while reusing the existing workspace.

Each topic has a worker that serializes its messages; different topics can run independently. Follow-ups use Codex `turn/steer` when an app-server turn is active, otherwise they are queued and merged. Telegram receives typing indicators and the final answer rather than the backend event stream.

Codex runs through a locally spawned app-server using loopback WebSocket JSON-RPC, with `codex exec` / `codex exec resume` as fallback for adapter availability and supported fallback errors. Selecting the app-server adapter still allows this CLI fallback. Automatic cross-provider fallback to Gemini applies only when starting a fresh Codex thread and encountering specific quota, rate-limit, capacity, or login errors. Existing threads are not migrated.

The Codex CLI adapter adds the shared workspace root with `--add-dir`; the app-server adapter passes the topic directory as its working directory and applies its sandbox settings. Workspace folders organize work; they are not a strict isolation boundary.

See [docs/architecture.md](docs/architecture.md) for the components, persistence tables, and lifecycle behavior.

## Prerequisites

- Go **1.26.6 or newer** for source builds, as required by [go.mod](go.mod). The SQLite driver is pure Go; no separate SQLite installation is needed.
- A Telegram bot token and the user/group IDs you intend to allow.
- An installed and authenticated Codex CLI for Codex operation. Gemini CLI must be separately installed and authenticated if used.
- An existing, writable workspace root and network access to Telegram and the selected backend.
- On macOS, Xcode Command Line Tools (`swiftc`, `swift`, `iconutil`) for the app build. DMG packaging also needs `python3` and `hdiutil`.

## Local setup, build, and run

Run these commands from the repository root:

```bash
go mod download
cp .env.example .env
mkdir -p bin
go build -o bin/telegram-codex-bridge ./cmd/bridge
```

Edit `.env` privately before starting: configure `TELEGRAM_BOT_TOKEN`, the allowlists, and `CODEX_WORKSPACE_ROOT` for your intended workspace. The workspace directory must already exist. Only the bot token is mandatory in the configuration loader; explicitly configure access restrictions for the intended audience.

`TELEGRAM_ALLOWED_USER_IDS` applies to senders in both private and group chats. `TELEGRAM_ALLOWED_CHAT_IDS` applies to shared/group chats; private chats bypass that list. An empty list leaves its corresponding restriction open. For ordinary group messages, the bot must be able to read them; startup logs warn when Telegram reports that it cannot.

The service reads `.env` from its current working directory. The parser accepts simple `KEY=VALUE` lines, skips comments and blank lines, and performs no shell evaluation or quote removal. Already-exported environment variables take precedence.

Run in the foreground:

```bash
./bin/telegram-codex-bridge serve
```

Invoking the binary without arguments also serves. `go run ./cmd/bridge` is available for development. Foreground serving uses the current directory for configuration and default state/log paths; `--project-root` is a management option and does not select the foreground serving directory.

Development checks:

```bash
go test ./...
go vet ./...
```

## Environment variable names

The bridge configuration names are listed below. Keep tokens, IDs, and machine-specific configuration in the local `.env`.

| Variable Name | Meaning | Required by Loader |
| :--- | :--- | :--- |
| `TELEGRAM_BOT_TOKEN` | Telegram bot authentication token. | **Yes** |
| `TELEGRAM_ALLOWED_USER_IDS` | Comma-separated list of allowed Telegram User IDs. | No |
| `TELEGRAM_ALLOWED_CHAT_IDS` | Comma-separated list of allowed Telegram Chat IDs (groups). | No |
| `TELEGRAM_API_BASE_URL` | Custom Telegram API base URL. | No |
| `TELEGRAM_POLL_TIMEOUT_SECONDS` | Timeout for long-polling updates. | No |
| `CODEX_BIN` | Path to the binary for the selected default provider. | No |
| `CODEX_PROVIDER` | Selected default backend provider. | No |
| `CODEX_ADAPTER` | Adapter mode for Codex. | No |
| `CODEX_WORKSPACE_ROOT` | Root directory for generated workspaces. | No |
| `CODEX_PERMISSION_MODE` | Permission mode for future executions. | No |
| `GEMINI_DEFAULT_MODEL` | Default Gemini model identifier. | No |
| `GEMINI_MODELS` | List of allowed Gemini models. | No |
| `BRIDGE_STATE_PATH` | Path to the SQLite database file. | No |
| `BRIDGE_LANGUAGE` | Default response language. | No |
| `BRIDGE_LOG_PATH` | Path to the log file. | No |
| `BRIDGE_LOG_LEVEL` | Logging verbosity. | No |
| `BRIDGE_LOG_MAX_SIZE_MB` | Max size of log file before rotation. | No |
| `BRIDGE_LOG_MAX_BACKUPS` | Number of rotated log files to keep. | No |
| `BRIDGE_PREVENT_SLEEP` | Enable system sleep prevention during tasks. | No |
| `TELEGRAM_CODEX_BRIDGE_ROOT` | **Swift-only** override for bridge root path. | No |

`GEMINI_MODELS` is comma-separated. `CODEX_BIN` overrides the CLI for the selected default provider; the alternate provider uses its conventional executable name. `TELEGRAM_CODEX_BRIDGE_ROOT` is read by the Swift app at startup, not by the Go configuration loader.

## Telegram commands

| Command | Description |
| :--- | :--- |
| `/start`, `/help` | Show available commands. |
| `/where` | Show chat/topic/user identifiers. |
| `/version` | Show bridge version. |
| `/status` | Show current session and provider status. |
| `/limit` | Show Codex quota usage. |
| `/lang` | Show or set the topic language. |
| `/provider` | Switch provider. Old binding archived; next message starts new thread. |
| `/model` | Show the model menu or set a provider-supported model. |
| `/think` | Show or set the reasoning level. |
| `/speed` | Show or set the service tier supported by the provider. |
| `/permission` | Set global permission mode (updates `.env`). |
| `/threads` | List stored bindings in this chat. |
| `/new` | Start new session. Optional immediate prompt; otherwise next ordinary message. Reuses existing workspace. |
| `/archive` | Archive current session binding. |
| `/delete` | Archive the binding, then delete the Telegram forum topic via the Bot API. |

Language, provider, model, reasoning, and service-tier preferences are per topic. `/permission` changes the global execution permission for future runs and writes `CODEX_PERMISSION_MODE` to `.env`. Switching providers archives the previous binding and starts a fresh thread on the next ordinary message.

## Service management

Both supported platforms use the unified binary:

```bash
./bin/telegram-codex-bridge paths --json
./bin/telegram-codex-bridge status --json
./bin/telegram-codex-bridge version
./bin/telegram-codex-bridge codex
./bin/telegram-codex-bridge limits
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
```

`codex` checks the configured backend; `limits` is Codex-only. `help` lists commands, `remove` removes the service registration, and `stop-unmanaged` stops a detected unmanaged bridge process.

Management resolves the runtime root from the executable layout: `<runtime-root>/bin/telegram-codex-bridge`. Use `--project-root` after the management subcommand when choosing a different root; that root must contain the expected binary. `start` writes the service registration and starts it; enabling autostart is a separate operation.

## Linux deployment and release archives

Build a native binary using the local build command above, then manage it with `start` and `set-autostart`. The generated user unit is `~/.config/systemd/user/com.telegramcodex.bridge.service`; its working directory is the runtime root, with `.env` and persistent `data/` beneath it.

To build all supported release archives:

```bash
./scripts/build-release-archives.sh
```

This writes archives for Linux and macOS, each on amd64 and arm64, under `dist/releases/`. Filenames follow `telegram-codex-bridge_<version>_<os>_<arch>.tar.gz`. Each archive contains only the executable and `README.md`; it does not create a local `bin/` or include `.env.example`.

For an archive installation, extract the matching executable into `<runtime-root>/bin/telegram-codex-bridge`, obtain the configuration template from the repository, and configure `<runtime-root>/.env`. Install and authenticate the backend CLI for the service user before starting.

The Linux unit searches `~/.local/bin`, `/usr/local/bin`, `/usr/bin`, and `/bin`. Use an absolute `CODEX_BIN` path if the chosen backend is installed elsewhere. To keep an enabled user service available after boot without an interactive login, enable lingering:

```bash
sudo loginctl enable-linger "$USER"
```

Release-script overrides are `DIST_DIR`, `VERSION`, `COMMIT`, `UPX_ENABLED`, and `UPX_ARGS`. UPX packing is opt-in and applies only to Linux binaries; if UPX is unavailable, the script skips packing. See [docs/linux.md](docs/linux.md).

## macOS app and deployment

Build and open the menu bar app:

```bash
./scripts/build-macos-app.sh
open "./dist/Telegram Codex Bridge.app"
```

The build produces `bin/telegram-codex-bridge` and `dist/Telegram Codex Bridge.app` for the host architecture. To build a drag-and-drop DMG:

```bash
mkdir -p dist
./scripts/build-macos-dmg.sh
```

The DMG script rebuilds the app and writes `dist/Telegram Codex Bridge.dmg`. Creating `dist/` first is needed on a clean checkout because its staging directory is allocated before the app build.

On first launch, the app copies the bundled bridge into `~/Library/Application Support/TelegramCodexBridge/bin`, validates the backend and token, and prompts for missing configuration. Setup requires a workspace and at least one user or chat allowlist entry.

The app runtime and its `.env` are separate from the repository runtime. To inspect the app-managed service:

```bash
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" status
```

The LaunchAgent is `~/Library/LaunchAgents/com.telegramcodex.bridge.plist`. Both runtime choices use the same service label per user, so choose the root you intend to manage. Saving menu setup rewrites `.env` with the UI-supported settings; reapply any additional advanced keys afterward. See [docs/macos.md](docs/macos.md).

## Optional local Whisper transcription

Voice and audio attachments can be transcribed locally. Install `ffmpeg` first; the installation helper also needs Python 3 with virtual-environment support and pip.

```bash
./bin/telegram-codex-bridge whisper-status
./bin/telegram-codex-bridge install-whisper
```

The helper reuses a ready existing installation or creates `<runtime-root>/data/whisper-venv` and installs `openai-whisper`. The fixed transcription model is `base`; the first transcription may download it. The macOS app also provides status and installation controls.

Attachments are saved under `<workspace>/.telegram/inbox/<message-id>`; transcripts are saved under `<workspace>/.telegram/transcripts/<message-id>`. Images are forwarded as native Codex image inputs. Other files are supplied as saved paths with caption context, and successful voice/audio transcripts are added to the prompt. Transcription failure is logged and processing continues with the original attachments.

## Logs and availability

Default runtime files are `data/bridge.db`, `data/logs/bridge.stdout.log`, and the managed service's `data/logs/bridge.stderr.log`. The internal main logger rotates its file; the service stderr file is outside that rotation. Configure log location, verbosity, size, and retention with the `BRIDGE_LOG_*` names above.

Run one poller per bot token. Stop a foreground instance before switching to service management. Status checks can report a matching bridge process running outside the service manager.

The host must remain awake to receive Telegram messages. Sleep prevention uses `caffeinate` on macOS or `systemd-inhibit` on Linux during active tasks when enabled. It does not keep an idle host awake or wake one that is already sleeping.

## CI and releases

[.github/workflows/build.yml](.github/workflows/build.yml) runs `go test ./...`, then builds four platform/architecture archives and macOS app ZIP/DMG artifacts. The Linux runner enables UPX packing. Pushing a `v*` tag publishes the assets to a GitHub Release with native generated notes configured by [.github/release.yml](.github/release.yml).

Build scripts derive version and commit metadata from Git unless overridden. Publish fixes with a new version tag; follow the release policy in [CONTRIBUTING.md](CONTRIBUTING.md) and the history in [CHANGELOG.md](CHANGELOG.md).

## Project layout and documentation

| Path | Responsibility |
| :--- | :--- |
| `cmd/bridge/` | Unified serving and management entrypoint. |
| `cmd/bridgectl/` | Legacy standalone control entrypoint; not included in packaged builds. |
| `internal/app/` | Topic workers, orchestration, settings, workspaces, and media. |
| `internal/codex/` | Provider routing, Codex app-server/CLI and Gemini CLI adapters. |
| `internal/telegram/`, `internal/store/` | Bot transport and SQLite persistence. |
| `internal/config/`, `internal/control/` | Configuration parsing and management commands. |
| `internal/service/`, `internal/macos/` | Platform service managers and launchd helpers. |
| `internal/transcribe/`, `internal/power/` | Local transcription and task sleep inhibitors. |
| `internal/logging/`, `internal/buildinfo/`, `internal/i18n/` | Rotating logs, version metadata, and localization. |
| `macos/BridgeStatusBarApp/`, `scripts/` | Swift menu bar app and packaging helpers. |

- [Architecture](docs/architecture.md)
- [Linux Guide](docs/linux.md)
- [macOS Guide](docs/macos.md)
- [Deployment Process and Verified Status](docs/deployment-process.md)
- [Dependency Health](docs/dependency-health.md)
- [Changelog](CHANGELOG.md)
- [Contributing](CONTRIBUTING.md)
