package main

// lanbridge server: 局域网联机工具的自建服务端
// 架构:REST API + SQLite(房间/成员登记) + ZeroTier 控制器桥接
// 设计说明见 docs/ARCHITECTURE.md(房间语义、授权闭环、IP 分配)
//
// 接口与 ZeroTier 概念的对应:
//   /room/create → 控制器建网络(= 房间)
//   /room/join   → 客户端上报 zt 节点ID,登记后等授权(成员自动注册闭环)
//   /room/leave  → 收回授权 + 删除成员记录
//   /room/list   → 列房间(成员数来自控制器 member 查询)

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ---------- 配置 ----------

type Config struct {
	Addr           string // 监听地址
	DBPath         string
	ZTBase         string // ZeroTier 控制器本地 API,如 http://127.0.0.1:9993
	ZTAuthToken    string
	PlanetPath     string // 分发给客户端的 planet 文件
	AllowedNetwork string // 允许注册的 CIDR,留空不限制(测试版)
}

func loadConfig() Config {
	return Config{
		Addr: envOr("ADDR", "0.0.0.0:8800"),
		// 默认值沿用早期部署的文件名，避免升级时读到空库；新部署建议用 DB 环境变量指定。
		DBPath:      envOr("DB", "lanbridge.db"),
		ZTBase:      envOr("ZT_BASE", "http://127.0.0.1:9993"),
		ZTAuthToken: os.Getenv("ZT_TOKEN"),
		PlanetPath:  envOr("PLANET", "planet"),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---------- 数据模型 ----------

type User struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Token    string `json:"token"` // 登录后下发,后续请求 Authorization: Bearer
	ZTNodeID string `json:"zt_node_id"`
}

type Room struct {
	ID        int64     `json:"id"`
	NetworkID string    `json:"network_id"` // ZeroTier 16 位 hex
	Game      string    `json:"game"`
	Name      string    `json:"name"`
	Password  string    `json:"password,omitempty"`
	OwnerID   int64     `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Membership struct {
	UserID     int64     `json:"user_id"`
	RoomID     int64     `json:"room_id"`
	ZTNodeID   string    `json:"zt_node_id"`
	AssignedIP string    `json:"assigned_ip"`
	JoinedAt   time.Time `json:"joined_at"`
}

// ---------- ZeroTier 控制器桥接 ----------

// ZTClient 只实现我们验证过的最小操作集(zas 实测)
type ZTClient struct {
	Base   string
	Token  string
	Client *http.Client
}

// ztAPIError 保留控制器返回的 HTTP 状态码,便于把"成员/网络不存在(404,可修复
// 或需重建)"与"超时/5xx(状态未知,不可据此判定丢失)"分开。纯字符串错误无法
// 可靠分类,所以单独建类型。
type ztAPIError struct {
	Status int
	Msg    string
}

func (e *ztAPIError) Error() string { return e.Msg }

// ztStatus 取控制器错误的 HTTP 状态码;非 API 错误(网络层)返回 0。
func ztStatus(err error) int {
	var ae *ztAPIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func (z *ZTClient) req(method, path string, body any) (map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, z.Base+path, rd)
	if err != nil {
		return nil, err
	}
	r.Header.Set("X-ZT1-Auth", z.Token)
	resp, err := z.Client.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	if resp.StatusCode >= 300 {
		return out, &ztAPIError{Status: resp.StatusCode, Msg: fmt.Sprintf("zt api %s %s: %d %s", method, path, resp.StatusCode, truncate(string(data), 200))}
	}
	return out, nil
}

func (z *ZTClient) CreateNetwork(nwid, name, ipStart, ipEnd, cidr string) error {
	if _, err := z.req("POST", "/controller/network/"+nwid, map[string]any{}); err != nil {
		return err
	}
	_, err := z.req("PUT", "/controller/network/"+nwid, map[string]any{
		"name":            name,
		"private":         true,
		"enableBroadcast": true,
		"v4AssignMode":    map[string]any{"zt": true},
		"ipAssignmentPools": []map[string]any{
			{"ipRangeStart": ipStart, "ipRangeEnd": ipEnd},
		},
		"routes": []map[string]any{{"target": cidr, "via": nil}},
	})
	return err
}

func (z *ZTClient) DeleteNetwork(nwid string) error {
	_, err := z.req("DELETE", "/controller/network/"+nwid, nil)
	return err
}

func (z *ZTClient) ListMembers(nwid string) (map[string]any, error) {
	return z.req("GET", "/controller/network/"+nwid+"/member", nil)
}

// DeleteMember 与 DeauthorizeMember 同义:从网络中删除成员记录

func (z *ZTClient) AuthorizeMember(nwid, nodeID, ip string) error {
	path := "/controller/network/" + nwid + "/member/" + nodeID
	_, err := z.req("PUT", path, map[string]any{
		"authorized":    true,
		"activeBridge":  false,
		"ipAssignments": []string{ip},
	})
	if err != nil {
		return err
	}
	member, err := z.req("GET", path, nil)
	if err != nil {
		return err
	}
	authorized, _ := member["authorized"].(bool)
	assignments, _ := member["ipAssignments"].([]any)
	for _, assignment := range assignments {
		if authorized && assignment == ip {
			return nil
		}
	}
	return fmt.Errorf("控制器尚未确认成员授权和 IP 分配")
}

func (z *ZTClient) DeauthorizeMember(nwid, nodeID string) error {
	_, err := z.req("DELETE", "/controller/network/"+nwid+"/member/"+nodeID, nil)
	return err
}

// CheckMember 复核控制器当前对该成员的认定,并把三种情况分开处理:
//
//	ok             : 控制器已授权且持有预期 IP
//	networkMissing : 网络本身在控制器上不存在(成员 GET 和网络 GET 都 404),
//	                 补授权无用,需要重建房间——调用方不得盲目 PUT。
//	err            : 传输/5xx 等暂时性故障,状态未知,调用方不得据此判定丢失。
//
// 注意:GET member 对"成员不存在"和"网络不存在"都返回 404,所以必须再查一次
// 网络才能区分(ZeroTier 1.16 EmbeddedNetworkController 的行为)。
func (z *ZTClient) CheckMember(nwid, nodeID, ip string) (ok bool, networkMissing bool, err error) {
	member, err := z.req("GET", "/controller/network/"+nwid+"/member/"+nodeID, nil)
	if err != nil {
		if ztStatus(err) != http.StatusNotFound {
			return false, false, err
		}
		// 成员或网络任一缺失都 404,查网络把两者分开。
		if _, nerr := z.req("GET", "/controller/network/"+nwid, nil); nerr != nil {
			if ztStatus(nerr) == http.StatusNotFound {
				return false, true, nil // 网络不存在
			}
			return false, false, nerr
		}
		return false, false, nil // 网络在、成员缺 → 可补授权
	}
	authorized, _ := member["authorized"].(bool)
	assignments, _ := member["ipAssignments"].([]any)
	if authorized {
		for _, a := range assignments {
			if a == ip {
				return true, false, nil
			}
		}
	}
	return false, false, nil // 记录在但不合规 → 可补授权
}

// ---------- IP 分配(每个网络独立 /24,按成员序号分配) ----------

// ipFor 给定网络序号 n 和成员序号 m,生成 10.(147+n).m+1
// 网络 1 → 10.147.1.0/24,网络 2 → 10.147.2.0/24 … 网络号上限 254
func ipFor(networkSeq, memberSeq int) string {
	return fmt.Sprintf("10.147.%d.%d", networkSeq, memberSeq+1)
}

func cidrFor(networkSeq int) string {
	return fmt.Sprintf("10.147.%d.0/24", networkSeq)
}

// ---------- 服务 ----------

type Server struct {
	cfg Config
	db  *sql.DB
	zt  *ZTClient
	mu  sync.Mutex // 网络号/成员序号分配串行化

	// 已授权成员向控制器复核的节流。key 绑定成员身份(房间/网络/节点/IP),
	// 换房或换节点后不会沿用旧的复核时间。DB 的 authorized 只代表"曾经确认过",
	// 不是控制器实时状态;控制器丢过成员时靠这里复核补授权。
	verifyMu   sync.Mutex
	lastVerify map[string]time.Time
}

// memberVerifyInterval: 已授权成员每隔这么久向控制器做一次只读复核。
// 与客户端授权后的轮询节奏(30s)对齐,又不会每个心跳都打控制器。
const memberVerifyInterval = 30 * time.Second

// verifyKey 用成员身份构造复核节流键,避免同一用户在换房/换节点后误命中旧时间。
func verifyKey(userID int64, roomID int, nwid, nodeID, ip string) string {
	return fmt.Sprintf("%d|%d|%s|%s|%s", userID, roomID, nwid, nodeID, ip)
}

// shouldVerify 判断该成员此刻是否该去控制器复核(节流)。返回 true 时已记录时间。
func (s *Server) shouldVerify(key string) bool {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if s.lastVerify == nil {
		s.lastVerify = make(map[string]time.Time)
	}
	now := time.Now()
	if t, ok := s.lastVerify[key]; ok && now.Sub(t) < memberVerifyInterval {
		return false
	}
	s.lastVerify[key] = now
	return true
}

func main() {
	cfg := loadConfig()
	if cfg.ZTAuthToken == "" {
		log.Fatal("需要 ZT_TOKEN 环境变量(zerotier-one authtoken.secret 内容)")
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1) // modernc/sqlite 单写者
	if err := migrate(db); err != nil {
		log.Fatal(err)
	}
	s := &Server{
		cfg: cfg,
		db:  db,
		zt: &ZTClient{
			Base:   cfg.ZTBase,
			Token:  cfg.ZTAuthToken,
			Client: &http.Client{Timeout: 10 * time.Second},
		},
	}
	s.startRoomGC() // 启动后台巡检, 定期清理空房与无主动连接的僵尸房间

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/planet", s.handlePlanet)
	mux.HandleFunc("POST /api/user/register", s.handleRegister)
	mux.HandleFunc("POST /api/user/login", s.handleLogin)
	mux.HandleFunc("GET /api/rooms", s.auth(s.handleListRooms))
	mux.HandleFunc("GET /api/room/members", s.auth(s.handleRoomMembers))
	mux.HandleFunc("POST /api/room/create", s.auth(s.handleCreateRoom))
	mux.HandleFunc("POST /api/room/join", s.auth(s.handleJoinRoom))
	mux.HandleFunc("POST /api/room/leave", s.auth(s.handleLeaveRoom))
	mux.HandleFunc("POST /api/room/dismiss", s.auth(s.handleDismissRoom))
	mux.HandleFunc("GET /api/join/status", s.auth(s.handleJoinStatus))
	mux.HandleFunc("POST /api/room/heartbeat", s.auth(s.handleRoomHeartbeat))
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe))

	log.Printf("lanbridge server 监听 %s (zt=%s)", cfg.Addr, cfg.ZTBase)
	log.Fatal(http.ListenAndServe(cfg.Addr, mux))
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	token TEXT UNIQUE NOT NULL,
	zt_node_id TEXT DEFAULT '',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS rooms(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	seq INTEGER NOT NULL,
	net_seq INTEGER,
	network_id TEXT UNIQUE NOT NULL,
	game TEXT NOT NULL,
	name TEXT NOT NULL,
	password TEXT DEFAULT '',
	owner_id INTEGER NOT NULL,
	closed INTEGER DEFAULT 0,
	last_active_at DATETIME,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS members(
	user_id INTEGER NOT NULL,
	room_id INTEGER NOT NULL,
	zt_node_id TEXT NOT NULL,
	assigned_ip TEXT NOT NULL,
	authorized INTEGER DEFAULT 0,
	joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(user_id, room_id)
);
-- counters: 单调递增的计数器。网络 ID 序号存在这里而不是 MAX(net_seq),
-- 这样删掉历史房间也不会让序号回退、导致网络 ID 复用。
CREATE TABLE IF NOT EXISTS counters(
	name TEXT PRIMARY KEY,
	value INTEGER NOT NULL DEFAULT 0
);
`)
	if err != nil {
		return err
	}
	// 针对已有数据表, 检查并安全添加 last_active_at 列
	var hasLastActive bool
	rows, err := db.Query(`PRAGMA table_info(rooms)`)
	if err == nil {
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil {
				if name == "last_active_at" {
					hasLastActive = true
				}
			}
		}
		rows.Close()
	}
	if !hasLastActive {
		if _, err := db.Exec(`ALTER TABLE rooms ADD COLUMN last_active_at DATETIME`); err != nil {
			log.Printf("[migrate] 添加 last_active_at 字段失败: %v", err)
		} else {
			_, _ = db.Exec(`UPDATE rooms SET last_active_at = created_at WHERE last_active_at IS NULL`)
			log.Printf("[migrate] 成功为 rooms 表添加 last_active_at 字段")
		}
	}
	// members.authorized: 记录控制器是否已确认本成员授权。
	// 加这一列之前每次查状态都会重写一次控制器, 现在授权只做一次。
	if !hasColumn(db, "members", "authorized") {
		if _, err := db.Exec(`ALTER TABLE members ADD COLUMN authorized INTEGER DEFAULT 0`); err != nil {
			log.Printf("[migrate] 添加 authorized 字段失败: %v", err)
		} else {
			log.Printf("[migrate] 成功为 members 表添加 authorized 字段")
		}
	}
	// last_touch_at: 服务端节流用。只有距上次写入超过 60 秒才真正 UPDATE,
	// 避免每个客户端心跳都落一次盘(该库 synchronous=2, 每次写入都要 fsync)。
	if !hasColumn(db, "rooms", "last_touch_at") {
		if _, err := db.Exec(`ALTER TABLE rooms ADD COLUMN last_touch_at DATETIME`); err != nil {
			log.Printf("[migrate] 添加 last_touch_at 字段失败: %v", err)
		} else {
			log.Printf("[migrate] 成功为 rooms 表添加 last_touch_at 字段")
		}
	}
	// net_seq: 网络 ID 的单调递增序号, 一经分配永不复用。
	//
	// 旧实现直接用 seq 拼网络 ID, 而 seq 会在房间销毁后复用。销毁房间会删掉控制器
	// 上的整个网络, 于是"新房间拿到旧网络 ID、控制器上却没有这个网络"——客户端
	// 表现为 ACCESS_DENIED / 网卡建不出来, 且服务端 DB 里 authorized=1 让它走只读
	// 复核分支, 不会自动补授权。
	//
	// 已有关闭房间的 net_seq 按 seq 回填, 保证历史网络 ID 不会被重新分配。
	if !hasColumn(db, "rooms", "net_seq") {
		if _, err := db.Exec(`ALTER TABLE rooms ADD COLUMN net_seq INTEGER`); err != nil {
			log.Printf("[migrate] 添加 net_seq 字段失败: %v", err)
		} else {
			_, _ = db.Exec(`UPDATE rooms SET net_seq = seq WHERE net_seq IS NULL`)
			log.Printf("[migrate] 成功为 rooms 表添加 net_seq 字段")
		}
	}
	// 计数器起步值取历史最大 net_seq, 保证升级后不会重新发放已用过的网络号。
	// ON CONFLICT 取较大值: 迁移可能被重复执行, 且历史行可能在首次迁移后才补齐。
	if _, err := db.Exec(`INSERT INTO counters(name, value)
		VALUES('net_seq', (SELECT COALESCE(MAX(net_seq), 0) FROM rooms))
		ON CONFLICT(name) DO UPDATE SET value = MAX(counters.value, excluded.value)`); err != nil {
		log.Printf("[migrate] 初始化 net_seq 计数器失败: %v", err)
	}
	return nil
}

