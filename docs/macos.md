# macOS Operation

The repository provides the unified bridge binary and a native Swift menu bar app. The menu app manages its own runtime directory and offers first-run setup, status, quota display, service controls, logs, and local Whisper controls.

## Build and installation

Source builds require Go **1.26.9 or newer** and Xcode Command Line Tools with `swiftc`, `swift`, and `iconutil`. Run from the repository root:

```bash
go mod download
./scripts/build-macos-app.sh
open "./dist/Telegram Codex Bridge.app"
```

The script produces `bin/telegram-codex-bridge` and `dist/Telegram Codex Bridge.app` for the current host architecture. The app bundle embeds the unified bridge executable.

To create a drag-and-drop installer, with `python3` and `hdiutil` available:

```bash
mkdir -p dist
./scripts/build-macos-dmg.sh
```

The DMG script rebuilds the app and writes `dist/Telegram Codex Bridge.dmg`. The explicit directory creation is needed on a clean checkout: the script allocates its staging directory before invoking the app build.

Published app ZIPs and DMGs can also be installed in Applications. Cross-compiled macOS command-line binaries are built separately by `scripts/build-release-archives.sh`; those archives do not contain the Swift app.

## First-run setup and runtime

At launch, the app copies the embedded bridge into its runtime `bin/` directory and removes the legacy `bridgectl` binary if present. Missing or incomplete configuration opens setup for the Telegram token, workspace root, and at least one user or chat allowlist entry. The app validates the token and backend readiness.

| File | App-managed location |
| :--- | :--- |
| Runtime root | `~/Library/Application Support/TelegramCodexBridge` |
| Bridge binary | `<runtime-root>/bin/telegram-codex-bridge` |
| Configuration | `<runtime-root>/.env` |
| Default SQLite state | `<runtime-root>/data/bridge.db` |
| Default main log | `<runtime-root>/data/logs/bridge.stdout.log` |
| Service stderr | `<runtime-root>/data/logs/bridge.stderr.log` |
| LaunchAgent | `~/Library/LaunchAgents/com.telegramcodex.bridge.plist` |

`TELEGRAM_CODEX_BRIDGE_ROOT` can override the Swift app's runtime root when supplied in the app's startup environment. The Go configuration loader does not read this variable.

The app's configuration is separate from the repository's `.env`. Saving menu setup updates all occurrences of its seven UI-supported keys: `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ALLOWED_USER_IDS`, `TELEGRAM_ALLOWED_CHAT_IDS`, `CODEX_WORKSPACE_ROOT`, `CODEX_BIN`, `BRIDGE_LANGUAGE`, and `CODEX_PERMISSION_MODE`. Existing provider, log, and other advanced settings, comments, blank lines, and their ordering are retained. Missing UI keys are appended. A missing file is created; other read errors abort the save. Values containing line breaks are rejected, and the resulting file is atomically replaced with owner-only permissions. Run `bash scripts/test-macos-config.sh` to verify this merge without starting the app or a poller. The complete configuration name list is in [README](../README.md#environment-variable-names).

## launchd and command-line control

Management commands infer the runtime root from the executable layout, rather than the current directory. To inspect and control the app-managed runtime:

```bash
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" paths --json
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" status --json
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" start
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" stop
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" restart
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" set-autostart on
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" set-autostart off
```

`start` installs/loads the LaunchAgent as needed and starts the bridge. Autostart at login is controlled separately through the menu or `set-autostart`. `remove` unloads the service and removes its plist while retaining runtime data.

For a repository-based runtime, configure the repository's `.env` and use its binary:

```bash
./bin/telegram-codex-bridge status
./bin/telegram-codex-bridge start
```

Both choices use the same `com.telegramcodex.bridge` LaunchAgent label for the user. Choose one runtime root to manage. `--project-root` may explicitly select a root after the management subcommand, but that root must contain `bin/telegram-codex-bridge`.

Foreground serving uses the current working directory; the serving command does not use `--project-root` to change configuration roots. Stop a manually started process before switching to launchd. Avoid concurrent pollers for the same token. Status can report a matching bridge process running outside launchd.

## Local Whisper

The menu app can check and install optional local transcription. Install `ffmpeg` first and provide Python 3 with venv/pip support. The helper reuses an already-ready installation; otherwise it installs `openai-whisper` under `<runtime-root>/data/whisper-venv`.

Use the app runtime's binary for the same installation:

```bash
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" whisper-status
"$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" install-whisper
```

Voice/audio transcription uses the fixed `base` model; the first run may download model files. Failed transcription is logged and attachment processing continues with the original files. See [README](../README.md#optional-local-whisper-transcription).

## Logs and sleep

The internal main log rotates according to `BRIDGE_LOG_MAX_SIZE_MB` and `BRIDGE_LOG_MAX_BACKUPS`. `BRIDGE_LOG_LEVEL` controls verbosity. The LaunchAgent's separate stderr file is outside that rotation; `BRIDGE_LOG_PATH` can move the application's main log.

When `BRIDGE_PREVENT_SLEEP` enables sleep prevention, the bridge uses `caffeinate` during backend tasks. A host that is asleep before a message arrives cannot respond; task sleep prevention does not wake it or keep it awake while idle.
