package main

// invite_test.go: 邀请链接的解析与"待处理邀请"的生命周期。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseInviteLink(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"lanbridge://join/78", 78, true},
		{"LANBRIDGE://JOIN/78", 78, true},
		{"  lanbridge://join/1234  ", 1234, true},
		// 聊天软件里常见的整段邀请文本
		{"来和我联机：鼠直66\nlanbridge://join/78\n房间号：78", 78, true},
		// 手抄的房间号
		{"78", 78, true},
		{"#78", 78, true},
		{"房间号：78", 78, true},
		// 无效输入
		{"", 0, false},
		{"lanbridge://join/abc", 0, false},
		{"lanbridge://join/", 0, false},
		{"没有数字的文本", 0, false},
		{"lanbridge://other/78", 0, false}, // 协议对但不是加入链接，不当成房间号
	}
	for _, c := range cases {
		got, ok := parseInvite(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseInvite(%q) = (%d,%v), 期望 (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPendingInviteTakeOnce(t *testing.T) {
	resetInviteState(t)

	if _, ok := takePendingInvite(); ok {
		t.Fatal("没有邀请时不应取到东西")
	}

	setPendingInvite(42)
	id, ok := takePendingInvite()
	if !ok || id != 42 {
		t.Fatalf("应取到房间 42，实际 (%d,%v)", id, ok)
	}
	if _, ok := takePendingInvite(); ok {
		t.Fatal("邀请只能取走一次，否则界面会反复加入同一个房间")
	}
}

func TestInvitePostAndGet(t *testing.T) {
	resetInviteState(t)

	body := strings.NewReader(`{"url":"lanbridge://join/99"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invite", body)
	rec := httptest.NewRecorder()
	handleInvitePost(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("提交有效链接应返回 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"room_id":99`) {
		t.Fatalf("响应里应带房间号: %s", rec.Body.String())
	}

	// 取一次
	rec = httptest.NewRecorder()
	handleInviteGet(rec, httptest.NewRequest(http.MethodGet, "/api/invite", nil))
	if !strings.Contains(rec.Body.String(), `"room_id":99`) {
		t.Fatalf("应取到刚提交的邀请: %s", rec.Body.String())
	}

	// 再取就没有了
	rec = httptest.NewRecorder()
	handleInviteGet(rec, httptest.NewRequest(http.MethodGet, "/api/invite", nil))
	if strings.Contains(rec.Body.String(), "room_id") {
		t.Fatalf("邀请取走后不应再返回: %s", rec.Body.String())
	}
}

func TestInvitePostRejectsGarbage(t *testing.T) {
	resetInviteState(t)

	req := httptest.NewRequest(http.MethodPost, "/api/invite", strings.NewReader(`{"url":"随便写点什么"}`))
	rec := httptest.NewRecorder()
	handleInvitePost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无效输入应返回 400，实际 %d", rec.Code)
	}
}

func TestInviteFromArgs(t *testing.T) {
	// 系统用链接调起时，参数就是链接本身
	if id, ok := inviteFromArgs([]string{"--elevated", "lanbridge://join/7"}); !ok || id != 7 {
		t.Fatalf("应从命令行取到房间 7，实际 (%d,%v)", id, ok)
	}
	// 没有链接的普通启动不能被误判
	if _, ok := inviteFromArgs([]string{"--elevated"}); ok {
		t.Fatal("普通启动不应产生邀请")
	}
	// 纯数字参数不当链接(避免把调试参数 42 当成房间号)
	if _, ok := inviteFromArgs([]string{"42"}); ok {
		t.Fatal("命令行里的纯数字不应被当作邀请")
	}
}

func resetInviteState(t *testing.T) {
	t.Helper()
	inviteMu.Lock()
	pendingInvite = 0
	inviteMu.Unlock()
}
