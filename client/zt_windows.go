//go:build windows

package main

// zt_windows.go: Windows 下把 ZeroTier(zttap300 驱动 + zerotier-one 服务二进制)
// 全部嵌入 exe,首次运行解压到 %ProgramData%\lanbridge\,自装驱动、拉起实例。
// 目标:单文件绿色运行,不依赖系统已安装 ZeroTier。

import (
	"bytes"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// gbkToUTF8 将 Windows 控制台输出的 GBK/CP936 字符串安全转码为 UTF-8,避免日志出现乱码
func gbkToUTF8(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	multiByteToWideChar := kernel32.NewProc("MultiByteToWideChar")

	r1, _, _ := multiByteToWideChar.Call(
		0, // CP_ACP
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		0,
		0,
	)
	if r1 == 0 {
		return string(b)
	}
	wchars := make([]uint16, r1)
	multiByteToWideChar.Call(
		0, // CP_ACP
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		uintptr(unsafe.Pointer(&wchars[0])),
		r1,
	)
	return string(utf16.Decode(wchars))
}

//go:embed zt-embed/zt-node.exe
var ztNodeExe []byte

//go:embed zt-embed/zttap300.inf
var ztDriverInf []byte

//go:embed zt-embed/zttap300.sys
var ztDriverSys []byte

//go:embed zt-embed/zttap300.cat
var ztDriverCat []byte

// ztRuntimeDir：ZeroTier 运行文件落地目录
func ztRuntimeDir() string {
	d := os.Getenv("ProgramData")
	if d == "" {
		d = homeDir
	}
	return filepath.Join(d, "lanbridge")
}

// isAdmin: 检查当前进程是否以管理员权限运行
func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// selfElevate: 以管理员身份重新启动自身(弹 UAC 对话框)
// 成功返回 true(当前进程应退出), 失败返回 false(继续以普通权限运行)
func selfElevate() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cwd, _ := os.Getwd()

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	cwdPtr, _ := syscall.UTF16PtrFromString(cwd)
	// 传递 --elevated 标记,防止循环提权
	argsPtr, _ := syscall.UTF16PtrFromString("--elevated")

	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		uintptr(unsafe.Pointer(cwdPtr)),
		1, // SW_SHOWNORMAL
	)
	return ret > 32
}

// ensureFirewallRules: 确保 Windows 防火墙不拦联机流量。
// 仅添加缺失的规则，不修改或删除已有同名规则。需管理员。
// 防火墙规则名。这些规则由本程序自己创建,名字带 LANBridge 前缀便于识别。
var firewallRules = [][]string{
	// 房间互 ping(ICMP 回显请求)
	{"name=LANBridge ICMP In", "protocol=icmpv4:8,any", "dir=in", "action=allow"},
	// ZeroTier 实例自身 UDP(9995)
	{"name=LANBridge ZT UDP In", "dir=in", "action=allow", "protocol=UDP", "localport=" + ztPort()},
	// 虚拟网段内的游戏流量(ZT 网卡默认被判为公用网络,入站会被拦)
	{"name=LANBridge ZT Subnet In", "dir=in", "action=allow", "remoteip=10.147.0.0/16"},
}

// legacyFirewallRules: 早期版本创建的规则名（不带 LANBridge 前缀），安装新版后清理掉，
// 避免新老规则重复放行。这些名字是历史遗留，不能改，否则清理不掉。
var legacyFirewallRules = []string{"Joel ICMP Allow", "Joel ZT UDP In", "Joel ZT Subnet In"}

func ensureFirewallRules() {
	for _, r := range firewallRules {
		name := r[0][len("name="):]
		if _, err := hideCmd("netsh", "advfirewall", "firewall", "show", "rule", "name="+name).CombinedOutput(); err == nil {
			log.Printf("[防火墙] 已存在同名规则 %s，保留原规则，请检查其配置", name)
			continue
		}
		args := append([]string{"advfirewall", "firewall", "add", "rule"}, r...)
		if out, err := hideCmd("netsh", args...).CombinedOutput(); err != nil {
			log.Printf("[防火墙] 规则 %s 添加失败: %v: %s", name, err, out)
		} else {
			log.Printf("[防火墙] 规则 %s 已生效", name)
		}
	}
	removeLegacyFirewallRules()
}

// removeLegacyFirewallRules 删除旧版本留下的同名规则,避免新老规则重复。
// 新规则已在上面创建,即使这里失败也不会影响放行。
func removeLegacyFirewallRules() {
	for _, name := range legacyFirewallRules {
		if _, err := hideCmd("netsh", "advfirewall", "firewall", "show", "rule", "name="+name).CombinedOutput(); err != nil {
			continue // 不存在,无需处理
		}
		if out, err := hideCmd("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name).CombinedOutput(); err != nil {
			log.Printf("[防火墙] 清理旧规则 %s 失败: %v: %s", name, err, out)
		} else {
			log.Printf("[防火墙] 已清理旧版规则 %s", name)
		}
	}
}

