//go:build !darwin && !windows

package main

// scheme_other.go: 非 mac / 非 windows 平台的占位。
// 这两个平台没有注册 URL Scheme 的实现，邀请链接退化为「复制 + 手动粘贴房间号」。

// ensureURLScheme 在其它平台无事可做。
func ensureURLScheme() {}

// isBundledApp 在其它平台恒为 true(不影响任何逻辑)。
func isBundledApp() bool { return true }
