package main

// reach.go：连通性分层探测与虚拟网络就绪判定。
//
// 一次"连不上"可能断在三个完全不同的地方，用户看到的却是同一个现象：
//
//	1. 本机虚拟网络没就绪 —— ZeroTier 没把网卡建出来，或者没拿到分配 IP
//	2. 隧道层没打通 —— 两台机器的 ZeroTier 进程之间没有可用的 UDP 路径
//	3. 数据层不通 —— 隧道是通的，但虚拟网卡之间发不出也收不到以太帧
//
// 旧实现拿第 2 层(peers 里的 latency)当 ping 结果，于是出现过
// "界面显示绿色 17ms、实际一个包都收不到"的误导。这里把三层分开测：
// 先判本机网络，再看隧道，最后发真实探测包(ICMP 优先，被防火墙拦时退回
// TCP 8890 —— 任一层数据面能通就算可达)。每个故障状态都带一套可执行的
// 排查步骤，并按状态变化写进 client.log。

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 探测结论。除 ok 外都代表"用户会看到连不上"。
const (
	layerOK         = "ok"          // 数据层可达
	layerTunnelOnly = "tunnel_only" // 隧道通，数据不通
	layerNoTunnel   = "no_tunnel"   // 隧道没建立
	layerLocalDown  = "local_down"  // 本机虚拟网络未就绪
)

// ---------- 本机虚拟网络就绪 ----------

// netState 是单个虚拟网络的就绪状态。Ready 为 false 时 Reason/Steps 说明原因与对策。
type netState struct {
	NetworkID string   `json:"network_id"`
	Status    string   `json:"status"` // ZeroTier 原生状态:OK / PORT_ERROR / ...
	Device    string   `json:"device"` // 网卡名,空 = 网卡没建出来
	PortError int      `json:"port_error"`
	IPs       []string `json:"ips"`
	Ready     bool     `json:"ready"`
	Reason    string   `json:"reason,omitempty"`
	Steps     []string `json:"steps,omitempty"`
	// UserReason/UserSteps 是给用户看的版本。开发版 Reason 里全是状态机名词,
	// 直接显示到结论栏会把"正在下发配置"这种正常等待说成"网卡没建出来"。
	UserReason string   `json:"user_reason,omitempty"`
	UserSteps  []string `json:"user_steps,omitempty"`
	// Rev = netconfRevision。0 表示加入后一直没拿到过服务端下发的网络配置,
	// 自愈据此判断"该重新 join 强制再拉一次"。
	Rev int `json:"rev"`
}

