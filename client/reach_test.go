package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// listnetworks 的真实结构(取自 ZeroTier 1.16.2 的 -j listnetworks 输出)。
func netJSON(id, status, dev string, portErr int, ips ...string) map[string]any {
	raw := make([]any, 0, len(ips))
	for _, ip := range ips {
		raw = append(raw, ip)
	}
	return map[string]any{
		"id": id, "status": status, "portDeviceName": dev,
		"portError": float64(portErr), "assignedAddresses": raw,
	}
}

func TestParseNetStateReady(t *testing.T) {
	ns := parseNetState(netJSON("bd5b8ad96c000001", "OK", "feth2651", 0, "10.147.1.2/24"))
	if !ns.Ready {
		t.Fatalf("应判定就绪, got %+v", ns)
	}
	if ns.Reason != "" || len(ns.Steps) != 0 {
		t.Fatalf("就绪时不该带故障说明: %+v", ns)
	}
}

// status=OK 只说明控制面没问题:网卡没建出来时依然一帧都收不到,
// 这正是 mercury 那台机器的症状,不能被判成就绪。
func TestParseNetStateNotReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    map[string]any
	}{
		{"网卡未创建但状态OK", netJSON("n", "OK", "", 0, "10.147.1.3/24")},
		{"无分配IP", netJSON("n", "OK", "feth1", 0)},
		{"PORT_ERROR", netJSON("n", "PORT_ERROR", "", 0)},
		{"portError非0", netJSON("n", "OK", "feth1", 1, "10.147.1.3/24")},
		{"等待配置", netJSON("n", "REQUESTING_CONFIGURATION", "", 0)},
		{"授权被拒", netJSON("n", "ACCESS_DENIED", "", 0)},
		{"未知状态", netJSON("n", "SOMETHING_NEW", "feth1", 0, "10.147.1.3/24")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := parseNetState(tc.m)
			if ns.Ready {
				t.Fatalf("不该判定为就绪: %+v", ns)
			}
			if ns.Reason == "" {
				t.Fatal("未就绪必须给出原因")
			}
			if len(ns.Steps) == 0 {
				t.Fatal("未就绪必须给出排查步骤")
			}
		})
	}
}

func stubProbes(t *testing.T, icmp, tcp func(string) (int, bool), peer func(string) peerTunnel) {
	t.Helper()
	oldICMP, oldTCP, oldPeer := icmpProbe, tcpProbe, peerLookup
	icmpProbe, tcpProbe, peerLookup = icmp, tcp, peer
	t.Cleanup(func() {
		icmpProbe, tcpProbe, peerLookup = oldICMP, oldTCP, oldPeer
		// 探测函数被换掉时缓存必须失效,否则下个用例会读到上个用例留下的快照
		resetReachCache()
	})
}

func resetReachCache() {
	peersMu.Lock()
	peersList, peersAt = nil, time.Time{}
	peersMu.Unlock()
	netsMu.Lock()
	netsList, netsAt = nil, time.Time{}
	netsMu.Unlock()
	reachMu.Lock()
	reachSeen = map[string]reachMark{}
	reachMu.Unlock()
}

func up(ms int) func(string) (int, bool) { return func(string) (int, bool) { return ms, true } }
func down() func(string) (int, bool)     { return func(string) (int, bool) { return -1, false } }
func noPeer(string) peerTunnel           { return peerTunnel{} }
func livePeer(ms int) func(string) peerTunnel {
	return func(id string) peerTunnel { return peerTunnel{Found: true, NodeID: id, Live: true, Latency: ms} }
}

var readyNet = netState{NetworkID: "n", Ready: true, IPs: []string{"10.147.1.2/24"}}

func TestProbePeerICMP(t *testing.T) {
	stubProbes(t, up(12), down(), livePeer(17))
	res := probePeer("10.147.1.1", "ce5b28bddf", readyNet)
	if res.State != layerOK || !res.Reachable || res.Via != "icmp" || res.Latency != 12 {
		t.Fatalf("应经 ICMP 判定可达: %+v", res)
	}
	if res.Tunnel != 17 {
		t.Fatalf("隧道延迟应单独下发, got %d", res.Tunnel)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("可达时不该带排查步骤: %v", res.Steps)
	}
}

