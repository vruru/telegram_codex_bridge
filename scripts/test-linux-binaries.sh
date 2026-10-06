#!/usr/bin/env bash
# Offline Linux checks; never serve, start a service, or poll Telegram.
set -euo pipefail
if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <bridge-binary> <compiled-store-test> [upx-binary]" >&2
  exit 2
fi
BRIDGE_BINARY="$1"
STORE_TEST="$2"
UPX_BINARY="${3:-}"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
mkdir -p "$TEST_DIR/bin"
cp "$BRIDGE_BINARY" "$TEST_DIR/bin/telegram-codex-bridge"
RUNTIME_BINARY="$TEST_DIR/bin/telegram-codex-bridge"
EXPECTED_VERSION="$("$RUNTIME_BINARY" version)"
check_runtime() {
  [[ "$("$RUNTIME_BINARY" version)" == "$EXPECTED_VERSION" ]]
  "$RUNTIME_BINARY" paths --json --project-root "$TEST_DIR" > "$TEST_DIR/paths.json"
  python3 - "$TEST_DIR" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
data = json.loads((root / 'paths.json').read_text())
assert data['project_root'] == str(root)
assert data['state_path'] == str(root / 'data/bridge.db')
assert not (root / 'data/bridge.db').exists(), 'management paths unexpectedly created state'
PY
}
check_runtime
"$STORE_TEST" -test.v
if [[ -n "$UPX_BINARY" ]]; then
  cp "$RUNTIME_BINARY" "$TEST_DIR/original"
  "$UPX_BINARY" --best --lzma "$RUNTIME_BINARY"
  "$UPX_BINARY" -t "$RUNTIME_BINARY"
  check_runtime
  "$UPX_BINARY" -d "$RUNTIME_BINARY"
  cmp "$RUNTIME_BINARY" "$TEST_DIR/original"
fi
echo "PASS: offline Linux version, paths, SQLite, and requested UPX checks"