func mapStr(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

func mapInt(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// isASCII 判断路径是否全为 ASCII。中文 Windows 用户名会让 %USERPROFILE%
// 带中文，ZeroTier 家目录也随之带中文，是命令行传参乱码的高危点。
func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func mapStrs(m map[string]any, k string) []string {
	raw, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseNetState 把 zerotier-cli listnetworks 的一条记录翻译成就绪状态。
// 判定必须是"全部条件都满足"才算就绪:status=OK 只代表控制面没问题，
// 网卡没建出来(portDeviceName 为空)或端口报错时依然收不到任何帧。
func parseNetState(m map[string]any) netState {
	ns := netState{
		NetworkID: mapStr(m, "id"),
		Status:    mapStr(m, "status"),
		Device:    mapStr(m, "portDeviceName"),
		PortError: mapInt(m, "portError"),
		IPs:       mapStrs(m, "assignedAddresses"),
		Rev:       mapInt(m, "netconfRevision"),
	}
	switch {
	case ns.Status == "OK" && ns.Device != "" && ns.PortError == 0 && len(ns.IPs) > 0:
		ns.Ready = true
	case ns.Status == "PORT_ERROR" || ns.PortError != 0:
		ns.Reason = "虚拟网卡创建失败(PORT_ERROR)"
		ns.Steps = portErrorSteps()
		ns.UserReason = "虚拟网卡没能创建出来"
		ns.UserSteps = []string{"点「诊断」里的「重新检测」", "以管理员身份重启 LANBridge", "首次启动时请允许弹窗授权"}
	case ns.Status == "REQUESTING_CONFIGURATION":
		ns.Reason = "正在等待服务端下发网络配置"
		ns.Steps = configSteps()
		// 这是加入房间后的正常过渡态,通常几秒内自己消失。
		// 说成"网卡没建"会让用户以为要改权限/重装,方向完全错了。
		ns.UserReason = "正在连接虚拟网络"
		ns.UserSteps = []string{"稍等几秒,通常会自动连上", "超过 1 分钟仍未连上,点「诊断」里的「重新检测」", "仍不行再看诊断里的事件记录"}
	case ns.Status == "ACCESS_DENIED":
		ns.Reason = "服务端拒绝授权"
		ns.Steps = deniedSteps()
		ns.UserReason = "服务端没放行这台机器"
		ns.UserSteps = []string{"回到大厅重新加入这个房间", "房间可能已被房主解散"}
	case ns.Status == "OK" && ns.Device == "":
		ns.Reason = "网络已加入但虚拟网卡未创建"
		ns.Steps = portErrorSteps()
		ns.UserReason = "虚拟网卡没能创建出来"
		ns.UserSteps = []string{"以管理员身份重启 LANBridge", "首次启动时请允许弹窗授权"}
	case ns.Status == "OK":
		ns.Reason = "网络已加入但未拿到分配 IP"
		ns.Steps = configSteps()
		ns.UserReason = "还没分到联机地址"
		ns.UserSteps = []string{"稍等几秒", "不行就退出房间后重新进入"}
	default:
		ns.Reason = "网络状态异常: " + ns.Status
		ns.Steps = configSteps()
		ns.UserReason = "虚拟网络状态异常"
		ns.UserSteps = []string{"点「诊断」里的「重新检测」"}
	}
	return ns
}

func portErrorSteps() []string {
	var steps []string
	if runtime.GOOS == "windows" {
		steps = []string{
			"以管理员身份重启 LANBridge:虚拟网卡驱动必须由提权进程安装",
			"确认 " + ztHome() + " 下存在 zttap300.inf / zttap300.sys / zttap300.cat",
			"查看 " + filepath.Join(ztHome(), "port_error_log.txt") + " 里的具体错误",
			"若装了杀毒/安全软件,把 LANBridge 与 zttap300 驱动加入信任列表后重启程序",
		}
	} else {
		steps = []string{
			"确认 ZeroTier 实例以 root 运行:启动时应在管理员授权框里输入密码",
			"执行 ifconfig 确认已创建 feth 开头的虚拟网卡",
			"查看 /tmp/lanbridge-zt.log 里 EthernetTap 相关的报错",
		}
	}
	if hint := ztPortErrorHint(); hint != "" {
		steps = append(steps, hint)
	}
	return steps
}

func configSteps() []string {
	return []string{
		"确认房间服务端可达: " + serverBase,
		"确认本机 UDP " + ztPort() + " 出站未被防火墙或公司/校园网拦截",
		"确认 " + filepath.Join(ztHome(), "planet") + " 与服务端控制器属于同一套(改过自托管地址要重新下载 planet)",
		"在客户端控制台查看 ZeroTier 实例日志里是否报 controller unreachable",
	}
}

func deniedSteps() []string {
	return []string{
		"房间可能已被房主解散,回到大厅重新加入",
		"若房间仍在,退出房间后重新进入以重新申请授权",
	}
}

func planetSteps() []string {
	return []string{
		"确认本机 UDP " + ztPort() + " 出站可访问外网(公司网络/校园网常拦 UDP)",
		"确认 " + filepath.Join(ztHome(), "planet") + " 与服务端 " + serverBase + " 提供的一致",
		"确认服务端的 ZeroTier 根节点进程在运行",
	}
}

// ---------- 隧道层 ----------

// peerTunnel 是某个节点在本机 ZeroTier 对端列表里的隧道状态。
type peerTunnel struct {
	Found   bool
	NodeID  string
	Live    bool // 有可用路径在收包
	Latency int  // 隧道 RTT;-1 = 未测出
	Paths   int
	Version string
}

// peerLookup 便于测试替换。
var peerLookup = lookupPeerCLI

// peersCache 缓存一次 peers 快照。成员表每 4.5 秒并发测一轮,
// 每个成员都单独跑一次 zerotier-cli 子进程会明显拖慢界面(成员越多越糟),
// 这里 2 秒内共用同一份结果——隧道延迟本来也是秒级刷新的量。
var (
	peersMu   sync.Mutex
	peersAt   time.Time
	peersList []map[string]any
)

const peersTTL = 2 * time.Second

// ztQuery 是 ztRun 的测试缝:缓存相关逻辑要在不真跑 zerotier-cli 的前提下验证。
var ztQuery = ztRun

func peerSnapshot() []map[string]any {
	peersMu.Lock()
	defer peersMu.Unlock()
	if time.Since(peersAt) < peersTTL && peersList != nil {
		return peersList
	}
	out, err := ztQuery("-j", "peers")
	if err != nil {
		peersList, peersAt = nil, time.Time{} // 失败不缓存,下次重试
		return nil
	}
	var peers []map[string]any
	if json.Unmarshal([]byte(out), &peers) != nil {
		peersList, peersAt = nil, time.Time{}
		return nil
	}
	peersList, peersAt = peers, time.Now()
	return peers
}

func lookupPeerCLI(nodeID string) peerTunnel {
	for _, p := range peerSnapshot() {
		if mapStr(p, "address") != nodeID {
			continue
		}
		pt := peerTunnel{Found: true, NodeID: nodeID, Latency: -1, Version: mapStr(p, "version")}
		if lat, ok := p["latency"].(float64); ok {
			pt.Latency = int(lat)
		}
		if paths, ok := p["paths"].([]any); ok {
			pt.Paths = len(paths)
		}
		pt.Live = peerLive(p, peerLiveWindow)
		return pt
	}
	return peerTunnel{}
}

// peerLiveWindow: 一条活跃路径在这么久内收到过包,就认为链路还活着。
// ZeroTier 的 HELLO 周期是 10s,放宽到 60s 容忍抖动,同时又不至于把
// 早已断开的节点判成可达。
const peerLiveWindow = 60 * time.Second

// peerLive 判断某个 peer 当前是否可达。
//
// 不能用 latency 的正负来判活:ZeroTier 对 PLANET 等 peer 的 latency 字段会报出
// 负值(线上实测 planet latency=-692,但其 preferred 路径 16s 前还在收包)。
// 这时链路明明是通的,却会因为 latency<0 被判成"无法连接 planet"(红灯误报)。
// 真正的存活信号是:存在一条 active 且未过期、且最近 peerLiveWindow 内收到过包的路径。
//
// 没有 paths 字段时退回 latency>=0,兼容不提供路径信息的输出。
func peerLive(p map[string]any, window time.Duration) bool {
	if paths, ok := p["paths"].([]any); ok && len(paths) > 0 {
		return freshPath(paths, window)
	}
	if lat, ok := p["latency"].(float64); ok {
		return lat >= 0
	}
	return false
}

func freshPath(paths []any, window time.Duration) bool {
	now := time.Now().UnixMilli()
	for _, raw := range paths {
		pm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if active, _ := pm["active"].(bool); !active {
			continue
		}
		if expired, _ := pm["expired"].(bool); expired {
			continue
		}
		lr, ok := pm["lastReceive"].(float64)
		if !ok {
			continue
		}
		if now-int64(lr) <= window.Milliseconds() {
			return true
		}
	}
	return false
}

// ---------- 数据层 ----------

var (
	icmpProbe = realICMPProbe
	tcpProbe  = realTCPProbe
)

// realICMPProbe 发一个真实 ICMP 回显请求。返回 (往返毫秒, 是否收到回应)。
func realICMPProbe(ip string) (int, bool) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// -w 单位毫秒;-n 1 只发一个
		cmd = hideCmd("ping", "-n", "1", "-w", "2000", ip)
	} else {
		// darwin 的 -W 单位是毫秒(linux 是秒,本程序只支持 darwin/windows)
		cmd = hideCmd("ping", "-c", "1", "-W", "800", ip)
	}
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return -1, false
	}
	return int(time.Since(start).Milliseconds()), true
}

