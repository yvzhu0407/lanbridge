#!/bin/bash
# build-windows-setup.sh:构建 Windows 客户端并打成 NSIS 安装包
#
# 用法: ./build-windows-setup.sh [产物目录]
#   VERSION  版本号，用于文件名和安装包内显示的版本，默认 1.0.0
#
# 产物:
#   <产物目录>/lanbridge-client.exe                 裸二进制（安装包内部使用）
#   <产物目录>/LANBridge-<版本>-windows-amd64-setup.exe  安装包
#
# 依赖: makensis（macOS: brew install makensis；Windows: 官方 NSIS）。
# Windows 客户端是纯用户态 API，可在 macOS 上交叉编译。
set -euo pipefail
cd "$(dirname "$0")"

OUT="${1:-dist}"
VERSION="${VERSION:-1.0.0}"
mkdir -p "$OUT"

command -v makensis >/dev/null 2>&1 || {
  echo "需要 makensis（macOS: brew install makensis）" >&2
  exit 1
}

# client/icon.ico 是从 client/icon.png 生成并提交的多尺寸图标（256/128/64/48/32/16）。
# 生成命令：magick client/icon.png -define icon:auto-resize=256,128,64,48,32,16 client/icon.ico
[ -f client/icon.ico ] || {
  echo "缺少 client/icon.ico（见脚本内注释的生成命令）" >&2
  exit 1
}

echo "==> 编译 windows/amd64"
# -H windowsgui：GUI 子系统，双击不弹控制台黑框
GOOS=windows GOARCH=amd64 go build -ldflags='-s -w -H windowsgui' \
  -o "$OUT/lanbridge-client.exe" ./client

echo "==> 生成安装包"
# VIProductVersion 需要纯数字 X.X.X.X：把 1.0.0-test 之类裁成 1.0.0
VERNUM=$(printf '%s' "$VERSION" | grep -oE '^[0-9]+(\.[0-9]+){0,2}' || true)
VERNUM="${VERNUM:-1.0.0}"
case "$(printf '%s' "$VERNUM" | awk -F. '{print NF}')" in
  1) VERNUM="$VERNUM.0.0" ;;
  2) VERNUM="$VERNUM.0" ;;
esac

makensis -V2 \
  -DVERSION="$VERSION" \
  -DVERNUM="$VERNUM" \
  -DSRCDIR="$(cd "$OUT" && pwd)" \
  -DOUTFILE="$(cd "$OUT" && pwd)/LANBridge-${VERSION}-windows-amd64-setup.exe" \
  lanbridge.nsi

echo "==> 完成"
ls -lh "$OUT/lanbridge-client.exe" "$OUT/LANBridge-${VERSION}-windows-amd64-setup.exe"
