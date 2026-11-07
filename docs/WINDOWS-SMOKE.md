# Windows 冒烟测试清单

发 Windows 版之前，在**真机**上照这份清单走一遍。macOS 上跑通不代表 Windows 上没问题——
下面每一条都对应一个真实发生过的 bug，都是只在 Windows 才现形的。

> 约定：`%HOME%` = `C:\Users\<你的用户名>`，`%ZT%` = `%ProgramData%\lanbridge\zt-node.exe`

---

## 0. 准备

- [ ] 先**完全卸载**旧版（控制面板 → 卸载 LANBridge），再装新的
- [ ] 卸载后确认 `%HOME%\.lanbridge\` 还在（配置保留是设计如此，不会被删）
- [ ] 安装时留意组件页，确认「开机自启」可以**勾选/取消**

---

## 1. 安装后首次启动

- [ ] 双击桌面图标，**弹 UAC**（装驱动需要管理员）
- [ ] 没有黑色控制台窗口一闪而过

**失败表现**：没弹 UAC → 驱动装不上；有黑框 → `-H windowsgui` 没生效。

---

## 2. 日志能写出来（曾经一直是空的）

- [ ] 看 `%HOME%\.lanbridge\client.log`

- [ ] **文件非空**，且第一行有时间戳

**为什么查这条**：Windows GUI 子系统下 stderr 是无效句柄，早期版本用 `io.MultiWriter`
写日志，一个 writer 失败就整条丢失，导致文件永远是 0 字节。

**失败表现**：文件 0 字节，或只有创建没有内容 → 看重启后有没有变化，
再不行看 `%ProgramData%\lanbridge\` 下有没有退到这里的日志。

---

## 3. 中文不乱码（中文 Windows 必查）

打开 `client.log`，找这几处：

- [ ] `[诊断] TAP 驱动已安装(原始名称:      zttap300.inf)`
- [ ] `[诊断] 防火墙配置档状态:` 下面的内容，应该是：
      ```
      域配置文件 设置:
      状态                                  关闭
      ```
- [ ] 如果驱动安装失败，错误信息里的中文应该是正常的，不是 `锟斤拷` 或 `????`

**为什么查这条**：`netsh` / `pnputil` / `taskkill` 在中文 Windows 上输出 GBK，
Go 按 UTF-8 解析就是乱码。已用 `winText()` 统一解码，但**只在开发机上验证过编译**，
没在中文系统实跑过。

**失败表现**：出现乱码 → 说明 `winText()` 的 UTF-8 判定或 CP_ACP 转换有问题，
把那段原始字节记下来。

---

## 4. 虚拟网卡建出来了（曾经完全没有）

- [ ] 加入房间后，等 1~2 分钟
- [ ] `ipconfig` 能看到一块网卡带 `10.147.x.x` 地址
- [ ] `Get-NetAdapter | Where-Object { $_.InterfaceDescription -like '*ZeroTier*' }`
      能列出这块网卡，`Status` 是 `Up`
- [ ] 日志里 `[诊断] 网络 xxx 状态=OK 网卡=xxx IP=[10.147.x.x/24]`
      ——**注意「网卡=」后面不能是空的**

**为什么查这条**：TAP 驱动的 `zttap300.inf/.sys/.cat` 必须放在 **ZeroTier 家目录**
（`%HOME%\.lanbridge\zt\`），不是 `%ProgramData%\lanbridge\driver\`。放错时 ZeroTier
照样返回 `200 join OK`，但网卡根本建不出来，`listnetworks` 里状态还是 `OK`——
这是最隐蔽的一种失败。

**失败表现**：
- `网卡=` 为空 → 驱动没到位
- 有 `%HOME%\.lanbridge\zt\port_error_log.txt` → 打开看具体错误

---

## 5. 跃点调优真的生效了（曾经 100% 失败）

- [ ] 日志里出现 `[网络调优] 已把网卡 xxx 的跃点从 N 降到 1`
- [ ] **不能**出现 `[网络调优] 降低网卡跃点失败(ethernet_XXXXX): 未找到网卡`

**为什么查这条**：ZeroTier 的 `portDeviceName` 在 Windows 上返回 `ethernet_32776`
这种**内部别名**，既不是网卡友好名（「以太网 2」）也不是 GUID。老代码按这两个去匹配，
永远匹配不上，所以这个功能从来没生效过。

**验证方式**：跑一下，把结果贴给我看
```powershell
Get-NetAdapter | Where-Object { $_.InterfaceDescription -like '*ZeroTier*' } |
  Select-Object Name, InterfaceIndex, InterfaceGuid, InterfaceDescription
