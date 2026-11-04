#!/bin/bash
# build-mac-app.sh:构建 mac 版 LANBridge.app(GUI bundle,双击启动无 Terminal)+ 裸二进制
# 产物:dist/LANBridge.app/、dist/lanbridge-client-mac
#
# 用法: ./build-mac-app.sh [输出目录]        默认 dist
# 环境变量:
#   VERSION  写入 Info.plist 的版本号，默认 1.0.0（发布时由 release workflow 传入 tag）
#
# 输出是 universal 二进制（arm64 + x86_64 用 lipo 合并），Intel 与 Apple Silicon 通用。
# 内嵌的 zt-node 本身已是 universal，所以两架构共用同一份运行时。
set -euo pipefail
cd "$(dirname "$0")"

OUT="${1:-dist}"
VERSION="${VERSION:-1.0.0}"
mkdir -p "$OUT"

echo "==> 编译 darwin/arm64 + darwin/amd64"
# 窗口层用了 cgo（window_darwin.go 里的 Objective-C），交叉架构编译时 Go 默认关掉 cgo，
# 不显式打开会导致 runWindow 未定义。必须在 macOS 主机上跑。
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags='-s -w' -o "$OUT/.lanbridge-arm64" ./client
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -ldflags='-s -w' -o "$OUT/.lanbridge-amd64" ./client

echo "==> 合并为 universal"
lipo -create "$OUT/.lanbridge-arm64" "$OUT/.lanbridge-amd64" \
  -output "$OUT/lanbridge-client-mac"
rm -f "$OUT/.lanbridge-arm64" "$OUT/.lanbridge-amd64"
chmod +x "$OUT/lanbridge-client-mac"

echo "==> 组装 LANBridge.app"
APP="$OUT/LANBridge.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$OUT/lanbridge-client-mac" "$APP/Contents/MacOS/LANBridge"
chmod +x "$APP/Contents/MacOS/LANBridge"

# icns 图标(从 client/icon.png 生成,iconutil 标准流程)
ICON_TMP=$(mktemp -d)
trap 'rm -rf "$ICON_TMP"' EXIT
ICONSET="$ICON_TMP/LANBridge.iconset"
mkdir -p "$ICONSET"
for sz in 16 32 128 256 512; do
  sips -z $sz $sz client/icon.png --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null
  sips -z $((sz*2)) $((sz*2)) client/icon.png --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/App.icns"

cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>LANBridge</string>
	<key>CFBundleDisplayName</key><string>桥联 LANBridge</string>
	<key>CFBundleIdentifier</key><string>com.lanbridge.client</string>
	<key>CFBundleVersion</key><string>${VERSION}</string>
	<key>CFBundleShortVersionString</key><string>${VERSION}</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleExecutable</key><string>LANBridge</string>
	<key>CFBundleIconFile</key><string>App</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSSupportsAutomaticGraphicsSwitching</key><true/>
</dict>
</plist>
EOF

echo "==> ad-hoc 签名"
# 没有 Apple 开发者证书，用 ad-hoc 签名(--sign -)。它不需要证书，
# 能让 bundle 在 Apple Silicon 上正常启动；但用户首次打开仍需「右键→打开」，
# 因为下载的文件带 com.apple.quarantine 扩展属性。
codesign --force --deep --sign - "$APP"

echo "==> 完成"
ls -la "$OUT" | grep -i 'lanbridge\|LANBridge'
lipo -archs "$OUT/lanbridge-client-mac"
du -sh "$APP"