// allocNetSeq 原子地取用下一个网络 ID 序号。必须在 s.mu 内调用。
// 用计数器而不是 MAX(net_seq): 历史房间被删除后序号仍不会回退, 网络 ID 永不复用。
func allocNetSeq(db *sql.DB) (int, error) {
	if _, err := db.Exec(`UPDATE counters SET value = value + 1 WHERE name = 'net_seq'`); err != nil {
		return 0, err
	}
	var n int
	if err := db.QueryRow(`SELECT value FROM counters WHERE name = 'net_seq'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// hasColumn: 判断某表是否已有某列(已有库升级用)
func hasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == column {
			return true
		}
	}
	return false
}

// ---------- 通用 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func okJSON(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": v})
}

func (s *Server) auth(next func(w http.ResponseWriter, r *http.Request, u *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			errJSON(w, 401, "未登录")
			return
		}
		var u User
		err := s.db.QueryRow(`SELECT id, name, token, zt_node_id FROM users WHERE token=?`, token).
			Scan(&u.ID, &u.Name, &u.Token, &u.ZTNodeID)
		if err != nil {
			errJSON(w, 401, "登录已失效")
			return
		}
		next(w, r, &u)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------- handlers ----------

func (s *Server) handlePlanet(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.cfg.PlanetPath)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || len(req.Password) < 4 {
		errJSON(w, 400, "参数不合法(用户名非空,密码≥4位)")
		return
	}
	token := randToken()
	_, err := s.db.Exec(`INSERT INTO users(name, password_hash, token) VALUES(?,?,?)`,
		req.Name, hashPwd(req.Password), token)
	if err != nil {
		errJSON(w, 409, "用户名已存在")
		return
	}
	okJSON(w, map[string]any{"token": token})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, 400, "参数不合法")
		return
	}
	var token string
	err := s.db.QueryRow(`SELECT token FROM users WHERE name=? AND password_hash=?`,
		req.Name, hashPwd(req.Password)).Scan(&token)
	if err != nil {
		errJSON(w, 401, "用户名或密码错误")
		return
	}
	okJSON(w, map[string]any{"token": token})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *User) {
	// 当前所在房间(若有)
	var roomID int64
	var netID, ip, roomName string
	_ = s.db.QueryRow(`SELECT m.room_id, ro.network_id, m.assigned_ip, ro.name
		FROM members m JOIN rooms ro ON ro.id=m.room_id WHERE m.user_id=?`, u.ID).
		Scan(&roomID, &netID, &ip, &roomName)
	okJSON(w, map[string]any{
		"id": u.ID, "name": u.Name, "zt_node_id": u.ZTNodeID,
		"room_id": roomID, "network_id": netID, "ip": ip, "room_name": roomName,
	})
}

// handleListRooms: 服务端搜索;MVP 版全量返回 + 关键词过滤
func (s *Server) handleListRooms(w http.ResponseWriter, r *http.Request, u *User) {
	q := "%" + r.URL.Query().Get("search") + "%"
	rows, err := s.db.Query(`SELECT ro.id, ro.network_id, ro.game, ro.name,
			ro.password != '' AS has_pw, ro.owner_id, ro.closed,
			(SELECT COUNT(*) FROM members m WHERE m.room_id=ro.id) AS cnt
		FROM rooms ro WHERE ro.closed=0 AND ro.name LIKE ? ORDER BY ro.id DESC LIMIT 200`, q)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type roomOut struct {
		ID        int64  `json:"id"`
		NetworkID string `json:"network_id"`
		Game      string `json:"game"`
		Name      string `json:"name"`
		HasPW     bool   `json:"password_required"`
		OwnerID   int64  `json:"owner_id"`
		UserCount int    `json:"user_count"`
	}
	var out []roomOut
	for rows.Next() {
		var ro roomOut
		var closed int
		if err := rows.Scan(&ro.ID, &ro.NetworkID, &ro.Game, &ro.Name, &ro.HasPW, &ro.OwnerID, &closed, &ro.UserCount); err != nil {
			continue
		}
		out = append(out, ro)
	}
	if out == nil {
		out = []roomOut{}
	}
	okJSON(w, out)
}

func (s *Server) handleRoomMembers(w http.ResponseWriter, r *http.Request, u *User) {
	roomIDStr := r.URL.Query().Get("room_id")
	var roomID int64
	if roomIDStr != "" {
		fmt.Sscanf(roomIDStr, "%d", &roomID)
	}
	if roomID == 0 {
		_ = s.db.QueryRow(`SELECT room_id FROM members WHERE user_id=?`, u.ID).Scan(&roomID)
	}
	if roomID == 0 {
		okJSON(w, []any{})
		return
	}
	rows, err := s.db.Query(`SELECT m.user_id, u.name, m.zt_node_id, m.assigned_ip, m.joined_at, (ro.owner_id=m.user_id) AS is_owner
		FROM members m
		JOIN users u ON u.id=m.user_id
		JOIN rooms ro ON ro.id=m.room_id
		WHERE m.room_id=? ORDER BY is_owner DESC, m.joined_at ASC`, roomID)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type memberOut struct {
		UserID     int64     `json:"user_id"`
		Name       string    `json:"name"`
		ZTNodeID   string    `json:"zt_node_id"`
		AssignedIP string    `json:"assigned_ip"`
		JoinedAt   time.Time `json:"joined_at"`
		IsOwner    bool      `json:"is_owner"`
	}
	var out []memberOut
	for rows.Next() {
		var mo memberOut
		if err := rows.Scan(&mo.UserID, &mo.Name, &mo.ZTNodeID, &mo.AssignedIP, &mo.JoinedAt, &mo.IsOwner); err == nil {
			out = append(out, mo)
		}
	}
	if out == nil {
		out = []memberOut{}
	}
	// 成员列表是只读查询, 不在这里写库; 保活由 POST /api/room/heartbeat 独立驱动。
	okJSON(w, out)
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Game, Name, Password string
		ZTNodeID             string `json:"zt_node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		errJSON(w, 400, "房间名不能为空")
		return
	}
	if req.ZTNodeID != "" {
		u.ZTNodeID = req.ZTNodeID
	}
	if u.ZTNodeID == "" {
		errJSON(w, 400, "缺少 ZeroTier 节点ID,请先运行客户端")
		return
	}

	s.mu.Lock()
	// 取可用 IP 子网序号(复用 1~254 中未被当前活跃房间占用的最小编号)。
	// 注意: 这只决定 10.147.<seq>.0/24, 与网络 ID 无关。
	usedSeq := make(map[int]bool)
	rows, err := s.db.Query(`SELECT seq FROM rooms WHERE closed=0`)
	if err == nil {
		for rows.Next() {
			var sq int
			if rows.Scan(&sq) == nil {
				usedSeq[sq] = true
			}
		}
		rows.Close()
	}
	seq := 0
	for i := 1; i <= 254; i++ {
		if !usedSeq[i] {
			seq = i
			break
		}
	}
	if seq == 0 {
		s.mu.Unlock()
		errJSON(w, 500, "网络号耗尽(当前活跃房间已达上限 254 个)")
		return
	}
	// 注意: 这里不再删除 seq 上已关闭的残留记录。
	// 关闭房间要保留下来, net_seq 才能单调递增 —— 删掉历史行会让 MAX(net_seq)
	// 回退, 网络 ID 又被复用, 重新踩回"控制器上网络不存在"的坑。

	// 网络 ID 序号: 永不复用。
	// 销毁房间会删控制器网络, 若网络 ID 跟着 seq 复用, 新房间就会指向一个
	// 控制器上不存在的网络(客户端 ACCESS_DENIED 且不会自动补授权)。
	netSeq, err := allocNetSeq(s.db)
	if err != nil {
		s.mu.Unlock()
		errJSON(w, 500, "分配网络号失败: "+err.Error())
		return
	}
	nwid := fmt.Sprintf("bd5b8ad96c%06x", netSeq) // 控制器ID前缀 + 6位hex网络序号
	// 建网络(zas 实测:POST + PUT 配置)
	if err := s.zt.CreateNetwork(nwid, req.Name, ipFor(seq, 253), ipFor(seq, 254), cidrFor(seq)); err != nil {
		s.mu.Unlock()
		errJSON(w, 500, "创建网络失败: "+err.Error())
		return
	}
	res, err := s.db.Exec(`INSERT INTO rooms(seq, net_seq, network_id, game, name, password, owner_id) VALUES(?,?,?,?,?,?,?)`,
		seq, netSeq, nwid, req.Game, req.Name, req.Password, u.ID)
	if err != nil {
		_ = s.zt.DeleteNetwork(nwid)
		s.mu.Unlock()
		errJSON(w, 500, err.Error())
		return
	}
	roomID, _ := res.LastInsertId()
	s.touchRoom(roomID)
	s.mu.Unlock() // joinInternal 内部会再锁;不能持锁调用
	if !s.joinInternal(u, roomID, nwid, seq, w) {
		s.mu.Lock()
		s.destroyRoom(roomID, nwid)
		s.mu.Unlock()
		return // 原房间保留，清理未能加入的新房间
	}
	okJSON(w, map[string]any{"room_id": roomID, "network_id": nwid})
}

