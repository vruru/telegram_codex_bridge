#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
APP_BUNDLE_NAME="Telegram Codex Bridge"
APP_EXECUTABLE_NAME="TelegramCodexBridgeStatus"
APP_BUNDLE="$ROOT_DIR/dist/$APP_BUNDLE_NAME.app"
APP_EXECUTABLE="$APP_BUNDLE/Contents/MacOS/$APP_EXECUTABLE_NAME"
APP_RESOURCES="$APP_BUNDLE/Contents/Resources"
ICONSET_DIR="$ROOT_DIR/dist/$APP_EXECUTABLE_NAME.iconset"
ICON_FILE="$APP_RESOURCES/AppIcon.icns"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
APP_VERSION="${VERSION#v}"
APP_BUILD="${APP_VERSION:-1}"
GO_LDFLAGS="-X telegram-codex-bridge/internal/buildinfo.Version=${VERSION} -X telegram-codex-bridge/internal/buildinfo.Commit=${COMMIT}"

mkdir -p "$ROOT_DIR/bin" "$ROOT_DIR/dist" "$APP_BUNDLE/Contents/MacOS" "$APP_RESOURCES"

cd "$ROOT_DIR"

go build -ldflags "$GO_LDFLAGS" -o "$ROOT_DIR/bin/telegram-codex-bridge" ./cmd/bridge

swiftc \
  "$ROOT_DIR/macos/BridgeStatusBarApp/main.swift" \
  -o "$APP_EXECUTABLE"

rm -rf "$ICONSET_DIR"
swift "$ROOT_DIR/scripts/render-macos-icon.swift" "$ICONSET_DIR"
iconutil --convert icns --output "$ICON_FILE" "$ICONSET_DIR"
rm -rf "$ICONSET_DIR"

sed \
  -e "s/__APP_VERSION__/${APP_VERSION}/g" \
  -e "s/__APP_BUILD__/${APP_BUILD}/g" \
  "$ROOT_DIR/macos/BridgeStatusBarApp/Info.plist" > "$APP_BUNDLE/Contents/Info.plist"
cp "$ROOT_DIR/bin/telegram-codex-bridge" "$APP_RESOURCES/telegram-codex-bridge"

chmod +x "$APP_EXECUTABLE" "$APP_RESOURCES/telegram-codex-bridge"

echo "Built $APP_BUNDLE"
