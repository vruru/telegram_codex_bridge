# Deployment Process: Telegram Codex Bridge

## Current deployment status (verified 2026-10-05)

The local Mac is dormant: the documented Application Support runtime, installed app locations, and `com.telegramcodex.bridge` LaunchAgent are absent. `status --json` reports `installed=false`, `loaded=false`, and `running=false`; `launchctl` cannot find the label, and no bridge or menu-app process is running. The repository's old binary is a development artifact, not an installation.

Project-name searches of project memory, LocalOps records, and `~/Library/LaunchAgents` found no documented remote instance or instance-specific update path. A read-only SSH audit checked the current 104-address active VPS fleet plus its documented management host: 104 of 105 targets were accessible, with no matching executable process, systemd registration, container name/image, or installation path in the inspected locations. One host failed SSH banner exchange on both attempts. That host remains **unverified**; unrecorded hosts and unconventional installation names/locations are also outside the evidence. This establishes **no known running deployment**, not a universal claim that the bridge runs nowhere.

No instance was installed, started, updated, or restarted. Detailed host addresses and audit evidence remain outside the repository in `/Users/tsy/LocalOps/telegram-codex-bridge-deploy-20261005/` (`deployment-check.json`, `other-hosts-audit.json`, and `other-hosts-retry.json`). A future deployment needs a chosen host, service user, private bot configuration, and authenticated backend.

The pinned runtime source below is `a28d01a1b760171448fbe9303c135d8d5d1e548a`: Go 1.26.6 and dependency updates, including `fc48cb9`'s file-link fix. The existing `4f939c1` commit only updates documentation. See the [README](../README.md), [macOS guide](macos.md), and [Linux guide](linux.md) for platform details.

## 1. Environment Preparation (Local Mac)

Use Go **1.26.9 or newer**. macOS app builds also require Xcode Command Line Tools (`swiftc`, `swift`, `iconutil`). Install and authenticate the Codex CLI as the eventual service user, and provide a writable workspace and Telegram access. Keep one poller per bot token across all hosts.

Use a detached worktree to build exactly the requested source. Adjust `BRIDGE_REPO_ROOT` if your clone differs; choose an unused `BRIDGE_BUILD_ROOT` (do not overwrite an existing worktree). All source, testing, and Git operations stay on the local Mac; remote servers receive only runtime files.

```bash
# Set paths
export BRIDGE_REPO_ROOT="$HOME/Documents/New project/telegram-codex-bridge"
export BRIDGE_BUILD_ROOT="$HOME/LocalOps/telegram-codex-bridge-build-a28d01a"
mkdir -p "$HOME/LocalOps"

# Prepare worktree
cd "$BRIDGE_REPO_ROOT"
git fetch origin
git worktree add --detach "$BRIDGE_BUILD_ROOT" a28d01a1b760171448fbe9303c135d8d5d1e548a

# Dependencies and validation
cd "$BRIDGE_BUILD_ROOT"
go mod download
go test ./...
go vet ./...
```

## 2. macOS Deployment

### Build

`VERSION` and `COMMIT` are optional overrides for traceability. Expected version output: `dev+a28d01a`.

```bash
cd "$BRIDGE_BUILD_ROOT"
VERSION=dev COMMIT=a28d01a ./scripts/build-macos-app.sh
./bin/telegram-codex-bridge version --json
```

### Install

The app copies the binary on launch *before* setup. No update prompt occurs. Setup saves `.env` and starts the service. Runtime data resides in `~/Library/Application Support/TelegramCodexBridge`.

```bash
BRIDGE_RUNTIME="$HOME/Library/Application Support/TelegramCodexBridge"
mkdir -p "$HOME/Applications"
ditto "$BRIDGE_BUILD_ROOT/dist/Telegram Codex Bridge.app" "$HOME/Applications/Telegram Codex Bridge.app"
open "$HOME/Applications/Telegram Codex Bridge.app"
```

### Update Procedure

Complete setup with the real bot token, an existing absolute workspace path, an authenticated backend, and at least one allowlist entry. Configure explicit allowed user IDs to restrict private chats too: private chats bypass the chat-ID allowlist. The app-managed `.env` is separate from the repository's `.env`.

For an existing installation, perform these steps **before** copying/opening the new bundle:

1. **Stop Runtime**: Use the old installed binary, then verify `running=false`.

   ```bash
   "$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" stop
   "$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" status --json
   ```

2. **Quit App**: Quit via the menu bar app.

