# 部署指南

本文说明如何从零搭一套 LANBridge 服务端：ZeroTier 私有控制器 + 自建 planet 根节点 + 房间服务器。

> 需要一台 Linux 服务器（本文以 AlmaLinux 9 / 任何 systemd 发行版为例），
> 一个公网 IP，以及能放行 **UDP 9993**。
> 客户端不需要公网 IP（NAT 后也能用，靠 ZeroTier 打洞）。

---

## 总览

```
┌──────────────────────────────────────────────┐
│ 服务器                                        │
│                                              │
│  zerotier-one（systemd）                      │
│    ├ 9993/tcp  控制器 REST API（本地）         │
│    ├ 9993/udp  planet 根节点（公网，客户端连这里）│
│    └ /var/lib/zerotier-one/                  │
│         ├ planet       ← 自建根节点文件        │
│         ├ controller.d ← 房间网络配置          │
│         └ authtoken.secret                   │
│                                              │
│  lanbridge-server（systemd）                  │
│    └ 8800/tcp  REST API + 分发 planet          │
└──────────────────────────────────────────────┘
```

一个 zerotier-one 进程同时承担两个角色：**planet 根节点**（给客户端打洞和中继）
和**私有网络控制器**（管理房间网络）。这两者是 ZeroTier 官方支持的能力。

---

## 第一步：安装 ZeroTier

```bash
# RHEL / AlmaLinux / Rocky
sudo yum install -y https://download.zerotier.com/redhat/el9/zerotier-one-1.16.2-1.el9.x86_64.rpm

# Debian / Ubuntu
# curl -s https://install.zerotier.com | sudo bash

sudo systemctl enable --now zerotier-one
sudo zerotier-cli info        # 确认在线
```

记下你的 **node ID**（`/var/lib/zerotier-one/identity.public` 的第一段），
下面生成 planet 时要用。

---

## 第二步：启用控制器

编辑 `/var/lib/zerotier-one/local.conf`：

```json
{
  "settings": {
    "controller": true
  }
}
```

```bash
sudo systemctl restart zerotier-one
```

验证（token 路径可能因安装方式不同）：

```bash
TOKEN=$(sudo cat /var/lib/zerotier-one/authtoken.secret)
curl -s -H "X-ZT1-Auth: $TOKEN" http://127.0.0.1:9993/controller/status
# 期望：{"controller":true,...}
```

---

## 第三步：生成自建 planet

planet 文件告诉客户端「根节点是谁」。不做这一步，客户端会连官方根节点，
无法和你的私有网络互通。

**关键前提：服务进程身份必须就是 planet 的根身份。**
也就是说，生成 moon/planet 用的 `identity.public`，必须正是
`/var/lib/zerotier-one/identity.public`——即当前 zerotier-one 进程自己的身份。
否则客户端 HELLO 能到达，服务器却不回包（现象：`listpeers` 里 PLANET 延迟 -1、OFFLINE）。

```bash
cd /var/lib/zerotier-one

# 1) 用本机身份生成 moon 定义
sudo zerotier-idtool initmoon identity.public > moon.json

# 2) 填公网地址，并把类型改成 planet（改成你的公网 IP:9993）
sudo python3 - <<'EOF'
import json
p = '/var/lib/zerotier-one/moon.json'
d = json.load(open(p))
d['roots'][0]['stableEndpoints'] = ['203.0.113.10/9993']
d['worldType'] = 'planet'          # 关键：把 moon 升级为 planet
json.dump(d, open(p, 'w'), indent=2)
EOF

# 3) 生成 planet 文件（约 257 字节）
sudo zerotier-idtool genmoon moon.json
# 产物 000000<根节点ID>.moon

# 4) 安装并重启
sudo cp 000000*.moon planet
sudo chown zerotier-one:zerotier-one planet
sudo systemctl restart zerotier-one
```

验证：`sudo zerotier-cli listpeers` 应该能看到自己这个 PLANET 根，
并且 `zerotier-cli info` 显示 ONLINE。

把最终的 planet 文件复制到房间服务器的工作目录：

```bash
sudo mkdir -p /opt/lanbridge
sudo cp /var/lib/zerotier-one/planet /opt/lanbridge/planet
```

> 这一步不需要额外的 C++ 工具。`initmoon + worldType:"planet" + genmoon`
> 是验证过可用的最小路径；不同 ZeroTier 版本对 `worldType` 字段的处理可能不同，
> 若新版本忽略该字段，请参考对应版本的 `zerotier-idtool` 文档。


---

## 第四步：部署房间服务器

```bash
# 编译（在你自己的开发机上）
GOOS=linux GOARCH=amd64 go build -o lanbridge-server ./server

# 上传
scp lanbridge-server root@你的服务器:/opt/lanbridge/
ssh root@你的服务器
chmod +x /opt/lanbridge/lanbridge-server
```

