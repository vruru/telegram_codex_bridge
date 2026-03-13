# Telegram Codex Bridge

`telegram-codex-bridge` is a Go service that turns Telegram private chats and forum topics into a conversational front end for local coding-agent threads. Codex remains the primary backend, and Gemini CLI is available as an alternate provider.

Release history: [CHANGELOG.md](CHANGELOG.md)

## What works now

- Telegram long polling over the Bot API
- User/chat allowlists
- `chat_id + topic_id -> Codex session_id` routing in SQLite
- Persistent Telegram update offsets across restarts
- Auto-create a Codex thread on the first normal message in a chat or topic
- Auto-create and bind a dedicated workspace subdirectory for each new chat/topic thread
- Auto-resume the same Codex thread on later messages
- Codex `app-server` as the primary adapter, with CLI fallback when needed
- Optional Gemini CLI backend for manual provider switching when Codex is unavailable or quota-limited
- Automatic Codex -> Gemini fallback for fresh thread creation when Codex is quota-limited or temporarily unavailable
- Topic-aware control commands: `/help`, `/where`, `/version`, `/status`, `/limit`, `/lang`, `/provider`, `/model`, `/think`, `/speed`, `/permission`, `/threads`, `/new`, `/archive`, `/delete`
- Telegram forum topic lifecycle sync for create/edit/close/reopen messages
- Telegram native `typing` status while Codex is working; only final Codex output is sent as message text
- Per-topic timing stats for new-thread and resume-thread runs
- Localized zh/en Telegram responses, plus a per-topic `/lang auto|zh|en` override
- Official `turn/steer` follow-up steering when an app-server turn is active
- Telegram attachment input for photos, documents, voice notes, and audio files
- Automatic download of incoming attachments into the bound topic workspace
- Image attachments forwarded to Codex as native image inputs
- Document, voice, and audio attachments forwarded to Codex with saved file paths and caption context
- Automatic return of generated images, audio files, and common document outputs back to Telegram
- macOS `launchd` management through the unified `telegram-codex-bridge` binary
- Linux `systemd --user` management through the same unified `telegram-codex-bridge` binary
- macOS menu bar app with first-run setup, configurable UI language, quota display, restart, logs, and auto-start toggle
- GitHub Actions workflow for tests, release archives, and macOS app artifacts

## Core model

- One Telegram private chat maps to one Codex thread
- One Telegram forum topic maps to one Codex thread
- The first thread for a chat/topic gets its own workspace folder under `CODEX_WORKSPACE_ROOT`
- Later runs in the same chat/topic keep reusing that same workspace folder
- Codex runs inside the per-topic folder but is additionally allowed to access the parent project root for cross-thread collaboration when needed
- Routing is keyed by `chat_id + topic_id`
- Private chat `topic_id=0` and group `topic_id=0` do not conflict because `chat_id` is part of the key

## Project layout

```text
telegram-codex-bridge/
├── bin/                         # built binaries
├── cmd/bridge/                  # unified bridge + management entrypoint
├── docs/                        # architecture, macOS, and Linux operation notes
├── internal/app/                # orchestration and message queueing
├── internal/control/            # cross-platform management subcommands
├── internal/codex/              # Codex app-server adapter, CLI fallback, and stream parsing
├── internal/config/             # env/config parsing
├── internal/macos/              # launchd helpers
├── internal/service/            # launchd/systemd service adapters
├── internal/store/              # SQLite topic-thread mapping
├── internal/telegram/           # Telegram transport
├── macos/BridgeStatusBarApp/    # menu bar app source
└── scripts/                     # build helpers
```

## Quick start

1. Copy `.env.example` to `.env`.
2. Fill in your Telegram bot token, allowed ids, and workspace path.
3. Optional: set `BRIDGE_LANGUAGE=auto|zh|en` for the default UI/system-message language.
4. Optional: set `CODEX_PROVIDER=codex|gemini` to choose the backend CLI.
5. Optional: when `CODEX_PROVIDER=codex`, set `CODEX_ADAPTER=auto|app-server|cli` to choose the Codex adapter.
6. Optional: set `CODEX_PERMISSION_MODE=default|full-access` for the default execution permission.
7. Optional: when `CODEX_PROVIDER=gemini`, set `GEMINI_DEFAULT_MODEL` and `GEMINI_MODELS` to control the `/model` menu.
8. Optional: set `BRIDGE_LOG_LEVEL=info|debug`, plus `BRIDGE_LOG_MAX_SIZE_MB` and `BRIDGE_LOG_MAX_BACKUPS` for rotating logs.
9. Optional: set `BRIDGE_PREVENT_SLEEP=true|false` to keep the computer awake while the active backend is processing a task.
8. Run the bridge:

