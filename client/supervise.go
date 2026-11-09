package main

// supervise.go:后台自愈守护。
//
// 目标是"界面关着、没人点按钮时,断掉的链路也能自己接回来"。常见的断链有三类,
// 分别对应三种恢复手段:
//
//	1. zt-node 守护进程挂了(本机实例退出)      → 重新拉起实例
//	2. 房间网络在本地列表里消失(客户端重启过)   → 重新 join
//	3. 网络长时间不就绪(授权/协商卡住)           → 退出重进,促其重新登记
//
// 三条约束必须守住,否则自愈会变成"抖动源":
//   - 只在真正"有目标网络"时工作(desiredNet);不在任何房间时什么都不做;
//   - 不就绪先给宽限期,别把刚进房间的正常协商阶段当故障;
//   - planet 不可达时【不在本机乱动】——那是上游问题,重启本机无用还添乱。

import (
	"sync"
	"time"
)

const (
	// 巡检周期。15s 足够快地在一次短暂断链后自愈,又不会太频繁地跑 CLI。
	superviseInterval = 15 * time.Second
	// 网络就绪宽限期:刚 join 后 ZeroTier 需要协商/等配置,这段时间不算故障。
	notReadyGrace = 45 * time.Second
	// 同一种纠正动作的最小间隔,避免连续多个巡检周期反复退出/重进。
	rejoinThrottle = 30 * time.Second
	// 拉起实例 / 重新 join 的最小间隔。
	lightThrottle = 5 * time.Second
	// 从未拿到过配置(netconfRevision=0)的节点,即使 planet 暂时不可达,卡这么久后
	// 也值得再 leave+join 强制重拉一次:节点不会自己高频重试,干等可能永远等不到。
	// 用较长阈值,避免在纯上游故障时反复抖动。
	notConfiguredHorizon = 3 * time.Minute
	// 目标网络持续无法出现在本地列表(join 发了也不落)这么久,说明实例卡死:
	// 它还在应答 info,但不再应用 join。此时只有重启实例能救。用较长阈值 +
	// 重启节流,避免把正常慢启动误判成卡死而反复重启。
	restartDaemonHorizon  = 2 * time.Minute
	restartDaemonThrottle = 5 * time.Minute
)

// recoverAction 是巡检应当采取的纠正动作。
type recoverAction int

const (
	actNone          recoverAction = iota // 无需动作(网络就绪或不在房间)
	actWait                               // 观察中,暂不动作(宽限期内 / planet 不可达 / 需人工)
	actStartDaemon                        // 拉起 zt-node 实例
	actJoin                               // 网络在本地缺失,重新 join
	actRejoin                             // 长时间不就绪,退出后重进
	actRestartDaemon                      // 实例卡死(应答但不再应用 join),重启实例
)

// recoverState 是下发给界面的自愈状态快照。
type recoverState struct {
	Recovering bool      `json:"recovering"`
	Note       string    `json:"note,omitempty"`
	Since      time.Time `json:"-"`
}

var (
	supMu       sync.Mutex
	desiredNet  string       // 需要一直保持就绪的房间网络;空 = 不在房间
	sup         recoverState // 当前自愈状态
	notReadyAt  time.Time    // 该网络开始"持续不就绪"的时刻
	lastAction  time.Time    // 上次纠正动作的时刻(用于节流)
	lastRestart time.Time    // 上次重启实例的时刻(单独节流,重启代价大)
)

// 测试缝:自愈逻辑的决策可在不真跑 CLI / 不碰真实进程的前提下验证。
var (
	supDaemonListening = ztDaemonListening
	supEnsureReady     = ensureZTReady
	supRun             = ztRun
	supNetStates       = localNetStates
	supPlanetState     = planetState
	supRestartDaemon   = restartZTDaemon
)

