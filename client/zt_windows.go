//go:build windows

package main

// zt_windows.go: Windows 下把 ZeroTier(zttap300 驱动 + zerotier-one 服务二进制)
// 全部嵌入 exe,首次运行解压到 %ProgramData%\lanbridge\,自装驱动、拉起实例。
// 目标:单文件绿色运行,不依赖系统已安装 ZeroTier。

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// codePageToUTF8 把指定代码页的字节转成 UTF-8。
// cp 传 0 表示 CP_ACP(系统 ANSI 码页,中文 Windows 上是 GBK/CP936)。
func codePageToUTF8(b []byte, cp uintptr) string {
	if len(b) == 0 {
		return ""
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	multiByteToWideChar := kernel32.NewProc("MultiByteToWideChar")

	r1, _, _ := multiByteToWideChar.Call(
		cp,
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		0,
		0,
	)
	if r1 == 0 {
		// 转换失败(含非法字节序列)。绝不能把原始字节当 string 返回:
		// Go 按 UTF-8 解释,中文 Windows 的 GBK 字节会变成乱码写进日志。
		return "[" + strconv.Itoa(len(b)) + " 字节无法解码]"
	}
	wchars := make([]uint16, r1)
	multiByteToWideChar.Call(
		cp,
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		uintptr(unsafe.Pointer(&wchars[0])),
		r1,
	)
	return string(utf16.Decode(wchars))
}

// gbkToUTF8 保留原名:系统 ANSI 码页 → UTF-8。
// 中文 Windows 上 netsh / pnputil / taskkill 的输出都是这个编码。
func gbkToUTF8(b []byte) string { return codePageToUTF8(b, 0) }

// winText 是外部命令输出的通用解码器。
//   - PowerShell 脚本已强制 UTF-8 输出,合法 UTF-8 原样采用;
//   - netsh / pnputil / taskkill 输出 ANSI 码页,按 CP_ACP 转换。
//
// 先判 UTF-8 再回退,是因为两类命令混在一起用,而中文 GBK 文本极少同时是合法 UTF-8。
func winText(b []byte) string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return gbkToUTF8(b)
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
			LogDev("[防火墙] 已存在同名规则 %s，保留原规则，请检查其配置", name)
			continue
		}
		args := append([]string{"advfirewall", "firewall", "add", "rule"}, r...)
		if out, err := hideCmd("netsh", args...).CombinedOutput(); err != nil {
			LogDev("[防火墙] 规则 %s 添加失败: %v: %s", name, err, winText(out))
		} else {
			LogDev("[防火墙] 规则 %s 已生效", name)
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
			LogDev("[防火墙] 清理旧规则 %s 失败: %v: %s", name, err, winText(out))
		} else {
			LogDev("[防火墙] 已清理旧版规则 %s", name)
		}
	}
}

// platformDiagnose: Windows 特有的诊断补充分(驱动 + 防火墙;公共部分见 main.go diagnoseZTCommon)
func platformDiagnose() {
	// TAP 驱动是否已装
	if out, err := hideCmd("pnputil", "/enum-drivers").CombinedOutput(); err == nil {
		found := false
		text := winText(out)
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "zttap300") {
				found = true
				LogDev("[诊断] TAP 驱动已安装(%s)", strings.TrimSpace(line))
				break
			}
		}
		if !found {
			LogDev("[诊断] TAP 驱动未找到(pnputil 枚举里没有 zttap300):网卡将无法创建,重启本程序触发重装")
		}
	} else {
		LogDev("[诊断] 无法枚举驱动(pnputil 失败,可能非管理员): %v", err)
	}
	// 防火墙规则是否生效
	if out, err := hideCmd("netsh", "advfirewall", "firewall", "show", "rule", "name=LANBridge ZT Subnet In").CombinedOutput(); err == nil {
		if strings.Contains(winText(out), "10.147.0.0/16") {
			LogDev("[诊断] 防火墙: 虚拟网段入站规则存在")
		} else {
			LogDev("[诊断] 防火墙: 虚拟网段入站规则缺失——你能 ping 出去但对方连不进你,重启本程序以管理员身份运行可重建规则")
		}
	}
	// 防火墙总开关(全关则规则无意义)
	if out, err := hideCmd("netsh", "advfirewall", "show", "allprofiles", "state").CombinedOutput(); err == nil {
		LogDev("[诊断] 防火墙配置档状态:\n%s", winText(out))
	}
}

