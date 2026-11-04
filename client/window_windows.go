//go:build windows

package main

// window_windows.go:原生 WebView2 窗口(Win10/11 系统自带运行时),关窗即退出
// 用 github.com/jchv/go-webview2(纯 Go 绑定,无 C++ 工具链要求)

import (
	"fmt"

	"github.com/jchv/go-webview2"
)

func runWindow(url string) error {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "桥联 · 联机大厅",
			Width:  980,
			Height: 760,
			Center: true,
			// 必须显式指定资源 ID：go-webview2 在 IconId==0 时加载的是系统默认
			// 图标(IDI_APPLICATION)，内嵌的 icon_windows_amd64.syso 不会生效。
			// syso 里 RT_GROUP_ICON 的资源名是 1，与 macOS 用同一份 icon.png 生成。
			IconId: 1,
		},
	})
	if w == nil {
		return fmt.Errorf("WebView2 创建失败(需要 Win10+ 或安装 WebView2 运行时)")
	}
	defer w.Destroy()
	w.SetSize(980, 760, webview2.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
