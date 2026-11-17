package main

// quit.go：关窗退出。
//
// 语义(和用户确认过)：关窗口 = 离开房间 = 放弃已经建立的虚拟网络。
// 但直接关窗会留下"服务端以为你还在房间里、本地还占着虚拟网卡"的半开状态，
// 下次要重启才能干净。所以在房间里关窗时先让界面问一句，确认后按固定顺序清理：
//
//	停自愈 → 通知服务端离开 → 本地离开网络 → 恢复网卡跃点
//
// 不在房间里则直接关，不打扰。
//
// 两道兜底保证窗口永远关得掉：
//   - 界面 3 秒内没有 ack(页面没加载/脚本没注册)，直接走退出流程；
//   - 清理的每一步都有超时，服务端连不上也不影响退出。
//
// 线程模型：
//   closeGuard 只由原生关窗回调(UI 线程)调用，内部只碰锁，不碰 WebView；
//   真正的询问由 watchCloseRequests 在普通 goroutine 里发出，
//   注入脚本经 Dispatch 回 UI 线程，强制关窗走 Terminate/PostMessage，都线程安全。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// 界面确认框的兜底时间(变量，便于测试缩短)
	quitPromptFallback = 3 * time.Second
	// 退出清理单步超时
	quitStepTimeout = 2 * time.Second
	// 关窗请求的轮询间隔(只转发一个标志位，开销可忽略)
	quitPollInterval = 120 * time.Millisecond
)

var (
	// evalInUI 把脚本注入页面，由各平台 runWindow 设置
	evalInUI func(js string)
	// forceCloseWindow 强制关闭原生窗口(跳过拦截)，由各平台 runWindow 设置
	forceCloseWindow func()

	quitMu        sync.Mutex
	closePending  bool        // 原生层收到关窗请求，等 watchCloseRequests 取走
	quitPrompting bool        // 已请求界面确认，等用户选择
	quitAcked     bool        // 界面已经弹出确认框
	quitForced    bool        // 已决定退出
	quitBusy      bool        // 正在清理，拒绝新的关闭请求以免打断
	quitTimer     *time.Timer // 界面无响应的兜底计时
)

// inRoom 报告当前是否在房间里。以最近一次房间接口的响应为准
// (noteRoomFromResponse 会同步)，不依赖界面轮询的时机。
func inRoom() bool {
	id, _ := roomLogSnapshot()
	return id > 0
}

// closeGuard 由原生窗口的关闭流程调用，返回 true 表示允许关闭。
// 在房间里时拦下，并置位 closePending 交给 watchCloseRequests 去询问界面。
func closeGuard() bool {
	quitMu.Lock()
	defer quitMu.Unlock()
	if quitBusy {
		// 清理进行中，重复点关闭不能打断(否则服务端仍以为你在房间)
		return false
	}
	if quitForced || !inRoom() {
		return true
	}
	closePending = true
	return false
}

// nativeCloseBlocked 是给"原生层自己轮询"的平台(macOS)用的同一套判断：
// 返回 true 表示应拦下本次关闭。关窗请求由平台自己取走。
func nativeCloseBlocked() bool {
	quitMu.Lock()
	defer quitMu.Unlock()
	if quitBusy {
		return true
	}
	return !quitForced && inRoom()
}

// watchCloseRequests 把原生层的关窗请求转成界面上的确认框。
// 只给"能在关窗回调里直接调 Go"的平台用(Windows 子类过程)；
// macOS 的 windowShouldClose: 是同步 C 调用，走平台自己的轮询 + promptClose。
func watchCloseRequests() {
	for {
		time.Sleep(quitPollInterval)
		quitMu.Lock()
		pending := closePending
		closePending = false
		quitMu.Unlock()
		if pending {
			promptClose()
		}
	}
}

