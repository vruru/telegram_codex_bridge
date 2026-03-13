# macOS Operation

This repository now ships two macOS-facing entrypoints:

- `bin/telegram-codex-bridge`: the Telegram <-> Codex bridge service
- `dist/Telegram Codex Bridge.app`: the installable menu bar app

The menu bar app is now the preferred operator experience.

## Build

```bash
./scripts/build-macos-app.sh
```

That script will:

1. build `bin/telegram-codex-bridge`
2. compile the menu bar app
3. package `dist/Telegram Codex Bridge.app`

To build a drag-and-drop DMG:

```bash
./scripts/build-macos-dmg.sh
```

## Menu bar app

Launch it with:

```bash
open "./dist/Telegram Codex Bridge.app"
```

The menu bar app can:

- perform first-run setup when configuration is missing
- validate Telegram bot token
- validate Codex installation/login state
- store config and runtime files in `~/Library/Application Support/TelegramCodexBridge`
- copy the embedded bridge binaries into that runtime directory
- show the current bridge status
- start, stop, and restart the bridge
- toggle bridge auto-start at login
- open the runtime folder
- open stdout and stderr logs

## launchd

`telegram-codex-bridge` manages the user LaunchAgent:

- plist path: `~/Library/LaunchAgents/com.telegramcodex.bridge.plist`
- runtime root: `~/Library/Application Support/TelegramCodexBridge`
- stdout log: `~/Library/Application Support/TelegramCodexBridge/data/logs/bridge.stdout.log`
- stderr log: `~/Library/Application Support/TelegramCodexBridge/data/logs/bridge.stderr.log`

The main bridge log now rotates automatically. By default:

- max size per file: `20 MB`
- retained backups: `5`
- level: `info`

For active debugging, you can raise verbosity with `BRIDGE_LOG_LEVEL=debug`.

Common commands:

```bash
./bin/telegram-codex-bridge status
./bin/telegram-codex-bridge status --json
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
./bin/telegram-codex-bridge remove
```

## Important note

Do not run both:

- a manually started `telegram-codex-bridge`
- and a launchd-managed `telegram-codex-bridge`

at the same time, or Telegram long polling will conflict.

`telegram-codex-bridge status` detects this and reports `running outside launchd` when it finds a manual process.

The setup window only appears when configuration is missing or incomplete. After setup is saved, the app returns to menu-bar-only behavior.

When `BRIDGE_PREVENT_SLEEP=true`, the bridge will try to keep macOS awake with `caffeinate` while a Codex task is running. This protects long-running tasks from sleeping mid-run, but it cannot wake a machine that is already asleep before a Telegram message arrives.
