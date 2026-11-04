//go:build windows

package main

// nettune_windows.go：Windows 下的网卡跃点调整。
//
// 思路：把房间虚拟网卡的接口跃点降到 1，让游戏流量优先走隧道（局域网联机工具
// 常见做法，例如把 "ZeroTier Virtual Port" 的 metric 设为 1）。
// 但：按 ZeroTier 实际返回的网卡标识（GUID 或连接名）定位接口，改动前记录原值，
// 离开房间时恢复，避免把用户机器永久改坏。
//
// 需要管理员权限；客户端在 Windows 上启动时已经提权，这里不再单独判断。

import (
	"fmt"
	"strconv"
	"strings"
)

type windowsNetTune struct{}

func platformNetTune() netTune { return windowsNetTune{} }

// psQuote 把值安全地嵌进 PowerShell 单引号字符串（单引号内只需转义单引号）。
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ztAdapterIndex 把 ZeroTier 的 portDeviceName 解析成接口索引。
// portDeviceName 在 Windows 上通常是网卡 GUID（带大括号），也可能被系统改成
// 友好名称，所以两种都比对；GUID 比较前统一去大括号、转小写。
func ztAdapterIndex(device string) (int, error) {
	script := fmt.Sprintf(`
$want = %s.Trim('{','}').ToLower()
$a = Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue | Where-Object {
  $_.Name -eq %s -or $_.InterfaceGuid.ToString().Trim('{','}').ToLower() -eq $want
} | Select-Object -First 1
if ($a) { $a.ifIndex }
`, psQuote(device), psQuote(device))
	out, err := hideCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("枚举网卡失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	idx, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || idx <= 0 {
		return 0, fmt.Errorf("未找到网卡 %q", device)
	}
	return idx, nil
}

func (windowsNetTune) LowerMetric(device string) (int, error) {
	idx, err := ztAdapterIndex(device)
	if err != nil {
		return 0, err
	}
	read := fmt.Sprintf(`(Get-NetIPInterface -AddressFamily IPv4 -InterfaceIndex %d -ErrorAction Stop).InterfaceMetric`, idx)
	out, err := hideCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", read).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("读取跃点失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	prev, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("跃点值无法解析: %q", strings.TrimSpace(string(out)))
	}
	if prev <= 1 {
		return prev, nil // 已经是最高优先级，无需改动
	}
	set := fmt.Sprintf(`Set-NetIPInterface -AddressFamily IPv4 -InterfaceIndex %d -InterfaceMetric 1 -ErrorAction Stop`, idx)
	if out, err := hideCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", set).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("设置跃点失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return prev, nil
}

func (windowsNetTune) RestoreMetric(device string, prev int) error {
	if prev <= 0 {
		return nil
	}
	idx, err := ztAdapterIndex(device)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`Set-NetIPInterface -AddressFamily IPv4 -InterfaceIndex %d -InterfaceMetric %d -ErrorAction Stop`, idx, prev)
	if out, err := hideCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		return fmt.Errorf("恢复跃点失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
