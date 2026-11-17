package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func lifecycleServer(t *testing.T, controller http.HandlerFunc) *Server {
	t.Helper()
	s := testServer(t)
	c := httptest.NewServer(controller)
	t.Cleanup(c.Close)
	s.zt = &ZTClient{Base: c.URL, Client: c.Client()}
	if _, err := s.db.Exec(`INSERT INTO members(user_id,room_id,zt_node_id,assigned_ip,authorized)
		VALUES(1,1,'node','10.147.1.1',1)`); err != nil {
		t.Fatal(err)
	}
	return s
}

func addTargetRoom(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO rooms(id,seq,network_id,game,name,owner_id)
		VALUES(2,2,'target','civ6','target room',2)`); err != nil {
		t.Fatal(err)
	}
}

func assertOriginalRoom(t *testing.T, s *Server) {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM members m JOIN rooms r ON r.id=m.room_id
		WHERE m.user_id=1 AND m.room_id=1 AND r.closed=0 AND m.authorized=1 AND m.assigned_ip='10.147.1.1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("original room was lost: count=%d err=%v", count, err)
	}
}

func TestRejoinSameRoomKeepsNetworkAndAuthorization(t *testing.T) {
	s := lifecycleServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("idempotent join must not change controller: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			s.handleJoinRoom(w, httptest.NewRequest("POST", "/api/room/join", strings.NewReader(`{"room_id":1}`)), &User{ID: 1, ZTNodeID: "node"})
			if w.Code != 200 {
				t.Errorf("rejoin: %d %s", w.Code, w.Body.String())
			}
		})
	}
	wg.Wait()
	assertOriginalRoom(t, s)
}

func TestFailedSwitchPreservesOriginalRoom(t *testing.T) {
	for _, failure := range []string{"full", "write failure", "closed", "wrong password"} {
		t.Run(failure, func(t *testing.T) {
			s := lifecycleServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("failed switch must not change controller: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			})
			addTargetRoom(t, s)
			switch failure {
			case "full":
				for i := range 253 {
					if _, err := s.db.Exec(`INSERT INTO members(user_id,room_id,zt_node_id,assigned_ip) VALUES(?,2,?,?)`, i+2, fmt.Sprint(i+2), ipFor(2, i)); err != nil {
						t.Fatal(err)
					}
				}
			case "write failure":
				// 新成员写入成功、删除旧成员失败时，整个切换也必须回滚。
				if _, err := s.db.Exec(`CREATE TRIGGER fail_switch BEFORE DELETE ON members
					WHEN OLD.user_id=1 BEGIN SELECT RAISE(FAIL, 'disk failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "closed":
				_, _ = s.db.Exec(`UPDATE rooms SET closed=1 WHERE id=2`)
			case "wrong password":
				_, _ = s.db.Exec(`UPDATE rooms SET password='secret' WHERE id=2`)
			}
			w := httptest.NewRecorder()
			s.handleJoinRoom(w, httptest.NewRequest("POST", "/api/room/join", strings.NewReader(`{"room_id":2}`)), &User{ID: 1, ZTNodeID: "node"})
			if w.Code == 200 {
				t.Fatalf("switch should fail: %s", w.Body.String())
			}
			assertOriginalRoom(t, s)
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM members WHERE user_id=1 AND room_id=2`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed switch left target membership: count=%d err=%v", count, err)
			}
		})
	}
}

func TestCreateNetworkFailurePreservesOriginalRoom(t *testing.T) {
	s := lifecycleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			t.Errorf("failed create must not delete original network: %s", r.URL.Path)
		}
		w.WriteHeader(500)
	})
	w := httptest.NewRecorder()
	s.handleCreateRoom(w, httptest.NewRequest("POST", "/api/room/create", strings.NewReader(`{"name":"new","game":"civ6"}`)), &User{ID: 1, ZTNodeID: "node"})
	if w.Code == 200 {
		t.Fatal("expected create failure")
	}
	assertOriginalRoom(t, s)
}

func TestSuccessfulSwitchCleansOnlyPreviousMembership(t *testing.T) {
	for _, hasOtherMember := range []bool{false, true} {
		t.Run(fmt.Sprint(hasOtherMember), func(t *testing.T) {
			var mu sync.Mutex
			deletedNetwork, deletedMember := false, false
			s := lifecycleServer(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodDelete {
					t.Errorf("unexpected controller mutation: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/controller/network/network/member/node":
					deletedMember = true
				case "/controller/network/network":
					deletedNetwork = true
				default:
					t.Errorf("unexpected controller target: %s", r.URL.Path)
				}
				fmt.Fprint(w, `{}`)
			})
			addTargetRoom(t, s)
			if hasOtherMember {
				_, _ = s.db.Exec(`INSERT INTO members(user_id,room_id,zt_node_id,assigned_ip) VALUES(2,1,'other','10.147.1.2')`)
			}
			w := httptest.NewRecorder()
			s.handleJoinRoom(w, httptest.NewRequest("POST", "/api/room/join", strings.NewReader(`{"room_id":2}`)), &User{ID: 1, ZTNodeID: "node"})
			if w.Code != 200 {
				t.Fatalf("switch failed: %s", w.Body.String())
			}
			var count, roomID, authorized, closed int
			if err := s.db.QueryRow(`SELECT COUNT(*), room_id, authorized FROM members WHERE user_id=1`).Scan(&count, &roomID, &authorized); err != nil || count != 1 || roomID != 2 || authorized != 0 {
				t.Fatalf("unexpected membership: count=%d room=%d auth=%d err=%v", count, roomID, authorized, err)
			}
			_ = s.db.QueryRow(`SELECT closed FROM rooms WHERE id=1`).Scan(&closed)
			mu.Lock()
			defer mu.Unlock()
			if !deletedMember || deletedNetwork == hasOtherMember || (closed == 1) == hasOtherMember {
				t.Fatalf("wrong cleanup: member=%v network=%v closed=%d", deletedMember, deletedNetwork, closed)
			}
		})
	}
}

func TestLeaveFailureDoesNotReportSuccess(t *testing.T) {
	s := lifecycleServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	w := httptest.NewRecorder()
	s.handleLeaveRoom(w, httptest.NewRequest("POST", "/api/room/leave", nil), &User{ID: 1})
	if w.Code == 200 {
		t.Fatal("controller failure must not report successful leave")
	}
	assertOriginalRoom(t, s)
}

func TestMeIncludesUserIdentity(t *testing.T) {
	s := testServer(t)
	w := httptest.NewRecorder()
	s.handleMe(w, httptest.NewRequest("GET", "/api/me", nil), &User{ID: 42, Name: "player"})
	if !strings.Contains(w.Body.String(), `"id":42`) {
		t.Fatalf("missing user identity: %s", w.Body.String())
	}
}
