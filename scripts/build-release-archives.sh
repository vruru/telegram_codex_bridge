#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/releases}"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
GO_LDFLAGS="-X telegram-codex-bridge/internal/buildinfo.Version=${VERSION} -X telegram-codex-bridge/internal/buildinfo.Commit=${COMMIT}"
UPX_ENABLED="${UPX_ENABLED:-false}"
UPX_ARGS="${UPX_ARGS:---best --lzma}"

mkdir -p "$DIST_DIR"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

for target in "${TARGETS[@]}"; do
  read -r GOOS GOARCH <<<"$target"
  STAGE_DIR="$(mktemp -d)"
  ARCHIVE_BASENAME="telegram-codex-bridge_${VERSION}_${GOOS}_${GOARCH}"
  BINARY_PATH="$STAGE_DIR/telegram-codex-bridge"

  echo "building $GOOS/$GOARCH"
  (
    cd "$ROOT_DIR"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags "$GO_LDFLAGS" -o "$BINARY_PATH" ./cmd/bridge
  )

  if [[ "$UPX_ENABLED" == "true" && "$GOOS" == "linux" ]]; then
    if command -v upx >/dev/null 2>&1; then
      echo "compressing $GOOS/$GOARCH with upx $UPX_ARGS"
      upx $UPX_ARGS "$BINARY_PATH"
    else
      echo "UPX requested but not available; skipping binary compression for $GOOS/$GOARCH" >&2
    fi
  fi

  cp "$ROOT_DIR/README.md" "$STAGE_DIR/README.md"
  tar -C "$STAGE_DIR" -czf "$DIST_DIR/${ARCHIVE_BASENAME}.tar.gz" telegram-codex-bridge README.md
  rm -rf "$STAGE_DIR"
done

echo "release archives written to $DIST_DIR"
