package main

import (
	"encoding/json"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回归测试:日志必须能落到文件里。
// 历史 bug:主流程用 io.MultiWriter(os.Stderr, file),MultiWriter 在第一个目标
// 写失败时直接 return,Windows GUI 子系统下 os.Stderr 无效,日志文件永远是空的。
func TestSetupLoggingWritesToFile(t *testing.T) {
	orig := homeDir
	origPath := logFilePath
	origOut := log.Writer()
	defer func() {
		homeDir = orig
		logFilePath = origPath
		log.SetOutput(origOut)
	}()

	homeDir = t.TempDir()

	path := setupLogging()
	if path == "" {
		t.Fatal("setupLogging 返回空路径,日志无处可写")
	}
	if logFilePath != path {
		t.Fatalf("logFilePath=%q,期望 %q", logFilePath, path)
	}

	log.Print("日志落盘测试 hello")

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if !strings.Contains(string(b), "日志落盘测试 hello") {
		t.Fatalf("日志内容未写入文件,实际内容: %q", string(b))
	}
}

// 写不进去时不能 panic,也不能丢掉后面的目标
func TestLogFanoutToleratesFailedWriter(t *testing.T) {
	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "x.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	closed, err := os.OpenFile(filepath.Join(dir, "y.log"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close() // 故意用已关闭的文件模拟"坏目标"

	w := &logFanout{file: f, console: closed}
	n, err := w.Write([]byte("abc"))
	if err != nil {
		t.Fatalf("Write 不应报错, got %v", err)
	}
	if n != 3 {
		t.Fatalf("Write 返回 %d, 期望 3", n)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "x.log"))
	if string(b) != "abc" {
		t.Fatalf("好目标没写进去, got %q", string(b))
	}
}

// 轮转:超过上限时旧日志保留为 .1,新文件从空开始
func TestRotateLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	big := make([]byte, logMaxSize+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}

	rotateLog(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("轮转后原文件应已移走")
	}
	st, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("轮转后应存在 .1 文件: %v", err)
	}
	if st.Size() != int64(len(big)) {
		t.Fatalf(".1 大小 %d, 期望 %d", st.Size(), len(big))
	}

	// 小文件不该轮转
	small := filepath.Join(t.TempDir(), "small.log")
	_ = os.WriteFile(small, []byte("x"), 0o644)
	rotateLog(small)
	if _, err := os.Stat(small); err != nil {
		t.Fatal("小文件不应被轮转")
	}
}

func TestHandleLogsDualStream(t *testing.T) {
	origPath := logFilePath
	defer func() { logFilePath = origPath }()

	dir := t.TempDir()
	logFilePath = filepath.Join(dir, "client.log")
	content := "d|2026/11/09 10:00:00 [开发] 原始日志1\n" +
		"u|2026-11-09T10:00:01|info|net_ready|网络就绪\n" +
		"d|2026/11/09 10:00:02 [开发] 原始日志2\n" +
		"u|2026-11-09T10:00:03|warn|peer_bad|连不上成员\n"
	if err := os.WriteFile(logFilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	call := func(view string) string {
		req := httptest.NewRequest("GET", "/api/logs?view="+view, nil)
		w := httptest.NewRecorder()
		handleLogs(w, req)
		res := w.Result()
		defer res.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		data, _ := body["data"].(map[string]any)
		logs, _ := data["logs"].(string)
		return logs
	}

	userLogs := call("user")
	if strings.Contains(userLogs, "[开发]") || !strings.Contains(userLogs, "网络就绪") || strings.Contains(userLogs, "u|") {
		t.Fatalf("user view 格式错误: %q", userLogs)
	}

	devLogs := call("dev")
	if strings.Contains(devLogs, "网络就绪") || !strings.Contains(devLogs, "原始日志1") || strings.Contains(devLogs, "d|") {
		t.Fatalf("dev view 格式错误: %q", devLogs)
	}

	allLogs := call("")
	if !strings.Contains(allLogs, "u|") || !strings.Contains(allLogs, "d|") {
		t.Fatalf("all view 应保留原始行: %q", allLogs)
	}
}