// setDesiredNet 记录"当前应保持就绪的房间网络"。切换网络时重置计时,避免用旧网络的
// 故障状态误判新网络。
func setDesiredNet(id string) {
	supMu.Lock()
	defer supMu.Unlock()
	if desiredNet == id {
		return
	}
	desiredNet = id
	notReadyAt = time.Time{}
	lastAction = time.Time{}
	lastRestart = time.Time{}
	sup = recoverState{}
}

// clearDesiredNet:离开房间后停止自愈。
func clearDesiredNet() {
	supMu.Lock()
	defer supMu.Unlock()
	desiredNet = ""
	notReadyAt = time.Time{}
	lastAction = time.Time{}
	lastRestart = time.Time{}
	sup = recoverState{}
}

func markRecovering(on bool, note string) {
	supMu.Lock()
	defer supMu.Unlock()
	if on {
		if !sup.Recovering {
			sup.Since = time.Now()
		}
		sup.Recovering = true
		sup.Note = note
		return
	}
	sup = recoverState{}
}

// recoverSnapshot 返回当前自愈状态(供 health 面板展示)。
func recoverSnapshot() recoverState {
	supMu.Lock()
	defer supMu.Unlock()
	return sup
}

// supThrottle 保证同一类动作在 min 间隔内不重复执行。
func supThrottle(min time.Duration) bool {
	supMu.Lock()
	defer supMu.Unlock()
	if !lastAction.IsZero() && time.Since(lastAction) < min {
		return false
	}
	lastAction = time.Now()
	return true
}

// supRestartThrottle 单独给"重启实例"节流:它会杀掉进程,代价远大于 join/leave,
// 不能被轻量动作的短节流窗口带得反复触发。
func supRestartThrottle(min time.Duration) bool {
	supMu.Lock()
	defer supMu.Unlock()
	if !lastRestart.IsZero() && time.Since(lastRestart) < min {
		return false
	}
	lastRestart = time.Now()
	return true
}

// startSupervisor 起后台自愈循环。
func startSupervisor() {
	go func() {
		t := time.NewTicker(superviseInterval)
		defer t.Stop()
		for range t.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						LogDev("[自愈] 巡检异常(已忽略): %v", r)
					}
				}()
				superviseOnce()
			}()
		}
	}()
	LogDev("[自愈] 后台链路守护已启动(周期 %s)", superviseInterval)
}

// planRecovery 是纯决策函数:根据观测结果决定下一步动作。抽出来便于单测。
//
//	daemonDown: 控制端口无人监听(实例没跑)
//	found:      目标网络是否在本地 listnetworks 里
//	ns:         该网络的状态;found=false 时无意义
//	planetOK:   planet 根节点当前是否可达(只是采样瞬间的判定)
//	everConfigured: 该网络是否拿到过服务端配置(netconfRevision>0)
//	stuck:      该网络已持续"不就绪"多久
func planRecovery(daemonDown, found bool, ns netState, planetOK, everConfigured bool, stuck time.Duration) (recoverAction, string) {
	if daemonDown {
		return actStartDaemon, "ZeroTier 实例未运行,正在重新拉起"
	}
	if !found {
		// join 发出去了却一直不落进本地列表,说明实例卡死:它还应答 info,
		// 但不再应用 join。这种只有重启实例能救;短时间缺失仍先轻量重 join。
		if stuck >= restartDaemonHorizon {
			return actRestartDaemon, "房间网络持续无法加入本地列表,正在重启 ZeroTier 实例"
		}
		return actJoin, "本地缺少房间网络,正在重新加入"
	}
	if ns.Ready {
		return actNone, ""
	}
	if stuck < notReadyGrace {
		return actWait, "网络未就绪:" + ns.Reason
	}
	// 端口报错是驱动/权限问题,退出重进治不了,明确告知需人工处理。
	if ns.Status == "PORT_ERROR" || ns.PortError != 0 {
		return actWait, "虚拟网卡创建失败,需以管理员身份重启客户端重建网卡"
	}
	// planet 不可达 = 上游问题:本机重启/重连通常无益,等它自己恢复,别制造抖动。
	// 但"从未拿到过配置"是例外:节点自己不会高频重拉,而 planetOK 只是采样瞬间的
	// 判定,UDP 抖动下它会在好坏间跳变。卡久了仍值得 leave+join 强制再拉一次,
	// 用较长阈值(idle 干等可能永远等不到配置)。
	if !planetOK {
		if !everConfigured && stuck >= notConfiguredHorizon {
			return actRejoin, "长时间未拿到网络配置,正在重新拉取"
		}
		return actWait, "planet 暂不可达,等待上游恢复(不重连本机)"
	}
	return actRejoin, "网络长时间未就绪,正在退出重进以重新协商"
}

