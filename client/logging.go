package main

// logging.go:客户端文件日志。
//
// 这里不能用 io.MultiWriter(os.Stderr, lf)。Windows 客户端以 GUI 子系统
// (-H windowsgui)运行,没有控制台时 GetStdHandle(STD_ERROR_HANDLE) 返回 NULL,
// os.Stderr 就成了句柄 0 的无效文件;而 MultiWriter 在**第一个目标写失败时直接
// return**,后面的文件永远轮不到——表现就是 client.log 被创建出来却始终是空的。
//
// 所以这里让每个目标独立写入、独立容错:文件优先,控制台失败一律忽略。

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func init() {
	log.SetPrefix("d|")
}

// LogDev writes a developer-facing log line with d| prefix.
func LogDev(format string, a ...any) {
	log.Printf(format, a...)
}

// LogUser writes a user-facing event line with u| prefix:
// u|2026-11-09T10:20:31|warn|net_not_ready|你这台机器的虚拟网卡还没建出来
func LogUser(level, code, text string) {
	now := time.Now().Format("2006-01-02T15:04:05")
	line := fmt.Sprintf("u|%s|%s|%s|%s\n", now, level, code, text)
	w := log.Writer()
	if w != nil {
		_, _ = w.Write([]byte(line))
	}
}

// logMaxSize:单个日志文件上限。长期常驻运行不至于把磁盘写满。
const logMaxSize = 4 << 20 // 4MB

// logFilePath 是当前实际生效的日志路径,供 /api/logs 返回给 UI 展示。
var logFilePath string

// logFanout 同时写文件和控制台。任一目标失败不影响另一个。
type logFanout struct {
	file    *os.File
	console *os.File
}

func (w *logFanout) Write(p []byte) (int, error) {
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	if w.console != nil {
		_, _ = w.console.Write(p) // GUI 子系统下必然失败,忽略
	}
	return len(p), nil // 永远不报错,避免上层以为日志已全部写成功
}

// logDirCandidates:用户目录优先;写不进去时依次退到全机数据目录和临时目录。
func logDirCandidates() []string {
	dirs := []string{homeDir}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("ProgramData"); d != "" {
			dirs = append(dirs, filepath.Join(d, "lanbridge"))
		}
	}
	dirs = append(dirs, filepath.Join(os.TempDir(), "lanbridge"))
	return dirs
}

// rotateLog:超过上限就把旧日志挪到 client.log.1(只留一份)。
// Windows 上 Rename 覆盖已存在文件会失败,所以先删旧的。
func rotateLog(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < logMaxSize {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}

// setupLogging 选定日志文件并接管 log 的输出。返回选中的路径(全部失败时为空)。
func setupLogging() string {
	for _, dir := range logDirCandidates() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		path := filepath.Join(dir, "client.log")
		rotateLog(path)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			continue
		}
		logFilePath = path
		log.SetOutput(&logFanout{file: f, console: os.Stderr})
		log.SetPrefix("d|")
		return path
	}
	// 所有目录都写不了:只能退回控制台(GUI 下等于没有日志,但不影响程序运行)
	log.SetOutput(&logFanout{console: os.Stderr})
	log.SetPrefix("d|")
	return ""
}

// loggingHint 在日志不可用时给用户一句可操作的话。
func loggingHint() string {
	if logFilePath != "" {
		return ""
	}
	return fmt.Sprintf("日志无法写入(%v)", logDirCandidates())
}
