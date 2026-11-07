# 在 Windows 上开发

这份写的是**在 Windows 机器上改代码、验证、出包**怎么走。发布流程本身见
[WINDOWS-SMOKE.md](WINDOWS-SMOKE.md)。

---

## 环境

```powershell
go version          # 需要 1.21+
git --version
```

装 NSIS（打安装包用）：

```powershell
winget install NSIS.NSIS
```

装好后 `makensis` 应该在 `PATH` 里。没在的话手动加
`C:\Program Files (x86)\NSIS`。

> 只改代码不打包的话，**不需要装 NSIS**。

---

## 拉代码

```powershell
git clone <仓库地址> lanbridge
cd lanbridge
```

确认内嵌的 ZeroTier 文件在（这几个是提交进仓库的二进制，不要删）：

```
client\zt-embed\zt-node.exe
client\zt-embed\zttap300.inf
client\zt-embed\zttap300.sys
client\zt-embed\zttap300.cat
```

---

## 日常开发

### 编译并运行

```powershell
go run .\client
```

> **必须用管理员身份的终端**（PowerShell 或 Windows Terminal 右键 →「以管理员身份运行」）。
> 装驱动、改网卡跃点、加防火墙规则都要提权。不提权的话虚拟网卡建不出来。

启动后浏览器/窗口会打开 `http://127.0.0.1:8899`。

### 看日志

```powershell
Get-Content "$env:USERPROFILE\.lanbridge\client.log" -Tail 50 -Wait
```

`-Wait` 会像 `tail -f` 一样持续输出。

ZeroTier 自己的日志：

```powershell
Get-Content "$env:USERPROFILE\.lanbridge\zt-node.log" -Tail 50 -Wait
```

### 直接问 ZeroTier

```powershell
$ZT = "$env:ProgramData\lanbridge\zt-node.exe"
$H  = "$env:USERPROFILE\.lanbridge\zt"

# 注意:-D 和路径之间不能有空格,-q 不能省
& $ZT -q "-D$H" -p9995 info
& $ZT -q "-D$H" -p9995 -j listnetworks
& $ZT -q "-D$H" -p9995 -j peers
```

### 跑测试

```powershell
go test .\...
go test .\client -run TestWinText -v    # 编码相关的,只能在 Windows 上跑
go vet .\...
```

`client\encoding_windows_test.go` 带 `//go:build windows`，在 macOS/Linux 上会被跳过。
**这部分逻辑只有在 Windows 上才能真正验证。**

---

## 改代码时的几个坑

### 1. 平台相关代码分文件

用文件名后缀区分，不要用 `runtime.GOOS` 的 if 分支：

```
client\zt_windows.go          ← 只在 Windows 编译
client\zt_darwin.go           ← 只在 macOS 编译
client\nettune_windows.go     ← 只在 Windows 编译
client\hidewindow_windows.go  ← 只在 Windows 编译
```

改 Windows 相关逻辑时，只在 `*_windows.go` 里动手。如果两边都要用，
放在不带后缀的公共文件里。

### 2. 调外部命令必须解码

Windows 的命令行工具输出编码不统一：

| 命令 | 输出编码 |
|---|---|
| `netsh` / `pnputil` / `taskkill` | 系统 ANSI 码页（中文 Windows 是 GBK） |
| PowerShell（加了 `psUTF8` 前缀后） | UTF-8 |

**统一用 `winText(out)` 解码**，不要直接 `string(out)`：

```go
out, err := hideCmd("netsh", args...).CombinedOutput()
log.Printf("失败: %s", winText(out))   // ✅
log.Printf("失败: %s", out)            // ❌ 中文 Windows 上是乱码
```

PowerShell 脚本用 `psRun()`，它会自动带上 `psUTF8` 前缀：

```go
out, err := psRun(`Get-NetAdapter | Select-Object -First 1`)  // ✅
out, err := hideCmd("powershell", "-Command", script).CombinedOutput()  // ❌ 中文会乱码
```

### 3. 日志绝不能因为写失败而丢内容

`client\logging.go` 里的 `logFanout` 专门处理这个：Windows GUI 子系统下
stderr 是无效句柄，**任何一个 writer 出错都不能让整条日志丢掉**。

改日志相关代码时别退回 `io.MultiWriter`。

### 4. 网卡不能靠名字找

ZeroTier 的 `portDeviceName` 在 Windows 上返回 `ethernet_32776` 这种内部别名，
既不是友好名也不是 GUID。找网卡请用：

```powershell
Get-NetAdapter | Where-Object {
    $_.InterfaceDescription -like '*ZeroTier*' -or
    $_.InterfaceDescription -like '*zttap300*'
}
```

---

## 打包

```powershell
$env:VERSION = "1.0.5"
.\build-windows-setup.sh
```

> **注意**：`build-windows-setup.sh` 是 bash 脚本，在 Windows 上需要
> Git Bash 或 WSL 才能直接跑。**在 macOS 上打 Windows 包是交叉编译，不用装 Windows 环境。**

纯 Go 编译不需要 bash：

```powershell
go build -ldflags='-s -w -H windowsgui' -o dist\lanbridge-client.exe .\client
```

产物：

```
dist\LANBridge-<版本>-windows-amd64-setup.exe
```

### 打包后自检

```powershell
# 安装包里解出来的 exe 必须和刚编的一模一样
7z x -odist\_check dist\LANBridge-1.0.5-windows-amd64-setup.exe
Get-FileHash dist\lanbridge-client.exe, dist\_check\lanbridge-client.exe
Remove-Item -Recurse dist\_check
```

两个哈希必须相同。不同说明安装包用的是旧文件。

---

## 提交前

```powershell
gofmt -l .\client .\server .\cmd     # 应该没有任何输出
go vet .\...
go test .\...
```

然后照 [WINDOWS-SMOKE.md](WINDOWS-SMOKE.md) 在真机上走一遍。
