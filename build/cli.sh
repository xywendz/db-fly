#!/usr/bin/env bash
# 构建 Linux/Windows 命令行工具（纯 Go，静态链接，不依赖 CGO）。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-dev}"
GOOS_TARGET="${GOOS_TARGET:-linux}"
GOARCH_TARGET="${GOARCH_TARGET:-amd64}"
OUTPUT="${OUTPUT:-dist/dbctl}"

mkdir -p "$(dirname "$OUTPUT")"
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" \
  go build -trimpath \
  -ldflags "-s -w -X github.com/dbclient/dbclient/internal/cli.Version=${VERSION}" \
  -o "$OUTPUT" ./cmd/dbctl

echo "已生成 $OUTPUT (${GOOS_TARGET}/${GOARCH_TARGET}, ${VERSION})"
