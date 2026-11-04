//go:build darwin

package main

// zt_darwin.go:macOS 下把 ZeroTier(zerotier-one universal binary + MacEthernetTapAgent)
// 嵌入客户端,解压到 ~/Library/Application Support/lanbridge/ 后拉起独立实例。
// 不依赖宿主机是否安装 ZeroTier.app。

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed zt-embed-mac/zt-node
var ztNodeBin []byte

//go:embed zt-embed-mac/MacEthernetTapAgent
var ztTapAgent []byte

// ztRuntimeDir:ZeroTier 运行文件落地目录
func ztRuntimeDir() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return homeDir
	}
	return filepath.Join(d, "Library", "Application Support", "lanbridge")
}

// ensureZTRuntime: 解压嵌入式 ZeroTier 文件并校验内容与执行权限。
func ensureZTRuntime() error {
	dir := ztRuntimeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"zt-node", ztNodeBin, 0o755},
		{"MacEthernetTapAgent", ztTapAgent, 0o755},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		current, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取 %s: %w", f.name, err)
		}
		if !bytes.Equal(current, f.data) {
			if err := os.WriteFile(p, f.data, f.mode); err != nil {
				return fmt.Errorf("解压 %s: %w", f.name, err)
			}
		}
		if err := os.Chmod(p, f.mode); err != nil {
			return fmt.Errorf("设置 %s 执行权限: %w", f.name, err)
		}
	}
	return nil
}

// installDriver:macOS 无需驱动(用 feth 虚拟网卡)
func installDriver() error { return nil }

// platformDiagnose: macOS 无驱动安装/防火墙规则环节,无平台特有诊断
func platformDiagnose() {}
