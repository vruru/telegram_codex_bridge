# Linux Notes

`telegram-codex-bridge` can run on Debian and other Linux distributions as a regular Go binary. The unified binary now manages a `systemd --user` service, so you do not need the macOS menu bar app on Linux.

## Runtime layout

The Linux service uses the same project/runtime root layout as macOS:

- bridge binary: `<project-root>/bin/telegram-codex-bridge`
- env file: `<project-root>/.env`
- SQLite state: `<project-root>/data/bridge.db`
- stdout log: `<project-root>/data/logs/bridge.stdout.log`
- stderr log: `<project-root>/data/logs/bridge.stderr.log`

The generated user service file lives at:

- `~/.config/systemd/user/com.telegramcodex.bridge.service`

## Service commands

Use the unified binary:

```bash
./bin/telegram-codex-bridge status
./bin/telegram-codex-bridge start
./bin/telegram-codex-bridge stop
./bin/telegram-codex-bridge restart
./bin/telegram-codex-bridge set-autostart on
./bin/telegram-codex-bridge set-autostart off
./bin/telegram-codex-bridge remove
```

`start` will:

- create the systemd user unit if needed
- run `systemctl --user daemon-reload`
- start the bridge service

`set-autostart on` enables the unit for the user. If you want it to start after reboot even without an interactive login, also enable lingering for that user:

```bash
sudo loginctl enable-linger "$USER"
```

## Codex requirement

Linux support assumes Codex itself is already installed and logged in on that machine. This bridge does not replace Codex; it automates and routes messages into the local Codex environment.

## Sleep and availability

If the Linux host is asleep or powered off, Telegram messages cannot be answered. For reliable availability, run the bridge on a machine or VM that remains awake.

If `BRIDGE_PREVENT_SLEEP=true`, the bridge will try to hold a sleep inhibitor with `systemd-inhibit` while a Codex task is running. This protects long tasks from being interrupted by sleep, but it does not wake a machine that is already asleep.
