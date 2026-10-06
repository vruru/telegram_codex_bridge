# Offline release validation, 2026-10-07

This closes the repository validation gaps in #1, #2, and #3. It does not select a deployment host, install an app, start a managed service, or poll Telegram. The macOS setup merge regression for #5 is documented in [macOS operation](macos.md).

The native Swift app, icon and DMG were built in an isolated copy of the checkout. A read-only mount contained the app, an Applications link, and an `icns` icon; Info.plist and the embedded bridge both reported the requested `issue-wave-20261007` version. Only the embedded binary's `version` command was executed. `scripts/test-macos-dmg.sh` repeats these checks and detaches only its own mount:

```sh
bash scripts/test-macos-config.sh
mkdir -p dist
VERSION=issue-wave-20261007 bash scripts/build-macos-dmg.sh
bash scripts/test-macos-dmg.sh 'dist/Telegram Codex Bridge.dmg' issue-wave-20261007
```

Both Linux amd64 and arm64 binaries actually executed `version`, `paths --json`, and the compiled `internal/store` SQLite regression in isolated Linux containers with no network or bot token. Arm64 ran natively in the local Linux VM; amd64 ran with the Docker platform emulation available on this Mac. This is execution evidence for both architectures, not native amd64 hardware acceptance.

UPX 5.2.1 from its official release compressed both architectures. Each passed `upx -t`, packed `version` and `paths` execution, decompression, and a byte-for-byte comparison with the original executable. This does not change CI or enable compression by default. The reproducible script needs Linux, Bash, Python 3 and executable temporary storage:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/bridge-arm64 ./cmd/bridge
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o /tmp/store-arm64.test ./internal/store
# On an isolated Linux environment for the selected architecture:
bash scripts/test-linux-binaries.sh /path/to/bridge-arm64 /path/to/store-arm64.test /path/to/upx
```

Repeat with `GOARCH=amd64` and its respective files. The optional third argument must be a Linux UPX executable that can run in the test environment. The script uses a temporary runtime, verifies paths without creating a state database, runs the SQLite test, tests packed commands, and checks decompression integrity. Do not pass `serve` or `start` for these checks.

The complete Mac Go suite and `go vet ./...`, 29 Swift merge assertions, and ShellCheck for the new Bash validation scripts passed. No real provider, Telegram, Whisper, deployment, or platform service lifecycle was exercised. #4 remains open for the outstanding host attribution and deployment/hibernation decision.
