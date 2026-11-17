package main

// quit_test.go:关窗退出的状态机。
//
// 这里只测"什么时候拦、什么时候放行、兜底会不会卡死窗口"，
// 真正的清理动作(离开房间、退网、恢复跃点)由 exitCleanup 做，
// 它在各平台的实测里验证，单测不重复跑外部命令。

import (
	"sync"
	"testing"
	"time"
)

// resetQuitState 把包级状态清干净，测试之间互不影响。
// 注意不能在 t.Cleanup 里再次调用自己(会无限递归注册)。
func resetQuitState(t *testing.T) {
	t.Helper()
	quitMu.Lock()
	if quitTimer != nil {
		quitTimer.Stop()
		quitTimer = nil
	}
	closePending = false
	quitPrompting = false
	quitAcked = false
	quitForced = false
	quitBusy = false
	quitMu.Unlock()
}

// snapshotQuitHooks 保存并恢复注入点，避免测试之间互相污染。
func snapshotQuitHooks(t *testing.T) {
	t.Helper()
	oldEval, oldForce := evalInUI, forceCloseWindow
	oldNotify, oldLeave, oldRestore := quitNotifyLeave, quitLeaveNets, quitRestoreNet
	t.Cleanup(func() {
		evalInUI, forceCloseWindow = oldEval, oldForce
		quitNotifyLeave, quitLeaveNets, quitRestoreNet = oldNotify, oldLeave, oldRestore
	})
}

func TestCloseGuardAllowsWhenNotInRoom(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	clearRoomLog()
	if !closeGuard() {
		t.Fatal("不在房间里时关窗应直接放行")
	}
	if nativeCloseBlocked() {
		t.Fatal("不在房间里时原生层不该拦截")
	}
}

func TestCloseGuardBlocksInRoom(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	setRoomLog(42, "net-a")
	defer clearRoomLog()

	if closeGuard() {
		t.Fatal("在房间里时关窗应被拦下")
	}
	if !nativeCloseBlocked() {
		t.Fatal("在房间里时原生层应拦截")
	}

	quitMu.Lock()
	pending := closePending
	quitMu.Unlock()
	if !pending {
		t.Fatal("拦下关闭后应置位 closePending，交给界面询问")
	}
}

func TestPromptCloseInjectsAndAckStopsFallback(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	setRoomLog(7, "net-b")
	defer clearRoomLog()

	var mu sync.Mutex
	var injected int
	evalInUI = func(string) {
		mu.Lock()
		injected++
		mu.Unlock()
	}

	promptClose()
	mu.Lock()
	n := injected
	mu.Unlock()
	if n != 1 {
		t.Fatalf("应注入一次关窗询问，实际 %d 次", n)
	}

	quitMu.Lock()
	prompting := quitPrompting
	hasTimer := quitTimer != nil
	quitMu.Unlock()
	if !prompting || !hasTimer {
		t.Fatal("询问期间应处于 prompting 状态且带兜底计时")
	}

	ackQuitPrompt()
	quitMu.Lock()
	acked, timer := quitAcked, quitTimer
	quitMu.Unlock()
	if !acked || timer != nil {
		t.Fatal("界面 ack 之后应停掉兜底计时")
	}

	// 界面已经接管，重复点击关闭不能再注入第二个询问框
	promptClose()
	mu.Lock()
	n = injected
	mu.Unlock()
	if n != 1 {
		t.Fatalf("询问期间重复关窗不应重复注入，实际 %d 次", n)
	}
}

func TestPromptCloseFallbackFiresWithoutAck(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	setRoomLog(8, "net-c")
	defer clearRoomLog()

	closed := make(chan struct{}, 1)
	forceCloseWindow = func() { closed <- struct{}{} }
	evalInUI = func(string) {} // 模拟页面没注册监听：不 ack

	// 兜底时间在 promptClose 里写死 3 秒，这里临时缩短以保持测试快速
	old := quitPromptFallback
	quitPromptFallback = 80 * time.Millisecond
	defer func() { quitPromptFallback = old }()

	promptClose()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("界面不 ack 时兜底应强制退出，否则窗口关不掉")
	}
}

func TestCancelPromptAllowsRetry(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	setRoomLog(9, "net-d")
	defer clearRoomLog()

	var mu sync.Mutex
	var injected int
	evalInUI = func(string) {
		mu.Lock()
		injected++
		mu.Unlock()
	}

	promptClose()
	ackQuitPrompt()
	cancelQuitPrompt()

	quitMu.Lock()
	prompting := quitPrompting
	quitMu.Unlock()
	if prompting {
		t.Fatal("取消后应退出 prompting 状态")
	}

	// 取消后再次关窗，应该重新询问
	promptClose()
	mu.Lock()
	n := injected
	mu.Unlock()
	if n != 2 {
		t.Fatalf("取消后应能再次询问，注入次数 %d", n)
	}
}

func TestRequestQuitClosesOnceAndUnblocks(t *testing.T) {
	snapshotQuitHooks(t)
	resetQuitState(t)
	setRoomLog(10, "net-e")
	defer clearRoomLog()

	var mu sync.Mutex
	var closes int
	forceCloseWindow = func() {
		mu.Lock()
		closes++
		mu.Unlock()
	}

	// 清理阶段的外部动作全部替换掉，只验证状态机和调用顺序
	var order []string
	quitNotifyLeave = func() { order = append(order, "server-leave") }
	quitLeaveNets = func() { order = append(order, "leave-nets") }
	quitRestoreNet = func() { order = append(order, "restore") }

	requestQuit()
	requestQuit() // 重复调用不应关两次

	mu.Lock()
	n := closes
	mu.Unlock()
	if n != 1 {
		t.Fatalf("关窗只应执行一次，实际 %d 次", n)
	}

	quitMu.Lock()
	forced := quitForced
	quitMu.Unlock()
	if !forced {
		t.Fatal("确认退出后 quitForced 应为 true")
	}
	if !closeGuard() {
		t.Fatal("确认退出后原生关窗应放行")
	}
	if nativeCloseBlocked() {
		t.Fatal("确认退出后原生层不该再拦截")
	}

	want := []string{"server-leave", "leave-nets", "restore"}
	if len(order) != len(want) {
		t.Fatalf("清理步骤数量不对: %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("清理顺序不对: 期望 %v, 实际 %v", want, order)
		}
	}
}
