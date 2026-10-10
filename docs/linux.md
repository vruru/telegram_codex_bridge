# Linux Operation

`telegram-codex-bridge` runs as a regular Go binary and can manage a `systemd --user` service. Install and authenticate the chosen backend CLI for the same user that runs the bridge.

## Source build and foreground run

Go **1.26.9 or newer** is required by [go.mod](../go.mod). Run from the repository root:

```bash
go mod download
cp .env.example .env
mkdir -p bin
go build -o bin/telegram-codex-bridge ./cmd/bridge
```

Edit `.env` privately before serving. Configure `TELEGRAM_BOT_TOKEN`, appropriate user/group allowlists, and an existing writable `CODEX_WORKSPACE_ROOT`. See the [README configuration table](../README.md#environment-variable-names) for all supported names and access rules.

```bash
./bin/telegram-codex-bridge serve
```

Foreground serving reads `.env` from the current directory, with exported environment variables taking precedence. Stop it before starting the managed service.

## Release archives

Build cross-platform archives with:

```bash
./scripts/build-release-archives.sh
```

The script builds Linux and macOS binaries for amd64 and arm64 into `dist/releases/`. Archive names follow `telegram-codex-bridge_<version>_<os>_<arch>.tar.gz`. UPX packing is optional and applies only to Linux binaries. The build overrides are `DIST_DIR`, `VERSION`, `COMMIT`, `UPX_ENABLED`, and `UPX_ARGS`.

To deploy a matching Linux archive, extract it and place its executable at `<runtime-root>/bin/telegram-codex-bridge`. Each archive contains only the executable and README; obtain `.env.example` from the repository and save the configured file at `<runtime-root>/.env`. The archive script itself does not populate the repository's `bin/` directory.

## Runtime layout

Management commands infer the runtime root from the executable's parent directory's parent. This requires the `bin/` layout. To specify another root, place `--project-root` after the management subcommand; the selected root must contain its own expected binary. `serve` uses its working directory instead.

| File | Location |
| :--- | :--- |
| Bridge executable | `<runtime-root>/bin/telegram-codex-bridge` |
| Configuration | `<runtime-root>/.env` |
| Default SQLite state | `<runtime-root>/data/bridge.db` |
| Default main log | `<runtime-root>/data/logs/bridge.stdout.log` |
| Service stderr | `<runtime-root>/data/logs/bridge.stderr.log` |
| User unit | `~/.config/systemd/user/com.telegramcodex.bridge.service` |

`BRIDGE_STATE_PATH` and `BRIDGE_LOG_PATH` can override the application's state and main log locations; the generated service output paths remain under the runtime root.

## Service commands

From the configured runtime root:

```bash
./bin/telegram-codex-bridge paths --json
./bin/telegram-codex-bridge status --json
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
./bin/telegram-codex-bridge remove
```

`start` creates the unit, reloads the user systemd manager, and starts the service. `set-autostart on` enables it for the user; `remove` disables/stops it and removes the unit while retaining configuration, state, and workspace files.

For an enabled user service to start after reboot without an interactive login, enable lingering:

```bash
sudo loginctl enable-linger "$USER"
```

The generated unit runs with the runtime root as its working directory and loads that root's `.env`. It restarts the process on exit. Its executable search path includes `~/.local/bin`, `/usr/local/bin`, `/usr/bin`, and `/bin`; if the chosen backend is elsewhere, configure an absolute `CODEX_BIN` path.

Useful diagnostics are `codex` for the configured backend, `limits` for Codex quota, `version`, and `help`. `status` can report a matching unmanaged bridge process; avoid two pollers using the same Telegram bot token.

## Optional transcription and availability

`whisper-status` checks local transcription readiness. `install-whisper` reuses a ready installation or creates `data/whisper-venv` and installs `openai-whisper`. Python 3 with venv/pip support and an already-installed `ffmpeg` are required. Transcription uses the fixed `base` model and may download it on first use. See [README](../README.md#optional-local-whisper-transcription) for attachment and transcript paths.

The internal main logger rotates according to `BRIDGE_LOG_MAX_SIZE_MB` and `BRIDGE_LOG_MAX_BACKUPS`; the separate service stderr file is outside that rotation.

A sleeping or powered-off host cannot answer Telegram messages. When sleep prevention is enabled through `BRIDGE_PREVENT_SLEEP`, the bridge uses `systemd-inhibit` during backend tasks. It does not wake an already-sleeping host or keep it awake while idle.