// platformDiagnose: Windows 特有的诊断补充分(驱动 + 防火墙;公共部分见 main.go diagnoseZTCommon)
func platformDiagnose() {
	// TAP 驱动是否已装
	if out, err := hideCmd("pnputil", "/enum-drivers").CombinedOutput(); err == nil {
		found := false
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "zttap300") {
				found = true
				log.Printf("[诊断] TAP 驱动已安装(%s)", strings.TrimSpace(line))
				break
			}
		}
		if !found {
			log.Print("[诊断] TAP 驱动未找到(pnputil 枚举里没有 zttap300):网卡将无法创建,重启本程序触发重装")
		}
	} else {
		log.Printf("[诊断] 无法枚举驱动(pnputil 失败,可能非管理员): %v", err)
	}
	// 防火墙规则是否生效
	if out, err := hideCmd("netsh", "advfirewall", "firewall", "show", "rule", "name=LANBridge ZT Subnet In").CombinedOutput(); err == nil {
		if strings.Contains(string(out), "10.147.0.0/16") {
			log.Print("[诊断] 防火墙: 虚拟网段入站规则存在")
		} else {
			log.Print("[诊断] 防火墙: 虚拟网段入站规则缺失——你能 ping 出去但对方连不进你,重启本程序以管理员身份运行可重建规则")
		}
	}
	// 防火墙总开关(全关则规则无意义)
	if out, err := hideCmd("netsh", "advfirewall", "show", "allprofiles", "state").CombinedOutput(); err == nil {
		log.Printf("[诊断] 防火墙配置档状态:\n%s", out)
	}
}

// checkElevation: 在 main 中调用,如果需要管理员权限则尝试提权
// 返回 true 表示已触发提权、当前进程应 os.Exit(0)
func checkElevation() bool {
	// 已经是管理员,不用提权
	if isAdmin() {
		log.Println("[权限] 已以管理员身份运行")
		ensureFirewallRules()
		return false
	}
	// 如果带了 --elevated 标记说明提权失败或被取消,不再循环
	for _, arg := range os.Args[1:] {
		if strings.TrimLeft(arg, "-") == "elevated" {
			log.Println("[权限] 提权已尝试但未成功,继续以普通权限运行")
			return false
		}
	}
	// 尝试提权
	log.Println("[权限] 需要管理员权限安装虚拟网卡驱动,正在请求提权…")
	if selfElevate() {
		// 提权成功,新进程已启动,当前进程退出
		return true
	}
	log.Println("[权限] 用户取消了提权,继续以普通权限运行(虚拟网卡可能无法创建)")
	return false
}

// ensureZTRuntime：解压嵌入式 ZeroTier 文件(幂等)
func ensureZTRuntime() error {
	dir := ztRuntimeDir()
	if err := os.MkdirAll(filepath.Join(dir, "driver"), 0o755); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{"zt-node.exe", ztNodeExe},
		{filepath.Join("driver", "zttap300.inf"), ztDriverInf},
		{filepath.Join("driver", "zttap300.sys"), ztDriverSys},
		{filepath.Join("driver", "zttap300.cat"), ztDriverCat},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		current, err := os.ReadFile(p)
		if err == nil && bytes.Equal(current, f.data) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取 %s: %w", f.name, err)
		}
		if err := os.WriteFile(p, f.data, 0o755); err != nil {
			return fmt.Errorf("解压 %s: %w", f.name, err)
		}
	}
	return nil
}

// isDriverInstalled: 检查系统中是否已导入 zttap300 虚拟网卡驱动
func isDriverInstalled() bool {
	out, err := hideCmd("pnputil", "/enum-drivers").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "zttap300")
}

// installDriver：pnputil 装 TAP 驱动(已装则无操作,pnputil 自身幂等)
func installDriver() error {
	// 若驱动已在系统中注册，无需反复执行安装命令
	if isDriverInstalled() {
		return nil
	}
	inf := filepath.Join(ztRuntimeDir(), "driver", "zttap300.inf")
	out, err := hideCmd("pnputil", "/add-driver", inf, "/install").CombinedOutput()
	outStr := gbkToUTF8(out)
	if err != nil {
		return fmt.Errorf("驱动安装提示: %v: %s", err, strings.TrimSpace(outStr))
	}
	return nil
}
