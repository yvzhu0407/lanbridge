//go:build darwin

package main

// checkElevation: macOS 上不需要提权检查(osascript 在启动 zt-node 时按需弹权限框)
func checkElevation() bool { return false }
