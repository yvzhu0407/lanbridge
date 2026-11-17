//go:build darwin

package main

// scheme_darwin.go: 把 lanbridge:// 注册给 LANBridge.app。
//
// macOS 认 Info.plist 里的 CFBundleURLTypes。裸二进制(go run / 直接执行)
// 没有 bundle，无法注册；这种情况只支持"复制链接 + 手动粘贴房间号"。
//
// 运行中的实例如何收到链接:Info.plist 的 CFBundleURLTypes 触发的是
// Apple Event(kAEGetURL)，必须由应用自己实现 NSAppleEventManager 处理器，
// 见 window_darwin.go 里的 installURLEventHandler。冷启动时链接在
// argv 里，由 main 解析。

import (
	"os"
	"path/filepath"
	"strings"
)

// isBundledApp 报告当前是否跑在 .app bundle 里。
// 只有 bundle 才能注册 URL Scheme。
func isBundledApp() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/")
}

// ensureURLScheme 在 macOS 上无事可做：注册信息写在 Info.plist 里，
// 由启动时生成的 App bundle 带走。
func ensureURLScheme() {}
