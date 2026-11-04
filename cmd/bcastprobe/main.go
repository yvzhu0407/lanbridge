// bcastprobe:局域网 UDP 广播收发探针,用来验证房间虚拟网卡是否真的能收到广播。
//
// 背景:文明6 靠 UDP 广播发现主机。ZeroTier 网络开了 enableBroadcast 之后,
// 发往 10.147.X.255 的包应当被转发给该网络的所有成员。这个工具不依赖 ZeroTier
// 代码,只在系统层面收发 UDP,所以能把「网络没通」和「游戏没发广播」区分开。
//
// 用法(两端各一条命令):
//
//	# A 机(接收方)
//	bcastprobe -listen -port 39000
//
//	# B 机(发送方),10.147.45.255 是本房间网段的广播地址
//	bcastprobe -send -addr 10.147.45.255:39000 -msg "hello from B"
//
// 接收方打印来源 IP 和内容,即说明广播链路可用。
// 在**同一台机器**上自测也可以:发送方用 255.255.255.255 或本机网段广播地址,
// 接收方绑 0.0.0.0 即可收到(仅验证本机收发,不验证跨机转发)。
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	listen := flag.Bool("listen", false, "监听并打印收到的 UDP 广播")
	send := flag.Bool("send", false, "发送一条 UDP 广播")
	addr := flag.String("addr", "10.147.0.255:39000", "发送目标 host:port(用本房间网段的 .255 广播地址)")
	port := flag.Int("port", 39000, "监听端口")
	msg := flag.String("msg", "lanbridge-broadcast-probe", "发送内容")
	interval := flag.Duration("interval", 1*time.Second, "发送间隔, 0 表示只发一次")
	flag.Parse()

	switch {
	case *listen:
		runListen(*port)
	case *send:
		runSend(*addr, *msg, *interval)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func runListen(port int) {
	// 绑 0.0.0.0 而不是具体网卡地址:广播包的目标地址是 .255,
	// 绑定单个地址会收不到。SO_REUSEADDR 由 Go 默认处理。
	pc, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听失败: %v\n", err)
		os.Exit(1)
	}
	defer pc.Close()
	fmt.Printf("监听 UDP 0.0.0.0:%d,等待广播…(Ctrl+C 退出)\n", port)
	buf := make([]byte, 2048)
	for {
		n, src, err := pc.ReadFrom(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[%s] 来自 %s: %s\n", time.Now().Format("15:04:05"), src, string(buf[:n]))
	}
}

func runSend(addr, msg string, interval time.Duration) {
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "目标地址不合法: %v\n", err)
		os.Exit(1)
	}
	// 本地地址留空,由系统按路由选出口网卡
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 socket 失败: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	local := "未知"
	if la := conn.LocalAddr(); la != nil {
		local = la.String()
	}
	fmt.Printf("从 %s 向 %s 发送广播\n", local, raddr)

	one := func() {
		if _, err := conn.Write([]byte(msg)); err != nil {
			fmt.Fprintf(os.Stderr, "发送失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[%s] 已发送到 %s\n", time.Now().Format("15:04:05"), raddr)
	}
	one()
	if interval <= 0 {
		return
	}
	for {
		time.Sleep(interval)
		one()
	}
}
