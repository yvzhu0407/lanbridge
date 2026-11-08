# 连通性故障复盘：授权缓存失效 + 单实例重复拉起

> 记录 2026-11-08 线上「一直连不上 / 不能一次连成功」故障的链路、复现与修复。
> 本文修正 [`CONNECTION.md`](CONNECTION.md) §5.1「授权只做一次（若 authorized=1 就不碰控制器）」的假设：
> 只做一次的前提是"服务端账本与控制器实时状态永久一致"，而这个前提无法保证——
> 账本一旦与控制器背离，客户端就永远拿不到配置。
>
> **修订说明**：初稿把「控制器重启 / 重置导致成员丢失」当作已确认根因。
> 对照 ZeroTier 1.16 上游后需澄清：控制器默认把网络与成员**持久化到文件**，
> 普通重启应能恢复，且 `GET member` 返回 404 既可能是**成员缺失**也可能是**网络缺失**
> （二者都是 404，需先 `GET network` 区分）。因此「重启丢成员」目前是**待核实假设**，
> 不是已证实的根因。**已证实的代码缺陷**是：`authorized=1` 分支从不复核控制器状态，
> 只要账本与控制器出现任何背离（成因待定），就再也无法自愈。§3 已据此重写。

## 1. 现象

两台机器（mac1 `10.147.1.1`、yvzhu2 `10.147.1.2`）本应通过自建 ZeroTier 在同一虚拟局域网内。故障时：

| 视角 | 观测 |
|---|---|
| 客户端网络状态 | `status = REQUESTING_CONFIGURATION`，`ips = []`（始终拿不到 IP） |
| 客户端提示 | 「网络状态异常：NOT_FOUND」 |
| 服务端 `/api/me` | `room_id=52`，`network_id` 已下发，`ip=10.147.1.2`（注意：`/api/me` 不返回 `authorized`，只反映"服务端认为你在房间里、分到了哪个 IP"） |
| 服务端 `/api/room/members` | 2 名成员，IP 分配正确 |
| planet / 控制器 peer | 有活跃路径，`lastReceive` 仅数秒前，链路可达 |
| 数据面 | 网卡 `ethernet_32776` 存在但 `Disconnected`、无 IP；ping / TCP 8890 全不通 |

一句话：**服务端账本认为"成员在房间、已分配 IP"，控制器却从不把网络配置推给本节点**，节点就永远停在等待配置的状态。

## 2. 全链路：控制面 vs 数据面

理解故障的关键是把"账本"和"实时状态"分开：

```
客户端本地 ZeroTier 节点                服务端
  │  NETWORK_CONFIG_REQUEST  ───────▶  ZeroTier 控制器(127.0.0.1:9993)
  │  ◀───────  网络配置(revision / 成员 IP)  │
  │                                        │
  │                                    房间服务(8800)  ── SQLite: rooms / members
  │                                        │            members.authorized 只是账本
  └─ 用配置里的 IP 建网卡、跑数据面           └─ 通过 ZT 控制器 API 授权成员
```

- **控制器**才是把"你已授权、你的 IP 是 X"真正推给节点的一方；只有收到配置，节点才会把 IP 落到网卡上。
- **服务端 DB** 的 `members.authorized` 只是"服务端曾经成功确认过"的历史记录，**不等于控制器此刻的状态**。
- 两者一旦不一致（DB=1，控制器已不认识你），就出现本次故障：账本绿、链路红。

## 3. 根因一：授权缓存是一次性的，从不复核（已证实的代码缺陷）

`server/main.go` `handleJoinStatus`（客户端在房间内每 30s 轮询一次）原逻辑：

```
查库 → 若 authorized=0：调 AuthorizeMember，成功则 SET authorized=1
       若 authorized=1：直接返回 authorized=true，不碰控制器   ← 缺陷所在
```

这正是 `CONNECTION.md` §5.1 为了压掉"每 3.5s PUT+GET"而做的优化。但副作用是：
**只要服务端 DB 的 `authorized=1` 与控制器实时状态出现任何背离，服务端就永远不再补授权**，
节点端便固定在 `REQUESTING_CONFIGURATION`，只能靠用户手动"离开房间 → 重新进房"恢复
（`leaveCurrent` 删成员行、`joinInternal` 重新插入 `authorized=0`，下次轮询才重新 `AuthorizeMember`）。

本次线上正是这样恢复的：`/api/room/leave` → `/api/room/join` → 轮询 `/api/join/status`，
命中 `authorized=0` 分支后 `AuthorizeMember` 成功，控制器随即把 `revision=2` 的配置推下来，
`status` 立刻变 `OK`、`ips=10.147.1.2/24`。

**背离是怎么产生的——待核实假设，非定论。** 初稿归因于"控制器重启丢了成员"，但对照上游需澄清：