// realTCPProbe 连一下对方的聊天端口。ICMP 常被防火墙整个拦掉，
// 但游戏和聊天走的是 TCP/UDP 数据面，所以这条链路也要测。
func realTCPProbe(ip string) (int, bool) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strings.TrimPrefix(chatPort, ":")), 1500*time.Millisecond)
	if err != nil {
		return -1, false
	}
	_ = conn.Close()
	return int(time.Since(start).Milliseconds()), true
}

// ---------- 综合探测 ----------

// probeResult 是一次成员探测的完整结论，直接下发给界面。
type probeResult struct {
	State     string   `json:"state"`      // ok / tunnel_only / no_tunnel / local_down
	StateText string   `json:"state_text"` // 给界面直接显示的中文结论
	Reachable bool     `json:"reachable"`  // 数据面真的通(不是隧道通)
	Latency   int      `json:"latency"`    // 真实往返毫秒;-1 = 不通
	Via       string   `json:"via"`        // 实际走通的方式:icmp / tcp / ""
	ICMP      int      `json:"icmp"`       // ICMP 结果;-1 = 无回应
	TCP       int      `json:"tcp"`        // TCP 8890 结果;-1 = 连不上
	Tunnel    int      `json:"tunnel"`     // ZeroTier 隧道 RTT;-1 = 未建立
	Steps     []string `json:"steps,omitempty"`
}