// checkElevation: 在 main 中调用,如果需要管理员权限则尝试提权
// 返回 true 表示已触发提权、当前进程应 os.Exit(0)
func checkElevation() bool {
	// 已经是管理员,不用提权
	if isAdmin() {
		LogDev("[权限] 已以管理员身份运行")
		ensureFirewallRules()
		return false
	}
	// 如果带了 --elevated 标记说明提权失败或被取消,不再循环
	for _, arg := range os.Args[1:] {
		if strings.TrimLeft(arg, "-") == "elevated" {
			LogDev("[权限] 提权已尝试但未成功,继续以普通权限运行")
			return false
		}
	}
	// 尝试提权
	LogDev("[权限] 需要管理员权限安装虚拟网卡驱动,正在请求提权…")
	if selfElevate() {
		// 提权成功,新进程已启动,当前进程退出
		return true
	}
	LogDev("[权限] 用户取消了提权,继续以普通权限运行(虚拟网卡可能无法创建)")
	return false
}

// ensureZTRuntime：解压嵌入式 ZeroTier 文件(幂等)
//
// 驱动三件套必须落在 **ZeroTier 家目录**,不是 ProgramData\lanbridge\driver\。
// 依据 ZeroTier 1.16.2 的 osdep/EthernetTap.cpp:new WindowsEthernetTap(homePath,...)
// 把 _pathToHelpers 设为 homePath,INF 路径就是 homePath\zttap300.inf(可执行文件里
// 能直接看到这个拼接常量)。放错目录时 SetupDiGetINFClassA() 读不到 INF,
// 网卡建不出来,但 join 仍然返回成功——故障非常隐蔽,见 port_error_log.txt。
func ensureZTRuntime() error {
	binDir := ztRuntimeDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	// zt-node.exe 留在运行目录;驱动直接铺到家目录
	if err := writeIfChanged(filepath.Join(binDir, "zt-node.exe"), ztNodeExe); err != nil {
		return err
	}
	return ensureDriverFiles()
}

// writeIfChanged:内容一致就不重写,避免每次启动都碰文件
func writeIfChanged(p string, data []byte) error {
	current, err := os.ReadFile(p)
	if err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取 %s: %w", filepath.Base(p), err)
	}
	if err := os.WriteFile(p, data, 0o755); err != nil {
		return fmt.Errorf("解压 %s: %w", filepath.Base(p), err)
	}
	return nil
}

// ensureDriverFiles: 只保证驱动三件套在家目录里,不碰 zt-node 实例。
// 每次启动都跑(幂等,内容一致就不写),这样从旧版本升级上来时
// ——实例已在跑、ensureZTRuntime 被跳过——也能把驱动补到位。
func ensureDriverFiles() error {
	home := ztHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("创建 ZeroTier 家目录: %w", err)
	}
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"zttap300.inf", ztDriverInf},
		{"zttap300.sys", ztDriverSys},
		{"zttap300.cat", ztDriverCat},
	} {
		if err := writeIfChanged(filepath.Join(home, f.name), f.data); err != nil {
			return err
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
	inf := filepath.Join(ztHome(), "zttap300.inf")
	out, err := hideCmd("pnputil", "/add-driver", inf, "/install").CombinedOutput()
	if err != nil {
		return fmt.Errorf("驱动安装提示: %v: %s", err, winText(out))
	}
	return nil
}
