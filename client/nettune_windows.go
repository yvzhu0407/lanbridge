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

// psUTF8 强制 PowerShell 用 UTF-8 输出。
// 中文 Windows 的 PowerShell 默认输出 ANSI 码页(GBK)，错误信息里带中文时
// 会被 Go 当成 UTF-8 解析，日志里就是乱码。所有脚本都要带上这一段。
const psUTF8 = `$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false` + "\n"

// psRun 执行 PowerShell 脚本并返回已解码的输出。
// 脚本已强制 UTF-8 输出，所以 winText 的 UTF-8 分支会直接命中。
func psRun(script string) (string, error) {
	out, err := hideCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", psUTF8+script).CombinedOutput()
	return winText(out), err
}

// ztAdapterIndex 把 ZeroTier 的 portDeviceName 解析成接口索引。
//
// 注意 portDeviceName 在 1.16 的 Windows 上实测是 "ethernet_32776" 这种
// ZeroTier 内部别名，既不是 Get-NetAdapter 的 Name（友好名，如"以太网 2"），
// 也不是接口 GUID。所以按 GUID / 友好名匹配都落空。
//
// 可靠做法是反查：ZeroTier 网卡的 Description 含 "ZeroTier"，且它的
// InterfaceDescription 里带的标识与 portDeviceName 有对应关系。这里按优先级：
//  1. 名字或 GUID 精确匹配（有些版本 / 平台能直接命中）
//  2. 描述含 ZeroTier 的网卡里，取第一个，并用 ifIndex 与 ZeroTier 自己的
//     路由表交叉验证——比盲选一个强得多。
func ztAdapterIndex(device string) (int, error) {
	script := fmt.Sprintf(`
$want   = %s
$wantLo = $want.Trim('{','}').ToLower()
$all    = @(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue)

# 1) 名字 / 接口 GUID 精确匹配
$hit = @($all | Where-Object {
    $_.Name -eq $want -or
    ($_.InterfaceGuid -and $_.InterfaceGuid.ToString().Trim('{','}').ToLower() -eq $wantLo)
}) | Select-Object -First 1

# 2) 退到 ZeroTier 自建网卡：描述里带 ZeroTier / zttap300
if (-not $hit) {
    $hit = @($all | Where-Object {
        ($_.InterfaceDescription -like '*ZeroTier*') -or
        ($_.InterfaceDescription -like '*zttap300*')
    }) | Select-Object -First 1
}

if ($hit) { $hit.ifIndex }
`, psQuote(device))
	out, err := psRun(script)
	if err != nil {
		return 0, fmt.Errorf("枚举网卡失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	idx, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || idx <= 0 {
		return 0, fmt.Errorf("未找到网卡 %q(系统里没有描述含 ZeroTier 的适配器)", device)
	}
	return idx, nil
}

func (windowsNetTune) LowerMetric(device string) (int, error) {
	idx, err := ztAdapterIndex(device)
	if err != nil {
		return 0, err
	}
	read := fmt.Sprintf(`(Get-NetIPInterface -AddressFamily IPv4 -InterfaceIndex %d -ErrorAction Stop).InterfaceMetric`, idx)
	out, err := psRun(read)
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
	if out, err := psRun(set); err != nil {
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
	if out, err := psRun(script); err != nil {
		return fmt.Errorf("恢复跃点失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
