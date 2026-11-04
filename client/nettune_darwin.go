//go:build darwin

package main

// nettune_darwin.go：macOS 不做跃点调整。
//
// 原因：macOS 没有 Windows 那种按接口 metric 选路的概念，出口由服务顺序
// （networksetup -listnetworkserviceorder）和路由表决定。把虚拟网卡提到
// 服务顺序第一位会改变用户的全局网络行为（例如默认上网可能绕经隧道），
// 影响面远超收益，而且需要用户密码逐次确认。
//
// 局域网游戏在 macOS 上通过组播/广播发现房间，ZeroTier 的 enableBroadcast
// 已经覆盖这部分；这里显式返回“无需调整”。

type darwinNetTune struct{}

func platformNetTune() netTune { return darwinNetTune{} }

func (darwinNetTune) LowerMetric(string) (int, error) { return -1, nil }
func (darwinNetTune) RestoreMetric(string, int) error { return nil }
