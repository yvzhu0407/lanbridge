package main

// nettune.go：加入房间后对虚拟网卡做的本地调优，以及离开时的恢复。
//
// 为什么需要：
//   - 局域网联机游戏靠广播/组播发现房间。广播复制本身由 ZeroTier 网络配置负责
//     （控制器下发 enableBroadcast=true），所有成员都会在自己的虚拟网卡上收到。
//   - 但一台机器通常有多块网卡（物理网卡、其他 VPN 残留）。系统按接口跃点
//     （metric）和路由选择出口，跃点更高的虚拟网卡可能拿不到游戏流量。
//     把虚拟网卡的跃点降下来，是让游戏走隧道的最简单做法。
//
// 所有调优都必须可逆：进入房间时改，离开/退出时恢复。改动前的原值写入
// <homeDir>/nettune-state.json，进程被强杀后下次启动也能恢复，不会把网卡
// 跃点永久改坏。
//
// 平台实现见 nettune_windows.go / nettune_darwin.go。

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// netTune 是平台相关的网络调优动作。
// LowerMetric 返回改动前的原值；返回 -1 表示该平台没有对应概念（无需恢复）。
type netTune interface {
	LowerMetric(device string) (int, error)
	RestoreMetric(device string, prev int) error
}

// tuneState 是持久化的调优记录，用于崩溃后恢复。
type tuneState struct {
	Device     string `json:"device"`
	PrevMetric int    `json:"prev_metric"`
	At         int64  `json:"at"`
}

var tuneMu sync.Mutex

// newNetTune 便于测试替换平台实现。
var newNetTune = platformNetTune

func tuneStatePath() string { return filepath.Join(homeDir, "nettune-state.json") }

func loadTuneState() (tuneState, bool) {
	b, err := os.ReadFile(tuneStatePath())
	if err != nil {
		return tuneState{}, false
	}
	var s tuneState
	if json.Unmarshal(b, &s) != nil || s.Device == "" {
		return tuneState{}, false
	}
	return s, true
}

func saveTuneState(s tuneState) {
	b, _ := json.Marshal(s)
	if err := os.WriteFile(tuneStatePath(), b, 0o600); err != nil {
		log.Printf("[网络调优] 保存状态失败: %v", err)
	}
}

func clearTuneState() { _ = os.Remove(tuneStatePath()) }

// tuneNetwork 在加入房间并拿到网卡名后调用；失败只记日志，不阻断连接。
func tuneNetwork(device string) {
	if device == "" {
		return
	}
	tuneMu.Lock()
	defer tuneMu.Unlock()
	if old, ok := loadTuneState(); ok {
		if old.Device == device {
			return
		}
		// 切换到了另一块网卡：先恢复旧的
		restoreTuneLocked(old)
	}
	prev, err := newNetTune().LowerMetric(device)
	if err != nil {
		log.Printf("[网络调优] 降低网卡跃点失败(%s): %v", device, err)
		return
	}
	if prev < 0 {
		log.Printf("[网络调优] %s 无需调整（平台无跃点概念）", device)
		return
	}
	saveTuneState(tuneState{Device: device, PrevMetric: prev, At: time.Now().Unix()})
	log.Printf("[网络调优] 已把网卡 %s 的跃点从 %d 降到 1", device, prev)
}

// restoreNetwork 离开房间时调用。
func restoreNetwork() {
	tuneMu.Lock()
	defer tuneMu.Unlock()
	if s, ok := loadTuneState(); ok {
		restoreTuneLocked(s)
	}
}

// restoreStaleTune 在进程启动时调用：上次进程异常退出会留下状态文件，
// 此时本进程还没有加入任何房间，直接恢复即可。必须在确认自己是唯一实例后调用。
func restoreStaleTune() {
	tuneMu.Lock()
	defer tuneMu.Unlock()
	if s, ok := loadTuneState(); ok {
		log.Printf("[网络调优] 发现上次退出未恢复的网卡调整，先恢复")
		restoreTuneLocked(s)
	}
}

func restoreTuneLocked(s tuneState) {
	if err := newNetTune().RestoreMetric(s.Device, s.PrevMetric); err != nil {
		// 保留状态文件，下次启动再试
		log.Printf("[网络调优] 恢复网卡跃点失败(%s): %v", s.Device, err)
		return
	}
	clearTuneState()
	log.Printf("[网络调优] 已恢复网卡 %s 的跃点(%d)", s.Device, s.PrevMetric)
}