3. **Backup**: Copy the stopped runtime and the old installed app; separately preserve workspace files and any state/log paths configured outside the runtime. For the user Applications installation above:

   ```bash
   BRIDGE_RUNTIME="$HOME/Library/Application Support/TelegramCodexBridge"
   BRIDGE_BACKUP="$HOME/LocalOps/telegram-codex-bridge-backup-$(date +%Y%m%d-%H%M%S)"
   mkdir -p "$BRIDGE_BACKUP"
   ditto "$BRIDGE_RUNTIME" "$BRIDGE_BACKUP/runtime"
   ditto "$HOME/Applications/Telegram Codex Bridge.app" "$BRIDGE_BACKUP/Telegram Codex Bridge.app"
   ```

4. **Install New**: Copy new app bundle. Open app.

5. **Start Service**:

   ```bash
   "$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" start
   "$HOME/Library/Application Support/TelegramCodexBridge/bin/telegram-codex-bridge" set-autostart on
   ```

### Configuration Notes

Saving setup rewrites UI-supported keys only; reapply advanced settings before starting if needed. The app and repository runtimes use the same LaunchAgent label, `com.telegramcodex.bridge`, per user. Manage the intended root through its own binary. The LaunchAgent is `~/Library/LaunchAgents/com.telegramcodex.bridge.plist`; startup at login is separate from `start`. An idle sleeping Mac cannot receive messages; task sleep prevention does not keep it awake while idle.

## 3. Linux Deployment

### Build (Cross-Compile on Mac)

Ensure `bin/linux-amd64` exists.

```bash
cd "$BRIDGE_BUILD_ROOT"
mkdir -p bin/linux-amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags '-X telegram-codex-bridge/internal/buildinfo.Version=dev -X telegram-codex-bridge/internal/buildinfo.Commit=a28d01a' \
  -o bin/linux-amd64/telegram-codex-bridge ./cmd/bridge
```

### Transfer to Remote

Set target details (chosen later, not current target).

```bash
BRIDGE_TARGET='bridge-user@chosen-host' # Replace with the intended service user/host
BRIDGE_SSH_PORT=22                     # Replace with its actual SSH port

# Create staging dir on remote
ssh -p "$BRIDGE_SSH_PORT" "$BRIDGE_TARGET" 'mkdir -p "$HOME/bridge-install-a28d01a"'

# Transfer binary and template
scp -P "$BRIDGE_SSH_PORT" "$BRIDGE_BUILD_ROOT/bin/linux-amd64/telegram-codex-bridge" \
  "$BRIDGE_BUILD_ROOT/.env.example" "$BRIDGE_TARGET:bridge-install-a28d01a/"
ssh -p "$BRIDGE_SSH_PORT" "$BRIDGE_TARGET"
```

### Remote Installation (Service User)

Run the following on the chosen Linux host as the service user. The standard runtime is `BRIDGE_RUNTIME="$HOME/.local/share/telegram-codex-bridge"`; `.env` must be at that root. These are the default paths (state/log overrides must be backed up separately):

*   `.env`: `$BRIDGE_RUNTIME/.env`
*   `db`: `$BRIDGE_RUNTIME/data/bridge.db`
*   `logs`: `$BRIDGE_RUNTIME/data/logs`
*   `binary`: `$BRIDGE_RUNTIME/bin/telegram-codex-bridge`

Use the binary's built-in management to generate the unit and select its working directory.

```bash
cd ~
BRIDGE_RUNTIME="$HOME/.local/share/telegram-codex-bridge"

# Stop existing service if binary exists
if [ -x "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" ]; then
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" stop
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" status --json
    # Continue only after running=false, and back up custom paths separately.
    BRIDGE_BACKUP="$HOME/bridge-backups/$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$BRIDGE_BACKUP"
    cp -a "$BRIDGE_RUNTIME" "$BRIDGE_BACKUP/runtime"
fi

# Install binary
mkdir -p "$BRIDGE_RUNTIME/bin"
install -m 755 "$HOME/bridge-install-a28d01a/telegram-codex-bridge" "$BRIDGE_RUNTIME/bin/telegram-codex-bridge"

# Initialize Config
if [ ! -f "$BRIDGE_RUNTIME/.env" ]; then
    mkdir -p "$BRIDGE_RUNTIME/data"
    cp "$HOME/bridge-install-a28d01a/.env.example" "$BRIDGE_RUNTIME/.env"
    chmod 600 "$BRIDGE_RUNTIME/.env"
fi
"${EDITOR:-vi}" "$BRIDGE_RUNTIME/.env"
```

