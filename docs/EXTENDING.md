# 二次开发指南

本项目的结构刻意保持扁平：客户端三个 Go 文件包里能读完，服务端只有一个 `main.go`，
UI 是单页 HTML 无构建步骤。下面是几个已经预留好的改动入口。

---

## 1. 扩展房间聊天（最常见）

### 现状

`client/chat.go` 的消息模型：

```go
type chatMsg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	TS   int64  `json:"ts"`
	Kind string `json:"kind"` // 目前固定为 text
	Text string `json:"text,omitempty"`
}
```

`kind` 字段**是故意保留的**。当前 `handleChatSend` 只接受 `kind == "text"`，
其余一律 400（有单测覆盖，见 `chat_test.go` 最后一段）。

### 加一种消息类型

以「图片」为例：

**服务端（`client/chat.go`）**

```go
// handleChatSend 里的校验
if err := json.NewDecoder(...).Decode(&m); err != nil || m.ID == "" ||
    (m.Kind != "text" && m.Kind != "image") ||
    strings.TrimSpace(m.Text) == "" {
    errJSON(w, 400, "消息不合法")
    return
}
```

内存存储和准入校验都**不需要改**——它们只认 `kind` 是字符串。

如果图片体积大，注意 `http.MaxBytesReader(w, r.Body, 64<<10)` 这个上限，
按需调大或改成 base64 分片。

**前端（`client/static/index.html`）**

发送：在 `chatSendText` 旁边加一个图片入口，构造 `{kind:'image', text:<dataURL>}`。

渲染：`chatRenderMsgs` 里按 `m.kind` 分支：

```js
const body = m.kind === 'image'
  ? `<img src="${escapeHtml(m.text)}" style="max-width:240px;border-radius:8px">`
  : `<div class="bubble">${escapeHtml(m.text)}</div>`;
```

**同步改测试**：`chat_test.go` 里把「非 text 被拒绝」的用例换成你允许的类型集合。

### 设计上的提醒

- 聊天是**浏览器轮询**（1.5 秒一次）拉全量增量，不是长连接。
  大文件不要走这条路——带宽和内存都会爆。
- 消息只在内存里存 500 条，重启即失。要持久化，在 `handleChatSend` 里加落盘，
  但注意别把「一次性房间」的语义改掉。
- 本机那份消息**不计入送达数**，改逻辑时保留这个区分，否则「已送达」会虚高。

---

## 2. 加一个新平台（Linux 桌面等）

客户端目前的平台相关部分有三块：

| 职责 | 文件 | 接口 |
|---|---|---|
| ZeroTier 二进制解压/启动 | `zt_*.go` | `ensureZTRuntime`、`startZTInstance`、`installDriver`、`platformDiagnose` |
| 网卡调优 | `nettune_*.go` | `netTune` 接口 |
| 窗口 | `window_*.go` / `elevation_*.go` | `runWindow`、`checkElevation` |

加 Linux 支持的最小路径：

```go
//go:build linux

package main

type linuxNetTune struct{}

func platformNetTune() netTune { return linuxNetTune{} }

// 接口路由 metric 可用 netlink 或 `ip route` 调
func (linuxNetTune) LowerMetric(dev string) (int, error) {
    // 返回改动前的原值；没有这个概念就 return -1, nil
}

func (linuxNetTune) RestoreMetric(dev string, prev int) error { return nil }
```

`nettune.go` 的编排逻辑（记录状态、崩溃恢复、换网卡先恢复再调）是通用的，
不用改。

`zt_linux.go` 照 `zt_darwin.go` 写：解压 `zerotier-one` + `MacEthernetTapAgent` 的
Linux 等价物，注意 Linux 上创建 TAP 需要 root 或 `CAP_NET_ADMIN`。

---

## 3. 换服务端地址 / 内置你的部署

`client/main.go`：

```go
var (
	listenAddr = envOr("LANBRIDGE_CLIENT_ADDR", "127.0.0.1:8899")
	serverBase = strings.TrimRight(envOr("LANBRIDGE_SERVER", "http://172.252.125.40:8800"), "/")
)
```

- **做自建发行版**：把默认值改成你自己的地址，重新编译分发。
- **让用户自选**：不动代码，用 `~/.lanbridge/config.json`：

```json
{ "server": "http://你的服务器:8800", "listen": "127.0.0.1:8899" }
```

读取逻辑在 `applyClientConfig()`，优先级：环境变量 > config.json > 默认值。
要加更多配置项，往 `clientConfig` 结构体里加字段即可。

---

## 4. 服务端加接口

`server/main.go` 的结构很直白：

```go
// 1. 路由注册
mux.HandleFunc("GET /api/xxx", s.auth(s.handleXxx))   // s.auth 包一层就带上用户

// 2. 处理函数
func (s *Server) handleXxx(w http.ResponseWriter, r *http.Request, u *User) {
	okJSON(w, map[string]any{...})
}
```

### 调 ZeroTier 控制器

`ZTClient` 只实现了用到的最小操作集。加新能力照着现有方法写：

```go
func (z *ZTClient) SetNetworkOption(nwid string, opt map[string]any) error {
	_, err := z.req("PUT", "/controller/network/"+nwid, opt)
	return err
}
```

**两个已验证的坑**：

