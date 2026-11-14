#!/usr/bin/env bash
# 构建 B/S 模式服务端（net/http + 内嵌 Vue 前端）。
# 前端模板: npm run build:web (--mode web) -> frontend/dist-web
# Go 入口:  ./cmd/db-fly-server，纯 Go，CGO_ENABLED=0
#
# 用法:
#   bash build/web.sh
#   VERSION=v1.0.0 bash build/web.sh
#   GOOS_TARGET=linux GOARCH_TARGET=arm64 OUTPUT=dist/db-fly-server bash build/web.sh
#   GOOS_TARGET=windows GOARCH_TARGET=amd64 OUTPUT=dist/db-fly-server.exe bash build/web.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# ---- 1. 版本号（注入 internal/bridge.Version）----
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

# ---- 2. 构建 Web 前端模板 ----
echo "==> 构建 Web 前端"
rm -rf frontend/dist-web
( cd frontend
  [ -d node_modules ] || npm install
  npm run build:web
)

# ---- 3. 复制到 Go 嵌入目录 ----
EMBED_DIR="internal/webui/dist"
echo "==> 复制前端产物到 $EMBED_DIR"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -r frontend/dist-web/. "$EMBED_DIR/"

# ---- 4. Go 构建（默认 linux/amd64；可用 GOOS_TARGET/GOARCH_TARGET 覆盖）----
export CGO_ENABLED=0
GOOS_TARGET="${GOOS_TARGET:-linux}"
GOARCH_TARGET="${GOARCH_TARGET:-amd64}"
OUTPUT="${OUTPUT:-dist/db-fly-server}"

mkdir -p "$(dirname "$OUTPUT")"
if [ "$GOOS_TARGET" = "windows" ] && [[ "$OUTPUT" != *.exe ]]; then
  OUTPUT="${OUTPUT}.exe"
fi
go build -trimpath \
  -ldflags "-s -w -X github.com/db-fly/db-fly/internal/bridge.Version=${VERSION}" \
  -o "$OUTPUT" ./cmd/db-fly-server

echo "==> 已生成 $OUTPUT (${GOOS_TARGET}/${GOARCH_TARGET}, ${VERSION})"
echo "==> 运行: DB_FLY_WEB_ADDR=127.0.0.1:8080 $OUTPUT   (打开 /login 注册)"
