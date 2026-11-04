package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