1. 创建网络必须 `POST` 一个**空的 JSON `{}`**，传 `nil` body 会 500；
2. 授权成员后要 `GET` 确认，只信 PUT 的 200 会误报成功。

### 加数据表

`migrate()` 里用 `CREATE TABLE IF NOT EXISTS`，已有表加列要像
`last_active_at` 那样先查 `PRAGMA table_info` 再 `ALTER TABLE`——
避免老库升级直接报错。

---

## 5. 改 UI

`client/static/index.html` 是完整的单页应用：内联 CSS、内联 JS、无依赖、无构建。
改完直接用浏览器打开 `http://127.0.0.1:8899` 即可看到效果（开发时不用重编 Go）。

主要结构：

```
<header>          logo、节点状态、登录
<nav>             三个视图的切换 pill
#view-lobby       大厅：房间列表、搜索、创建
#view-room        房间：IP 指引、聊天面板、成员延迟表
#view-console     控制台：日志、配置
<script>          state 对象 + 各视图的渲染/轮询函数
```

状态集中在 `state` 对象里，轮询入口是 `pollRoomStatus()`（3.5 秒）和
`chatSyncState()`（1.5 秒）。加新面板时，渲染函数挂到 `updateRoomView()` 或
`switchView()` 的分支里即可。

---

## 6. 构建

首次从源码构建前，先准备内嵌的 ZeroTier 运行时（体积大、第三方，不入库）：

```bash
./fetch-zt-embed.sh 1.16.2     # 从官方发行包提取到 client/zt-embed*/
```

脚本会解出 macOS 的 `zerotier-one`（universal）与 `MacEthernetTapAgent`，
以及 Windows 的 `zerotier-one_x64.exe`，并从 ZeroTierOne 源码树的
`ext/bin/tap-windows-ndis6/x64/` 取 TAP 驱动三个文件（`zttap300.inf/.sys/.cat`）。
解包 Windows `.msi` 需要 `msiextract`（`brew install msitools`）或 `7z`；
只想在 macOS 上跑测试时加 `--mac-only`。

然后：

```bash
# 测试（client 只能在 macOS / Windows 上跑，见下）
go test ./server

# 客户端
CGO_ENABLED=1 GOOS=darwin  GOARCH=arm64 go build -o dist/lanbridge-client-mac         ./client
GOOS=windows GOARCH=amd64 go build -ldflags='-H windowsgui' \
  -o dist/lanbridge-client-windows.exe ./client

# macOS .app 打包（图标 + Info.plist + lipo 合并 universal + ad-hoc 签名），
# 输出目录和版本号均可传参
VERSION=1.0.0 ./build-mac-app.sh

# 安装包
VERSION=1.0.0 ./build-macos-dmg.sh        # → dist/LANBridge-1.0.0-macOS-universal.dmg
VERSION=1.0.0 ./build-windows-setup.sh    # → dist/LANBridge-1.0.0-windows-amd64-setup.exe

# 服务端
GOOS=linux GOARCH=amd64 go build -o dist/lanbridge-server-linux ./server
```

Windows 安装包用 NSIS 生成，脚本是仓库根目录的 `lanbridge.nsi`：

```bash
brew install makensis   # 解 .msi 还需要 msitools（fetch-zt-embed.sh 用）
```

改安装行为（默认安装目录、开机自启、快捷方式、卸载清理范围）都改这个文件。
它按 `-DVERSION` / `-DVERNUM` / `-DSRCDIR` / `-DOUTFILE` 接收参数，
`build-windows-setup.sh` 已经处理好路径转换和版本号格式（`VIProductVersion` 只接受纯数字）。

**卸载器刻意不删** `%ProgramData%\lanbridge\`、TAP 驱动和防火墙规则：
驱动是系统级共享资源，同机可能还有别的 ZeroTier 在用，误删会导致对方断网。

darwin 的窗口层是 cgo（Objective-C），所以：
**mac 二进制必须在 macOS 主机上编**，交叉架构时要显式 `CGO_ENABLED=1`，
否则 `runWindow` 会报未定义。Windows 侧是用户态 API（无 cgo），可在任意平台交叉编译。

> **平台限制**：`client/` 依赖 `*_darwin.go` / `*_windows.go` 里的平台实现，
> 没有 Linux 分支，所以 `go test ./...` 在 Linux 上会因缺符号失败。
> 在 Linux 上只测 `./server`；CI 也是如此（服务端在 Ubuntu、客户端在 macOS runner）。

内嵌的 ZeroTier 版本要和你服务端的控制器版本大体一致，
否则控制器 API 的行为可能有差异（例如创建网络时的必填字段）。

---

## 7. 测试

现有测试覆盖：

| 文件 | 覆盖内容 |
|---|---|
| `client/chat_test.go` | 准入校验（网段/IPv6/回环/跨房间隔离）、非 text 拒绝、CORS 预检 |
| `client/nettune_test.go` | 跃点记录/恢复/幂等/换网卡/平台 no-op/恢复失败保留状态/文件权限 |
| `server/main_test.go` | IP 分配、成员加入、授权状态 |

`nettune` 的测试用 `newNetTune` 这个钩子替换平台实现，不碰真实网卡：

```go
old := newNetTune
newNetTune = func() netTune { return &fakeNetTune{prev: 25} }
t.Cleanup(func() { newNetTune = old })
```

给平台代码加测试时照这个模式写，避免测试真的去改系统设置。