func promptClose() {
	quitMu.Lock()
	if quitForced || quitBusy || quitPrompting {
		quitMu.Unlock()
		return
	}
	quitPrompting = true
	quitAcked = false
	quitTimer = time.AfterFunc(quitPromptFallback, func() {
		quitMu.Lock()
		waiting := !quitAcked && !quitForced
		quitMu.Unlock()
		if waiting {
			// 页面没响应我们的询问(加载失败/脚本未注册)，不能把窗口卡死
			LogDev("[退出] 界面未响应关窗确认，直接退出")
			requestQuit()
		}
	})
	quitMu.Unlock()

	LogDev("[退出] 正在房间里，请求界面确认")
	if evalInUI != nil {
		evalInUI(`window.dispatchEvent(new CustomEvent('lanbridge:close-request'))`)
	}
}

// ackQuitPrompt 界面弹出了确认框，停掉兜底计时。
func ackQuitPrompt() {
	quitMu.Lock()
	defer quitMu.Unlock()
	quitAcked = true
	if quitTimer != nil {
		quitTimer.Stop()
		quitTimer = nil
	}
}

// cancelQuitPrompt 用户点了"取消"，下次关窗重新询问。
func cancelQuitPrompt() {
	quitMu.Lock()
	defer quitMu.Unlock()
	quitPrompting = false
	quitAcked = false
	if quitTimer != nil {
		quitTimer.Stop()
		quitTimer = nil
	}
}

// requestQuit 确认要退出了：清理本地状态，然后强制关闭窗口。
// 只执行一次，重复调用(兜底和界面同时到达)直接返回。
func requestQuit() {
	quitMu.Lock()
	if quitForced {
		quitMu.Unlock()
		return
	}
	quitForced = true
	quitBusy = true
	if quitTimer != nil {
		quitTimer.Stop()
		quitTimer = nil
	}
	quitMu.Unlock()

	roomNetworkMu.Lock()
	exitCleanup()
	roomNetworkMu.Unlock()

	quitMu.Lock()
	quitBusy = false
	quitMu.Unlock()

	if forceCloseWindow != nil {
		forceCloseWindow()
	}
}

// 测试缝：退出清理对各外部依赖的调用，便于在不碰真实 ZeroTier 的前提下验证顺序。
var (
	quitNotifyLeave = notifyServerLeave
	quitLeaveNets   = leaveAllNetworks
	quitRestoreNet  = restoreNetwork
)

// notifyServerLeave 通知服务端离开房间。失败只记日志：
// 服务端对空房间有回收，不会因此留下僵尸房间。
func notifyServerLeave() {
	tok := loadToken()
	if tok == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), quitStepTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverBase+"/api/room/leave", strings.NewReader("{}"))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		LogDev("[退出] 通知服务端离开失败(忽略): %v", err)
		return
	}
	resp.Body.Close()
}

// leaveAllNetworks 让本地 ZeroTier 离开所有已加入的网络。
func leaveAllNetworks() {
	out, _ := ztRun("-j", "listnetworks")
	if out == "" {
		return
	}
	var raw []map[string]any
	if json.Unmarshal([]byte(out), &raw) != nil {
		return
	}
	for _, m := range raw {
		if id, _ := m["id"].(string); id != "" {
			_, _ = ztRun("leave", id)
		}
	}
}

// exitCleanup 把占用的东西还回去。
//
// 顺序不能反：先停自愈，否则它在下面 leave 之后又把网络 join 回来；
// 服务端 leave 失败也无所谓，服务端对空房间有自己的回收。
func exitCleanup() {
	clearDesiredNet()
	quitNotifyLeave()
	quitLeaveNets()
	quitRestoreNet()
	LogDev("[退出] 本地清理完成")
}

// ---------- 本地接口，供界面调用 ----------

func handleAppQuit(w http.ResponseWriter, r *http.Request) {
	okJSON(w, map[string]any{"quitting": true})
	// 先回响应再退出：界面端的 fetch 才拿得到结果
	go requestQuit()
}

func handleAppQuitAck(w http.ResponseWriter, r *http.Request) {
	ackQuitPrompt()
	okJSON(w, map[string]any{"ok": true})
}

func handleAppQuitCancel(w http.ResponseWriter, r *http.Request) {
	cancelQuitPrompt()
	okJSON(w, map[string]any{"ok": true})
}