func probePeer(ip, nodeID string, local netState) probeResult {
	res := probeResult{Latency: -1, ICMP: -1, TCP: -1, Tunnel: -1}

	// 第 1 层:本机虚拟网络。这一层没就绪，后面的探测没有意义。
	if !local.Ready {
		res.State = layerLocalDown
		res.StateText = "本机网络未就绪"
		if local.Reason != "" {
			res.Steps = append(res.Steps, local.Reason)
		}
		res.Steps = append(res.Steps, local.Steps...)
		return res
	}

	var tunnel peerTunnel
	if nodeID != "" {
		tunnel = peerLookup(nodeID)
	}
	// 第 2 层:隧道。有可用路径在收包说明两台 ZeroTier 进程已经换了包。
	// 但"没有新鲜直连路径"不等于不通:走中继(relay)时,ZeroTier 在 LEAF 上
	// 往往不记录直连路径(latency=-1、paths=[]),数据其实经 planet 中继能通。
	// 所以只有"对端根本不在 peers 列表"时才敢直接下结论并跳过实测。
	if !tunnel.Found {
		res.State = layerNoTunnel
		res.StateText = "链路未建立"
		res.Steps = noTunnelSteps(ip, nodeID)
		return res
	}
	if tunnel.Live {
		// 活着但 latency 未知时用 0 表示"通但延迟未测出",避免下发 -1 被界面显示成"超时"。
		res.Tunnel = tunnel.Latency
		if res.Tunnel < 0 {
			res.Tunnel = 0
		}
	}
	// 走到这里说明对端已被发现。即使尚未直连(relay / 握手未完成),也要实测数据面:
	// 中继链路同样能通,不能因为看不到直连路径就判死。

	// 第 3 层:数据面。ICMP 是最直接的真实 ping;被拦时退到 TCP 端口探测。
	if ip != "" {
		if lat, ok := icmpProbe(ip); ok {
			res.ICMP = lat
			res.Latency, res.Via, res.Reachable = lat, "icmp", true
			res.State, res.StateText = layerOK, "已就绪"
			return res
		}
		if lat, ok := tcpProbe(ip); ok {
			res.TCP = lat
			res.Latency, res.Via, res.Reachable = lat, "tcp", true
			res.State, res.StateText = layerOK, "已就绪"
			return res
		}
	}

	if !tunnel.Live {
		// 本来就没有新鲜直连路径(relay / 握手未完成),数据面也实测不通:
		// 仍属"协商中"。这里给的是排查建议,不能当成"隧道通但数据不通"。
		res.State = layerNoTunnel
		res.StateText = "链路协商中"
		res.Steps = handshakePendingSteps(ip, nodeID)
		return res
	}
	res.State = layerTunnelOnly
	res.StateText = "隧道通·数据不通"
	res.Steps = tunnelOnlySteps(ip, local, res.Tunnel)
	return res
}

func localIPOf(ns netState) string {
	if len(ns.IPs) == 0 {
		return "本机虚拟 IP"
	}
	return strings.SplitN(ns.IPs[0], "/", 2)[0]
}