// handleJoinRoom: 目标登记成功后才移出旧房，同房间重复加入保持幂等。
// 客户端流程:先上报自己的 zt 节点ID(注册/登录时或本请求),join 后轮询 /api/join/status
func (s *Server) handleJoinRoom(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		RoomID   int64  `json:"room_id"`
		Password string `json:"password"`
		ZTNodeID string `json:"zt_node_id"` // 客户端本地实例的节点ID,可为空(用已保存的)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RoomID == 0 {
		errJSON(w, 400, "参数不合法")
		return
	}
	if req.ZTNodeID != "" {
		u.ZTNodeID = req.ZTNodeID
	}
	var nwid, pw string
	var seq int
	var closed int
	err := s.db.QueryRow(`SELECT network_id, password, seq, closed FROM rooms WHERE id=?`, req.RoomID).
		Scan(&nwid, &pw, &seq, &closed)
	if err != nil || closed == 1 {
		errJSON(w, 404, "房间不存在或已关闭")
		return
	}
	if pw != "" && pw != req.Password {
		errJSON(w, 403, "房间密码错误")
		return
	}
	if !s.joinInternal(u, req.RoomID, nwid, seq, w) {
		return
	}
	s.touchRoom(req.RoomID)
	okJSON(w, map[string]any{"room_id": req.RoomID, "network_id": nwid})
}

