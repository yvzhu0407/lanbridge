package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLogoutClearsCredentialsAndNetworks(t *testing.T) {
	snapshotQuitHooks(t)
	oldHome := homeDir
	homeDir = t.TempDir()
	t.Cleanup(func() { homeDir = oldHome; clearDesiredNet(); clearRoomLog() })
	saveToken("test-token", "player")
	setDesiredNet("network")
	setRoomLog(1, "network")
	var order []string
	quitNotifyLeave = func() {
		if loadToken() == "" {
			t.Error("leave notification needs the existing token")
		}
		order = append(order, "notify")
	}
	quitLeaveNets = func() { order = append(order, "leave") }
	quitRestoreNet = func() { order = append(order, "restore") }
	for range 2 {
		w := httptest.NewRecorder()
		handleLogout(w, httptest.NewRequest("POST", "/api/logout", nil))
		if w.Code != 200 {
			t.Fatalf("logout failed: %s", w.Body.String())
		}
		// 第二次退出是幂等的，不再要求有凭据。
		quitNotifyLeave = func() { order = append(order, "notify") }
	}
	if loadToken() != "" || inRoom() {
		t.Fatal("logout left credentials or active room")
	}
	if _, err := os.Stat(filepath.Join(homeDir, "session.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session file still exists: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"notify", "leave", "restore", "notify", "leave", "restore"}) {
		t.Fatalf("wrong cleanup order: %v", order)
	}
	supMu.Lock()
	defer supMu.Unlock()
	if desiredNet != "" {
		t.Fatal("logout must stop automatic rejoin")
	}
}

func TestProxyRequestsHaveTimeout(t *testing.T) {
	oldBase, oldClient, oldHome := serverBase, remoteHTTPClient, homeDir
	homeDir = t.TempDir()
	saveToken("test-token", "player")
	t.Cleanup(func() { serverBase, remoteHTTPClient, homeDir = oldBase, oldClient, oldHome })
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer remote.Close()
	serverBase = remote.URL
	remoteHTTPClient = &http.Client{Timeout: 30 * time.Millisecond}
	for _, authed := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{}`))
		started := time.Now()
		if authed {
			proxyAuthed(w, r)
		} else {
			proxyNoAuth(w, r)
		}
		if w.Code != 502 || time.Since(started) > time.Second {
			t.Fatalf("proxy did not release failed request promptly: %d %s", w.Code, w.Body.String())
		}
	}
}

// 已启动的恢复必须先结束，退出再释放网络；退出后下一轮恢复不能重新加入。
func TestLogoutWaitsForRecoveryAndPreventsRejoin(t *testing.T) {
	stubSup(t)
	snapshotQuitHooks(t)
	oldHome := homeDir
	homeDir = t.TempDir()
	t.Cleanup(func() { homeDir = oldHome; clearRoomLog() })
	setDesiredNet("network")
	started := make(chan struct{})
	release := make(chan struct{})
	recovered := make(chan struct{})
	events := make(chan string, 4)
	supRun = func(args ...string) (string, error) {
		close(started)
		<-release
		events <- "join"
		return "", nil
	}
	quitNotifyLeave = func() { events <- "notify" }
	quitLeaveNets = func() { events <- "leave" }
	quitRestoreNet = func() { events <- "restore" }
	go func() { superviseOnce(); close(recovered) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	logoutStarted := make(chan struct{})
	loggedOut := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(logoutStarted)
		w := httptest.NewRecorder()
		handleLogout(w, httptest.NewRequest("POST", "/api/logout", nil))
		loggedOut <- w
	}()
	<-logoutStarted
	close(release)
	<-recovered
	select {
	case w := <-loggedOut:
		if w.Code != 200 {
			t.Fatalf("logout failed: %s", w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("logout did not finish after recovery")
	}
	for _, want := range []string{"join", "notify", "leave", "restore"} {
		if got := <-events; got != want {
			t.Fatalf("cleanup order: got %q, want %q", got, want)
		}
	}
	// 保留会在第二次调用时失败的替身，确认退出后不再触发 join。
	superviseOnce()
	if len(events) != 0 || recoverSnapshot().Recovering {
		t.Fatal("recovery resumed after logout")
	}
}