func tunnelOnlySteps(ip string, local netState, tunnel int) []string {
	return []string{
		fmt.Sprintf("ZeroTier 隧道已打通(%dms),但 %s 的虚拟网卡收不到包:故障在对方机器上,本机无需改动", tunnel, ip),
		"让对方打开 LANBridge 的「控制台」,确认其虚拟网络状态为「已就绪」(虚拟网卡已创建且已拿到分配 IP)",
		"让对方以管理员身份重启 LANBridge,让虚拟网卡驱动重新安装",
		"让对方放行虚拟网段的入站流量:Windows 会把新建的虚拟网卡判为公用网络,默认拦截入站",
		fmt.Sprintf("对方修复后,让其反向 ping %s 验证双向可达", localIPOf(local)),
	}
}

func noTunnelSteps(ip, nodeID string) []string {
	steps := []string{
		"目标节点不在本机 ZeroTier 对端列表里:对方可能没启动 LANBridge,或没加入这个房间网络",
		"确认双方都还在房间里(大厅列表里能看到对方的虚拟 IP)",
		"确认双方 UDP " + ztPort() + " 出站未被防火墙拦截:ZeroTier 靠 UDP 打洞建立直连",
	}
	if nodeID != "" {
		steps = append(steps, "核对 "+ip+" 对应的节点 ID 是否为 "+nodeID+":服务端记录的节点 ID 与对方实际运行的实例不一致时永远连不上")
	}
	return steps
}

// handshakePendingSteps 用于"节点已在 peers 列表里但还没握手"的情况。
// 这通常只是刚加入房间、路径还在协商,几秒后会自动好;持续不通才是问题。
func handshakePendingSteps(ip, nodeID string) []string {
	return []string{
		"节点 " + nodeID + " 已被发现,但 ZeroTier 还在协商直连路径——刚进房间时属于正常现象",
		"等 10~30 秒后点「测速」重试;若一直停在这里,让对方确认 UDP " + ztPort() + " 出站未被防火墙或公司网络拦截",
		"双方都在对称 NAT / 公司网络后时可能只能走中继,延迟会明显偏高但仍可用",
		"核对 " + ip + " 对应的节点 ID 是否为 " + nodeID,
	}
}

// ---------- 日志(按状态变化去重) ----------

const reachRemindEvery = 5 * time.Minute

var (
	reachMu   sync.Mutex
	reachSeen = map[string]reachMark{}
)

type reachMark struct {
	state string
	at    time.Time
}

// logOnce 决定这次要不要写日志,避免 4.5 秒一次的轮询把 client.log 刷满。
// 规则:状态变化必写;持续故障每 5 分钟提醒一次;正常且没变化则静默。
func logOnce(key, state string, healthy bool) (write, recovered bool) {
	reachMu.Lock()
	defer reachMu.Unlock()
	prev, seen := reachSeen[key]
	now := time.Now()
	if seen && prev.state == state {
		if healthy || now.Sub(prev.at) < reachRemindEvery {
			return false, false
		}
	}
	reachSeen[key] = reachMark{state: state, at: now}
	return true, seen && prev.state != state && healthy
}

func fmtLat(ms int) string {
	if ms < 0 {
		return "超时"
	}
	return fmt.Sprintf("%dms", ms)
}

// logProbe 把探测结论写进 client.log,故障时附上排查步骤。
func logProbe(desc string, res probeResult) {
	write, recovered := logOnce("peer:"+desc, res.State, res.State == layerOK)
	if !write {
		return
	}
	if recovered {
		LogDev("[连通] %s 已恢复:数据面可达 %s(经 %s)", desc, fmtLat(res.Latency), res.Via)
		LogUser("info", "peer_ok", fmt.Sprintf("与 %s 的连接已恢复", desc))
		return
	}
	if res.State == layerOK {
		return
	}
	LogDev("[连通] %s 故障:%s(隧道=%s ICMP=%s TCP=%s)", desc, res.StateText,
		fmtLat(res.Tunnel), fmtLat(res.ICMP), fmtLat(res.TCP))
	LogUser("warn", "peer_bad", fmt.Sprintf("连不上 %s", desc))
	for i, s := range res.Steps {
		LogDev("[排查] %d. %s", i+1, s)
	}
}

// ---------- 健康巡检 ----------

