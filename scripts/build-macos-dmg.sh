#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
APP_NAME="Telegram Codex Bridge"
APP_BUNDLE="$ROOT_DIR/dist/$APP_NAME.app"
DMG_PATH="$ROOT_DIR/dist/$APP_NAME.dmg"
VOLUME_NAME="$APP_NAME"
STAGING_DIR="$(mktemp -d "$ROOT_DIR/dist/.dmg-staging.XXXXXX")"
TEMP_DMG="$ROOT_DIR/dist/.${APP_NAME// /-}.$$.$RANDOM.dmg"

cleanup() {
  rm -rf "$STAGING_DIR"
  rm -f "$TEMP_DMG"
}

detach_stale_volumes() {
  python3 - "$VOLUME_NAME" <<'PY'
import plistlib
import subprocess
import sys

volume_name = sys.argv[1]
prefix = f"/Volumes/{volume_name}"

try:
    raw = subprocess.check_output(["hdiutil", "info", "-plist"], stderr=subprocess.DEVNULL)
except Exception:
    sys.exit(0)

try:
    info = plistlib.loads(raw)
except Exception:
    sys.exit(0)

seen = set()
for image in info.get("images", []):
    for entity in image.get("system-entities", []):
        mount_point = entity.get("mount-point", "")
        dev_entry = entity.get("dev-entry", "")
        if dev_entry and mount_point.startswith(prefix) and dev_entry not in seen:
            seen.add(dev_entry)
            print(dev_entry)
PY
}

trap cleanup EXIT

"$ROOT_DIR/scripts/build-macos-app.sh"

rm -f "$DMG_PATH"
while IFS= read -r device; do
  [[ -n "$device" ]] || continue
  hdiutil detach "$device" -force >/dev/null 2>&1 || true
done < <(detach_stale_volumes)

cp -R "$APP_BUNDLE" "$STAGING_DIR/"
ln -s /Applications "$STAGING_DIR/Applications"

hdiutil create \
  -volname "$VOLUME_NAME" \
  -srcfolder "$STAGING_DIR" \
  -ov \
  -format UDZO \
  "$TEMP_DMG"

mv "$TEMP_DMG" "$DMG_PATH"

echo "Built $DMG_PATH"
