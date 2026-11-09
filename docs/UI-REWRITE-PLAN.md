# UI 重构实施方案（Vue 3 + TypeScript）

> 状态：待实施。本文是给实施 agent 的完整输入，技术选型与取舍已定稿，**不需要重新讨论方案**。
> 涉及行号均对照 `client/static/index.html` 的 2026-11-09 版本（2465 行）；重构后该文件会被构建产物覆盖。

---

## 0. 结论摘要

| 项 | 决定 |
|---|---|
| 技术栈 | Vue 3.5.45 + TypeScript 5.9.3 + Vite 8.3.6 |
| 产物 | `vite-plugin-singlefile` → `client/static/index.html`（单文件，`go:embed` 路径不变） |
| 生产依赖 | 只有 `vue` 一个 |
| 构建目标 | `['chrome111', 'safari15']`（对应 macOS 11.0 最低版本） |
| Go 侧改动 | 日志双流（`u\|` / `d\|` 前缀 + `/api/logs?view=`）、`issue` 增加用户话术字段 |
| 服务端改动 | **无**。`server/` 与数据库不动，`game` 字段保留，前端固定传 `"civ6"` |

三条产品约束（来自需求方）：

1. 房间页聊天**不能常驻占视野**，默认收起；网络未就绪时不得占位
2. **不要控制台页面**。日志分两套：给用户看的结论、给开发者看的原始日志
3. **不强调游戏类别**。只做文明6，用户只需要能创建房间、能建立局域网环境

---

## 1. 体积评估（已实测）

用 Vite 8.3.6 + Vue 3.5.45 + vite-plugin-singlefile 2.3.4 做过真实构建验证，不是估算：

| 项目 | 未压缩 | gzip |
|---|---|---|
| 当前 `client/static/index.html` | 89.7 KB（CSS 19.8 + JS 43.7 + 结构 28.3） | ~20 KB |
| Vue 运行时底噪（空 app） | 64.3 KB | 25.3 KB |
| 三个视图的验证 app | 78.0 KB | 30.6 KB |

- Vue 框架成本 ≈ 58 KB，业务代码增量只有 14 KB
- 完整重写后预计 **130~160 KB / gzip 45~55 KB**
- 对产物影响：`lanbridge-client-mac` 42 MB → +0.14%；`lanbridge-client.exe` 14 MB → +0.4%；DMG / setup.exe 基本不变
- 模板编译器**不会**被打进产物（runtime-only 构建，已验证产物中无 `compileToFunction`）

工程侧成本（这才是主要代价）：

| 项 | 量级 |
|---|---|
| `node_modules` | 72 MB，**必须**加入 `.gitignore` |
| 依赖数量 | 26 个包，`package-lock.json` 35 KB |
| CI | `build.yml` / `release.yml` 各加 `setup-node` + `npm ci`（30~60s） |

### 1.1 体积控制红线（实施时必须遵守）

1. **不引入 UI 组件库**（Element Plus / Naive UI / Ant Design Vue 一律不用）
2. **不引入 Tailwind CSS**。v4 要求 Safari 16.4 / Chrome 111，与 macOS 11.0 最低版本冲突
3. **不引入 vue-router / pinia / axios**。应用只有两个视图 + 一个抽屉，`ref` + 单个 `fetch` 封装足够
4. **锁死版本**并提交 `package-lock.json`
5. **构建产物提交进仓库**，保证只有 Go 环境的人 `go build` 直接能跑；CI 校验产物与源码一致

---

## 2. 目录结构

```
client/ui/                    # 新增，前端工程
  package.json
  package-lock.json           # 提交
  vite.config.ts
  tsconfig.json
  index.html                  # Vite 入口模板（仅 <div id="app">）
  src/
    main.ts
    App.vue
    api.ts                    # fetch 封装 + 全部接口调用
    types.ts                  # Member / Room / Probe / HealthReport / Issue
    store.ts                  # 单一 reactive 状态：me / nodeId / currentRoom / health
    lib/
      format.ts               # 延迟、时间、状态话术映射
      userCopy.ts             # 所有面向用户的中文文案（集中管理）
    styles/
      tokens.css              # 现有 :root 变量原样迁移
      base.css                # reset + 字体 + 滚动
    components/
      AppHeader.vue
      LobbyView.vue
      CreateRoomDrawer.vue
      RoomView.vue
      RoomSummary.vue         # 结论卡 + 我的虚拟 IP
      ConnectGuide.vue        # 游戏内三步指引
      MemberList.vue
      MemberRow.vue
      ChatPanel.vue
      DiagnoseDrawer.vue
      DiagnoseFriendly.vue    # 「问题诊断」tab
      DiagnoseDev.vue         # 「开发者日志」tab
client/static/index.html      # 构建产物（提交，勿手改）
```