// joinInternal: 登记成员;授权不在这里阻塞等待(客户端本地 join 发生在请求返回之后,
// 控制器上此刻必然还没有成员)。授权由 handleJoinStatus 轮询时补做(幂等)。
func (s *Server) joinInternal(u *User, roomID int64, nwid string, seq int, w http.ResponseWriter) bool {
	if u.ZTNodeID == "" {
		errJSON(w, 400, "缺少 ZeroTier 节点ID,请先运行客户端")
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 与离房、解散和 GC 使用同一把锁，防止校验后目标房间被关闭。
	var closed int
	if err := s.db.QueryRow(`SELECT closed FROM rooms WHERE id=?`, roomID).Scan(&closed); err != nil || closed != 0 {
		errJSON(w, 404, "房间不存在或已关闭")
		return false
	}
	rows, err := s.db.Query(`SELECT user_id, assigned_ip FROM members WHERE room_id=?`, roomID)
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	used := make(map[string]bool)
	var existingIP string
	for rows.Next() {
		var userID int64
		var assignedIP string
		if err := rows.Scan(&userID, &assignedIP); err != nil {
			rows.Close()
			errJSON(w, 500, err.Error())
			return false
		}
		if userID == u.ID {
			existingIP = assignedIP
		} else {
			used[assignedIP] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	ip := ""
	if existingIP != "" && !used[existingIP] {
		ip = existingIP
	} else {
		for i := 0; i < 253; i++ {
			if candidate := ipFor(seq, i); !used[candidate] {
				ip = candidate
				break
			}
		}
	}
	if ip == "" {
		errJSON(w, 500, "房间已满")
		return false
	}
	// 先记录需撤销的旧授权；事务失败时不动原成员及原网络。
	oldRows, err := s.db.Query(`SELECT m.room_id, ro.network_id, m.zt_node_id
		FROM members m JOIN rooms ro ON ro.id=m.room_id
		WHERE m.user_id=? AND (m.room_id!=? OR m.zt_node_id!=?)`, u.ID, roomID, u.ZTNodeID)
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	type oldMembership struct {
		roomID            int64
		networkID, nodeID string
	}
	var previous []oldMembership
	for oldRows.Next() {
		var old oldMembership
		if err = oldRows.Scan(&old.roomID, &old.networkID, &old.nodeID); err != nil {
			break
		}
		previous = append(previous, old)
	}
	if err == nil {
		err = oldRows.Err()
	}
	oldRows.Close()
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	tx, err := s.db.Begin()
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO members(user_id, room_id, zt_node_id, assigned_ip) VALUES(?,?,?,?)
		ON CONFLICT(user_id,room_id) DO UPDATE SET
		authorized=CASE WHEN members.zt_node_id=excluded.zt_node_id AND members.assigned_ip=excluded.assigned_ip
			THEN members.authorized ELSE 0 END,
		zt_node_id=excluded.zt_node_id, assigned_ip=excluded.assigned_ip`, u.ID, roomID, u.ZTNodeID, ip)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM members WHERE user_id=? AND room_id!=?`, u.ID, roomID)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE users SET zt_node_id=? WHERE id=?`, u.ZTNodeID, u.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		errJSON(w, 500, err.Error())
		return false
	}
	for _, old := range previous {
		if old.nodeID != "" {
			if err := s.zt.DeauthorizeMember(old.networkID, old.nodeID); err != nil {
				log.Printf("撤销旧成员授权失败 room=%d: %v", old.roomID, err)
			}
		}
		if old.roomID != roomID {
			var remaining int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM members WHERE room_id=?`, old.roomID).Scan(&remaining); err == nil && remaining == 0 {
				s.destroyRoom(old.roomID, old.networkID)
			} else {
				s.touchRoom(old.roomID)
			}
		}
	}
	return true
}

