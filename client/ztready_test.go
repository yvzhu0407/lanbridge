package main

// ztready_test.go:「实例活着但不健康」的自救路径单测。
// 覆盖三件事:PORT_ERROR 判定、修复动作(补驱动+杀实例)、冷却节流。

import (
	"errors"
	"testing"
	"time"
)

func TestPortErrorInList(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"空列表", `[]`, false},
		{"健康网络", `[{"id":"a","status":"OK","portError":0}]`, false},
		{"等待配置", `[{"id":"a","status":"REQUESTING_CONFIGURATION"}]`, false},
		{"PORT_ERROR状态", `[{"id":"a","status":"PORT_ERROR"}]`, true},
		{"portError字段", `[{"id":"a","status":"OK","portError":5}]`, true},
		{"非JSON", `not json`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := portErrorInList(tc.out); got != tc.want {
				t.Fatalf("portErrorInList = %v, want %v", got, tc.want)
			}
		})
	}
}

// withRepairStubs 换掉修复的外部动作,并恢复 ztRepairLast。
func withRepairStubs(t *testing.T, installErr, killErr error) (installs, kills *int) {
	t.Helper()
	oi, ok := repairInstallDriver, repairKillDaemon
	ol := ztRepairLast
	t.Cleanup(func() {
		repairInstallDriver, repairKillDaemon, ztRepairLast = oi, ok, ol
	})
	installs, kills = new(int), new(int)
	repairInstallDriver = func() error { *installs++; return installErr }
	repairKillDaemon = func() error { *kills++; return killErr }
	ztRepairLast = time.Time{}
	return installs, kills
}

var errRepairFake = errors.New("fake repair failure")

// 不健康实例应被修复:补装驱动 + 杀实例;杀失败时返回 false。
func TestMaybeRepairUnhealthyActs(t *testing.T) {
	installs, kills := withRepairStubs(t, nil, nil)
	if !maybeRepairUnhealthy("test") {
		t.Fatal("首次修复不应被冷却拦下")
	}
	if *installs != 1 || *kills != 1 {
		t.Fatalf("installs=%d kills=%d, want 1/1", *installs, *kills)
	}
}

// 冷却期内不许反复杀实例。
func TestMaybeRepairUnhealthyThrottled(t *testing.T) {
	installs, kills := withRepairStubs(t, nil, nil)
	ztRepairLast = time.Now().Add(-30 * time.Second) // 刚修过,冷却中
	if maybeRepairUnhealthy("test") {
		t.Fatal("冷却期内不应执行修复")
	}
	if *installs != 0 || *kills != 0 {
		t.Fatalf("冷却期内不该有动作: installs=%d kills=%d", *installs, *kills)
	}
	// 冷却刚过则应放行
	ztRepairLast = time.Now().Add(-ztRepairCooldown - time.Second)
	if !maybeRepairUnhealthy("test") {
		t.Fatal("冷却期过后应放行修复")
	}
}

// 补装驱动失败不阻断重启实例(驱动可能只是没 settle,重启后能建卡)。
func TestMaybeRepairUnhealthyDriverFailureContinues(t *testing.T) {
	_, kills := withRepairStubs(t, errRepairFake, nil)
	if !maybeRepairUnhealthy("test") {
		t.Fatal("驱动失败也应继续杀实例")
	}
	if *kills != 1 {
		t.Fatalf("kills=%d, want 1", *kills)
	}
}

// 杀实例失败要报 false,让调用方对外报"修复未完成"。
func TestMaybeRepairUnhealthyKillFailure(t *testing.T) {
	installs, _ := withRepairStubs(t, nil, errRepairFake)
	if maybeRepairUnhealthy("test") {
		t.Fatal("杀实例失败时应返回 false")
	}
	if *installs != 1 {
		t.Fatalf("installs=%d, want 1(先补驱动再杀)", *installs)
	}
}
