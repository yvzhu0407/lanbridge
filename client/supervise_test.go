package main

import (
	"testing"
	"time"
)

func resetSupervisor() {
	supMu.Lock()
	desiredNet = ""
	sup = recoverState{}
	notReadyAt = time.Time{}
	lastAction = time.Time{}
	lastRestart = time.Time{}
	supMu.Unlock()
}

// stubSup 把自愈依赖全部换成测试替身,默认"实例在、无网络、planet 可达、CLI 空操作",
// 保证测试绝不跑真实 CLI、不碰真实进程。
func stubSup(t *testing.T) {
	t.Helper()
	od, oe, orun, ons, op, ord := supDaemonListening, supEnsureReady, supRun, supNetStates, supPlanetState, supRestartDaemon
	t.Cleanup(func() {
		supDaemonListening, supEnsureReady, supRun, supNetStates, supPlanetState, supRestartDaemon = od, oe, orun, ons, op, ord
		resetSupervisor()
	})
	supDaemonListening = func() bool { return true }
	supEnsureReady = func() error { return nil }
	supRun = func(args ...string) (string, error) { return "", nil }
	supNetStates = func() []netState { return nil }
	supPlanetState = func() (bool, int) { return true, 10 }
	supRestartDaemon = func() error { return nil }
	resetSupervisor()
}

func readyNetOn(id string) netState {
	return netState{NetworkID: id, Status: "OK", Device: "feth1", IPs: []string{"10.147.1.2/24"}, Ready: true}
}

func TestPlanRecoveryDecision(t *testing.T) {
	notReady := netState{NetworkID: "n", Status: "REQUESTING_CONFIGURATION", Reason: "等待配置"}
	portErr := netState{NetworkID: "n", Status: "PORT_ERROR", PortError: 1, Reason: "建卡失败"}
	for _, tc := range []struct {
		name           string
		daemonDown     bool
		found          bool
		ns             netState
		planetOK       bool
		everConfigured bool
		stuck          time.Duration
		want           recoverAction
	}{
		{"实例没跑", true, false, netState{}, true, false, 0, actStartDaemon},
		{"网络在本地缺失", false, false, netState{}, true, false, time.Minute, actJoin},
		// join 发了却一直不落:实例卡死,短缺失先 join,久缺失才重启实例。
		{"网络久缺失重启实例", false, false, netState{}, true, false, restartDaemonHorizon + time.Second, actRestartDaemon},
		{"已就绪", false, true, readyNetOn("n"), true, true, time.Minute, actNone},
		{"宽限期内不动", false, true, notReady, true, false, 5 * time.Second, actWait},
		{"planet 不可达不重连", false, true, notReady, false, true, time.Minute, actWait},
		{"端口报错宽限期内不动", false, true, portErr, true, false, 5 * time.Second, actWait},
		// 建卡失败过宽限期后 leave+join 重建网卡(leave 拆掉失败端口),不再死等人工。
		{"端口报错过宽限期重进重建", false, true, portErr, true, false, time.Minute, actRejoin},
		{"长时间不就绪重进", false, true, notReady, true, true, time.Minute, actRejoin},
		// 从未拿到过配置的节点:planet 只是采样瞬间不可达,卡久了仍应强制重拉。
		{"从未配置久卡即使planet抖动也重进", false, true, notReady, false, false, notConfiguredHorizon + time.Second, actRejoin},
		// 刚卡住还没到长阈值,不该急着在上游故障时乱动。
		{"从未配置但未到长阈值先等", false, true, notReady, false, false, time.Minute, actWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := planRecovery(tc.daemonDown, tc.found, tc.ns, tc.planetOK, tc.everConfigured, tc.stuck)
			if got != tc.want {
				t.Fatalf("planRecovery=%v, want %v", got, tc.want)
			}
		})
	}
}

// 不在任何房间时,自愈必须完全静默:连守护进程都不该去探测。
func TestSuperviseIdleWhenNoRoom(t *testing.T) {
	stubSup(t)
	probed := false
	supDaemonListening = func() bool { probed = true; return true }
	superviseOnce()
	if probed {
		t.Fatal("没有目标网络时不该做任何探测/动作")
	}
	if recoverSnapshot().Recovering {
		t.Fatal("不在房间时不应报告 recovering")
	}
}

// 守护进程挂了 → 重新拉起。
func TestSuperviseRestartsDeadDaemon(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supDaemonListening = func() bool { return false }
	started := 0
	supEnsureReady = func() error { started++; return nil }

	superviseOnce()
	if started != 1 {
		t.Fatalf("实例没跑时应拉起一次, 实际 %d", started)
	}
	if !recoverSnapshot().Recovering {
		t.Fatal("重启期间应报告 recovering")
	}
}

