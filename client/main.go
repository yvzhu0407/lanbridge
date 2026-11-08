// lanbridge 客户端:本地 127.0.0.1:8899 起 HTTP 服务,内嵌 UI,自动开浏览器。
// 职责(对照优化设计):zerotier 常驻服务 + 本地 join/leave 编排 + 远端 API 代理。
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFS embed.FS

var (
	listenAddr = envOr("LANBRIDGE_CLIENT_ADDR", "127.0.0.1:8899")
	serverBase = strings.TrimRight(envOr("LANBRIDGE_SERVER", "http://172.252.125.40:8800"), "/")
	homeDir    string // ~/.lanbridge
	ztStartErr string // ZT 实例自动启动失败原因(给 UI 展示)
	ztStartMu  sync.Mutex
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// clientConfig 是 ~/.lanbridge/config.json 的可选配置。
// 优先级:环境变量 > 配置文件 > 内置默认值。自托管者改这里即可指向自己的服务端。
type clientConfig struct {
	Server string `json:"server"` // 房间服务器地址,如 http://1.2.3.4:8800
	Listen string `json:"listen"` // 本地 UI 监听地址,默认 127.0.0.1:8899
}

func applyClientConfig() {
	b, err := os.ReadFile(filepath.Join(homeDir, "config.json"))
	if err != nil {
		return
	}
	var c clientConfig
	if json.Unmarshal(b, &c) != nil {
		log.Printf("config.json 解析失败,忽略并使用默认配置")
		return
	}
	if c.Server != "" && os.Getenv("LANBRIDGE_SERVER") == "" {
		serverBase = strings.TrimRight(c.Server, "/")
	}
	if c.Listen != "" && os.Getenv("LANBRIDGE_CLIENT_ADDR") == "" {
		listenAddr = c.Listen
	}
}

func main() {
	d, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	homeDir = filepath.Join(d, ".lanbridge")
	_ = os.MkdirAll(homeDir, 0o700)
	applyClientConfig()
	// 文件日志:Windows GUI 模式无控制台,出错只能看这里(实现见 logging.go)
	if p := setupLogging(); p != "" {
		log.Printf("日志文件: %s", p)
	} else {
		log.Printf("警告: 日志无法写入磁盘,仅输出到控制台 — %s", loggingHint())
	}
	log.Printf("=== lanbridge 客户端启动 (pid %d) ===", os.Getpid())
	// Windows: 检查管理员权限,需要时弹 UAC 提权(macOS 此函数为空)
	if checkElevation() {
		// 提权成功,新的管理员进程已启动,当前进程退出
		os.Exit(0)
	}
	if err := ensureZTReady(); err != nil {
		log.Printf("ZeroTier 启动未就绪: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "UI missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /api/config", handleConfig)
	mux.HandleFunc("POST /api/login", proxyNoAuth)
	mux.HandleFunc("POST /api/register", proxyNoAuth)
	mux.HandleFunc("GET /api/planet", proxyNoAuth)

	// 需要鉴权的远端接口:一律转发,自动带 token
	for _, p := range []string{
		"GET /api/me", "GET /api/rooms", "GET /api/room/members", "GET /api/join/status",
		"POST /api/room/heartbeat",
		"POST /api/room/create", "POST /api/room/join",
		"POST /api/room/leave", "POST /api/room/dismiss",
	} {
		mux.HandleFunc(p, proxyAuthed)
	}

	// 本地 ZeroTier 编排与诊断
	mux.HandleFunc("GET /api/zt/status", handleZTStatus)
	mux.HandleFunc("GET /api/zt/peers", handleZTPeers)
	mux.HandleFunc("GET /api/zt/health", handleZTHealth)
	mux.HandleFunc("GET /api/ping", handlePing)
	mux.HandleFunc("GET /api/logs", handleLogs)
	mux.HandleFunc("POST /api/zt/join", handleZTJoin)

	log.Printf("lanbridge 客户端 http://%s (server=%s)", listenAddr, serverBase)
	url := "http://" + listenAddr

	// 单实例:8899 被占说明已有一个客户端在跑,直接拉起已有实例的窗口后退出
	// (裸二进制双击两次 / .app 双击两次都会触发;避免两个进程抢端口互相打架)
	if resp, err := http.Get(url + "/api/config"); err == nil {
		resp.Body.Close()
		log.Print("已有 lanbridge 客户端在运行,本次启动转为唤起既有实例")
		if err := runWindow(url); err != nil {
			openBrowser(url)
		}
		return
	}

	// 房间聊天:8890 局域网直连服务(成员互发文字,不经房间服务器)
	go startChatServer()

	// 后台健康巡检:界面没打开时,网络未就绪/planet 不可达也会写进 client.log
	startHealthWatch()

	// 上次进程异常退出可能留下网卡跃点调整,趁当前还没加入房间先恢复
	restoreStaleTune()
	// 正常退出(关窗)时恢复网卡跃点
	defer restoreNetwork()

	// HTTP 服务在后台;主线程跑原生 WebView 窗口,关窗即退出
	// (WebView 初始化必须在主线程;失败则退回开浏览器)
	go func() {
		if err := http.ListenAndServe(listenAddr, mux); err != nil {
			log.Printf("HTTP 服务退出: %v", err)
			os.Exit(1)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	if err := runWindow(url); err != nil {
		log.Printf("原生窗口不可用(%v),退回浏览器", err)
		openBrowser(url)
		select {} // 保持进程
	}
}

// ---------- session ----------

type session struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

func loadToken() string {
	b, err := os.ReadFile(filepath.Join(homeDir, "session.json"))
	if err != nil {
		return ""
	}
	var s session
	_ = json.Unmarshal(b, &s)
	return s.Token
}

func saveToken(tok, name string) {
	b, _ := json.Marshal(session{Token: tok, Name: name})
	_ = os.WriteFile(filepath.Join(homeDir, "session.json"), b, 0o600)
}

// ---------- 远端代理 ----------

func handleConfig(w http.ResponseWriter, r *http.Request) {
	okJSON(w, map[string]any{"logged_in": loadToken() != "", "server": serverBase})
}

func proxyNoAuth(w http.ResponseWriter, r *http.Request) {
	// 客户端路由与远端路由的差异映射(/api/login → /api/user/login)
	remotePath := r.URL.Path
	switch remotePath {
	case "/api/login":
		remotePath = "/api/user/login"
	case "/api/register":
		remotePath = "/api/user/register"
	}
	body, _ := io.ReadAll(r.Body)
	resp, err := http.Post(serverBase+remotePath, "application/json", bytes.NewReader(body))
	if err != nil {
		errJSON(w, 502, "无法连接服务器: "+err.Error())
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	// 登录/注册成功 → 本地保存 token(data 为 {token:...})
	var out struct {
		OK   bool `json:"ok"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &out) == nil && out.OK && out.Data.Token != "" {
		if strings.HasSuffix(remotePath, "/login") || strings.HasSuffix(remotePath, "/register") {
			saveToken(out.Data.Token, "")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}

func proxyAuthed(w http.ResponseWriter, r *http.Request) {
	tok := loadToken()
	if tok == "" {
		errJSON(w, 401, "未登录")
		return
	}
	body, _ := io.ReadAll(r.Body)
	remoteURL := serverBase + r.URL.Path
	if r.URL.RawQuery != "" {
		remoteURL += "?" + r.URL.RawQuery
	}
	req, _ := http.NewRequest(r.Method, remoteURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		errJSON(w, 502, "无法连接服务器: "+err.Error())
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}

// ensureZTReady:按需检查并拉起 ZeroTier 实例。
// 启动时的一次性初始化可能因权限/驱动/安全软件失败,以前只能重开程序;
// 现在 status/join 等入口都会再试一次,失败原因写进 ztStartErr 给 UI 展示。
func ensureZTReady() error {
	ztStartMu.Lock()
	defer ztStartMu.Unlock()

	// 驱动文件必须在 zt-node 启动前就在家目录里(EthernetTap 只认 homePath)。
	// 这一步不能放在 ztRunning() 之后:从旧版本升级上来时实例已经在跑,
	// 早返回会让驱动永远补不到位。这里只做存在性检查,开销很小。
	if err := ensureDriverFiles(); err != nil {
		ztStartErr = err.Error()
		return fmt.Errorf("TAP 驱动文件准备失败: %w", err)
	}
	if ztRunning() {
		ztStartErr = ""
		return nil
	}
	if err := ensureZTRuntime(); err != nil {
		ztStartErr = err.Error()
		return fmt.Errorf("ZeroTier 运行文件准备失败: %w", err)
	}
	if err := ensureZTInstance(); err != nil {
		ztStartErr = err.Error()
		return fmt.Errorf("ZeroTier 实例初始化失败: %w", err)
	}
	// 标准单实例:ZeroTier 守护进程独占 TCP 控制端口(-p 同时用于 UDP 与
	// TCP/HTTP,见 zt-node -h)。端口已监听说明已有实例在跑或正在启动——只能
	// 等它,绝不能再拉一个:第二个实例会抢同一块 TAP,冒出
	// "ERROR: unable to remove ip address",让连接反复抖动。只有确实没人监听
	// 时才拉起新实例。
	if !ztDaemonListening() {
		if err := startZTInstance(); err != nil {
			ztStartErr = err.Error()
			return fmt.Errorf("启动 ZeroTier 失败: %w", err)
		}
	} else {
		log.Printf("ZeroTier 实例已在启动中,等待其就绪(不再重复拉起)")
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ztRunning() {
			ztStartErr = ""
			log.Printf("ZeroTier 实例已就绪")
			return nil
		}
		time.Sleep(time.Second)
	}
	ztStartErr = "ZeroTier 实例启动后未响应(可能被安全软件拦截,或驱动未安装)"
	return errors.New(ztStartErr)
}

// ---------- ZeroTier 本地编排 ----------

// ztHome / ztCLI:编排「独立 ZeroTier 实例」(不碰系统 ZeroTier,两者可并存)
// 桥联实例家目录 = ~/.lanbridge/zt,端口 9995
// 环境变量 ZT_HOME / ZT_PORT 可覆盖
func ztHome() string {
	if v := os.Getenv("ZT_HOME"); v != "" {
		return v
	}
	return filepath.Join(homeDir, "zt")
}

func ztPort() string {
	if v := os.Getenv("ZT_PORT"); v != "" {
		return v
	}
	return "9995"
}

// ensureZTInstance:首次运行时初始化实例目录(planet + local.conf + TapAgent)
func ensureZTInstance() error {
	home := ztHome()
	needPlanet := false
	if _, err := os.Stat(filepath.Join(home, "planet")); err != nil {
		needPlanet = true
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if needPlanet {
		// 从服务端拉 planet
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(serverBase + "/api/planet")
		if err != nil {
			return fmt.Errorf("下载 planet 失败: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("下载 planet 失败: HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("读取 planet 失败: %w", err)
		}
		if len(b) < 64 {
			return fmt.Errorf("下载的 planet 内容不完整")
		}
		if err := os.WriteFile(filepath.Join(home, "planet"), b, 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(home, "local.conf")); err != nil {
		_ = os.WriteFile(filepath.Join(home, "local.conf"), []byte(`{"settings":{"primaryPort":`+ztPort()+`}}`), 0o644)
	}
	// macOS:MacEthernetTapAgent 必须在实例目录内(嵌入式副本已在 zt_darwin.go 解压到运行目录)
	if runtime.GOOS == "darwin" {
		src := filepath.Join(ztRuntimeDir(), "MacEthernetTapAgent")
		dst := filepath.Join(home, "MacEthernetTapAgent")
		if _, err := os.Stat(dst); err != nil {
			if b, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(dst, b, 0o755)
			}
		}
	}
	return nil
}

// ztRunning:实例服务是否在跑(探测 CLI info)
func ztRunning() bool {
	_, err := ztRun("info")
	return err == nil
}

// ztControlPort 返回本机守护进程的控制端口。ZeroTier 会把实际绑定的端口写进
// <home>/zerotier-one.port(-p0 随机端口时必须靠它发现);读不到则退回 -p 配置值。
func ztControlPort() string {
	if b, err := os.ReadFile(filepath.Join(ztHome(), "zerotier-one.port")); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return p
		}
	}
	return ztPort()
}

// ztDaemonListening 探测本地控制端口是否已被 ZeroTier 守护进程监听。
// ZeroTier 靠守护进程独占该端口来保证单实例(第二个同端口实例 bind 失败退出),
// 所以"端口可连"就等于"已有实例在跑或正在启动"。客户端据此决定等待还是拉起,
// 不再自己追踪子进程。
func ztDaemonListening() bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", ztControlPort()), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// startZTInstance:拉起嵌入式 ZeroTier 实例服务
// darwin:需要 root(建 feth 网卡),经 osascript 弹管理员框
// windows:需要管理员运行本程序,直接子进程拉起
func startZTInstance() error {
	if err := ensureZTRuntime(); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		bin := filepath.Join(ztRuntimeDir(), "zt-node")
		// osascript 里 shell 命令是双层转义:AppleScript 字符串一层,sh 一层
		script := `do shell script "'` + bin + `' -p` + ztPort() + ` '` + ztHome() + `' >> /tmp/lanbridge-zt.log 2>&1 &" with administrator privileges`
		cmd := exec.Command("osascript", "-e", script)
		return cmd.Run()
	case "windows":
		if err := installDriver(); err != nil {
			return fmt.Errorf("驱动安装失败: %w", err)
		}
		bin := filepath.Join(ztRuntimeDir(), "zt-node.exe")
		// -C:从命令行运行(不装服务);-U:不尝试权限分离(我们已要求管理员)
		lf, err := os.OpenFile(ztNodeLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			lf = nil
		}
		cmd := hideCmd(bin, "-C", "-U", "-p"+ztPort(), ztHome())
		if lf != nil {
			cmd.Stdout, cmd.Stderr = lf, lf
		}
		err = cmd.Start()
		if lf != nil {
			_ = lf.Close()
		}
		return err
	default:
		return fmt.Errorf("不支持的平台")
	}
}

func ztCLI() []string {
	if v := os.Getenv("ZT_CLI"); v != "" { // 整个命令覆盖
		return strings.Fields(v)
	}
	// 嵌入式 zt-node 兼 CLI:-q 进入查询模式,-D 指定家目录(authtoken 自动探测),-p 端口
	// -D/-p 与值之间不能有空格(zerotier-cli 的 getopt 约定)
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(ztRuntimeDir(), "zt-node.exe"), "-q", "-D" + ztHome(), "-p" + ztPort()}
	}
	return []string{filepath.Join(ztRuntimeDir(), "zt-node"), "-q", "-D" + ztHome(), "-p" + ztPort()}
}

// ztNodeLogPath:zt-node 服务进程的日志文件
func ztNodeLogPath() string { return filepath.Join(homeDir, "zt-node.log") }

func ztRun(args ...string) (string, error) {
	base := ztCLI()
	cmd := hideCmd(base[0], append(base[1:], args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// handleZTStatus: 节点ID + 当前加入的网络与 IP(JSON 输出更可靠)
func handleZTStatus(w http.ResponseWriter, r *http.Request) {
	if err := ensureZTReady(); err != nil {
		errJSON(w, 503, "ZeroTier 未就绪: "+err.Error())
		return
	}
	info, err := ztRun("info")
	if err != nil {
		msg := "ZeroTier 未运行"
		if ztStartErr != "" {
			msg += ": " + ztStartErr
		}
		errJSON(w, 500, msg)
		return
	}
	// "200 info <nodeid> <version> <online>"
	parts := strings.Fields(info)
	nodeID := ""
	if len(parts) >= 3 {
		nodeID = parts[2]
	}
	listOut, _ := ztRun("-j", "listnetworks")
	var raw []map[string]any
	nets := []map[string]any{}
	if json.Unmarshal([]byte(listOut), &raw) == nil {
		for _, m := range raw {
			net := map[string]any{
				"network_id": m["id"], "status": m["status"], "device": m["portDeviceName"],
			}
			if ips, ok := m["assignedAddresses"].([]any); ok {
				s := make([]string, 0, len(ips))
				for _, ip := range ips {
					s = append(s, fmt.Sprint(ip))
				}
				net["ips"] = s
			}
			nets = append(nets, net)
		}
	}
	okJSON(w, map[string]any{"node_id": nodeID, "networks": nets})
}

// handleZTPeers: 获取 ZeroTier 节点已连接/发现的对端列表(供链路拓扑展示)
func handleZTPeers(w http.ResponseWriter, r *http.Request) {
	listOut, err := ztRun("-j", "peers")
	if err != nil {
		okJSON(w, []any{})
		return
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(listOut), &raw); err != nil {
		okJSON(w, []any{})
		return
	}
	okJSON(w, raw)
}

// handleLogs: 获取客户端最近运行日志(尾部 120 行)
// 同时回传实际路径,方便用户直接去文件管理器里找(写不进用户目录时会退到别处)。
func handleLogs(w http.ResponseWriter, r *http.Request) {
	if logFilePath == "" {
		okJSON(w, map[string]any{"logs": "", "path": "", "error": loggingHint()})
		return
	}
	b, err := os.ReadFile(logFilePath)
	if err != nil {
		okJSON(w, map[string]any{"logs": "", "path": logFilePath, "error": err.Error()})
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	okJSON(w, map[string]any{"logs": strings.Join(lines, "\n"), "path": logFilePath})
}

// handlePing: 分层探测目标成员(本机网络 → ZeroTier 隧道 → 真实数据面)。
// 达标的是"真实 ICMP",peers 里的 latency 只作为隧道层的参考值下发,
// 绝不再拿它冒充 ping 结果:那样会出现界面绿色 17ms、实际一个包都收不到。
func handlePing(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	nodeID := r.URL.Query().Get("node_id")
	netID := r.URL.Query().Get("net")
	if ip == "" && nodeID == "" {
		errJSON(w, 400, "缺少 ip 或 node_id")
		return
	}
	// 本机网络状态决定探测是否有意义;net 缺省时取第一个网络
	local := bootNetState(netID)
	res := probePeer(ip, nodeID, local)
	logProbe(probeDesc(ip, nodeID), res)
	okJSON(w, res)
}

// bootNetState 取指定网络(缺省为第一个)的就绪状态。
func bootNetState(netID string) netState {
	states := localNetStates()
	if len(states) == 0 {
		return netState{Reason: "本机没有加入任何房间网络", Steps: []string{
			"先在大厅创建或加入一个房间",
		}}
	}
	if netID == "" {
		return states[0]
	}
	for _, s := range states {
		if s.NetworkID == netID {
			return s
		}
	}
	return netState{Reason: "本机未加入该房间网络", Steps: []string{
		"退出房间后重新进入,让客户端重新 join 该网络",
	}}
}

func probeDesc(ip, nodeID string) string {
	if ip != "" && nodeID != "" {
		return ip + "(" + nodeID + ")"
	}
	if ip != "" {
		return ip
	}
	return nodeID
}

// handleZTHealth: 本机虚拟网络 / planet / 各层故障与排查建议,供控制台展示。
func handleZTHealth(w http.ResponseWriter, r *http.Request) {
	rep := buildHealth()
	logHealth(rep)
	okJSON(w, rep)
}

// handleZTJoin: 先离开本地其它网络,再 join 新网络(避免新旧实例抢网卡)
// network_id 为空 + leave=all:仅离开所有网络(离开房间时用)
func handleZTJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NetworkID string `json:"network_id"`
		Leave     string `json:"leave"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, 400, "参数不合法")
		return
	}
	if req.NetworkID == "" && req.Leave != "all" {
		errJSON(w, 400, "参数不合法")
		return
	}
	// 离开旧网络(服务端也会收回授权,双保险);用 JSON 解析避免列数变化
	listOut, _ := ztRun("-j", "listnetworks")
	var raw []map[string]any
	if json.Unmarshal([]byte(listOut), &raw) == nil {
		for _, m := range raw {
			id, _ := m["id"].(string)
			if id != "" && id != req.NetworkID {
				_, _ = ztRun("leave", id)
			}
		}
	}
	if req.NetworkID == "" {
		restoreNetwork()
		okJSON(w, map[string]any{"left": true})
		return
	}
	if err := ensureZTReady(); err != nil {
		errJSON(w, 503, "ZeroTier 未就绪: "+err.Error())
		return
	}
	if out, err := ztRun("join", req.NetworkID); err != nil {
		errJSON(w, 500, "join 失败: "+out)
		return
	}
	// join 的退出码不能作为成功依据:驱动缺失时 CLI 照样返回 "200 join OK",
	// 但网络根本没进状态表(实测 listnetworks 仍为 [])。必须再查一次确认。
	if net, _ := findNetwork(req.NetworkID); net == nil {
		msg := "ZeroTier 拒绝加入网络: join 返回成功但网络未出现在本地列表"
		if pe := ztPortErrorHint(); pe != "" {
			msg += " — " + pe
		}
		log.Printf("[join] %s", msg)
		errJSON(w, 500, msg)
		return
	}
	go func() {
		// 网卡创建耗时波动很大:实测有机器要 50 秒以上(planet 往返 400ms 时更慢)。
		// 等不到不能就此放弃——跃点调优虽然只是锦上添花,但漏做会让游戏流量
		// 走不到隧道。所以这里放宽到 90 秒,并单独重试一次。
		if dev := waitNetworkDevice(req.NetworkID, 90*time.Second); dev != "" {
			tuneNetwork(dev)
		} else {
			log.Printf("[网络调优] 等待网卡超时(90s),跳过跃点调整;网络仍可用,只是游戏可能不走隧道")
		}
		diagnoseZTCommon("加入网络 " + req.NetworkID)
	}()
	okJSON(w, map[string]any{"joined": true})
}

// findNetwork 从 listnetworks 里取指定网络的条目;不存在返回 nil。
func findNetwork(networkID string) (map[string]any, error) {
	out, err := ztRun("-j", "listnetworks")
	if err != nil {
		return nil, err
	}
	var nets []map[string]any
	if err := json.Unmarshal([]byte(out), &nets); err != nil {
		return nil, err
	}
	for _, n := range nets {
		if id, _ := n["id"].(string); id == networkID {
			return n, nil
		}
	}
	return nil, nil
}

// ztPortErrorHint 把常见的建卡失败原因翻译成一句可操作的话。
// port_error_log.txt 是 ZeroTier 自己记的 TAP 创建失败日志(Windows 专用)。
func ztPortErrorHint() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	logPath := filepath.Join(ztHome(), "port_error_log.txt")
	b, err := os.ReadFile(logPath)
	if err != nil || len(b) == 0 {
		return ""
	}
	if strings.Contains(string(b), "SetupDiGetINFClassA") {
		return "TAP 驱动 INF 读不到:确认 zttap300.inf/.sys/.cat 在 " + ztHome() + " 下"
	}
	return "详见 " + logPath
}

// waitNetworkDevice 轮询 listnetworks，等 ZeroTier 把网卡建出来并返回设备名。
// 超时返回空串：调优是锦上添花，拿不到网卡名只跳过，不影响已建立的连接。
func waitNetworkDevice(networkID string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if out, err := ztRun("-j", "listnetworks"); err == nil {
			var nets []map[string]any
			if json.Unmarshal([]byte(out), &nets) == nil {
				for _, n := range nets {
					if n["id"] == networkID {
						if dev, _ := n["portDeviceName"].(string); dev != "" {
							return dev
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(time.Second)
	}
}

// diagnoseZTCommon:平台无关的网络健康诊断(join 后排障用,写 client.log)
func diagnoseZTCommon(reason string) {
	log.Printf("[诊断] ==== %s ====", reason)
	out, err := ztRun("-j", "listnetworks")
	if err != nil {
		log.Printf("[诊断] ZeroTier 实例无响应: %v(日志见 %s)", err, ztNodeLogPath())
		return
	}
	var nets []map[string]any
	if json.Unmarshal([]byte(out), &nets) != nil {
		return
	}
	if len(nets) == 0 {
		log.Print("[诊断] 未加入任何桥联网络")
	}
	for _, n := range nets {
		dev, _ := n["portDeviceName"].(string)
		st, _ := n["status"].(string)
		log.Printf("[诊断] 网络 %v 状态=%v 网卡=%v IP=%v 路由=%v",
			n["id"], st, dev, n["assignedAddresses"], n["routes"])
		switch st {
		case "PORT_ERROR":
			log.Print("[诊断] 网卡创建失败(PORT_ERROR):TAP 驱动未装好或被安全软件拦截,重装驱动或给本程序加白名单")
		case "REQUESTING_CONFIGURATION":
			log.Print("[诊断] 正在等待服务端下发配置:服务端不可达(检查 UDP 9995 出站)或 planet 不匹配")
		case "ACCESS_DENIED":
			log.Print("[诊断] 服务端拒绝授权:房间已解散或成员被移除")
		case "OK":
			log.Print("[诊断] 网络配置正常(OK);若仍 ping 不通,看对方侧日志与防火墙")
		}
	}
	// peers:判断与 planet 根/对方节点的链路
	if pout, err := ztRun("-j", "peers"); err == nil {
		var peers []map[string]any
		if json.Unmarshal([]byte(pout), &peers) == nil {
			leafs, pending := 0, 0
			for _, p := range peers {
				role, _ := p["role"].(string)
				lat, _ := p["latency"].(float64)
				paths, _ := p["paths"].([]any)
				switch role {
				case "PLANET":
					log.Printf("[诊断] planet 根 %v 延迟=%vms 路径数=%d(延迟<0 说明服务端不可达)", p["address"], lat, len(paths))
				case "LEAF":
					leafs++
					if lat < 0 {
						pending++
						log.Printf("[诊断] 房间节点 %v 已发现但未握手(路径数=%d):链路还在建立,通常再等几秒即可", p["address"], len(paths))
						continue
					}
					log.Printf("[诊断] 房间节点 %v 延迟=%vms 链路已通", p["address"], lat)
				}
			}
			if leafs == 0 {
				log.Print("[诊断] 尚未发现房间其他节点:对方可能没加入,或双方都在线但还没互发流量(ping 一次即可触发)")
			} else if pending > 0 {
				// "在列表但没握手"和"不在列表"是两种完全不同的故障，
				// 排查方向也不同，不能混为一谈。
				log.Printf("[诊断] %d 个节点尚未握手:Zerotier 正在协商路径,若持续数分钟仍如此,对方 UDP 出站可能被拦", pending)
			}
		}
	}
	platformDiagnose()
	log.Print("[诊断] ==== 诊断结束 ====")
}

// ---------- 浏览器 ----------

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = hideCmd("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func okJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": v})
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
