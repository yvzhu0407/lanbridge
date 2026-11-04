#!/bin/bash
# fetch-zt-embed.sh:从 ZeroTier 官方发行包提取客户端需要内嵌的运行时文件。
#
# 客户端把 ZeroTier 二进制和 Windows TAP 驱动编译进产物，所以从源码构建前
# 需要先准备好 client/zt-embed/ 和 client/zt-embed-mac/。
# 这两个目录体积大且属于第三方二进制，不纳入版本控制。
#
# 用法: ./fetch-zt-embed.sh [版本号] [--mac-only]     默认版本 1.16.2
#   --mac-only  只准备 macOS 运行时（CI 在 macOS runner 上跑测试时用，
#               因为 Linux/macOS 默认没有解 .msi 的工具）
#
# 依赖:
#   macOS 侧: pkgutil、cpio（系统自带）
#   Windows 侧: msiextract（brew install msitools）或 7z
#
# 内嵌版本必须和服务端控制器版本大体一致，否则控制器 API 行为可能有差异。
set -euo pipefail
cd "$(dirname "$0")"

VER="${1:-1.16.2}"
MAC_ONLY=0
for arg in "$@"; do [ "$arg" = "--mac-only" ] && MAC_ONLY=1; done
BASE="https://download.zerotier.com/RELEASES/${VER}/dist"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "==> ZeroTier ${VER}"

# ---------- macOS ----------
echo "==> 提取 macOS 运行时"
curl -fsSL "$BASE/ZeroTier%20One.pkg" -o "$TMP/zt.pkg"

# --expand-full 能直接解开 Payload；老版本 pkgutil 不支持时退回 --expand + cpio。
if ! pkgutil --expand-full "$TMP/zt.pkg" "$TMP/pkg" 2>/dev/null; then
  pkgutil --expand "$TMP/zt.pkg" "$TMP/pkg"
  PAYLOAD=$(find "$TMP/pkg" -type f -name Payload | head -1)
  mkdir -p "$TMP/pkg/ZeroTier_One.pkg/Payload"
  ( cd "$TMP/pkg/ZeroTier_One.pkg/Payload" && gunzip -c "$PAYLOAD" 2>/dev/null | cpio -idm --quiet 2>/dev/null \
      || cpio -idm --quiet < "$PAYLOAD" )
fi

NODE=$(find "$TMP/pkg" -type f -name "zerotier-one" | head -1)
AGENT=$(find "$TMP/pkg" -type f -name "MacEthernetTapAgent" | head -1)
[ -n "$NODE" ] && [ -n "$AGENT" ] || { echo "未在 pkg 中找到 ZeroTier 文件" >&2; exit 1; }

mkdir -p client/zt-embed-mac
cp "$NODE"  client/zt-embed-mac/zt-node
cp "$AGENT" client/zt-embed-mac/MacEthernetTapAgent
chmod +x client/zt-embed-mac/zt-node client/zt-embed-mac/MacEthernetTapAgent
echo "    client/zt-embed-mac/zt-node ($("$NODE" -v 2>/dev/null | head -1 || echo unknown))"
echo "    client/zt-embed-mac/MacEthernetTapAgent"

if [ "$MAC_ONLY" = "1" ]; then
  echo "==> 跳过 Windows（--mac-only）"
  exit 0
fi

# ---------- Windows ----------
echo "==> 提取 Windows 运行时"
curl -fsSL "$BASE/ZeroTier%20One.msi" -o "$TMP/zt.msi"

mkdir -p "$TMP/win"
if command -v msiextract >/dev/null 2>&1; then
  msiextract -C "$TMP/win" "$TMP/zt.msi" >/dev/null
elif command -v 7z >/dev/null 2>&1; then
  ( cd "$TMP/win" && 7z x -y "$TMP/zt.msi" >/dev/null )
else
  echo "需要 msiextract（macOS: brew install msitools）或 7z 来解包 .msi" >&2
  exit 1
fi

EXE=$(find "$TMP/win" -type f -iname "zerotier-one_x64.exe" | head -1)
[ -n "$EXE" ] || { echo "未在 msi 中找到 zerotier-one_x64.exe" >&2; exit 1; }

mkdir -p client/zt-embed
cp "$EXE" client/zt-embed/zt-node.exe
echo "    client/zt-embed/zt-node.exe"

# TAP 驱动（zttap300.*）不在 ZeroTier 安装包里，从 ZeroTierOne 源码树的
# ext/bin/tap-windows-ndis6/x64/ 取，版本与上面保持一致。
echo "==> 提取 Windows TAP 驱动"
TAPBASE="https://raw.githubusercontent.com/zerotier/ZeroTierOne/${VER}/ext/bin/tap-windows-ndis6/x64"
for f in zttap300.inf zttap300.sys zttap300.cat; do
  curl -fsSL "$TAPBASE/$f" -o "client/zt-embed/$f" || {
    echo "下载 $f 失败（该版本可能没有此路径），请从已安装 ZeroTier 的 Windows 机器复制:" >&2
    echo "  C:\\Program Files (x86)\\ZeroTier\\One\\tap-windows\\$f" >&2
    rm -f "client/zt-embed/$f"
    exit 1
  }
  echo "    client/zt-embed/$f"
done
# 仓库里的 .inf 是 LF，Windows 驱动安装按 CRLF 解析更稳妥，统一转换
python3 - "client/zt-embed/zttap300.inf" <<'PY'
import sys
p = sys.argv[1]
data = open(p, 'rb').read().replace(b'\r\n', b'\n').replace(b'\n', b'\r\n')
open(p, 'wb').write(data)
PY

echo "==> 完成"