创建 systemd 单元 `/etc/systemd/system/lanbridge.service`：

```ini
[Unit]
Description=LANBridge room server
After=network-online.target zerotier-one.service
Wants=network-online.target

[Service]
WorkingDirectory=/opt/lanbridge
# controller token 每次启动时从本机读取，无需硬编码
ExecStart=/bin/sh -c "ZT_TOKEN=$(cat /var/lib/zerotier-one/authtoken.secret) exec /opt/lanbridge/lanbridge-server"
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

环境变量（可写进 systemd 的 `Environment=` 或单独的文件）：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADDR` | `0.0.0.0:8800` | 监听地址 |
| `DB` | `lanbridge.db` | SQLite 路径 |
| `ZT_BASE` | `http://127.0.0.1:9993` | 控制器本地 API |
| `ZT_TOKEN` | — | **必填**，控制器 token |
| `PLANET` | `planet` | 下发给客户端的 planet 路径 |

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lanbridge
sudo systemctl status lanbridge
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8800/api/planet   # 期望 200
```

> 若沿用早期部署的数据库文件（旧版本默认命名为 `joel.db`），把 `DB` 指过去即可，
> 表结构会在启动时自动补齐新列。

---

## 第五步：防火墙

```bash
sudo firewall-cmd --permanent --add-port=9993/udp   # planet 根节点，必须
sudo firewall-cmd --permanent --add-port=8800/tcp   # 房间 API
sudo firewall-cmd --reload
```

**9993/udp 必须对公网开放**，否则客户端连不到根节点，只能等中继（会非常慢）。

---

## 第六步：客户端指向你的服务器

分发客户端时，有两种方式让用户连到你的服务端：

**方式一：改内置默认值**（推荐用于自建发行版）
编辑 `client/main.go` 里的 `serverBase` 默认值，重新编译。

**方式二：让用户自己配置**

```bash
# 环境变量
LANBRIDGE_SERVER=http://你的服务器:8800 ./lanbridge-client
```

```json
// 或 ~/.lanbridge/config.json
{ "server": "http://你的服务器:8800" }
```

优先级：环境变量 > `config.json` > 内置默认值。

---

## 验证清单

按顺序做一遍，任何一步失败都能立刻定位：

1. `curl http://服务器:8800/api/planet` 返回 planet 文件内容；
2. 注册一个账号，创建房间，返回 `network_id`（形如 `bd5b8ad96c000001`）；
3. `zerotier-cli -D/var/lib/zerotier-one -j listnetworks` 能看到这个网络；
4. 客户端进房后 `/api/join/status` 返回 `authorized: true`；
5. 两台机器进同一房间，各自有 `10.147.X.Y` 的 IP，能互相 `ping`；
6. 房间聊天能看到对方的文字，送达状态显示 `已送达 1/1`。

---

## 常见问题

**客户端一直 REQUESTING_CONFIGURATION**
→ 客户端连不到根节点。检查 9993/udp 是否开放，planet 文件是否和服务器上的一致。

**join/status 一直 authorized=false**
→ 看服务端日志里的 `authorize room ... node ...`。常见原因是
`ZT_TOKEN` 不对，或控制器没启用（`local.conf` 没写 `controller: true`）。

**房间建得出来但成员互相 ping 不通**
→ 通常是 Windows 防火墙拦了虚拟网段的入站。客户端启动时会尝试加规则
（`LANBridge ZT Subnet In`），但被杀软拦截时需要手动放行。

**网卡没建出来（PORT_ERROR）**
→ TAP 驱动没装好。Windows 上以管理员身份重启客户端会重试安装。

**服务器重启后旧房间还在**
→ 正常。房间记录在 SQLite 里，后台巡检（30 秒一次）会清理空房间
（空置 60 秒）和长时间无心跳的房间（10 分钟）。

---

## 升级与回滚

```bash
# 升级：备份 → 替换 → 重启
sudo cp /opt/lanbridge/lanbridge-server /opt/lanbridge/lanbridge-server.bak
sudo cp /opt/lanbridge/lanbridge.db /opt/lanbridge/lanbridge.db.bak
sudo install -m 755 lanbridge-server /opt/lanbridge/lanbridge-server
sudo systemctl restart lanbridge

# 回滚
sudo cp /opt/lanbridge/lanbridge-server.bak /opt/lanbridge/lanbridge-server
sudo systemctl restart lanbridge
```

数据库可向前兼容（启动时会自动补列），但旧版本读不了新增列——
回滚二进制时如果新版本已经改过表结构，需要一起回滚数据库。