// ICMP 被防火墙整个拦掉是常态,此时要靠 TCP 数据面判定可达。
func TestProbePeerTCPFallback(t *testing.T) {
	stubProbes(t, down(), up(30), livePeer(17))
	res := probePeer("10.147.1.1", "ce5b28bddf", readyNet)
	if res.State != layerOK || res.Via != "tcp" || res.Latency != 30 {
		t.Fatalf("ICMP 不通但 TCP 通,应判可达: %+v", res)
	}
	if res.ICMP != -1 {
		t.Fatalf("ICMP 结果应保留为 -1, got %d", res.ICMP)
	}
}

// 这是 mercury 的场景:隧道 17ms(界面一片绿),实际数据一帧都过不来。
func TestProbePeerTunnelOnly(t *testing.T) {
	stubProbes(t, down(), down(), livePeer(17))
	res := probePeer("10.147.1.3", "7fc64952d6", readyNet)
	if res.State != layerTunnelOnly {
		t.Fatalf("应判定隧道通但数据不通: %+v", res)
	}
	if res.Reachable {
		t.Fatal("隧道通不等于可达")
	}
	if res.Latency != -1 {
		t.Fatalf("不可达时延迟必须是 -1, got %d", res.Latency)
	}
	if len(res.Steps) == 0 {
		t.Fatal("必须给出排查步骤")
	}
	// 隧道通说明故障在对方机器上,建议必须指向对方而不是本机
	if !strings.Contains(strings.Join(res.Steps, " "), "对方") {
		t.Fatalf("排查建议应指向对方机器: %v", res.Steps)
	}
}

func TestProbePeerNoTunnel(t *testing.T) {
	stubProbes(t, down(), down(), noPeer)
	res := probePeer("10.147.1.9", "ffffffff", readyNet)
	if res.State != layerNoTunnel {
		t.Fatalf("应判定链路未建立: %+v", res)
	}
	if res.Tunnel != -1 || res.Reachable {
		t.Fatalf("无隧道时不能有隧道延迟或可达: %+v", res)
	}
	if len(res.Steps) == 0 {
		t.Fatal("必须给出排查步骤")
	}
}

// 隧道都没建立时,数据面必然不通:再去等 ICMP/TCP 超时只会让界面卡几秒,
// 成员越多越明显。必须直接下结论,不发起任何数据面探测。
func TestProbePeerNoTunnelSkipsDataProbes(t *testing.T) {
	var probed bool
	stubProbes(t,
		func(string) (int, bool) { probed = true; return -1, false },
		func(string) (int, bool) { probed = true; return -1, false },
		noPeer)
	res := probePeer("10.147.1.9", "ffffffff", readyNet)
	if res.State != layerNoTunnel {
		t.Fatalf("应判定链路未建立: %+v", res)
	}
	if probed {
		t.Fatal("无隧道时不应再发起 ICMP/TCP 探测")
	}
}

// 节点已在 peers 列表里但 latency=-1,是"正在协商路径",不是"对方没加入"。
// 这两种情况排查方向完全不同,必须给出不同措辞和建议。
func TestProbePeerHandshakePending(t *testing.T) {
	pending := func(id string) peerTunnel {
		return peerTunnel{Found: true, NodeID: id, Live: false, Latency: -1, Paths: 1}
	}
	stubProbes(t, down(), down(), pending)
	res := probePeer("10.147.1.3", "7fc64952d6", readyNet)
	if res.State != layerNoTunnel {
		t.Fatalf("仍属链路未建立: %+v", res)
	}
	if res.StateText != "链路协商中" {
		t.Fatalf("措辞应区分协商中, got %q", res.StateText)
	}
	joined := strings.Join(res.Steps, " ")
	if strings.Contains(joined, "不在本机 ZeroTier 对端列表里") {
		t.Fatal("已发现节点时不能给'不在列表'的建议")
	}
	if !strings.Contains(joined, "协商") && !strings.Contains(joined, "等待") {
		t.Fatalf("应提示是路径协商中: %v", res.Steps)
	}
	// 未握手时同样不该去发数据包
	if res.Reachable {
		t.Fatal("未握手不能判可达")
	}
}

