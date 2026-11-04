//go:build !windows

package main

// hidewindow_other.go:非 Windows 平台 hideCmd 就是普通 exec.Command(无窗口概念)

import "os/exec"

func hideCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
