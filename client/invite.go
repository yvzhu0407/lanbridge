package main

// invite.go: 邀请链接 lanbridge://join/<房间号>。
//
// 房主点「邀请」复制链接，对方点链接 → 系统调起 LANBridge → 自动加入同一个房间。
// 服务端不参与：大厅本来就列出全部房间，/api/room/join 也已经能用，
// 链接只是把「在大厅里找到这个房间再点加入」缩短成一次点击。
//
// 链接解析在客户端做，两个来源：
//   - 冷启动：命令行参数里带链接(系统拉起新进程)
//   - 热启动：已经有客户端在跑，第二个进程把参数 HTTP 转发给 /api/invite
//
// 房间号来自链接，不能直接信：加入前一律用大厅的房间列表核对，
// 房间是否设了密码也以服务端返回为准。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var invitePattern = regexp.MustCompile(`(?i)lanbridge://join/(\d+)`)
var bareRoomPattern = regexp.MustCompile(`#?\s*(\d{1,9})`)

var (
	inviteMu      sync.Mutex
	pendingInvite int64 // 等待界面取走的房间号；0 = 没有
)

// parseInvite 从任意文本里取出房间号。
// 只有带 lanbridge:// 的文本才按链接解析；其余按"手抄的房间号"处理，
// 因此纯数字(以及"房间号：78"这种整句)也能用。
func parseInvite(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(strings.ToLower(s), "lanbridge://") {
		m := invitePattern.FindStringSubmatch(s)
		if m == nil {
			return 0, false
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		return id, err == nil && id > 0
	}
	if m := bareRoomPattern.FindStringSubmatch(s); m != nil {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

// setPendingInvite 记录一条邀请，等界面来取。
func setPendingInvite(roomID int64) {
	inviteMu.Lock()
	pendingInvite = roomID
	inviteMu.Unlock()
	LogDev("[邀请] 收到房间 #%d 的邀请", roomID)
}

// takePendingInvite 取走邀请(取走即清空，避免界面反复处理同一条)。
func takePendingInvite() (int64, bool) {
	inviteMu.Lock()
	defer inviteMu.Unlock()
	if pendingInvite == 0 {
		return 0, false
	}
	id := pendingInvite
	pendingInvite = 0
	return id, true
}

// handleInvitePost 接收另一个进程转来的链接(热启动)，或界面手动粘贴。
func handleInvitePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, ok := parseInvite(req.URL)
	if !ok {
		errJSON(w, 400, "不是有效的邀请链接")
		return
	}
	setPendingInvite(id)
	okJSON(w, map[string]any{"room_id": id})
}

// handleInviteGet 供界面轮询：有新邀请时返回房间号，没有则返回空对象。
func handleInviteGet(w http.ResponseWriter, r *http.Request) {
	if id, ok := takePendingInvite(); ok {
		okJSON(w, map[string]any{"room_id": id})
		return
	}
	okJSON(w, map[string]any{})
}

// inviteFromArgs 从命令行里找出邀请链接，冷启动时调用。
// 用 %1 调起时参数就是完整链接，这里同时接受带引号/带尾随字符的形式。
func inviteFromArgs(args []string) (int64, bool) {
	for _, a := range args {
		if !strings.Contains(strings.ToLower(a), "lanbridge://") {
			continue
		}
		if id, ok := parseInvite(a); ok {
			return id, true
		}
	}
	return 0, false
}

// forwardInvite 把本进程命令行里的邀请转发给已经在跑的实例。
// 冷启动与热启动的分界就在这里：端口被占时不会有新进程去处理链接，
// 不转发的话用户点链接看起来"没反应"。
func forwardInvite(baseURL string) {
	link := ""
	for _, a := range os.Args[1:] {
		if strings.Contains(strings.ToLower(a), "lanbridge://") {
			link = a
			break
		}
	}
	if link == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"url": link})
	resp, err := http.Post(baseURL+"/api/invite", "application/json", bytes.NewReader(body))
	if err != nil {
		LogDev("[邀请] 转发给运行中的实例失败: %v", err)
		return
	}
	resp.Body.Close()
	LogDev("[邀请] 已把链接转发给运行中的实例")
}