// 本机网络没就绪时不该去骚扰对端,也不该把原因归到对方头上。
func TestProbePeerLocalDown(t *testing.T) {
	var probed bool
	stubProbes(t,
		func(string) (int, bool) { probed = true; return -1, false },
		func(string) (int, bool) { probed = true; return -1, false },
		livePeer(17))
	bad := netState{NetworkID: "n", Reason: "虚拟网卡创建失败(PORT_ERROR)", Steps: []string{"重装驱动"}}
	res := probePeer("10.147.1.1", "ce5b28bddf", bad)
	if res.State != layerLocalDown {
		t.Fatalf("应判定本机网络未就绪: %+v", res)
	}
	if probed {
		t.Fatal("本机网络未就绪时不应发起任何对端探测")
	}
	if len(res.Steps) < 2 {
		t.Fatalf("应包含本机原因与排查步骤: %v", res.Steps)
	}
}

// 4.5 秒一次的轮询不能把日志刷满:状态不变就不写,持续故障低频提醒。
func TestLogOnceDedup(t *testing.T) {
	reachMu.Lock()
	reachSeen = map[string]reachMark{}
	reachMu.Unlock()

	if write, _ := logOnce("k", layerTunnelOnly, false); !write {
		t.Fatal("首次故障应写日志")
	}
	if write, _ := logOnce("k", layerTunnelOnly, false); write {
		t.Fatal("同一故障重复应静默")
	}
	if write, _ := logOnce("k", layerNoTunnel, false); !write {
		t.Fatal("故障状态变化应写日志")
	}
	if write, recovered := logOnce("k", layerOK, true); !write || !recovered {
		t.Fatalf("恢复应写日志并标记恢复: write=%v recovered=%v", write, recovered)
	}
	if write, _ := logOnce("k", layerOK, true); write {
		t.Fatal("正常状态重复应静默")
	}
}

// 故障持续存在时要低频提醒,否则早期报错会被后来的一堆日志淹没。
func TestLogOnceReminds(t *testing.T) {
	reachMu.Lock()
	reachSeen = map[string]reachMark{}
	reachMu.Unlock()
	logOnce("k", layerTunnelOnly, false)
	reachMu.Lock()
	reachSeen["k"] = reachMark{state: layerTunnelOnly, at: time.Now().Add(-reachRemindEvery - time.Second)}
	reachMu.Unlock()
	if write, _ := logOnce("k", layerTunnelOnly, false); !write {
		t.Fatal("超过提醒间隔应再次写入")
	}
}

// 健康巡检的日志去重与成员探测用不同 key 空间,互不覆盖。
func TestLogOnceHealthSeparateKeys(t *testing.T) {
	reachMu.Lock()
	reachSeen = map[string]reachMark{}
	reachMu.Unlock()
	if logOnceHealth("net_not_ready:n", false) != true {
		t.Fatal("首次故障应写")
	}
	if logOnceHealth("net_not_ready:n", false) != false {
		t.Fatal("重复故障应静默")
	}
	if logOnceHealth("planet_down", false) != true {
		t.Fatal("不同故障应各自记录")
	}
}

func TestMapHelpers(t *testing.T) {
	m := map[string]any{"s": "x", "i": float64(3), "ss": []any{"a", "b", 1}}
	if mapStr(m, "s") != "x" || mapStr(m, "missing") != "" {
		t.Fatal("mapStr")
	}
	if mapInt(m, "i") != 3 || mapInt(m, "s") != 0 {
		t.Fatal("mapInt")
	}
	got := mapStrs(m, "ss")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("mapStrs 应跳过非字符串: %v", got)
	}
}

// 并发轮询下 logOnce 不能出现数据竞争。
func TestLogOnceConcurrent(t *testing.T) {
	reachMu.Lock()
	reachSeen = map[string]reachMark{}
	reachMu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); logOnce("k", layerOK, true) }()
	}
	wg.Wait()
}