// 网络在本地消失 → 重新 join。
func TestSuperviseRejoinsMissingNetwork(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supNetStates = func() []netState { return nil }
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if len(calls) != 1 || calls[0][0] != "join" || calls[0][1] != "nwidA" {
		t.Fatalf("应在本地缺失时 join 该网络, got %v", calls)
	}
}

// 线上真实故障(11865 那台):join 返回成功,但网络一直不落进 listnetworks,
// 旧逻辑只会循环重发 join,永远救不回来。持续久缺失必须重启实例。
func TestSuperviseRestartsDaemonWhenNetworkNeverLands(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supMu.Lock()
	notReadyAt = time.Now().Add(-restartDaemonHorizon - time.Minute)
	supMu.Unlock()
	supNetStates = func() []netState { return nil }
	restarted := 0
	supRestartDaemon = func() error { restarted++; return nil }
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if restarted != 1 {
		t.Fatalf("持续缺失应重启实例一次, 实际 %d", restarted)
	}
	// 重启后要补一次 join 重新登记网络
	if len(calls) != 1 || calls[0][0] != "join" || calls[0][1] != "nwidA" {
		t.Fatalf("重启后应重新 join, got %v", calls)
	}
}

// 重启代价大,冷却期内不许反复杀进程:只退回轻量 join。
func TestSuperviseRestartThrottled(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supMu.Lock()
	notReadyAt = time.Now().Add(-restartDaemonHorizon - time.Minute)
	lastRestart = time.Now() // 刚重启过,处于冷却期
	supMu.Unlock()
	supNetStates = func() []netState { return nil }
	restarted := 0
	supRestartDaemon = func() error { restarted++; return nil }
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if restarted != 0 {
		t.Fatalf("冷却期内不该再重启实例, 实际 %d", restarted)
	}
	if len(calls) != 1 || calls[0][0] != "join" {
		t.Fatalf("冷却期内应退回轻量 join, got %v", calls)
	}
}

// 网络长时间不就绪且 planet 可达 → 退出后重进。
func TestSuperviseRejoinWhenStuck(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supMu.Lock()
	notReadyAt = time.Now().Add(-time.Minute)
	lastAction = time.Time{}
	supMu.Unlock()
	supNetStates = func() []netState {
		return []netState{{NetworkID: "nwidA", Status: "REQUESTING_CONFIGURATION", Reason: "等待配置"}}
	}
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if len(calls) != 2 || calls[0][0] != "leave" || calls[1][0] != "join" {
		t.Fatalf("持续不就绪应 leave+join, got %v", calls)
	}
}

// planet 不可达时,即使网络不就绪也不许动本机(上游问题,重连只会添乱)。
func TestSuperviseWaitsWhenPlanetDown(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supMu.Lock()
	notReadyAt = time.Now().Add(-time.Minute)
	supMu.Unlock()
	supNetStates = func() []netState {
		return []netState{{NetworkID: "nwidA", Status: "REQUESTING_CONFIGURATION", Reason: "等待配置"}}
	}
	supPlanetState = func() (bool, int) { return false, -1 }
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if len(calls) != 0 {
		t.Fatalf("planet 不可达时不该 leave/join, got %v", calls)
	}
	if !recoverSnapshot().Recovering {
		t.Fatal("上游故障也应让界面显示 recovering(只是不动本机)")
	}
}

// 就绪后 recovering 必须归零,否则界面会卡在"恢复中"。
func TestSuperviseClearsRecoveringWhenReady(t *testing.T) {
	stubSup(t)
	setDesiredNet("nwidA")
	supNetStates = func() []netState { return []netState{readyNetOn("nwidA")} }
	var calls [][]string
	supRun = func(args ...string) (string, error) { calls = append(calls, args); return "", nil }

	superviseOnce()
	if len(calls) != 0 {
		t.Fatalf("已就绪时不该有动作, got %v", calls)
	}
	if recoverSnapshot().Recovering {
		t.Fatal("已就绪时 recovering 必须为 false")
	}
}

// 切换/离开网络要能中断旧网络的自愈,避免拿旧故障误判新网络。
func TestDesiredNetSwitchResetsState(t *testing.T) {
	resetSupervisor()
	setDesiredNet("a")
	markRecovering(true, "旧故障")
	if !recoverSnapshot().Recovering {
		t.Fatal("应先处于 recovering")
	}
	setDesiredNet("b")
	if recoverSnapshot().Recovering {
		t.Fatal("切换网络后应清空旧的自愈状态")
	}
	clearDesiredNet()
	supMu.Lock()
	d := desiredNet
	supMu.Unlock()
	if d != "" {
		t.Fatalf("clearDesiredNet 后应为空, got %q", d)
	}
}