- ZeroTier 控制器默认把网络与成员**持久化到文件**，普通重启应能恢复，不应默认当作"丢成员"；
- `GET /controller/network/{nwid}/member/{nodeid}` 对「成员不存在」和「网络不存在」
  **都返回 404**，必须先 `GET /controller/network/{nwid}` 才能区分；
- 节点端的 `NOT_FOUND`（网络对象缺失）与 `REQUESTING_CONFIGURATION`（等待配置）是**不同状态**，
  不能据此断定"成员授权丢失"。

所以真正的定论只有一条：**服务端从不复核**。至于那次线上究竟是成员被删、网络重建、
还是其它原因导致账本与控制器背离，现有证据不足以区分——修复方式（复核 + 分情况处理）
对上述任一种成因都成立，见 §6.1。

## 4. 根因二：客户端单实例判定不可靠，可能重复拉起 zt-node（加固项 / 待观察的诱因）

`client/main.go` `ensureZTReady` 原来用 `ztRunning()`（即 CLI `info` 成功）判定实例是否存活：

```go
if !ztRunning() { startZTInstance() }   // 旧逻辑
```

`info` 在节点启动窗口、请求抖动、权限异常时都可能瞬时失败，于是客户端可能误以为"没在跑"，
**再拉起一个 zt-node**。若两个实例抢同一块 TAP，日志里会出现：

```
ERROR: unable to remove ip address 10.147.1.1/24
```

网卡上的 IP 被反复增删，连接随之抖动。

**因果强度说明**：`~/.lanbridge/zt-node.log` 里密集的 `Starting Control Plane`
曾是"重复拉起"的旁证，但正常重启也会产生同样日志，无法单凭它证明本次故障由重复实例引起。
因此这项作为**确定性加固**保留（TCP 探测本身正确且无副作用），不把它当作本次故障的已证实根因。

**修复遵循 ZeroTier 自己的标准单实例机制**（不是自造进程跟踪）：
守护进程独占 TCP 控制端口（`-p` 同时用于 UDP 与 TCP/HTTP，见 `zt-node -h`），
第二个同端口实例 bind 失败即退出。因此客户端只需探测**该控制端口是否已被监听**：

```go
if !ztDaemonListening() { startZTInstance() }   // 新逻辑
```

- `ztControlPort()`：优先读 `<home>/zerotier-one.port`（`-p0` 随机端口时靠它发现），否则退回配置端口；
- `ztDaemonListening()`：对 `127.0.0.1:<port>` 做 300ms TCP 探测；
- 端口已监听 = 已有实例在跑或正在启动 → **只等，不再拉起**。

## 5. 复现

### 5.1 确定性复现（单元测试）

`server/main_test.go` 用 `fakeController` 模拟控制器三种状态（网络是否存在 / 成员是否存在 / 是否授权），
并统计 `PUT`、`member GET`、`network GET` 次数：

- `TestJoinStatusSelfHealsWhenMemberMissing`
  - 造状态：DB 里 `members.authorized=1`，但控制器 `GET member` 返回 **404**、`GET network` 返回 200（成员缺失、网络仍在）；
  - 断言：轮询后**必须发生一次 PUT（重新授权）**，且返回 `authorized:true`，且确实做了 `network GET` 区分。
  - 修复前该用例会失败：`authorized=1` 分支根本不碰控制器。
- `TestJoinStatusNetworkMissingDoesNotPut`
  - 造状态：`GET member` 404、`GET network` 也 404（网络本身缺失）；
  - 断言：**不得盲目 PUT**（补授权无用），但必须查网络以区分。
- `TestJoinStatusUnauthorizedReauthorizes`
  - 造状态：成员存在但 `authorized:false`；
  - 断言：补授权 PUT 并返回已授权。
- `TestJoinStatusTransportErrorKeepsState`
  - 造状态：成员 GET 返回 **5xx**（暂时性故障）；
  - 断言：保持已授权、不降级、不 PUT，下周期再验。
- `TestJoinStatusVerifyThrottled`
  - 已授权且控制器正常：相邻两次轮询只允许**一次只读 GET**、**零次 PUT**（验证节流，避免回到 §4.1 的高频写）。

运行：

```bash
go test ./server/ -run 'TestJoinStatus' -v
```

### 5.2 现场复现要点（手工）

1. 让客户端进入房间、拿到 `status=OK`；
2. 在控制器侧**手动删除**该成员（`DELETE /controller/network/<nwid>/member/<nodeid>`，或删除整个网络），**不动服务端 DB**；
3. 观察客户端：`/api/zt/status` 卡 `REQUESTING_CONFIGURATION`、`ips=[]`，
   而服务端 `/api/room/members` 仍列出该成员并分配了 IP（账本绿、链路红）—— 即本文故障态；
4. 旧行为下不会自愈，只能"离开→重进"；修复后 ≤30s 内自动补授权恢复。

## 6. 修复

### 6.1 服务端：定期只读复核 + 分情况处理

新增 `ZTClient.CheckMember(nwid, nodeID, ip) (ok, networkMissing bool, err error)`——**只读**：