// handleJoinStatus: 客户端轮询状态。未授权时补授权;已授权时按 30s 节流做只读
// 复核,区分"成员缺失(补授权)"与"网络缺失(需重建)",避免控制器与 DB 不一致
// 时客户端永远卡在 REQUESTING_CONFIGURATION。
func (s *Server) handleJoinStatus(w http.ResponseWriter, r *http.Request, u *User) {
	// 授权期间不能被切房/离房穿插，否则旧轮询可能把已撤销的授权加回来。
	s.mu.Lock()
	defer s.mu.Unlock()
	var roomID, seq int
	var nwid, ip string
	var ztNodeID string
	var authorized int
	err := s.db.QueryRow(`SELECT m.room_id, m.assigned_ip, m.zt_node_id, ro.network_id, ro.seq, COALESCE(m.authorized,0)
		FROM members m JOIN rooms ro ON ro.id=m.room_id WHERE m.user_id=?`, u.ID).
		Scan(&roomID, &ip, &ztNodeID, &nwid, &seq, &authorized)
	if errors.Is(err, sql.ErrNoRows) {
		okJSON(w, map[string]any{"joined": false})
		return
	}
	if err != nil {
		errJSON(w, 500, "暂时无法读取房间状态，请稍后重试")
		return
	}
	if authorized == 0 {
		// 从未授权成功:每次轮询都补授权(客户端此时是快轮询),成功后落库。
		if err := s.zt.AuthorizeMember(nwid, ztNodeID, ip); err != nil {
			log.Printf("authorize room %s node %s: %v", nwid, ztNodeID, err)
		} else if _, err := s.db.Exec(`UPDATE members SET authorized=1 WHERE user_id=? AND room_id=?`, u.ID, roomID); err != nil {
			log.Printf("标记授权状态失败 room=%d user=%d: %v", roomID, u.ID, err)
		} else {
			authorized = 1
			s.touchRoom(int64(roomID))
		}
	} else if s.shouldVerify(verifyKey(u.ID, roomID, nwid, ztNodeID, ip)) {
		// 已授权也要定期向控制器复核,但要把"成员缺失(可补)"与"网络缺失(需重建)"
		// 分开,且绝不把传输/5xx 故障当成成员丢失。
		ok, netMissing, verr := s.zt.CheckMember(nwid, ztNodeID, ip)
		switch {
		case verr != nil:
			// 控制器暂时不可达/5xx:状态未知,保持现状,下个周期再验,不误降级。
			log.Printf("verify member room %s node %s: %v", nwid, ztNodeID, verr)
		case netMissing:
			// 网络本身在控制器上不存在:补授权无用,需要重建房间,不做盲 PUT。
			log.Printf("控制器上网络 %s 不存在,房间 %d 需重建(不自动补授权)", nwid, roomID)
		case !ok:
			log.Printf("控制器未授权成员 room %s node %s,重新补授权", nwid, ztNodeID)
			if err := s.zt.AuthorizeMember(nwid, ztNodeID, ip); err != nil {
				log.Printf("re-authorize room %s node %s: %v", nwid, ztNodeID, err)
				// 补授权失败:降级为未授权,让后续轮询持续重试而不是假装已连上。
				if _, err := s.db.Exec(`UPDATE members SET authorized=0 WHERE user_id=? AND room_id=?`, u.ID, roomID); err == nil {
					authorized = 0
				}
			}
		}
	}
	okJSON(w, map[string]any{"joined": true, "authorized": authorized == 1, "ip": ip, "network_id": nwid, "room_id": roomID})
}

