//go:build windows

package main

// window_windows.go:原生 WebView2 窗口(Win10/11 系统自带运行时)，关窗即退出
// 用 github.com/jchv/go-webview2(纯 Go 绑定，无 C++ 工具链要求)
//
// 关窗拦截:go-webview2 的 wndproc 收到 WM_CLOSE 就直接 DestroyWindow，
// 没有留钩子。我们给窗口挂一个子类过程(SetWindowLongPtrW + GWLP_WNDPROC)，
// 在 WM_CLOSE 时先问一句，不关就把消息吃掉(返回 0)，
// 其余消息一律 CallWindowProcW 交回原过程，窗口行为不变。
//
// 注意:吃掉 WM_CLOSE 只是取消本次关闭，窗口还在；
// "确认退出"靠重新投递一次 WM_CLOSE，此时 closeGuard 已放行。

import (
	"fmt"
	"syscall"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const (
	wmClose     = 0x0010
	gwlpWndProc = ^uintptr(3) // -4
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procSetWindowLng = user32.NewProc("SetWindowLongPtrW")
	procCallWindowPr = user32.NewProc("CallWindowProcW")
	procDefWindowPr  = user32.NewProc("DefWindowProcW")
	procPostMessage  = user32.NewProc("PostMessageW")

	oldWndProc uintptr // 原窗口过程，未处理的消息转交回它
)

// wndProcCloseSink 是挂在窗口上的子类过程。
func wndProcCloseSink(hwnd, msg, wp, lp uintptr) uintptr {
	if msg == wmClose && !closeGuard() {
		return 0 // 拦下本次关闭，等界面确认
	}
	if oldWndProc == 0 {
		// 子类化失败(理论上不会发生)：退回默认过程，绝不能把窗口弄成关不掉
		r, _, _ := procDefWindowPr.Call(hwnd, msg, wp, lp)
		return r
	}
	r, _, _ := procCallWindowPr.Call(oldWndProc, hwnd, msg, wp, lp)
	return r
}

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
	w.SetSize(820, 620, webview2.HintNone)
	w.Navigate(url)

	hwnd := uintptr(w.Window())
	oldWndProc, _, _ = procSetWindowLng.Call(hwnd, gwlpWndProc, windows.NewCallback(wndProcCloseSink))

	evalInUI = func(js string) {
		// WebView2 的脚本执行必须在 UI 线程上，Dispatch 负责排队
		w.Dispatch(func() { w.Eval(js) })
	}
	forceCloseWindow = func() {
		_, _, _ = procPostMessage.Call(hwnd, wmClose, 0, 0)
	}
	go watchCloseRequests()

	w.Run()
	return nil
}
