# LANBridge 桥联

一个**自托管的局域网联机工具**：把散落在不同网络的朋友，放进同一个虚拟局域网，
让只支持「局域网联机」的老游戏（文明6、帝国时代、泰拉瑞亚等）能正常看到彼此的房间。

底层不自研隧道，直接构建在 [ZeroTier](https://www.zerotier.com/) 之上：
自建 planet 根节点和网络控制器，客户端内置一份 ZeroTier 实例，
用户装一个客户端就能进房间、拿到虚拟 IP、看到所有成员，并在房间里直接聊天。

当前 UI 只开放了文明6房间（大厅、提示文案、创建表单都按它写死），
但协议层和网络层与具体游戏无关——换游戏只需要改 UI 文案，见
[docs/EXTENDING.md](docs/EXTENDING.md)。

> **项目来源**：原理来自对现成联机工具的协议与系统调用行为做逆向观察，
> 逐条验证后自己总结出来的；代码在 AI（Claude）协助下完成。
> 这里不复制任何闭源实现，只保留被验证有效的机制，并全部以可读、可替换的方式重写。
> 详见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

---

## 它解决什么问题

普通 VPN 能让两台机器互通，但联机游戏通常还有两个额外要求：

1. **房间发现**：游戏靠 UDP 广播/组播在局域网里找主机。虚拟网卡必须能收到这些包。
2. **流量走对网卡**：机器上有多块网卡时，游戏会按系统路由/接口跃点选出口，
   虚拟网卡优先级低就拿不到游戏流量。

LANBridge 把这两件事做成**进房间时自动配置、离开时自动还原**，
用户只需要「注册 → 建房/进房」，不用碰路由器、不用改系统设置。

---

## 架构一览

```
                    ┌──────────────────────────────────────────┐
                    │           房间服务器 (server/)            │
                    │  REST API · SQLite · ZeroTier 控制器桥接   │
                    │                                          │
   HTTP 控制面      │  /api/room/create  建网络 + 登记房主       │
  ────────────────► │  /api/room/join    登记成员、分配虚拟 IP   │
   (仅信封与授权)   │  /api/join/status  轮询并补授权            │
                    │  /api/planet       下发自建 planet 文件    │
                    └───────────────┬──────────────────────────┘
                                    │ 本地 127.0.0.1:9993
                                    ▼
                    ┌──────────────────────────────────────────┐
                    │      ZeroTier 控制器 + planet 根节点      │
                    │   每个房间 = 一个私有网络 (10.147.X.0/24) │
                    └───────────────┬──────────────────────────┘
                                    │ 加密 P2P（UDP 打洞 / 中继）
          ┌─────────────────────────┼─────────────────────────┐
          ▼                         ▼                         ▼
    ┌───────────┐             ┌───────────┐             ┌───────────┐
    │ 客户端 A  │             │ 客户端 B  │             │ 客户端 C  │
    │ LANBridge │             │ LANBridge │             │ LANBridge │
    │  + zt-node│◄── 10.147 ─►│  + zt-node│◄── 10.147 ─►│  + zt-node│
    └───────────┘   数据面     └───────────┘   数据面     └───────────┘
        ▲                                                         ▲
        └──────────── 房间聊天 / 游戏流量：直连虚拟 IP ─────────────┘
```

**控制面与数据面严格分离。**

- 控制面（HTTP + ZeroTier 控制器）只负责**发信封**：建房、登记成员、授权、下发 IP。
  消息内容、游戏流量完全不经过服务器。
- 数据面是 ZeroTier 的加密 P2P 网络。能打洞就直连，打不通才走 planet 中继。

这样服务器只是个很轻的协调者：一台 1C1G 的机器足够，断线也不会带走已建立的连接。

---

## 核心设计

### 1. 一个房间 = 一个私有 ZeroTier 网络

服务端为每个房间在控制器上创建一个私有网络，网段固定为 `10.147.<房间号>.0/24`：

- 房间号 1..254，因此同一时刻最多 254 个活跃房间，天然隔离；
- 成员 IP 由服务端按成员序号分配（`.1` ~ `.253`），保存在 SQLite 里；
- 房主离开且房间空了 → 立刻删网络、标记房间关闭。

**为什么不用「一个大网络 + 靠软件分房间」**：隔离要靠服务端代码保证，一处疏漏就串房；
用独立网络则是 ZeroTier 在协议层就隔离了，代价只是一次控制器的 `POST/PUT`，很便宜。

### 2. 授权闭环是轮询补做的

ZeroTier 的成员授权必须发生在**客户端已经用这个节点 ID join 之后**。
但建房接口返回时，客户端还没开始 join——此刻控制器上根本不存在这个成员。

所以流程是：

```
客户端 POST /api/room/join   → 服务端登记成员、分配 IP（不阻塞等授权）
客户端 zt join <network_id>  → 本地实例加入网络，状态 REQUESTING_CONFIGURATION
客户端轮询 /api/join/status  → 服务端 PUT+GET 控制器授权，确认成功才回 authorized=true
客户端看到 authorized && status=OK 且有 IP → 进入房间
```

这里**必须 PUT 之后再用 GET 确认**，不能只看 PUT 返回 200——
控制器接受请求和成员真正出现在列表里之间有延迟，只看 PUT 会误报成功。

### 3. 房间聊天：数据面直连，服务器零参与

房间内文字聊天不走服务器，浏览器直接对**每个成员的虚拟 IP** 发 HTTP：

```
POST http://10.147.3.7:8890/chat/send?net=<网络ID>   {id, kind:"text", text, name, ts}
GET  http://10.147.3.7:8890/chat/messages?net=<网络ID>
```

每个客户端同时是发送方和接收方：本地内存保留最近 500 条，供别人拉取。
不做离线投递、不持久化——聊天只保证「在线成员之间的实时文字互通」。

**准入校验**不是简单的「你连得上就让你发」，而是：

- 请求带的网络 ID 必须是本机**已加入且状态 OK** 的网络；
- 连接的实际**本地目标地址**必须等于该网络分配给我的地址；
- 来源地址必须落在同一网段内。

用实际 socket 地址而不是 `Host` 或 `X-Forwarded-For`，避免伪造目标地址绕过校验。
这仍是网络层准入，不是成员身份认证（同网段的节点天然互访）——够用且有明确文档说明。

### 4. 网络调优：可逆、可崩溃恢复

见 [nettune.go](client/nettune.go)。进入房间后：

| 动作 | 平台 | 作用 |
|---|---|---|
| 降低虚拟网卡跃点（metric → 1） | Windows | 让游戏流量优先走隧道 |
| 广播复制 | ZeroTier 网络配置（服务端） | `enableBroadcast: true`，所有成员都能收到广播/组播 |

Windows 侧用 PowerShell 精确改目标网卡的跃点，**改前的原值写入
`~/.lanbridge/nettune-state.json`**；离开房间、正常退出、甚至上次进程被强杀后下次启动，
都会把跃点还原。不会把用户的网卡永久改坏。

macOS 不做跃点调整：它没有这个概念，出口由网络服务顺序决定，硬改会影响用户全机上网。

---

## 快速开始

### 客户端

从 [Releases](https://github.com/yvzhu0407/lanbridge/releases) 下载安装包（免费开源项目没有购买代码签名证书，
首次打开会被系统拦一次，处理方式见 [docs/USAGE.md](docs/USAGE.md#第一次启动)）：

| 平台 | 文件 | 说明 |
|---|---|---|
| macOS（Apple Silicon / Intel） | `LANBridge-<版本>-macOS-universal.dmg` | 拖进「应用程序」即可，自带 ZeroTier |
| Windows x64 | `LANBridge-<版本>-windows-amd64-setup.exe` | 标准安装包，自带 ZeroTier 与 TAP 驱动，需 WebView2 Runtime |

首次启动：Windows 会请求 UAC 授权（装 TAP 驱动），macOS 启动 ZeroTier 时会要管理员密码。
之后：注册 → 创建/加入房间 → 看到虚拟 IP 和成员 → 在房间里聊天。
游戏侧选择「局域网 / LAN」即可在列表里看到主机。

### 自托管服务端

需要一台能跑 ZeroTier 的 Linux（推荐 1C1G 以上），并在上面搭好
**自建 planet + 私有控制器**（这是 ZeroTier 官方支持的能力，步骤见 `docs/DEPLOY.md`）。

```bash
# 环境变量
export ADDR=0.0.0.0:8800                 # 监听地址
export DB=/opt/lanbridge/lanbridge.db    # SQLite 路径
export ZT_BASE=http://127.0.0.1:9993     # 控制器本地 API
export ZT_TOKEN=$(cat /var/lib/zerotier-one/authtoken.secret)
export PLANET=/opt/lanbridge/planet      # 下发给客户端的 planet

./lanbridge-server
```

客户端指向自己的服务端，有两种方式：

```bash
# 1) 环境变量
LANBRIDGE_SERVER=http://你的服务器:8800 ./lanbridge-client

# 2) 配置文件 ~/.lanbridge/config.json
{ "server": "http://你的服务器:8800" }
```

优先级：环境变量 > `config.json` > 内置默认值。

---

## 目录结构

```
lanbridge/
├── client/                 客户端（单文件绿色版）
│   ├── main.go             本地 HTTP 服务、UI 嵌入、远端 API 代理、join 编排
│   ├── chat.go             房间聊天服务（8890，P2P 直连）
│   ├── nettune.go          网络调优编排（通用部分）
│   ├── nettune_windows.go  Windows：接口跃点调整/恢复
│   ├── nettune_darwin.go   macOS：显式 no-op（附原因）
│   ├── zt_windows.go       Windows：内嵌 ZT/TAP 解压、驱动安装、防火墙
│   ├── zt_darwin.go        macOS：内嵌 ZT 解压、执行权限修复
│   └── static/index.html   UI 单页（无构建步骤）
├── server/                 房间服务器
│   ├── main.go             REST API + SQLite + 控制器桥接
│   ├── crypto.go           密码哈希、token
│   └── main_test.go
├── cmd/bcastprobe/         局域网 UDP 广播收发探针（排查游戏能不能发现主机）
├── docs/                   设计文档、部署、扩展与使用说明
├── fetch-zt-embed.sh       从官方包提取内嵌 ZeroTier（构建前先跑一次）
├── build-mac-app.sh        macOS .app 打包（universal + ad-hoc 签名）
├── build-macos-dmg.sh      macOS .dmg 安装包
├── build-windows-setup.sh  Windows 客户端 + setup.exe 安装包
└── lanbridge.nsi           Windows 安装包定义（NSIS）
```

构建产物输出到 `dist/`（已加入 `.gitignore`，不入库）。

## 排查：游戏里看不到主机

先确认广播本身能不能跨机器收到。`cmd/bcastprobe` 是一个不依赖 ZeroTier 代码的
UDP 收发探针，把「网络没通」和「游戏没发广播」分开：

```bash
# A 机（接收方）
go build -o bcastprobe ./cmd/bcastprobe && ./bcastprobe -listen -port 39000

# B 机（发送方），10.147.<房间号>.255 是本房间网段的广播地址
./bcastprobe -send -addr 10.147.45.255:39000 -msg "hello from B"
```

A 机打印出来源 IP 和内容，说明虚拟网卡的广播转发是通的；
此时游戏仍然看不到主机，问题就在游戏端（端口、防火墙、游戏本身的局域网发现）。

## 从源码构建

```bash
./fetch-zt-embed.sh 1.16.2   # 准备内嵌 ZeroTier（第三方二进制，不入库）
go test ./...                # 需在 macOS 或 Windows 上跑（client 无 Linux 实现）
./build-mac-app.sh           # 产出 universal 的 dist/LANBridge.app 与裸二进制
./build-macos-dmg.sh         # 产出 dist/LANBridge-<版本>-macOS-universal.dmg
./build-windows-setup.sh     # 产出 Windows 客户端与 setup.exe 安装包（需 makensis）
```

安装包脚本用 `VERSION=x.y.z` 指定版本号，默认 `1.0.0`。
Windows / 服务端构建方式和内嵌文件说明见 [docs/EXTENDING.md](docs/EXTENDING.md#6-构建)。
客户端安装与使用见 [docs/USAGE.md](docs/USAGE.md)。
各层心跳频率与连接维持机制见 [docs/CONNECTION.md](docs/CONNECTION.md)。

### 发布

`.github/workflows/release.yml` 在推送 `v*` 标签时自动构建并创建 Release：

```bash
git tag v1.0.0 && git push origin v1.0.0
```

产物：macOS `.dmg`、Windows `setup.exe`、Linux amd64/arm64 服务端、`SHA256SUMS.txt`。
首次对某个 tag 运行会创建 Release；若 Release 已存在（例如手动建过），会自动改为补传资产。

---

## 二次开发

项目刻意保留了几处清晰的扩展点，详见 [docs/EXTENDING.md](docs/EXTENDING.md)：

1. **聊天消息 `kind` 字段**：当前只接受 `text`，校验在 `chat.go` 一处。
   想加文件/图片/表情，改这一个校验 + 前端渲染即可，网络层不用动。
2. **`netTune` 接口**：平台调优是接口，加新平台只要实现 `LowerMetric/RestoreMetric`。
3. **`applyClientConfig`**：客户端配置集中在 `~/.lanbridge/config.json`。
4. **服务端 `ZTClient`**：只实现了用到的最小控制器操作集，加新能力照着加方法即可。
5. **UI 无构建步骤**：`static/index.html` 直接改直接生效，不需要 npm。

---

## 已知限制

- 密码使用 SHA-256（生产环境应换 bcrypt/argon2），token 明文存 `~/.lanbridge/session.json`。
- 房间聊天不做离线投递和历史持久化；成员离线期间的消息不会补发。
- macOS 不做网卡跃点调整（原因见上）。
- Windows 真机与多个杀毒软件的兼容性仍在验证；部分安全软件会拦截未签名程序内嵌的 ZeroTier。
- 房间上限 254 个（网段编号决定）；单房间成员上限 253。

---

## 许可

本项目代码以 [MIT License](LICENSE) 发布。

客户端内嵌的 ZeroTier 二进制、TAP 驱动**不是**本项目的作品，
遵循其上游许可（ZeroTier 采用 BSL 1.1，驱动来自 OpenVPN TAP-Windows），
分发时请一并遵守。详见 [LICENSE](LICENSE) 末尾的第三方说明。