```bash
go run ./cmd/bridge
```

## macOS build and control

Build the binaries and menu bar app:

```bash
./scripts/build-macos-app.sh
```

This produces:

- `bin/telegram-codex-bridge`
- `dist/Telegram Codex Bridge.app`

Useful commands:

```bash
./bin/telegram-codex-bridge status
./bin/telegram-codex-bridge version
./bin/telegram-codex-bridge limits
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
```

Open the menu bar app:

```bash
open "./dist/Telegram Codex Bridge.app"
```

Or build a drag-and-drop DMG:

```bash
./scripts/build-macos-dmg.sh
```

More details: [docs/macos.md](docs/macos.md)

## Linux build and control

Build release archives for Linux and macOS binaries:

```bash
./scripts/build-release-archives.sh
```

This produces `.tar.gz` archives for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`

When `UPX_ENABLED=true`, Linux release binaries are additionally packed with UPX before archiving. macOS app bundles and the `.dmg` are not UPX-packed; the `.dmg` is already a compressed disk image, and UPX on macOS app executables is more likely to hurt compatibility than help.

On Debian or other systemd-based Linux distributions, the unified binary can manage a user service:

```bash
./bin/telegram-codex-bridge status
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
```

Linux service details: [docs/linux.md](docs/linux.md)

## Operational notes

The menu bar app now uses `~/Library/Application Support/TelegramCodexBridge` as its runtime root. On first launch, it will:

- copy the embedded bridge binaries into that runtime directory
- check whether configuration exists
- validate Codex availability and login
- ask for Telegram token, workspace, and allowlist values if config is missing

Avoid running two bridge instances at once. If you already started `telegram-codex-bridge` manually, stop that process before switching to the menu bar app or `launchd`. `telegram-codex-bridge status` will warn when it detects a bridge process running outside `launchd`.

Because this bridge runs locally beside Codex, a sleeping machine cannot answer Telegram messages. For 24/7 availability:

- keep the macOS host awake
- or run the bridge on a Linux machine that does not auto-sleep
- or use a dedicated Debian service user with `systemd --user` and lingering enabled

When `BRIDGE_PREVENT_SLEEP=true`, the bridge will also try to prevent the machine from sleeping while a Codex task is actively running:

- macOS: `caffeinate`
- Linux: `systemd-inhibit`

That helps during long-running tasks, but it cannot wake a machine that is already asleep before a Telegram message arrives.

## Logging

The bridge now writes its main operational log through an internal rotating logger:

- default path: `data/logs/bridge.stdout.log`
- default size limit: `20 MB`
- default retained backups: `5`
- default level: `info`

Use `BRIDGE_LOG_LEVEL=debug` only when actively debugging. The default `info` mode avoids the noisy per-message development logs.

## GitHub Actions

The repository now includes [build.yml](.github/workflows/build.yml), which will:

- run `go test ./...`
- build cross-platform release archives
- build the macOS `.app` and `.dmg`
- upload all of them as workflow artifacts
- install UPX on the Linux build runner and pack Linux release binaries before archiving
- automatically create a GitHub Release when you push a `v*` tag
- attach the generated Linux/macOS archives and macOS installer assets to that Release
- keep a rolling draft release updated on `main/master` via [release-drafter.yml](.github/release-drafter.yml)
- publish the drafted release body when you push a `v*` tag
- use [release.yml](.github/release.yml) and [pull_request_template.md](.github/pull_request_template.md) to make changelogs and issue links more consistent

Binary and app versions are derived from the git tag at build time. GitHub Releases remains the source of truth for published versions, while release history also lives in [CHANGELOG.md](CHANGELOG.md).

## Next milestones

1. Approval prompts bridged into Telegram action buttons
2. Topic deletion and archive lifecycle hardening
3. Telegram entrypoints for Codex automations
4. Telegram entrypoints for Codex multi-agent workflows
