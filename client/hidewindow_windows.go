//go:build windows

package main

// hidewindow_windows.go:Windows GUI 子系统下,exec 出的子进程默认会闪控制台黑框。
// 统一经 hideCmd 创建:CREATE_NO_WINDOW + HideWindow,黑框不再出现。
// 日志仍写 client.log / zt-node.log,不受影响。

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hideCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}