// netStateFor 从本机网络列表里取指定网络的状态。
func netStateFor(id string) (netState, bool) {
	for _, ns := range supNetStates() {
		if ns.NetworkID == id {
			return ns, true
		}
	}
	return netState{}, false
}

// superviseOnce 执行一轮巡检。只做安全且幂等的本地动作,绝不乱杀进程。
func superviseOnce() {
	supMu.Lock()
	net := desiredNet
	supMu.Unlock()

	if net == "" {
		markRecovering(false, "")
		return
	}

	daemonDown := !supDaemonListening()
	var ns netState
	var found bool
	planetOK := false
	if !daemonDown {
		ns, found = netStateFor(net)
		planetOK, _ = supPlanetState()
	}

	// 维护"持续不就绪"计时:一旦就绪立即清零,恢复后下次故障能快速响应。
	supMu.Lock()
	if found && ns.Ready {
		notReadyAt = time.Time{}
		lastAction = time.Time{}
	} else if notReadyAt.IsZero() {
		notReadyAt = time.Now()
	}
	stuck := time.Duration(0)
	if !notReadyAt.IsZero() {
		stuck = time.Since(notReadyAt)
	}
	supMu.Unlock()

	act, note := planRecovery(daemonDown, found, ns, planetOK, found && ns.Rev > 0, stuck)
	markRecovering(daemonDown || !found || !ns.Ready, note)

	switch act {
	case actNone, actWait:
		return
	case actStartDaemon:
		if !supThrottle(lightThrottle) {
			return
		}
		LogDev("[自愈] %s", note)
		if err := supEnsureReady(); err != nil {
			LogDev("[自愈] 拉起实例失败: %v", err)
		}
	case actJoin:
		if !supThrottle(lightThrottle) {
			return
		}
		LogDev("[自愈] %s", note)
		if _, err := supRun("join", net); err != nil {
			LogDev("[自愈] 重新加入失败: %v", err)
		}
	case actRestartDaemon:
		if !supRestartThrottle(restartDaemonThrottle) {
			// 冷却期内先退回轻量 join,总比干等强。
			if supThrottle(lightThrottle) {
				_, _ = supRun("join", net)
			}
			return
		}
		LogDev("[自愈] %s", note)
		if err := supRestartDaemon(); err != nil {
			LogDev("[自愈] 重启实例失败: %v", err)
			return
		}
		LogUser("warn", "self_heal", "检测到网络卡住，已自动重启修复")
		// 重启后实例是干净的,补一次 join 让它重新登记该网络。
		if _, err := supRun("join", net); err != nil {
			LogDev("[自愈] 重启后重新加入失败: %v", err)
		}
		// 重启+join 后重新给宽限期,别在同一故障点上连环动作。
		supMu.Lock()
		notReadyAt = time.Now()
		supMu.Unlock()
	case actRejoin:
		if !supThrottle(rejoinThrottle) {
			return
		}
		LogDev("[自愈] %s", note)
		_, _ = supRun("leave", net)
		time.Sleep(time.Second)
		if _, err := supRun("join", net); err != nil {
			LogDev("[自愈] 重进失败: %v", err)
		}
		// 重进后重新给一个宽限期,别在同一故障点上连环动作。
		supMu.Lock()
		notReadyAt = time.Now()
		supMu.Unlock()
	}
}