- 先 `GET member`，校验 `authorized==true` 且 `ipAssignments` 含预期 IP；
- 若 `GET member` 返回 **404**（上游对"成员缺失"和"网络缺失"都返回 404），再 `GET network` 区分：
  - `GET network` 也 404 → `networkMissing=true`（网络重建需求）；
  - `GET network` 200 → 成员缺失（可补授权）；
- 其它非 404 错误（传输失败 / 5xx）原样返回，视为暂时性故障。
- 只读比 `PUT+GET` 轻，适合高频。

`handleJoinStatus` 的 `authorized=1` 分支按 30s 节流调用 `CheckMember`，三分支处理：

```
若 authorized=0：每次轮询补授权（客户端此时是 2s 快轮询）
若 authorized=1：按 30s 节流做一次 CheckMember 复核
        - err != nil（控制器不可达 / 5xx）：保持现状，下周期再验，不误降级、不 PUT
        - networkMissing：网络本身缺失，补授权无意义 → 只记日志提示"需重建房间"，不盲 PUT
        - ok == false 且网络仍在（成员缺失或未授权）：重新 AuthorizeMember
              · 成功 → 维持 authorized=1
              · 失败 → 降级 authorized=0，让后续轮询持续重试
```

节流用 `Server.shouldVerify`（`server/main.go:235`）+ `memberVerifyInterval=30s`
（与客户端授权后的 `POLL_SLOW=30s` 对齐），key 由 `verifyKey(userID, roomID, nwid, nodeID, ip)` 生成，
保证换房 / 换节点 / 换 IP 时立即重新复核。

**为什么是复核而不是回到"每次都 PUT+GET"**：`CONNECTION.md` §4.1 已证明高频 `PUT+GET`
会吃满 1 vCPU 服务器。这里把稳态开销压到「每成员每 30s 一次 GET」，既轻又能自愈。

### 6.2 客户端：用控制端口做标准单实例判定

见 §4。改 `ensureZTReady` + 新增 `ztControlPort/ztDaemonListening`（`client/main.go:260/384/397`）。

## 7. 旧 / 新行为对照

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 账本与控制器背离（成员缺失/未授权），DB 仍 authorized=1 | 永远 `REQUESTING_CONFIGURATION`，需手动退房重进 | ≤30s 只读复核发现并自动补授权 |
| 网络本身缺失（`GET network` 404） | 同上，且补授权会一直失败 | 复核识别为网络缺失，**不盲 PUT**，日志提示需重建房间 |
| 控制器正常、稳态 | 不碰控制器（省资源，但无法自愈） | 每 30s 一次 GET 复核（轻量） |
| 控制器暂时不可达 / 5xx | — | 复核失败不降级、不 PUT，等下次复核 |
| 客户端启动窗口内 `info` 抖动 | 可能重复拉起 zt-node → 抢 TAP、IP 抖动 | 探测控制端口，只等不重复拉起（加固项，见 §4） |
| 补授权失败 | 不适用 | 降级 `authorized=0`，持续重试而非假绿 |

## 8. 验收

- `go test ./server/`（含 5 个 `TestJoinStatus*` 用例）通过；
- `go build ./...` 通过；
- 现场（2026-11-08 修复后实测）：
  - `/api/zt/status` → `status=OK`、`ips=["10.147.1.2/24"]`；
  - `/api/zt/health` → `node_ready=true`、`planet_ok=true`、`issues=null`；
  - `ping 10.147.1.1` → 4/4、0% 丢包；TCP 8890 → mac1 成功；
  - 部署新客户端时复用了运行中的 zt-node（PID 不变）；杀掉旧节点后能干净拉起一个新实例。

## 9. 已知限制

- 复核已能**区分**"成员缺失"（补授权）与"网络缺失"（不盲 PUT、提示重建），但**不能自动重建网络**。
  网络缺失时需要在服务端`handleCreateRoom`重新 `CreateNetwork` 建一个房间；
  现有机制只负责如实报告，不做危险的重建 / 迁移动作。
- 复核由 `handleJoinStatus` 触发，故其节奏取决于客户端轮询（进房后 30s 一次；
  不在房间或界面未运行时不会触发）。用户重新进房 / 重新打开界面即恢复自愈。
- 本次线上背离的**具体成因仍未定论**（见 §3）。复核机制对已知的几种成因都有效，
  但不能反推证明当初是哪一种——若要定位真因，需在下次复现时抓控制器日志与
  `GET network` / `GET member` 的实际返回。

## 10. 相关文档

- [`CONNECTION.md`](CONNECTION.md)：§4.1「授权没有记忆」、§5.1「授权只做一次」——本文修正其"永不复核"的假设；
- [`ARCHITECTURE.md`](ARCHITECTURE.md) §4「授权闭环」；
- [`REQUIREMENTS-CONNECTIVITY.md`](REQUIREMENTS-CONNECTIVITY.md)：连通性可观测性需求（分层探测、就绪判定、日志）。
