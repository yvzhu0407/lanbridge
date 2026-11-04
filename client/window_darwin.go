//go:build darwin

package main

// window_darwin.go:原生 WebView 窗口(WKWebView),关窗即退出

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>

static void setAppIcon(const void* data, int len) {
	@autoreleasepool {
		NSData* nsData = [NSData dataWithBytes:data length:len];
		NSImage* img = [[NSImage alloc] initWithData:nsData];
		if (img) {
			[NSApp setApplicationIconImage:img];
		}
	}
}

static void activateAsRegularApp() {
	// 裸二进制运行时进程默认是 NSApplicationActivationPolicyProhibited
	// (无 Dock 图标、无菜单);.app bundle 由 LaunchServices 自动设为 Regular,
	// 这里兜底显式声明,两种运行方式行为一致。
	[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
	[NSApp activateIgnoringOtherApps:YES];
}
*/
import "C"

import (
	_ "embed"
	"fmt"
	"unsafe"

	"github.com/webview/webview_go"
)

//go:embed icon.png
var appIconBytes []byte

func runWindow(url string) error {
	if len(appIconBytes) > 0 {
		C.setAppIcon(unsafe.Pointer(&appIconBytes[0]), C.int(len(appIconBytes)))
	}
	C.activateAsRegularApp()
	w := webview.New(false)
	if w == nil {
		return fmt.Errorf("WebView 创建失败")
	}
	defer w.Destroy()
	w.SetTitle("桥联 · 联机大厅")
	w.SetSize(980, 760, webview.HintNone)
	w.Navigate(url)
	w.Run() // 阻塞到关窗
	return nil
}
