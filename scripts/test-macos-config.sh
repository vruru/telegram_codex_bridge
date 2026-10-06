#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT
swiftc "$ROOT_DIR/macos/BridgeStatusBarApp/EnvFile.swift" \
  "$ROOT_DIR/macos/tests/main.swift" -o "$BUILD_DIR/envfile-tests"
"$BUILD_DIR/envfile-tests"
