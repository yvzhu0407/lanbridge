#!/bin/bash
# build-macos-dmg.sh:把 LANBridge.app 打成 DMG 安装包（拖进「应用程序」的标准安装方式）
#
# 用法: ./build-macos-dmg.sh [产物目录] [输出文件]
#   VERSION  版本号，同时用于 Info.plist 和 DMG 文件名，默认 1.0.0
#
# 依赖: 先跑 build-mac-app.sh 生成 LANBridge.app
set -euo pipefail
cd "$(dirname "$0")"

OUT="${1:-dist}"
VERSION="${VERSION:-1.0.0}"
DMG="${2:-$OUT/LANBridge-${VERSION}-macOS-universal.dmg}"
APP="$OUT/LANBridge.app"

[ -d "$APP" ] || { echo "找不到 $APP，请先运行 build-mac-app.sh" >&2; exit 1; }

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

echo "==> 组织 DMG 内容"
cp -R "$APP" "$STAGE/"
# 指向 /Applications 的软链，用户直接把 App 拖过去即可
ln -s /Applications "$STAGE/Applications"

rm -f "$DMG"
echo "==> 生成 DMG"
hdiutil create \
  -volname "LANBridge ${VERSION}" \
  -srcfolder "$STAGE" \
  -ov -format UDZO \
  -fs HFS+ \
  "$DMG" >/dev/null

echo "==> 完成"
ls -lh "$DMG"