### Service Configuration

Replace the placeholder token, configure explicit allowed user IDs (and group IDs if needed), and set `CODEX_WORKSPACE_ROOT` to an existing writable absolute directory. The bridge's `.env` parser uses simple unquoted `KEY=VALUE` lines without shell evaluation. Authenticate the installed Codex CLI as this user **before** starting. Use an absolute `CODEX_BIN` if it is outside the generated unit's search path: `~/.local/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`.

Run `start` to generate the systemd unit (`~/.config/systemd/user/com.telegramcodex.bridge.service`) with correct `WorkingDirectory` and `.env`.

```bash
"$BRIDGE_RUNTIME/bin/telegram-codex-bridge" start
"$BRIDGE_RUNTIME/bin/telegram-codex-bridge" set-autostart on
```

To enable autostart at boot without login:

```bash
sudo loginctl enable-linger "$USER"
```

This requires a working systemd user session; confirm `systemctl --user status` connects to the user manager. For arm64 hosts, replace `amd64` with `arm64` in the local build output and `GOARCH`, then transfer that artifact instead. Do not build or clone source on the production server.

## 4. Verification

Choose `BRIDGE_RUNTIME` for the platform above. No HTTP health endpoint exists. Perform the same checks for a first installation and an update:

1.  **Version Check**:

    ```bash
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" version --json
    # Expected: version: "dev+a28d01a", commit: "a28d01a"
    ```

2.  **Paths Check**:

    ```bash
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" paths --json
    ```

3.  **Status Check**:

    ```bash
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" status --json
    "$BRIDGE_RUNTIME/bin/telegram-codex-bridge" codex --json
    # installed=true, loaded=true, running=true, positive PID; backend ready=true
    ```

4.  **Log Audit**:

    On-disk version/status is not proof of running revision. Check startup logs for:
    `starting build version=dev+a28d01a commit=a28d01a`
    Verify new stable service PID.

    ```bash
    tail -n 100 "$BRIDGE_RUNTIME/data/logs/bridge.stdout.log"
    tail -n 100 "$BRIDGE_RUNTIME/data/logs/bridge.stderr.log"
    ```

    If `BRIDGE_LOG_PATH` is customized, inspect that file too. Resolve recent authentication, polling-conflict, SQLite, or backend errors; inspect Linux's `systemctl --user status com.telegramcodex.bridge.service` / `journalctl --user -u com.telegramcodex.bridge.service -n 50 --no-pager`, or macOS's `launchctl print "gui/$(id -u)/com.telegramcodex.bridge"`, for service exits.

5.  **Running revision and stability**: From an allowlisted Telegram chat, the operator sends `/version`, `/status`, and a small backend prompt. `/version` must identify `dev+a28d01a` and `a28d01a`; `/status` shows the topic/session binding. Confirm a backend reply. Observe for a full poll timeout (default 30 seconds), repeat `status --json`, and verify the new PID remains stable. A version query against the binary on disk alone does not prove the running process was updated. Only one poller may use this token.

## 5. Rollback & Safety

**Baseline**: No production baseline known. Today's commits do not modify store/schema.

The previous deployed revision is unknown. These commits alone do not establish compatibility with any older instance later discovered. Review its actual revision and state before updating it.

### macOS Rollback

1.  Stop runtime: `"$BRIDGE_RUNTIME/bin/telegram-codex-bridge" stop` (via path in Application Support).
2.  Quit app via menu.
3.  Restore the previous runtime binary from the stopped backup.
4.  Restore the old `.app` bundle so reopening copies the previous binary.
5.  Restart runtime and set-autostart.

### Linux Rollback

1.  Stop service: `"$BRIDGE_RUNTIME/bin/telegram-codex-bridge" stop`
2.  Restore previous binary from `~/bridge-backups`.
3.  If schema/config incompatible after review:

    *   Data restore only if necessary.
    *   SQLite consistent backup (when STOPPED) includes WAL/SHM.
4.  Workspace files are separate.
5.  Restart: `"$BRIDGE_RUNTIME/bin/telegram-codex-bridge" start`

### State Management

*   **Data Restore**: Only performed if schema/config incompatibility is confirmed post-review.
*   **SQLite**: Ensure consistent backup (WAL/SHM) when service is **STOPPED**.
*   **Workspace**: Files are separate from app/runtime binary.

After rollback, repeat the health checks against the restored revision. A first installation has no prior deployed binary to restore; stop the failed service and preserve its configuration/state for diagnosis.