---

## 3. 构建配置

### 3.1 `client/ui/vite.config.ts`

```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { viteSingleFile } from 'vite-plugin-singlefile'

export default defineConfig({
  plugins: [vue(), viteSingleFile()],
  base: './',
  server: {
    proxy: { '/api': 'http://127.0.0.1:8899' },
  },
  build: {
    target: ['chrome111', 'safari15'], // 对齐 macOS 11.0 最低支持
    outDir: '../static',
    emptyOutDir: true,
    cssCodeSplit: false,
    assetsInlineLimit: 100 * 1024 * 1024,
  },
})
```

### 3.2 `client/ui/package.json`

```json
{
  "name": "lanbridge-ui",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc --noEmit && vite build",
    "check": "vue-tsc --noEmit"
  },
  "dependencies": {
    "vue": "3.5.45"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "6.0.10",
    "typescript": "5.9.3",
    "vite": "8.3.6",
    "vite-plugin-singlefile": "2.3.4",
    "vue-tsc": "3.3.14"
  }
}
```

> TypeScript 用 5.9.3 而不是 `latest`：`latest` 当前是 7.0.2（原生端口），`vue-tsc 3.3.14` 只声明 `typescript >=5.0.0`，对 7.x 未验证。

### 3.3 构建顺序约束

`outDir` 指向 `../static` 且 `emptyOutDir: true`，**构建失败前会先清空目录**。因此 `build` 脚本必须先跑 `vue-tsc --noEmit`，类型不过就不碰 `client/static/`。

### 3.4 `.gitignore` 追加

```gitignore
client/ui/node_modules/
```

`client/static/index.html` 仍在版本控制内，不要加进 `.gitignore`（现有 `dist/` 规则不影响它）。

---

## 4. Go 侧改动

### 4.1 日志双流 —— `client/logging.go`

新增两个写入入口，**不改现有文件路径、轮转逻辑与 `logFanout`**：

```go
// 机器可读前缀，供 /api/logs?view= 过滤
// u|2026-11-09T10:20:31|warn|net_not_ready|你这台机器的虚拟网卡还没建出来
// d|2026/11/09 10:20:31 [诊断] TAP 驱动已安装(ok)
func LogUser(level, code, text string) // 写 u| 行，同时进文件与用户面板
func LogDev(format string, a ...any)   // 写 d| 行，仅进文件与开发面板
```

实施步骤：

1. 把 `client/` 下 **57 处** `log.Printf` 机械替换为 `LogDev`。**只加前缀，不改文案**
2. 只在「用户能据此做点什么」的位置补 `LogUser`，按下表逐个核对，**不要漏也不要多**：

| 位置 | level | code | 用户看到的话 |
|---|---|---|---|
| `client/reach.go` 网络就绪 | `info` | `net_ready` | 虚拟网络已连上，可以邀请朋友了 |
| `client/reach.go` 网络故障 | `warn` | `net_not_ready` | 你这台机器的虚拟网卡没建出来 |
| `client/main.go:497` 连通性恢复 | `info` | `peer_ok` | 与 {成员} 的连接已恢复 |
| `client/main.go:503` 连通性故障 | `warn` | `peer_bad` | 连不上 {成员} |
| `client/supervise.go` 重启实例 | `warn` | `self_heal` | 检测到网络卡住，已自动重启修复 |
| `client/main.go:343` planet 不可达 | `error` | `server_down` | 连不上房间服务器 |

### 4.2 `handleLogs` —— `client/main.go:546`

加 `view` 查询参数：

```go
view := r.URL.Query().Get("view") // "" = 全部（向后兼容）| "user" | "dev"
```

- `user` → 只保留 `u|` 开头的行，**去掉前缀后**返回
- `dev` → 只保留 `d|` 开头的行，去掉前缀后返回
- 空 → 全部返回，保持现有 200 行截断行为

**顺序必须是先按前缀过滤、再取最后 200 行**。反过来的话用户面板会被开发日志挤空。

### 4.3 `issue` 增加用户话术字段 —— `client/reach.go:512`

现有结构（保留不动，开发 tab 继续用）：