type issue struct {
	Code      string   `json:"code"`
	Text      string   `json:"text"`       // 开发用，含技术名词
	Steps     []string `json:"steps"`      // 开发用排查步骤
	UserText  string   `json:"user_text"`  // 用户用，动词开头
	UserSteps []string `json:"user_steps"` // 用户用，1~3 步
}

type healthReport struct {
	NodeReady     bool       `json:"node_ready"`
	NodeID        string     `json:"node_id"`
	Networks      []netState `json:"networks"`
	PlanetOK      bool       `json:"planet_ok"`
	PlanetLatency int        `json:"planet_latency"`
	Issues        []issue    `json:"issues"`
	// 后台自愈状态:界面据此显示"自动恢复中"而不是干等。
	Recovering  bool   `json:"recovering"`
	RecoverNote string `json:"recover_note,omitempty"`
}

// netsCache 与 peersCache 同理:一轮测速里所有成员共用同一份网络状态。
var (
	netsMu   sync.Mutex
	netsAt   time.Time
	netsList []netState
)

func localNetStates() []netState {
	netsMu.Lock()
	defer netsMu.Unlock()
	if time.Since(netsAt) < peersTTL && netsList != nil {
		return netsList
	}
	out, err := ztQuery("-j", "listnetworks")
	var raw []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &raw) != nil {
		netsList, netsAt = nil, time.Time{}
		return nil
	}
	states := make([]netState, 0, len(raw))
	for _, m := range raw {
		states = append(states, parseNetState(m))
	}
	netsList, netsAt = states, time.Now()
	return states
}

func planetState() (bool, int) {
	for _, p := range peerSnapshot() {
		if mapStr(p, "role") != "PLANET" {
			continue
		}
		if !peerLive(p, peerLiveWindow) {
			continue
		}
		lat := -1
		if l, ok := p["latency"].(float64); ok && l >= 0 {
			lat = int(l)
		}
		return true, lat
	}
	return false, -1
}

func buildHealth() healthReport {
	rs := recoverSnapshot()
	rep := healthReport{PlanetLatency: -1, Recovering: rs.Recovering, RecoverNote: rs.Note}
	if info, err := ztRun("info"); err == nil {
		if parts := strings.Fields(info); len(parts) >= 3 {
			rep.NodeID = parts[2]
		}
		rep.NodeReady = true
	}
	if !rep.NodeReady {
		text := "ZeroTier 实例未运行"
		if ztStartErr != "" {
			text += ": " + ztStartErr
		}
		steps := []string{
			"重启 LANBridge;Windows 上需要管理员权限才能拉起 ZeroTier 实例",
			"查看 " + ztNodeLogPath() + " 里的启动错误",
			"确认 UDP " + ztPort() + " 未被其它程序占用",
		}
		// 用户名含中文时,ZeroTier 家目录会带中文路径。命令行参数传递一旦
		// 走了 ANSI 码页就会变成乱码目录,实例启动后找不到 identity/planet。
		// 把路径原样写进日志,出问题时能一眼看出是不是这个问题。
		if home := ztHome(); !isASCII(home) {
			steps = append(steps, "检测到 ZeroTier 家目录含非 ASCII 字符,若实例反复启动失败,请把 Windows 用户名改为英文或设置 ZT_HOME 指向纯英文路径: "+home)
		}
		rep.Issues = append(rep.Issues, issue{
			Code:      "node_down",
			Text:      text,
			Steps:     steps,
			UserText:  "后台服务没有响应",
			UserSteps: []string{"打开「诊断」看最近事件", "重启 LANBridge", "还不行就重装"},
		})
		return rep
	}

	rep.Networks = localNetStates()
	anyReady := false
	for _, ns := range rep.Networks {
		if ns.Ready {
			anyReady = true
			continue
		}
		// 用户文案跟具体状态走,不能一律说"网卡没建出来":正在等下发配置、
		// 被服务端拒绝、没分到 IP 是三种完全不同的故障,处理方式也不同。
		userText, userSteps := ns.UserReason, ns.UserSteps
		if userText == "" {
			userText = "你这台机器的虚拟网络还没就绪"
			userSteps = []string{"点「诊断」里的「重新检测」", "重启 LANBridge"}
		}
		rep.Issues = append(rep.Issues, issue{
			Code:      "net_not_ready:" + ns.NetworkID,
			Text:      ns.Reason,
			Steps:     ns.Steps,
			UserText:  userText,
			UserSteps: userSteps,
		})
	}
	if ok, lat := planetState(); ok {
		rep.PlanetOK, rep.PlanetLatency = true, lat
	} else if planetShouldAlarm(false, anyReady) {
		rep.Issues = append(rep.Issues, issue{
			Code:      "planet_down",
			Text:      "无法连接 ZeroTier planet 根节点",
			Steps:     planetSteps(),
			UserText:  "连不上房间服务器",
			UserSteps: []string{"检查网络能否访问服务器", "稍等 1 分钟后重试"},
		})
	}
	return rep
}