// handleRoomHeartbeat: 业务保活, 只更新房间活跃时间, 不碰控制器。
// 客户端约 60 秒发一次; 服务端另有 60 秒节流, 多端/多标签页也不会重复落盘。
func (s *Server) handleRoomHeartbeat(w http.ResponseWriter, r *http.Request, u *User) {
	var roomID int64
	var nwid string
	err := s.db.QueryRow(`SELECT m.room_id, ro.network_id
		FROM members m JOIN rooms ro ON ro.id=m.room_id WHERE m.user_id=?`, u.ID).
		Scan(&roomID, &nwid)
	if err != nil {
		okJSON(w, map[string]any{"joined": false})
		return
	}
	s.touchRoom(roomID)
	okJSON(w, map[string]any{"joined": true, "room_id": roomID, "network_id": nwid})
}

// leaveCurrent: 把用户从当前房间移出(收回授权 + 删记录)。无房间则静默。
// 规则: 若最后一个人离开房间, 立即销毁房间！
func (s *Server) leaveCurrent(u *User) error {
	var roomID int64
	var nwid, ztNodeID string
	err := s.db.QueryRow(`SELECT m.room_id, ro.network_id, m.zt_node_id
		FROM members m JOIN rooms ro ON ro.id=m.room_id WHERE m.user_id=?`, u.ID).
		Scan(&roomID, &nwid, &ztNodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ztNodeID != "" {
		if err := s.zt.DeauthorizeMember(nwid, ztNodeID); err != nil && ztStatus(err) != http.StatusNotFound {
			return fmt.Errorf("撤销网络授权失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM members WHERE user_id=? AND room_id=?`, u.ID, roomID); err != nil {
		return err
	}

	// 规则 2 落地: 检查房间剩余人数，如果最后一个人离开，立即销毁房间！
	var remaining int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM members WHERE room_id=?`, roomID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		s.destroyRoom(roomID, nwid)
	} else {
		s.touchRoom(roomID)
	}
	return nil
}

func (s *Server) handleLeaveRoom(w http.ResponseWriter, r *http.Request, u *User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.leaveCurrent(u); err != nil {
		errJSON(w, 502, "离开房间失败: "+err.Error())
		return
	}
	okJSON(w, map[string]any{"left": true})
}

// destroyRoom: 彻底销毁房间(删除 ZeroTier 控制器网络 + 标记 closed=1 + 清理 members)
func (s *Server) destroyRoom(roomID int64, nwid string) {
	if roomID <= 0 {
		return
	}
	log.Printf("[GC] 销毁房间 #%d (网络 %s)", roomID, nwid)
	if nwid != "" {
		if err := s.zt.DeleteNetwork(nwid); err != nil {
			log.Printf("[GC] 删除控制器网络 %s: %v", nwid, err)
		}
	}
	_, _ = s.db.Exec(`UPDATE rooms SET closed=1 WHERE id=?`, roomID)
	_, _ = s.db.Exec(`DELETE FROM members WHERE room_id=?`, roomID)
}

// touchRoom: 更新房间最后活跃时间。
// 60 秒内重复调用直接返回——该库 synchronous=2, 每次写都要 fsync(实测 6.1ms),
// 客户端心跳频率高, 不节流会把磁盘打满。
func (s *Server) touchRoom(roomID int64) {
	if roomID <= 0 {
		return
	}
	var recent int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM rooms
		WHERE id=? AND last_touch_at IS NOT NULL
		  AND (strftime('%s','now') - strftime('%s', last_touch_at)) < 60`, roomID).Scan(&recent)
	if recent > 0 {
		return
	}
	_, _ = s.db.Exec(`UPDATE rooms SET last_active_at=CURRENT_TIMESTAMP, last_touch_at=CURRENT_TIMESTAMP WHERE id=?`, roomID)
}

// startRoomGC: 后台定时巡检, 自动清理空置或长时间无人主动连接的房间
func (s *Server) startRoomGC() {
	go func() {
		// 启动时延迟 1 秒立即做一次清理
		time.Sleep(1 * time.Second)
		s.runGC()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.runGC()
		}
	}()
}

func (s *Server) runGC() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 规则 1: 房间内 0 人且空置超过 60 秒(1分钟)无人连接, 自动关闭销毁
	rows, err := s.db.Query(`
		SELECT ro.id, ro.network_id,
		       (strftime('%s', 'now') - strftime('%s', COALESCE(ro.last_active_at, ro.created_at))) AS idle_sec
		FROM rooms ro
		WHERE ro.closed = 0
		  AND (SELECT COUNT(*) FROM members m WHERE m.room_id = ro.id) = 0
	`)
	if err != nil {
		log.Printf("[GC] 巡检空房间查询失败: %v", err)
	} else {
		type emptyRoom struct {
			id   int64
			nwid string
			idle int64
		}
		var list []emptyRoom
		for rows.Next() {
			var r emptyRoom
			if rows.Scan(&r.id, &r.nwid, &r.idle) == nil {
				list = append(list, r)
			}
		}
		rows.Close()
		for _, r := range list {
			if r.idle >= 60 {
				log.Printf("[GC] 房间 #%d (网络 %s) 空置达 %d 秒且无人连接，自动销毁", r.id, r.nwid, r.idle)
				s.destroyRoom(r.id, r.nwid)
			}
		}
	}

	// 规则 2: 长时间无主动连接心跳的房间 (超过 10 分钟 = 600 秒无任何 API 请求/心跳)
	rows2, err2 := s.db.Query(`
		SELECT ro.id, ro.network_id,
		       (strftime('%s', 'now') - strftime('%s', COALESCE(ro.last_active_at, ro.created_at))) AS idle_sec
		FROM rooms ro
		WHERE ro.closed = 0
		  AND (strftime('%s', 'now') - strftime('%s', COALESCE(ro.last_active_at, ro.created_at))) >= 600
	`)
	if err2 != nil {
		log.Printf("[GC] 巡检失活房间查询失败: %v", err2)
	} else {
		type idleRoom struct {
			id   int64
			nwid string
			idle int64
		}
		var list2 []idleRoom
		for rows2.Next() {
			var r idleRoom
			if rows2.Scan(&r.id, &r.nwid, &r.idle) == nil {
				list2 = append(list2, r)
			}
		}
		rows2.Close()
		for _, r := range list2 {
			log.Printf("[GC] 房间 #%d (网络 %s) 超过 %d 秒无任何主动连接心跳，自动销毁", r.id, r.nwid, r.idle)
			s.destroyRoom(r.id, r.nwid)
		}
	}
}

// handleDismissRoom: 房主主动解散房间
func (s *Server) handleDismissRoom(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		RoomID int64 `json:"room_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RoomID == 0 {
		errJSON(w, 400, "参数不合法")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var nwid string
	var ownerID int64
	err := s.db.QueryRow(`SELECT network_id, owner_id FROM rooms WHERE id=?`, req.RoomID).Scan(&nwid, &ownerID)
	if err != nil {
		errJSON(w, 404, "房间不存在")
		return
	}
	if ownerID != u.ID {
		errJSON(w, 403, "只有房主可以解散房间")
		return
	}
	s.destroyRoom(req.RoomID, nwid)
	okJSON(w, map[string]any{"dismissed": true})
}

// ---------- 工具 ----------

func randToken() string {
	b := make([]byte, 24)
	_, _ = randRead(b)
	const hex = "0123456789abcdef"
	out := make([]byte, 48)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0xf]
	}
	return string(out)
}

func hashPwd(p string) string {
	// 测试版:sha256;生产应换 bcrypt。够用且零依赖。
	b := make([]byte, 32)
	sum := sha256Sum([]byte(p))
	for i := range b {
		b[i] = sum[i]
	}
	return fmt.Sprintf("%x", b)
}
