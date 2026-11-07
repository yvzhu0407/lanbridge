//go:build windows

package main

import (
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// toUTF16LE 模拟 Windows 上 UTF-16LE 编码的字符串转成字节。
func toUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// 合法 UTF-8(PowerShell 强制 UTF-8 输出后就是这种)必须原样保留。
func TestWinTextKeepsUTF8(t *testing.T) {
	for _, s := range []string{
		"Set-NetIPInterface : 找不到任何匹配的对象",
		"plain ascii",
		"混合 English 和 中文",
	} {
		if got := winText([]byte(s)); got != s {
			t.Fatalf("UTF-8 输入应原样返回:\n got=%q\nwant=%q", got, s)
		}
	}
}

// 中文 Windows 上 netsh/pnputil 输出的是 GBK。GBK 字节不是合法 UTF-8,
// 必须走 CP_ACP 转换,否则日志里全是乱码。
func TestWinTextDecodesGBK(t *testing.T) {
	// "驱动器" 的 GBK 编码,故意挑一个不是合法 UTF-8 的序列
	gbk := []byte{0xC7, 0xFD, 0xB6, 0xAF, 0xC6, 0xF7}
	if utf8.Valid(gbk) {
		t.Skip("该字节序列恰好是合法 UTF-8,用例失效")
	}
	got := winText(gbk)
	if got == "" {
		t.Fatal("GBK 输入不应返回空")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("转换结果必须是合法 UTF-8, got %q", got)
	}
}

// 这是修复前的老 bug:转换失败时返回原始字节,Go 按 UTF-8 解释就是乱码。
// 现在即便解不出来也必须返回合法 UTF-8 的占位文本。
func TestWinTextNeverReturnsInvalidUTF8(t *testing.T) {
	for _, b := range [][]byte{
		{0xFF, 0xFE, 0xFD},           // 非法 UTF-8,CP_ACP 也大概率解不出
		{0x81, 0x40},                 // GBK 未定义区
		append(toUTF16LE("x"), 0x00), // 带 NUL 的杂数据
	} {
		got := winText(b)
		if !utf8.ValidString(got) {
			t.Fatalf("输入 % X 转换后不是合法 UTF-8: %q", b, got)
		}
	}
}

func TestWinTextEmpty(t *testing.T) {
	for _, b := range [][]byte{nil, {}, []byte("  \n\t ")} {
		if got := winText(b); got != "" {
			t.Fatalf("空白输入应返回空串, got %q", got)
		}
	}
}

func TestIsASCII(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{`C:\Users\alice\.lanbridge\zt`, true},
		{`C:\Users\张三\.lanbridge\zt`, false},
		{`C:\Users\Ünïcode`, false},
		{"", true},
	} {
		if got := isASCII(tc.s); got != tc.want {
			t.Errorf("isASCII(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// 中文用户名的路径必须能触发诊断提示。
func TestChineseHomeDirWarning(t *testing.T) {
	old := homeDir
	homeDir = `C:\Users\张三\.lanbridge`
	t.Cleanup(func() { homeDir = old })

	if isASCII(ztHome()) {
		t.Fatalf("中文家目录应被识别为非 ASCII: %s", ztHome())
	}
}
