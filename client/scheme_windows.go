//go:build windows

package main

// scheme_windows.go: 把 lanbridge:// 注册到当前用户的注册表。
//
// 写 HKCU 而不是 HKLM：不需要管理员权限，天然按用户生效，
// 卸载时也只清理自己写的那份。安装包已经以管理员运行，但注册表项
// 还是写 HKCU —— 换用户登录后新用户第一次启动会自己注册一次。
//
// 值的内容用双引号包住 exe 路径：Program Files 下有空格，
// 不加引号系统按空格切分参数，链接会被当成 exe 名的一部分。

import (
	"os"
)

const (
	schemeKey = `Software\Classes\lanbridge`
)

// ensureURLScheme 每次启动都写一遍(幂等)：程序被挪动位置或换了版本，
// 注册表里的命令会过期，重写一次即可自愈。
func ensureURLScheme() {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		LogDev("[邀请] 无法确定程序路径，跳过 lanbridge:// 注册")
		return
	}
	cmd := `"` + exe + `" "%1"`

	// 兼容旧数据：删掉可能存在的无引号写法
	if out, err := hideCmd("reg", "add", schemeKey, "/ve", "/d", "URL:LANBridge Protocol", "/f").CombinedOutput(); err != nil {
		LogDev("[邀请] 注册 lanbridge:// 失败: %v: %s", err, winText(out))
		return
	}
	hideCmd("reg", "add", schemeKey, "/v", "URL Protocol", "/d", "", "/f").Run()
	if out, err := hideCmd("reg", "add", schemeKey+`\shell\open\command`, "/ve", "/d", cmd, "/f").CombinedOutput(); err != nil {
		LogDev("[邀请] 写入 lanbridge:// 启动命令失败: %v: %s", err, winText(out))
	}
}

// isBundledApp 在 Windows 上没有意义，恒为 true：
// 注册表方案不依赖安装形态，裸 exe 也能被系统调起。
func isBundledApp() bool { return true }
