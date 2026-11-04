package main

// chat.go：房间内文字聊天的局域网点对点服务（端口 8890）。
//
// 设计要点：
//   - 数据面完全不经房间服务器：浏览器把消息直接 POST 到房间内每个成员的虚拟 IP。
//   - 每个客户端同时是发送方和接收方，本地内存保留最近 500 条消息供他人拉取。
//   - 不做离线投递、不做历史持久化——这里只保证"在线成员之间"的实时文字互通。
//   - 准入：请求携带的 ZeroTier 网络 ID 必须是本机已加入且状态 OK 的网络，
//     且连接的实际本地目标地址属于该网络分配地址、来源地址落在同一网段内。
//     这只是网络层面的准入，不是成员身份认证（同网段节点仍可互相访问）。

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const chatPort = ":8890"

// chatMsg 是一条聊天消息。目前只支持 text；保留 kind 字段便于二次开发扩展。
type chatMsg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	TS   int64  `json:"ts"`
	Kind string `json:"kind"` // 目前固定为 text
	Text string `json:"text,omitempty"`
}

var (
	chatMu   sync.Mutex
	chatMsgs = make(map[string][]chatMsg) // key: ZeroTier 网络 ID

	chatNetMu sync.Mutex
	chatNets  []chatNetwork
	chatNetAt time.Time
)

// chatNetwork 保存 ZeroTier 返回的分配地址及其网段。
type chatNetwork struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Addresses []string `json:"assignedAddresses"`
}

// chatNetworks 读取本机 ZeroTier 网络列表，带 3 秒缓存；失败时清空缓存，
// 避免 CLI 不可用时继续沿用过期准入信息。
func chatNetworks() []chatNetwork {
	chatNetMu.Lock()
	defer chatNetMu.Unlock()
	if time.Since(chatNetAt) < 3*time.Second {
		return chatNets
	}
	out, err := ztRun("-j", "listnetworks")
	var nets []chatNetwork
	if err != nil || json.Unmarshal([]byte(out), &nets) != nil {
		nets = nil
	}
	chatNets = nets
	chatNetAt = time.Now()
	return chatNets
}

// chatNetworkAllows 使用连接的实际本地地址而非 Host 或转发头，防止客户端伪造目标地址。
func chatNetworkAllows(n chatNetwork, localAddr, remoteAddr string) bool {
	localHost, _, err := net.SplitHostPort(localAddr)
	if err != nil {
		return false
	}
	remoteHost, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	local, err := netip.ParseAddr(localHost)
	if err != nil {
		return false
	}
	remote, err := netip.ParseAddr(remoteHost)
	if err != nil {
		return false
	}
	local, remote = local.Unmap(), remote.Unmap()
	if local.IsLoopback() && remote.IsLoopback() {
		return true
	}
	for _, address := range n.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil {
			continue
		}
		assigned := prefix.Addr().Unmap()
		if local == assigned && prefix.Contains(remote) {
			return true
		}
	}
	return false
}

// chatAllowed 统一 CORS 应答并执行网络准入；OPTIONS 预检直接放行。
func chatAllowed(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	id := r.URL.Query().Get("net")
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if id != "" && ok && local != nil {
		for _, n := range chatNetworks() {
			if n.ID == id && n.Status == "OK" && chatNetworkAllows(n, local.String(), r.RemoteAddr) {
				return true
			}
		}
	}
	errJSON(w, http.StatusForbidden, "聊天校验失败：对方当前不在此房间网络中")
	return false
}

func handleChatWho(w http.ResponseWriter, r *http.Request) {
	if !chatAllowed(w, r) {
		return
	}
	name := "玩家"
	if b, err := os.ReadFile(filepath.Join(homeDir, "session.json")); err == nil {
		var s session
		if json.Unmarshal(b, &s) == nil && s.Name != "" {
			name = s.Name
		}
	}
	okJSON(w, map[string]any{"name": name})
}

// handleChatSend 保存一条消息并返回 stored=<消息 ID>，发送方据此判定送达。
func handleChatSend(w http.ResponseWriter, r *http.Request) {
	if !chatAllowed(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		errJSON(w, 405, "方法不允许")
		return
	}
	var m chatMsg
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&m); err != nil ||
		m.ID == "" || m.Kind != "text" || strings.TrimSpace(m.Text) == "" {
		errJSON(w, 400, "消息不合法")
		return
	}
	if m.TS == 0 {
		m.TS = time.Now().UnixMilli()
	}
	netID := r.URL.Query().Get("net")
	chatMu.Lock()
	msgs := append(chatMsgs[netID], m)
	if len(msgs) > 500 {
		msgs = msgs[len(msgs)-500:]
	}
	chatMsgs[netID] = msgs
	chatMu.Unlock()
	okJSON(w, map[string]any{"stored": m.ID})
}

func handleChatMessages(w http.ResponseWriter, r *http.Request) {
	if !chatAllowed(w, r) {
		return
	}
	chatMu.Lock()
	roomMsgs := chatMsgs[r.URL.Query().Get("net")]
	msgs := make([]chatMsg, len(roomMsgs))
	copy(msgs, roomMsgs)
	chatMu.Unlock()
	okJSON(w, map[string]any{"messages": msgs})
}

func startChatServer() {
	mux := http.NewServeMux()
	// 方法约束在 handler 内部做（chatAllowed 先接住 OPTIONS 预检）
	mux.HandleFunc("/chat/who", handleChatWho)
	mux.HandleFunc("/chat/send", handleChatSend)
	mux.HandleFunc("/chat/messages", handleChatMessages)
	log.Printf("房间聊天服务监听 %s（仅桥联网络内可达）", chatPort)
	if err := http.ListenAndServe(chatPort, mux); err != nil {
		log.Printf("聊天服务启动失败（端口被占用？）: %v", err)
	}
}
