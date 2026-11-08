package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rooms(id,seq,network_id,game,name,owner_id) VALUES(1,1,'network','game','room',1)`); err != nil {
		t.Fatal(err)
	}
	return &Server{db: db}
}

func TestJoinAllocatesFreeAddress(t *testing.T) {
	s := testServer(t)
	join := func(id int64) {
		t.Helper()
		w := httptest.NewRecorder()
		if !s.joinInternal(&User{ID: id, ZTNodeID: fmt.Sprint(id)}, 1, "network", 1, w) {
			t.Fatalf("join %d: %s", id, w.Body.String())
		}
	}
	join(1)
	join(2)
	join(3)
	if _, err := s.db.Exec(`DELETE FROM members WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	join(4)
	join(3)
	rows, err := s.db.Query(`SELECT user_id, assigned_ip FROM members ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var ip string
		if err := rows.Scan(&id, &ip); err != nil {
			t.Fatal(err)
		}
		got[id] = ip
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got[1] != "10.147.1.1" || got[3] != "10.147.1.3" || got[4] != "10.147.1.2" {
		t.Fatalf("unexpected assignments: %v", got)
	}
}

func TestJoinStatusRequiresControllerConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		putCode, getCode int
		body             string
		want             bool
	}{
		{"confirmed", 200, 200, `{"authorized":true,"ipAssignments":["10.147.1.1"]}`, true},
		{"put failed", 500, 200, `{}`, false},
		{"get failed", 200, 500, `{}`, false},
		{"not authorized", 200, 200, `{"authorized":false,"ipAssignments":["10.147.1.1"]}`, false},
		{"wrong address", 200, 200, `{"authorized":true,"ipAssignments":["10.147.1.2"]}`, false},
		{"malformed response", 200, 200, `not json`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/controller/network/network/member/node" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Method == http.MethodPut {
					w.WriteHeader(tc.putCode)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method %s", r.Method)
				}
				w.WriteHeader(tc.getCode)
				fmt.Fprint(w, tc.body)
			}))
			defer controller.Close()
			s := testServer(t)
			s.zt = &ZTClient{Base: controller.URL, Client: controller.Client()}
			if _, err := s.db.Exec(`INSERT INTO members(user_id,room_id,zt_node_id,assigned_ip) VALUES(1,1,'node','10.147.1.1')`); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.handleJoinStatus(w, httptest.NewRequest(http.MethodGet, "/api/join/status", nil), &User{ID: 1})
			var result struct {
				Data struct {
					Joined     bool `json:"joined"`
					Authorized bool `json:"authorized"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Data.Joined || result.Data.Authorized != tc.want {
				t.Fatalf("unexpected result %s", w.Body.String())
			}
		})
	}
}

// fakeController 模拟控制器里"网络是否存在 / 成员是否存在 / 是否授权"三种状态,
// 可统计 member PUT/GET 与 network GET 次数,用于复现 404 的两种来源与暂时性故障。
type fakeController struct {
	mu                      sync.Mutex
	networkExists           bool
	memberExists            bool
	authorized              bool
	ip                      string
	memberGetCode           int // 非 0 时强制 member GET 返回该状态码(模拟 5xx 等暂时性故障)
	puts, gets, networkGets int
}