```go
type issue struct {
    Code  string   `json:"code"`
    Text  string   `json:"text"`
    Steps []string `json:"steps"`
}
```

新增两个字段：

```go
type issue struct {
    Code      string   `json:"code"`
    Text      string   `json:"text"`       // 开发用，含技术名词
    Steps     []string `json:"steps"`      // 开发用排查步骤
    UserText  string   `json:"user_text"`  // 用户用，动词开头
    UserSteps []string `json:"user_steps"` // 用户用，1~3 步
}
```

三个产生点各补两条文案：

- `client/reach.go:599` → `node_down`
- `client/reach.go:610` → `net_not_ready:<nwid>`
- `client/reach.go:615` → `planet_down`

用户话术原则：**动词开头、不出现 ZeroTier / TAP / planet / 端口 / 跃点 / 网卡编号**，最多 3 步，每步一句话。

示例：

| Code | UserText | UserSteps |
|---|---|---|
| `node_down` | 后台服务没有响应 | 1. 打开「诊断」看最近事件<br>2. 重启 LANBridge<br>3. 还不行就重装 |
| `net_not_ready` | 你这台机器的虚拟网卡还没建出来 | 1. 点「诊断」里的「重新检测」<br>2. 以管理员身份重启 LANBridge<br>3. 首次启动时请允许弹窗授权 |
| `planet_down` | 连不上房间服务器 | 1. 检查网络能否访问服务器<br>2. 稍等 1 分钟后重试 |

> 前端必须对 `UserText` 为空的情况降级到 `Text`，不显示空白（防止前后端版本不一致）。

---

## 5. 界面结构

### 5.1 全局

```
┌──────────────────────────────────────────────────────────┐
│ 桥联   [大厅] [房间]                          ⚠ 诊断      │ 44px
├──────────────────────────────────────────────────────────┤
│                                                          │
```

- 删除「控制台」导航项与整个 `<section id="view-console">`
- 「诊断」按钮放 header 右侧，**视觉上不显眼**：默认灰字无边框；`healthReport.issues` 非空时加红色圆点
- 保留：点击 Node ID 复制、单实例逻辑、窗口层（`window_darwin.go` / `window_windows.go`）**完全不动**

### 5.2 大厅 —— `LobbyView.vue`

**删除**：

- `civ6-notice-banner`（`index.html:880-893`）
- 4 张指标卡（`index.html:896-917`）
- 游戏徽标（`index.html:927`）与隐藏的 `#gameFilter`（`index.html:928-935`）
- 房间表「游戏」列（`index.html:958` 起）
- 创建抽屉里的游戏字段（`index.html:945-955`），包括 `#newRoomGame`
- `gameLabel` / `gameShort`（`index.html:1260-1266`）与 `state.gameFilter` 相关逻辑（`index.html:1445-1446`、`1489-1490`）

**新布局**：

```
┌ 搜索房间名 / ID ────────────────  [＋ 创建房间]  [刷新] ┐
│ #12  周末四人对局        4 人  公开            [加入]  │
│ #13  老王的档           2 人  🔒              [加入]  │
└────────────────────────────────────────────────────────┘
```

创建表单只剩两项：`房间名` + `加入密码（可选）`。`game` 字段在 `api.ts` 里固定传 `'civ6'`，并加注释：

```ts
// 协议层保留 game 字段（服务端 rooms.game NOT NULL），界面不再暴露游戏选择。
```

### 5.3 房间 —— `RoomView.vue`（核心）

目标：**980×760 单屏不滚动**。外壳 `height:100%; min-height:0; overflow:hidden`，只有成员列表和消息列表内部滚动。

```
┌ 结论卡 ────────────────────────────────────────────────┐
│  ✅ 可以联机 · 3/4 人可达                                │
│  你的联机地址   10.147.12.5        [复制]               │
└────────────────────────────────────────────────────────┘
┌ 主区 minmax(0,1fr) ──────────────────┐ ┌ 聊天 300px ──┐
│ [看如何联机 ▸]  ← 折叠，默认收起       │ │ 💬 聊天  (3) │
│                                        │ │  收起态 44px │
│ 玩家        联机地址      延迟          │ │ ─────────────│
│ 我(房主)    10.147.12.5   —            │ │ 展开后整列   │
│ 老王        10.147.12.6   12 ms        │ │ 占满高度     │
│ 小李        10.147.12.7   正在连接…    │ │              │
│ 阿强        10.147.12.8   连不上 ⓘ     │ │ [输入框]     │
└────────────────────────────────────────┘ └──────────────┘
```