```
我想确认 `ethernet_32776` 里的数字是不是 `InterfaceIndex`——如果是，匹配能更精确。

**失败表现**：还是「未找到网卡」→ 说明 `InterfaceDescription` 里不含 `ZeroTier`
或 `zttap300`，把上面那条命令的输出发我。

---

## 6. ping 显示的是真延迟（曾经是假的）

- [ ] 房间里有两个以上成员时，看成员表的「往返延迟」列
- [ ] 鼠标悬停在「已就绪」上，提示应包含 `真实探测往返 Nms · 经 ICMP`
- [ ] 故意找一台连不上的机器，它应该显示 **`隧道通·数据不通`** 或 **`不可达`**，
      **绝不能是绿色的数字**

**为什么查这条**：老代码拿 `zerotier-cli peers` 里的 `latency` 当 ping 结果返回。
那个值是**两台 ZeroTier 进程之间**的隧道延迟，不代表能通数据。曾经出现过
「界面显示绿色 17ms、实际一个包都收不到」。

**失败表现**：连不上的机器还显示绿色数字 → `probePeer` 的分层判定被绕过了。

---

## 7. 双向可达

- [ ] A 机：`ping 10.147.<房间号>.2`（对方 IP）
- [ ] B 机：反向 ping A
- [ ] **两个方向都要通**

**为什么查这条**：单向通是防火墙的典型症状。Windows 会把新建的虚拟网卡判为
「公用网络」，默认拦截入站。程序会加防火墙规则，但要管理员权限。

**失败表现**：A → B 通但 B → A 不通 → 看 B 机的防火墙规则
`netsh advfirewall firewall show rule name="LANBridge ZT Subnet In"`

---

## 8. 广播（局域网游戏发现房间的关键）

- [ ] 在有 `cmd\bcastprobe` 的机器上：
      ```powershell
      # A 机先监听
      .\bcastprobe.exe -listen -port 39000
      # B 机发送
      .\bcastprobe.exe -send -addr 10.147.<房间号>.255:39000 -interval 1s -msg HELLO
      ```
- [ ] A 机能收到 `HELLO`

**为什么查这条**：这条**从来没验证过**。局域网游戏靠广播发现房间，广播不通的话
游戏里就看不到房主。

---

## 9. 关窗/重开

- [ ] 关掉窗口，进程真的退出了（任务管理器里没有残留）
- [ ] 重新打开，能自动回到上次的房间
- [ ] 日志里看到一次新的 `=== lanbridge 客户端启动 (pid XXXXX) ===`

---

## 出问题怎么反馈

把 `%HOME%\.lanbridge\client.log` 整个发我，以及：

```powershell
# ZeroTier 自己的状态
& "%ProgramData%\lanbridge\zt-node.exe" -q "-D%HOME%\.lanbridge\zt" -p9995 -j listnetworks
& "%ProgramData%\lanbridge\zt-node.exe" -q "-D%HOME%\.lanbridge\zt" -p9995 -j peers

# 驱动到底装没装
pnputil /enum-drivers | Select-String zttap300

# 网卡到底有没有
Get-NetAdapter | Where-Object { $_.InterfaceDescription -like '*ZeroTier*' }
```

> `-D` 和路径之间**不能有空格**，必须写成 `-D"C:\..."` 或 `-D%HOME%\...`。
> `-q` 不能省，否则只会打印帮助信息。
