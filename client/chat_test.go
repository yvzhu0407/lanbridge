package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chatTestRequest(method, target, local, remote, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = remote
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(local), Port: 8890}))
}

func TestChatNetworkAllows(t *testing.T) {
	n := chatNetwork{Addresses: []string{"10.42.0.2/24", "fd00:42::2/64", "invalid"}}
	for _, tc := range []struct {
		name, local, remote string
		want                bool
	}{
		{"same IPv4 subnet", "10.42.0.2:8890", "10.42.0.3:1234", true},
		{"IPv4 mapped", "10.42.0.2:8890", "[::ffff:10.42.0.3]:1234", true},
		{"wrong subnet", "10.42.0.2:8890", "10.43.0.3:1234", false},
		{"physical destination", "192.168.1.2:8890", "10.42.0.3:1234", false},
		{"loopback", "127.0.0.1:8890", "127.0.0.1:1234", true},
		{"remote to loopback", "127.0.0.1:8890", "10.42.0.3:1234", false},
		{"loopback to assigned", "10.42.0.2:8890", "127.0.0.1:1234", false},
		{"IPv6", "[fd00:42::2]:8890", "[fd00:42::3]:1234", true},
		{"invalid remote", "10.42.0.2:8890", "bad", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatNetworkAllows(n, tc.local, tc.remote); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChatRoomIsolationAndAdmission(t *testing.T) {
	chatNetMu.Lock()
	oldNets, oldAt := chatNets, chatNetAt
	chatNets = []chatNetwork{
		{ID: "room-a", Status: "OK", Addresses: []string{"10.42.0.2/24"}},
		{ID: "room-b", Status: "OK", Addresses: []string{"10.43.0.2/24"}},
		{ID: "offline", Status: "ACCESS_DENIED"},
	}
	chatNetAt = time.Now().Add(time.Hour)
	chatNetMu.Unlock()
	chatMu.Lock()
	oldMsgs := chatMsgs
	chatMsgs = make(map[string][]chatMsg)
	chatMu.Unlock()
	t.Cleanup(func() {
		chatNetMu.Lock()
		chatNets, chatNetAt = oldNets, oldAt
		chatNetMu.Unlock()
		chatMu.Lock()
		chatMsgs = oldMsgs
		chatMu.Unlock()
	})

	call := func(handler http.HandlerFunc, method, url, local, remote, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, chatTestRequest(method, url, local, remote, body))
		return w
	}
	w := call(handleChatSend, "POST", "/chat/send?net=room-a", "10.42.0.2", "10.42.0.3:1234", `{"id":"a","kind":"text","text":"secret"}`)
	if w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	w = call(handleChatMessages, "GET", "/chat/messages?net=room-b", "10.43.0.2", "10.43.0.3:1234", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("cross-room leak: %d %s", w.Code, w.Body)
	}
	w = call(handleChatMessages, "GET", "/chat/messages?net=room-a", "127.0.0.1", "127.0.0.1:1234", "")
	var response struct {
		Data struct {
			Messages []chatMsg `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data.Messages) != 1 || response.Data.Messages[0].ID != "a" {
		t.Fatalf("room-a messages: %d %s, %v", w.Code, w.Body, err)
	}
	for _, tc := range []struct{ url, local, remote string }{
		{"/chat/messages?net=room-a", "10.43.0.2", "10.43.0.3:1234"},
		{"/chat/messages?net=room-a", "10.42.0.2", "192.168.1.5:1234"},
		{"/chat/messages?net=missing", "127.0.0.1", "127.0.0.1:1234"},
		{"/chat/messages?net=offline", "127.0.0.1", "127.0.0.1:1234"},
	} {
		w = call(handleChatMessages, "GET", tc.url, tc.local, tc.remote, "")
		if w.Code != http.StatusForbidden {
			t.Errorf("expected forbidden for %+v: %d", tc, w.Code)
		}
	}
	// 非 text 类型（例如二次开发误发文件卡片）应被拒绝。
	w = call(handleChatSend, "POST", "/chat/send?net=room-a", "10.42.0.2", "10.42.0.3:1234", `{"id":"b","kind":"file","text":"x"}`)
	if w.Code != 400 {
		t.Fatalf("non-text kind should be rejected: %d", w.Code)
	}
	w = call(handleChatSend, "OPTIONS", "/chat/send", "127.0.0.1", "127.0.0.1:1234", "")
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d", w.Code)
	}
}