**关键约束**（对应需求 2a / 2b）：

| 要求 | 实现 |
|---|---|
| 聊天不常驻占视野 | 默认**收起成 44px 单行**，显示「聊天 · N 条未读」；点击展开，展开后 `flex:1; min-height:0` 占满右栏，**删除现有 `min-height:300px`** |
| 网络未就绪时不占位 | 收起态与网络状态解耦；未就绪时输入框禁用，提示「等虚拟网络就绪后可以聊天」 |
| 成员表是第一屏主体 | 左栏第一屏可见，不再被聊天压到下面 |
| 去重状态列 | 合并现有 `STATE` + `PING` 两列为单一徽标；删除 `NODE ID` 列，移到行内 `⋯` 菜单的「复制节点 ID」 |

**状态徽标映射**（`lib/format.ts`），输入是 `/api/ping` 返回的 `probeResult`：

| `state_text` | 用户看到 | 色调 |
|---|---|---|
| `已就绪` | `12 ms` | 绿 |
| `链路协商中` | `正在连接…` | 橙 |
| `链路未建立` | `连不上` | 灰 |
| `隧道通·数据不通` | `对方网络异常` ⓘ | 灰（tooltip：让对方以管理员重启 LANBridge） |
| `本机网络未就绪` | `你这台没连上` | 红 |

**`RoomSummary` 结论优先级**：

1. 本机网络未就绪 → 红：`你这台机器没连上`
2. 全部成员可达 → 绿：`可以联机`
3. 部分可达 → 黄：`N/M 人可达`
4. 尚无探测结果 → 灰：`正在检测…`

**`ConnectGuide`**：把现在内联在 `updateRoomView()` 里的整段教程（`index.html:1780-1782`）收进折叠区，三步一句话：

1. 房主在《文明6》里选「多人游戏 → 局域网 → 创建游戏」
2. 朋友在「局域网」列表里直接点你的房间
3. 看不到就点上面的「复制」把地址发给对方手动加

### 5.4 诊断抽屉 —— `DiagnoseDrawer.vue`

右侧抽屉，宽 `min(520px, 92vw)`，窄窗口下全屏。两个 tab：

**问题诊断**（默认）
- 数据源：`/api/zt/health` → 读 `issue.UserText` / `issue.UserSteps`
- 全中文、无技术名词、动词开头
- 下面是「最近事件」，数据源 `/api/logs?view=user`，取最后 40 行

**开发者日志**
- 数据源 `/api/logs?view=dev`
- 保留现有「刷新 / 复制全部 / 日志文件路径」能力（对应 `index.html:2083-2118`）
- `<pre>` 等宽显示，等宽字体沿用 `var(--mono)`

房间页 `issues` 非空时，header 上的诊断按钮加红点。

---

## 6. 删除清单（对照现有行号）

| 位置 | 删除内容 |
|---|---|
| `index.html:845` | `#pillConsole` |
| `index.html:1107-1157` | `view-console` 整段 |
| `index.html:880-893` | `civ6-notice-banner` |
| `index.html:896-917` | 4 张指标卡 |
| `index.html:927-935` | 游戏徽标、`#gameFilter` |
| `index.html:945-955` | 创建抽屉的游戏字段 |
| `index.html:994` | hero 的「文明6」tag |
| `index.html:1039-1063` | 旧聊天面板（由 `ChatPanel.vue` 重写） |
| `index.html:1068-1103` | 旧成员表（由 `MemberList.vue` 重写） |
| `index.html:1260-1266` | `gameLabel` / `gameShort` |
| `index.html:1445-1446`、`1489-1490` | `gameFilter` 事件与过滤 |
| `index.html:1544` | `const game = 'civ6'`（移到 `api.ts`） |
| `index.html:1767-1769` | `gameKey` / `roomGameTag` / `roomAvatar` |
| `index.html:1780-1782` | 内联游戏教程（移到 `ConnectGuide.vue`） |
| `index.html:2083-2118` | `loadLogs` / `btnRefreshLogs` / `btnRefreshHealth`（搬进 `DiagnoseDev.vue`） |

**不要动**：

- `client/chat.go`（8890 局域网聊天服务）
- 聊天协议逻辑 `chatPeers` / `chatPollRemotes` / `chatSendText`（只重写渲染层）
- `server/` 全部
- `client/supervise.go`、`client/nettune*.go`、`client/reach.go` 的探测逻辑（只加 `LogUser` 调用和 `issue` 字段）
- `client/window_darwin.go`、`client/window_windows.go`

