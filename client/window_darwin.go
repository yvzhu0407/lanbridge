//go:build darwin

package main

// window_darwin.go:原生 WebView 窗口(WKWebView)，关窗即退出。
//
// 关窗拦截:webview 库自带的 WebviewNSWindowDelegate 只注册了
// windowWillClose:(此时关窗已不可逆)，没有 windowShouldClose:，拦不住。
// 我们在窗口建好后换成自己的 NSWindowDelegate 子类，只覆写
// windowShouldClose: 和 windowWillClose:，其余选择子通过
// forwardingTargetForSelector: 原样转发给原 delegate，
// webview 库自己的关窗收尾(停 run loop)不受影响。
//
// 数据如何回传:windowShouldClose: 里只置一个"有人点了关闭"的标志位，
// 由 Go 侧的轮询循环取走(120ms 一次，只有一次 C 调用)，不走 //export 回调
// —— cgo 的 //export 会把前导块复制进两个编译单元，Objective-C 类定义
// 会因此重复、链接失败。
//
// 注意:windowShouldClose: 返回 NO 只是取消本次关闭，不会停 run loop，
// 所以"确认退出"必须自己停(webview_terminate)。

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#include <string.h>

static id g_origWindowDelegate = nil;
static int g_blockClose = 0;     // 在房间里：拦下关闭
static int g_closeRequests = 0;  // 有人点了关闭，等 Go 侧取走

static void lanbridgeSetBlockClose(int v) { g_blockClose = v; }

// 取走并清零关窗请求；返回非 0 表示窗口刚被要求关闭过。
static int lanbridgeTakeCloseRequest(void) {
	int n = g_closeRequests;
	g_closeRequests = 0;
	return n;
}

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

@interface LanbridgeWindowDelegate : NSObject <NSWindowDelegate>
@end

// Apple Event 处理器：链接打开时由系统回调
@interface LanbridgeURLEventHandler : NSObject
@end

// 声明在前：处理器的 @implementation 要按名字调用
static void lanbridgeHandleURL(NSAppleEventDescriptor* event);

@implementation LanbridgeWindowDelegate
// 其余 NSWindowDelegate 方法(尺寸变化、全屏、缩放等)一律交回原 delegate
- (id)forwardingTargetForSelector:(SEL)aSelector {
	return g_origWindowDelegate;
}
- (BOOL)windowShouldClose:(NSWindow*)sender {
	if (!g_blockClose) {
		return YES;
	}
	g_closeRequests = 1;
	return NO;
}
- (void)windowWillClose:(NSNotification*)notification {
	[g_origWindowDelegate windowWillClose:notification];
}
@end

// 把窗口的 delegate 换成会先询问的版本，原 delegate 留作转发目标。
static void installCloseGuard(void* window) {
	NSWindow* w = (NSWindow*)window;
	if (!w) {
		return;
	}
	id current = [w delegate];
	if (!current || [current isKindOfClass:[LanbridgeWindowDelegate class]]) {
		return;
	}
	g_origWindowDelegate = current;
	[w setDelegate:[[LanbridgeWindowDelegate alloc] init]];
}

// ---------- 邀请链接(lanbridge://join/N) ----------
//
// macOS 不会把链接塞进正在运行的实例的命令行，而是发 Apple Event(kAEGetURL)。
// 处理器只能用 Objective-C 实现，这里把拿到的 URL 存起来，
// 由 Go 侧的轮询取走(同样的理由：事件回调里不能进 Go)。

static char g_pendingURL[2048];
static int g_hasPendingURL = 0;

static void lanbridgeHandleURL(NSAppleEventDescriptor* event) {
	NSAppleEventDescriptor* d = [event paramDescriptorForKeyword:keyDirectObject];
	NSString* s = [d stringValue];
	if (!s) {
		return;
	}
	const char* utf8 = [s UTF8String];
	if (!utf8) {
		return;
	}
	strncpy(g_pendingURL, utf8, sizeof(g_pendingURL) - 1);
	g_pendingURL[sizeof(g_pendingURL) - 1] = '\0';
	g_hasPendingURL = 1;
}

@implementation LanbridgeURLEventHandler
- (void)handleGetURLEvent:(NSAppleEventDescriptor*)event withReplyEvent:(NSAppleEventDescriptor*)replyEvent {
	lanbridgeHandleURL(event);
}
@end

// 返回非 0 表示取到一个链接，内容写在 out 里(调用方提供 2048 字节缓冲)。
static int lanbridgeTakeURL(char* out, int cap) {
	if (!g_hasPendingURL) {
		return 0;
	}
	g_hasPendingURL = 0;
	strncpy(out, g_pendingURL, cap - 1);
	out[cap - 1] = '\0';
	return 1;
}

static void installURLEventHandler(void) {
	[[NSAppleEventManager sharedAppleEventManager]
		setEventHandler:[[LanbridgeURLEventHandler alloc] init]
		andSelector:@selector(handleGetURLEvent:withReplyEvent:)
		forEventClass:kInternetEventClass
		andEventID:kAEGetURL];
}
*/
import "C"

import (
	"bytes"
	_ "embed"
	"fmt"
	"time"
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
	w.SetSize(820, 620, webview.HintNone)
	w.Navigate(url)

	// Eval 必须在主线程：调用方可能来自轮询 goroutine，一律经 Dispatch 排队
	evalInUI = func(js string) { w.Dispatch(func() { w.Eval(js) }) }
	// windowShouldClose: 已经拦下了关闭；这里停 run loop 让窗口真正关掉，
	// 随后的 w.Destroy() 负责释放窗口。
	forceCloseWindow = func() { w.Dispatch(func() { w.Terminate() }) }

	// 窗口创建完再换 delegate；此时 main 线程就在当前 goroutine 上
	C.installCloseGuard(w.Window())
	C.installURLEventHandler()

	// 同步"是否该拦"标志位 + 取走关窗请求 + 取走系统转来的邀请链接。
	// 标志位等放在这里同步而不是关窗时现问 Go：windowShouldClose: 是同步的
	// C 调用，不能进 Go(会触发 cgo 的线程检查)。
	go func() {
		buf := make([]byte, 2048)
		for {
			time.Sleep(quitPollInterval)
			block := 0
			if nativeCloseBlocked() {
				block = 1
			}
			C.lanbridgeSetBlockClose(C.int(block))
			if C.lanbridgeTakeCloseRequest() != 0 {
				promptClose()
			}
			if C.lanbridgeTakeURL((*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))) != 0 {
				link := string(buf[:bytes.IndexByte(buf, 0)])
				if id, ok := parseInvite(link); ok {
					setPendingInvite(id)
				}
			}
		}
	}()

	w.Run() // 阻塞到关窗
	return nil
}
