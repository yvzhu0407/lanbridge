package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 日志要按房间分段:换房间后,上一个房间的故障记录不能再出现在面板里。
func TestLogsClipToCurrentRoom(t *testing.T) {
	origPath := logFilePath
	defer func() { logFilePath = origPath }()
	origID, origNet := roomLogID, roomLogNet
	defer func() { roomLogID, roomLogNet = origID, origNet }()

	dir := t.TempDir()
	logFilePath = filepath.Join(dir, "client.log")
	content := "d|2026/11/09 10:00:00 [房间] 进入 #11 (netA)\n" +
		"d|2026/11/09 10:00:05 [诊断] 网络 netA 状态=ACCESS_DENIED\n" +
		"u|2026-11-09T10:00:06|warn|net_not_ready|上个房间的旧故障\n" +
		"d|2026/11/09 10:01:00 [房间] 离开\n" +
		"d|2026/11/09 10:01:05 [房间] 进入 #12 (netB)\n" +
		"d|2026/11/09 10:01:10 [诊断] 网络 netB 状态=OK\n"
	if err := os.WriteFile(logFilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// 当前在 #12,日志应按最后一个房间标记裁剪
	roomLogID, roomLogNet = 12, "netB"

	logs := callLogs(t, "dev")
	if strings.Contains(logs, "上个房间的旧故障") || strings.Contains(logs, "netA") {
		t.Fatalf("换房间后不应再出现上个房间的日志: %q", logs)
	}
	// netB 会被替换成房间号,所以这里认"房间#12"
	if !strings.Contains(logs, "房间#12") {
		t.Fatalf("当前房间的日志被误删: %q", logs)
	}
}

// 面板里不应该出现网络 hash,要换成房间号。
func TestLogsMaskNetworkID(t *testing.T) {
	origPath := logFilePath
	defer func() { logFilePath = origPath }()
	origID, origNet := roomLogID, roomLogNet
	defer func() { roomLogID, roomLogNet = origID, origNet }()

	dir := t.TempDir()
	logFilePath = filepath.Join(dir, "client.log")
	content := "d|2026/11/09 10:00:00 [房间] 进入 #12 (netB)\n" +
		"d|2026/11/09 10:00:05 [诊断] 网络 netB 状态=ACCESS_DENIED\n"
	if err := os.WriteFile(logFilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	roomLogID, roomLogNet = 12, "netB"
	logs := callLogs(t, "dev")
	if strings.Contains(logs, "netB") {
		t.Fatalf("应把网络 ID 换成房间号: %q", logs)
	}
	if !strings.Contains(logs, "房间#12") {
		t.Fatalf("缺少房间号标注: %q", logs)
	}
}

// 离开房间后不能只剩一行"离开":面板要能看到本次启动以来的记录。
func TestLogsAfterLeaveShowSession(t *testing.T) {
	origPath := logFilePath
	defer func() { logFilePath = origPath }()
	origID, origNet := roomLogID, roomLogNet
	defer func() { roomLogID, roomLogNet = origID, origNet }()

	dir := t.TempDir()
	logFilePath = filepath.Join(dir, "client.log")
	content := "d|2026/11/09 09:00:00 [房间] 进入 #5 (netOld)\n" +
		"d|2026/11/09 09:00:05 上个会话的旧记录\n" +
		"d|2026/11/09 10:00:00 [房间] 启动\n" +
		"d|2026/11/09 10:00:05 本次启动的调试信息\n" +
		"d|2026/11/09 10:00:10 [房间] 进入 #6 (netCur)\n" +
		"d|2026/11/09 10:00:15 房间内诊断\n" +
		"d|2026/11/09 10:05:00 [房间] 离开\n"
	if err := os.WriteFile(logFilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不在房间:保留本次启动以来的全部日志,但不含上个会话的
	roomLogID, roomLogNet = 0, ""
	logs := callLogs(t, "dev")
	if strings.Contains(logs, "上个会话的旧记录") {
		t.Fatalf("不应出现上个会话的日志: %q", logs)
	}
	if !strings.Contains(logs, "本次启动的调试信息") || !strings.Contains(logs, "房间内诊断") {
		t.Fatalf("离开后应保留本次启动以来的记录: %q", logs)
	}
}

// 代理层看到房间接口的响应后要同步分段状态,不能漏掉某条路径。
func TestNoteRoomFromResponse(t *testing.T) {
	origID, origNet := roomLogID, roomLogNet
	defer func() { roomLogID, roomLogNet = origID, origNet }()
	clearRoomLog()

	noteRoomFromResponse("/api/room/create", []byte(`{"ok":true,"data":{"room_id":7,"network_id":"netX"}}`))
	if id, net := roomLogSnapshot(); id != 7 || net != "netX" {
		t.Fatalf("创建房间后应记录房间, got %d %q", id, net)
	}

	noteRoomFromResponse("/api/room/leave", []byte(`{"ok":true,"data":{"left":true}}`))
	if id, net := roomLogSnapshot(); id != 0 || net != "" {
		t.Fatalf("离开房间后应清空, got %d %q", id, net)
	}

	// join/status 未加入时同样清空
	noteRoomFromResponse("/api/room/create", []byte(`{"ok":true,"data":{"room_id":8,"network_id":"netY"}}`))
	noteRoomFromResponse("/api/join/status", []byte(`{"ok":true,"data":{"joined":false}}`))
	if id, _ := roomLogSnapshot(); id != 0 {
		t.Fatalf("join/status 未加入时应清空, got %d", id)
	}
}

func callLogs(t *testing.T, view string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/logs?view="+view, nil)
	w := httptest.NewRecorder()
	handleLogs(w, req)
	res := w.Result()
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	data, _ := body["data"].(map[string]any)
	logs, _ := data["logs"].(string)
	return logs
}
