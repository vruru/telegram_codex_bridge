#!/usr/bin/env bash
# Inspect a built DMG without installing or launching its menu app.
set -euo pipefail
if [[ $# != 2 ]]; then
  echo "usage: $0 <dmg-path> <expected-version-without-v-prefix>" >&2
  exit 2
fi
TEST_DIR="$(mktemp -d)"
MOUNT_POINT="$TEST_DIR/mount"
mkdir "$MOUNT_POINT"
MOUNTED=false
cleanup() {
  if [[ "$MOUNTED" == true ]]; then
    hdiutil detach "$MOUNT_POINT" >/dev/null 2>&1 || hdiutil detach -force "$MOUNT_POINT" >/dev/null
  fi
  rm -rf "$TEST_DIR"
}
trap cleanup EXIT
hdiutil attach -readonly -nobrowse -mountpoint "$MOUNT_POINT" "$1" > "$TEST_DIR/attach.log"
MOUNTED=true
python3 - "$MOUNT_POINT" "$2" <<'PY'
import pathlib, plistlib, subprocess, sys
mount = pathlib.Path(sys.argv[1])
app = mount / 'Telegram Codex Bridge.app' / 'Contents'
info = plistlib.loads((app / 'Info.plist').read_bytes())
assert info['CFBundleShortVersionString'] == sys.argv[2]
assert (mount / 'Applications').is_symlink()
assert (app / 'MacOS' / info['CFBundleExecutable']).is_file()
assert (app / 'Resources/AppIcon.icns').read_bytes().startswith(b'icns')
version = subprocess.check_output([str(app / 'Resources/telegram-codex-bridge'), 'version'], text=True)
assert 'version=' in version and sys.argv[2] in version
print('PASS: mounted DMG app/icon, Applications link, and embedded version')
PY
