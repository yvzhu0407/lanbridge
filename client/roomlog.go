package main

// roomlog.go: 让诊断日志按房间分段。
//
// client.log 是整个进程生命周期追加写的,换了房间之后,上一个房间的
// "网络 xxx 未就绪 / 连不上 10.147.5.2" 还留在文件里。用户在诊断面板里看到
// 一堆历史房间的记录,分不清哪条是当前房间的;而且界面上出现的是网络 ID
// (bd5b8ad96c00001e)这种 hash,用户对不上号。
//
// 这里在房间切换时写一条分界标记,读取时只保留最后一个标记之后的日志,
// 并把当前网络 ID 替换成房间号。日志文件本身仍然完整保留,只是视图变了。

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// roomMark 是分界标记的固定前缀,handleLogs 靠它切分日志。
const roomMark = "[房间] "

var (
	roomMu     sync.Mutex
	roomLogID  int
	roomLogNet string
)

// setRoomLog 记录当前所在房间;房间变化时写一条分界标记。
func setRoomLog(roomID int, networkID string) {
	if roomID <= 0 {
		clearRoomLog()
		return
	}
	roomMu.Lock()
	defer roomMu.Unlock()
	if roomLogID == roomID && roomLogNet == networkID {
		return
	}
	roomLogID, roomLogNet = roomID, networkID
	// 标记行只写房间号:写网络 ID 的话,下面的 masking 会把它替换成
	// "房间#69 (房间#69)" 这种重复内容。
	LogDev("%s进入 #%d", roomMark, roomID)
}

// clearRoomLog 在离开/解散房间后停止分段,后续日志不再归属任何房间。
func clearRoomLog() {
	roomMu.Lock()
	defer roomMu.Unlock()
	if roomLogID == 0 && roomLogNet == "" {
		return
	}
	roomLogID, roomLogNet = 0, ""
	LogDev("%s离开", roomMark)
}

func roomLogSnapshot() (int, string) {
	roomMu.Lock()
	defer roomMu.Unlock()
	return roomLogID, roomLogNet
}

// noteRoomFromResponse 从鉴权代理的响应里同步"当前房间"。
// 放在代理层而不是各处 UI 调用点:创建/加入/离开/解散最终都会经过这里,
// 不会漏掉某条路径而导致日志分段错位。
func noteRoomFromResponse(path string, body []byte) {
	switch path {
	case "/api/join/status", "/api/room/create", "/api/room/join",
		"/api/room/leave", "/api/room/dismiss":
	default:
		return
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Joined    bool   `json:"joined"`
			RoomID    int    `json:"room_id"`
			NetworkID string `json:"network_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil || !env.OK {
		return
	}
	switch path {
	case "/api/join/status":
		if env.Data.Joined {
			setRoomLog(env.Data.RoomID, env.Data.NetworkID)
		} else {
			clearRoomLog()
		}
	case "/api/room/create", "/api/room/join":
		setRoomLog(env.Data.RoomID, env.Data.NetworkID)
	default: // leave / dismiss
		clearRoomLog()
	}
}

// isRoomMark 判断一行(已剥掉 d| 前缀)是否是分界标记。
func isRoomMark(line string) bool {
	return strings.Contains(line, roomMark)
}

// clipFromLast 只保留最后一个满足 match 的标记之后的行。
// 没有匹配时原样返回(旧日志/首次启动),不做猜测。
func clipFromLast(lines []string, match func(string) bool) []string {
	for i := len(lines) - 1; i >= 0; i-- {
		if match(strings.TrimPrefix(lines[i], "d|")) {
			return lines[i:]
		}
	}
	return lines
}

// markSessionStart 在进程启动时打一条分界标记。
// 不这样做的话,首次运行还没有"进入房间"的记录,面板会把上一次运行的日志
// 也算进当前房间,用户看到的仍然是历史记录。
func markSessionStart() {
	LogDev("%s启动", roomMark)
}

// clipLogs 按当前房间裁剪日志:
// 在房间里 → 只看这个房间的记录;不在房间 → 看本次启动以来的全部记录,
// 这样离开房间后诊断面板不会只剩一行"离开"。
func clipLogs(lines []string) []string {
	if id, _ := roomLogSnapshot(); id > 0 {
		return clipFromLast(lines, isRoomMark)
	}
	return clipFromLast(lines, func(l string) bool {
		return strings.Contains(l, roomMark+"启动")
	})
}

// maskNetworkID 把网络 hash 换成房间号:用户只认房间号,认不出 24 位网络 ID。
func maskNetworkID(text string, roomID int, networkID string) string {
	if networkID == "" || roomID <= 0 {
		return text
	}
	return strings.ReplaceAll(text, networkID, "房间#"+strconv.Itoa(roomID))
}