// 一轮成员测速里,每个成员都跑一次 zerotier-cli 子进程会明显拖慢界面。
// 快照缓存必须让同一轮的多次查询只调用一次 CLI。
func TestPeerSnapshotCached(t *testing.T) {
	resetReachCache()
	var calls int
	var mu sync.Mutex
	orig := ztQuery
	ztQuery = func(args ...string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return `[{"address":"aaa","latency":12,"role":"LEAF","paths":[{}]},` +
			`{"address":"bbb","latency":-1,"role":"PLANET"}]`, nil
	}
	t.Cleanup(func() { ztQuery = orig; resetReachCache() })

	for i := 0; i < 5; i++ {
		if pt := lookupPeerCLI("aaa"); !pt.Found || pt.Latency != 12 {
			t.Fatalf("第 %d 次查询结果不对: %+v", i, pt)
		}
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("5 次查询只应调用 1 次 CLI, 实际 %d 次", got)
	}
}

// planet 判定也用同一份快照,不该再单独起一个子进程。
func TestPeerSnapshotSharedWithPlanet(t *testing.T) {
	resetReachCache()
	var calls int
	var mu sync.Mutex
	orig := ztQuery
	ztQuery = func(args ...string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return `[{"address":"aaa","latency":12,"role":"LEAF"},{"address":"p","latency":140,"role":"PLANET"}]`, nil
	}
	t.Cleanup(func() { ztQuery = orig; resetReachCache() })

	lookupPeerCLI("aaa")
	ok, lat := planetState()
	if !ok || lat != 140 {
		t.Fatalf("planet 判定错误: ok=%v lat=%d", ok, lat)
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("应共用快照只调 1 次, 实际 %d 次", got)
	}
}

// CLI 失败时不能把空快照缓存住,否则故障期间界面会一直显示"没人可达"。
func TestPeerSnapshotRetriesOnError(t *testing.T) {
	resetReachCache()
	var calls int
	var mu sync.Mutex
	orig := ztQuery
	ztQuery = func(args ...string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "", fmt.Errorf("boom")
	}
	t.Cleanup(func() { ztQuery = orig; resetReachCache() })

	lookupPeerCLI("aaa")
	lookupPeerCLI("aaa")
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("失败不应缓存,两次查询应各调一次, 实际 %d 次", got)
	}
}

// captureLog 把 log 输出临时重定向到缓冲区,返回读取最新内容的函数。
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return buf.String
}

// 节点正常(NodeReady=true)恰恰是"网络未就绪 / planet 不可达"最需要记录的时刻。
// 历史 bug:logHealth 用 logOnceHealth("node", true) 做整体早退,而该函数对健康
// 输入恒返回 false,导致节点一正常就再也不写任何健康故障。
func TestLogHealthRecordsIssuesWhenNodeReady(t *testing.T) {
	resetReachCache()
	out := captureLog(t)

	logHealth(healthReport{
		NodeReady: true,
		Issues: []issue{{
			Code:  "planet_down",
			Text:  "无法连接 ZeroTier planet 根节点",
			Steps: []string{"检查 UDP 出站"},
		}},
	})

	got := out()
	if !strings.Contains(got, "planet 根节点") {
		t.Fatalf("节点正常时也必须记录 planet 故障, got %q", got)
	}
	if !strings.Contains(got, "检查 UDP 出站") {
		t.Fatalf("故障日志必须带排查步骤, got %q", got)
	}
}

// 故障消失后要补一条"已恢复",否则日志里只剩故障、看不出后来好了。
func TestLogHealthLogsRecovery(t *testing.T) {
	resetReachCache()
	out := captureLog(t)

	logHealth(healthReport{
		NodeReady: true,
		Issues:    []issue{{Code: "planet_down", Text: "planet 不可达"}},
	})
	if !strings.Contains(out(), "planet 不可达") {
		t.Fatalf("应先记录故障, got %q", out())
	}

	// 下一轮故障消失
	logHealth(healthReport{NodeReady: true})
	if !strings.Contains(out(), "已恢复") {
		t.Fatalf("故障消失后应写一条已恢复, got %q", out())
	}
}