---

## 7. 样式迁移

现有 `:root` 变量（`index.html:17-73`，含 `prefers-color-scheme: dark` 分支）**原样搬进 `tokens.css`**，一个值都不要改——色板已经调过，重写时改颜色属于无谓风险。`--radius: 5px` 也保持。

新增的只有三类，全部用 CSS 变量，不引入预处理器：

- 两栏栅格布局（`grid-template-columns: minmax(0,1fr) 300px`）
- 右侧抽屉（`position: fixed; inset: 0` + flex 右对齐）
- 折叠面板（`v-show` + `max-height` 过渡）

**浏览器特性禁用清单**（因为要兼容 Safari 15）：

- `:has()`
- `@container`
- `subgrid`
- `color-mix()`

---

## 8. 开发与验证流程

```bash
# 终端 1：Go 客户端（提供真实 /api 与 ZeroTier 状态）
go run ./client

# 终端 2：Vite dev server（热更新）
cd client/ui && npm run dev     # http://localhost:5173，/api 代理到 8899
```

`client/chat.go:109` 已设 `Access-Control-Allow-Origin: *`，dev server 的 5173 origin 直连各成员 `:8890` 能通，不需要额外配置。

发布：

```bash
cd client/ui && npm ci && npm run build   # 产出 ../static/index.html
cd ../.. && go build ./client             # go:embed 自动带上
```

CI 改动（`build.yml` 与 `release.yml` 都要加）：

```yaml
- uses: actions/setup-node@v4
  with: { node-version: '22' }
- run: cd client/ui && npm ci && npm run build
- run: git diff --exit-code client/static/index.html   # 产物与源码一致性
```

---

## 9. 验收标准

1. `npm run build` 类型零错误，产物是**单个** `client/static/index.html`，无外链 CSS / JS / 字体
2. 产物未压缩大小 ≤ 200 KB
3. `go build ./client && go vet ./client` 通过，`go test ./client` 不受影响
4. 980×760 窗口下，大厅与房间页**均不出现整页滚动条**
5. 房间页首屏可见：结论卡 + 你的联机地址 + 成员列表；聊天默认收起
6. 断网状态下进入房间：结论卡变红、诊断按钮出现红点、聊天不占位
7. 诊断抽屉两个 tab 内容不串（`/api/logs?view=user` 不应包含 `[诊断] TAP 驱动已安装` 这类行）
8. 全站搜索 `文明6` / `CIV` / `game`，仅剩 `api.ts` 里固定传值那一处与 `ConnectGuide` 的游戏名文案
9. 深色模式（`prefers-color-scheme: dark`）下所有新组件可读
10. `client/static/index.html` 已提交，`git status` 干净

---

## 10. 已知风险与取舍

| 风险 | 应对 |
|---|---|
| `emptyOutDir` 清空 `static/` | `build` 脚本先 `vue-tsc --noEmit`；产物提交进 git，随时可回滚 |
| Big Sur 上未更新的 Safari（可能低到 15） | 构建目标锁 `safari15`，禁用第 7 节的 CSS 特性 |
| 新增 Node 工具链，Go 开发者门槛提高 | 产物提交进仓库，只改 Go 的人无需 Node；CI 强制校验一致性 |
| `issue` 新增字段后前后端不同步 | 前端 `UserText` 为空时降级到 `Text` |
| 重写渲染层时损坏聊天 P2P 逻辑 | `chat.go` 与协议函数不动，只替换 `chatRender*` 的渲染实现 |

---

## 11. 建议实施顺序

1. 脚手架：`client/ui/` + `vite.config.ts` + 空 `App.vue`，确认能构建出单文件到 `client/static/index.html`
2. Go 侧日志双流 + `/api/logs?view=` + `issue` 新字段（可独立验证，不依赖 UI）
3. `tokens.css` + `App.vue` 外壳 + 视图切换
4. `LobbyView` → 验证建房/加入流程
5. `RoomView` + `RoomSummary` + `MemberList`（含新的状态徽标映射）
6. `ChatPanel`（折叠态 + 展开态）
7. `DiagnoseDrawer` 两个 tab
8. 去游戏化清理 + 死代码删除
9. CI 与文档同步（`docs/USAGE.md` 第 2 步「选择游戏」、`README.md` 与 `docs/EXTENDING.md` 中「只开放文明6房间」的表述）
10. 按第 9 节逐条验收