// planetShouldAlarm:planet 不可达只有在"还没有任何可用的虚拟网络"时才算用户可见故障。
// 一旦有网络就绪,它靠已缓存的配置继续工作,planet 的 UDP 抖动不影响联机;此时报红
// 只会误导——线上正是它在健康面板里对着已连上的网络反复闪红。
func planetShouldAlarm(planetOK, anyNetReady bool) bool {
	return !planetOK && !anyNetReady
}

// logHealth 把巡检结论写进日志(同样按状态变化去重)。
// 注意:不能按 rep.NodeReady 做整体早退——节点正常时恰恰是"网络未就绪/planet
// 不可达"这类故障最需要记录的时刻。节点本身的状态已作为 node_down 出现在
// Issues 里,和其余故障走同一套边沿逻辑即可。
func logHealth(rep healthReport) {
	current := make(map[string]issue, len(rep.Issues))
	for _, is := range rep.Issues {
		current[is.Code] = is
	}
	for _, is := range rep.Issues {
		if !logOnceHealth(is.Code, false) {
			continue
		}
		LogDev("[健康] 异常:%s", is.Text)
		if strings.HasPrefix(is.Code, "net_not_ready:") {
			text := is.UserText
			if text == "" {
				text = "你这台机器的虚拟网络还没就绪"
			}
			LogUser("warn", "net_not_ready", text)
		}
		for i, s := range is.Steps {
			LogDev("[排查] %d. %s", i+1, s)
		}
	}
	logHealthRecovered(current)
}

// logHealthRecovered 对"上一轮还在报、这一轮已消失"的故障补一条恢复记录。
// 只看 health 命名空间,避免和成员探测的 key 混在一起。
func logHealthRecovered(current map[string]issue) {
	var recovered []string
	reachMu.Lock()
	now := time.Now()
	for key, mark := range reachSeen {
		if !strings.HasPrefix(key, "health:") || mark.state != "bad" {
			continue
		}
		code := strings.TrimPrefix(key, "health:")
		if _, still := current[code]; still {
			continue
		}
		reachSeen[key] = reachMark{state: "ok", at: now}
		recovered = append(recovered, code)
	}
	reachMu.Unlock()
	for _, code := range recovered {
		LogDev("[健康] 已恢复:%s", code)
		if strings.HasPrefix(code, "net_not_ready:") {
			LogUser("info", "net_ready", "虚拟网络已连上，可以邀请朋友了")
		}
	}
}

// logOnceHealth 用一个单独的 key 空间,避免和成员探测互相干扰。
// 只处理"故障出现/消失"两种边沿。
func logOnceHealth(code string, healthy bool) bool {
	reachMu.Lock()
	defer reachMu.Unlock()
	key := "health:" + code
	prev, seen := reachSeen[key]
	now := time.Now()
	if healthy {
		if !seen || prev.state != "ok" {
			reachSeen[key] = reachMark{state: "ok", at: now}
		}
		return false
	}
	if seen && prev.state == "bad" && now.Sub(prev.at) < reachRemindEvery {
		return false
	}
	reachSeen[key] = reachMark{state: "bad", at: now}
	return true
}

// startHealthWatch 后台巡检:即使界面没打开,虚拟网络/ping 故障也会落进日志。
// 不要在这里按 ztRunning() 早退:"ZeroTier 实例未运行"本身就是需要记录的故障,
// buildHealth 会把它翻译成 node_down 一并写日志。
func startHealthWatch() {
	go func() {
		t := time.NewTicker(90 * time.Second)
		defer t.Stop()
		for range t.C {
			logHealth(buildHealth())
		}
	}()
}