func (f *fakeController) handler(nwid, nodeID string) http.HandlerFunc {
	memberPath := "/controller/network/" + nwid + "/member/" + nodeID
	networkPath := "/controller/network/" + nwid
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == memberPath:
			switch r.Method {
			case http.MethodPut:
				f.puts++
				if !f.networkExists {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				f.memberExists = true
				f.authorized = true
				fmt.Fprintf(w, `{"authorized":true,"ipAssignments":["%s"]}`, f.ip)
			case http.MethodGet:
				f.gets++
				if f.memberGetCode != 0 {
					w.WriteHeader(f.memberGetCode)
					return
				}
				if !f.networkExists || !f.memberExists {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprintf(w, `{"authorized":%v,"ipAssignments":["%s"]}`, f.authorized, f.ip)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		case r.URL.Path == networkPath:
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			f.networkGets++
			if !f.networkExists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{}`)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func (f *fakeController) counts() (puts, gets, networkGets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts, f.gets, f.networkGets
}

func callJoinStatus(s *Server, userID int64) (authorized bool) {
	w := httptest.NewRecorder()
	s.handleJoinStatus(w, httptest.NewRequest(http.MethodGet, "/api/join/status", nil), &User{ID: userID})
	var result struct {
		Data struct {
			Authorized bool `json:"authorized"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		panic(err)
	}
	return result.Data.Authorized
}

// newAuthorizedMember 造一个"DB 已授权"的成员(模拟历史上曾授权成功过)。
func newAuthorizedMember(t *testing.T, fc *fakeController) *Server {
	t.Helper()
	ctrl := httptest.NewServer(fc.handler("network", "node"))
	t.Cleanup(ctrl.Close)
	s := testServer(t)
	s.zt = &ZTClient{Base: ctrl.URL, Client: ctrl.Client()}
	if _, err := s.db.Exec(`INSERT INTO members(user_id,room_id,zt_node_id,assigned_ip,authorized) VALUES(1,1,'node','10.147.1.1',1)`); err != nil {
		t.Fatal(err)
	}
	return s
}

// 成员记录丢失(网络仍在):应补授权(PUT)并恢复。
func TestJoinStatusSelfHealsWhenMemberMissing(t *testing.T) {
	fc := &fakeController{networkExists: true, memberExists: false, ip: "10.147.1.1"}
	s := newAuthorizedMember(t, fc)
	if !callJoinStatus(s, 1) {
		t.Fatal("成员缺失应补授权后返回已授权")
	}
	puts, gets, netGets := fc.counts()
	if puts == 0 {
		t.Fatalf("成员缺失应 PUT 补授权,实际 puts=0 gets=%d", gets)
	}
	if gets == 0 {
		t.Fatalf("应做 member GET 复核,实际 gets=0")
	}
	if netGets == 0 {
		t.Fatalf("member 404 后应查网络区分,实际 networkGets=0")
	}
}

// 网络本身缺失:不得盲目 PUT(补授权无用),需重建房间。
func TestJoinStatusNetworkMissingDoesNotPut(t *testing.T) {
	fc := &fakeController{networkExists: false, ip: "10.147.1.1"}
	s := newAuthorizedMember(t, fc)
	_ = callJoinStatus(s, 1)
	puts, _, netGets := fc.counts()
	if puts != 0 {
		t.Fatalf("网络缺失不应 PUT,实际 puts=%d", puts)
	}
	if netGets == 0 {
		t.Fatalf("应查网络确认缺失,实际 networkGets=0")
	}
}

// 成员存在但未授权:应补授权。
func TestJoinStatusUnauthorizedReauthorizes(t *testing.T) {
	fc := &fakeController{networkExists: true, memberExists: true, authorized: false, ip: "10.147.1.1"}
	s := newAuthorizedMember(t, fc)
	if !callJoinStatus(s, 1) {
		t.Fatal("未授权应补授权后返回已授权")
	}
	puts, _, _ := fc.counts()
	if puts == 0 {
		t.Fatalf("未授权成员应 PUT 补授权,实际 puts=0")
	}
}

// 暂时性故障(5xx):状态未知,保持已授权,不降级、不 PUT。
func TestJoinStatusTransportErrorKeepsState(t *testing.T) {
	fc := &fakeController{networkExists: true, memberExists: true, authorized: true, ip: "10.147.1.1", memberGetCode: http.StatusInternalServerError}
	s := newAuthorizedMember(t, fc)
	if !callJoinStatus(s, 1) {
		t.Fatal("暂时性故障时应保持已授权,不应降级")
	}
	puts, gets, _ := fc.counts()
	if puts != 0 {
		t.Fatalf("5xx 不应触发 PUT,实际 puts=%d", puts)
	}
	if gets == 0 {
		t.Fatalf("应尝试复核,实际 gets=0")
	}
}

// 已授权且控制器正常:相邻两次轮询只复核一次,且绝不 PUT。
func TestJoinStatusVerifyThrottled(t *testing.T) {
	fc := &fakeController{networkExists: true, memberExists: true, authorized: true, ip: "10.147.1.1"}
	s := newAuthorizedMember(t, fc)
	if !callJoinStatus(s, 1) {
		t.Fatal("首次轮询应返回已授权")
	}
	if !callJoinStatus(s, 1) {
		t.Fatal("第二次轮询应返回已授权")
	}
	puts, gets, _ := fc.counts()
	if gets != 1 {
		t.Fatalf("30s 内两次轮询应只复核一次,实际 gets=%d", gets)
	}
	if puts != 0 {
		t.Fatalf("控制器状态正常时不应 PUT,实际 puts=%d", puts)
	}
}
