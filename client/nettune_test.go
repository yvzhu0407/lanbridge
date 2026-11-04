package main

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeNetTune struct {
	calls    []string
	prev     int
	lowerErr error
	restErr  error
}

func (f *fakeNetTune) LowerMetric(device string) (int, error) {
	f.calls = append(f.calls, "lower:"+device)
	return f.prev, f.lowerErr
}

func (f *fakeNetTune) RestoreMetric(device string, prev int) error {
	f.calls = append(f.calls, "restore:"+device+":"+itoa(prev))
	return f.restErr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func withFakeTune(t *testing.T, f *fakeNetTune) {
	t.Helper()
	oldHome, oldNew := homeDir, newNetTune
	homeDir = t.TempDir()
	newNetTune = func() netTune { return f }
	t.Cleanup(func() {
		homeDir, newNetTune = oldHome, oldNew
	})
}

func TestTuneNetworkSavesAndRestores(t *testing.T) {
	f := &fakeNetTune{prev: 25}
	withFakeTune(t, f)

	tuneNetwork("ztabc")
	if len(f.calls) != 1 || f.calls[0] != "lower:ztabc" {
		t.Fatalf("calls = %v", f.calls)
	}
	s, ok := loadTuneState()
	if !ok || s.Device != "ztabc" || s.PrevMetric != 25 {
		t.Fatalf("state = %+v, ok=%v", s, ok)
	}

	restoreNetwork()
	if len(f.calls) != 2 || f.calls[1] != "restore:ztabc:25" {
		t.Fatalf("calls = %v", f.calls)
	}
	if _, ok := loadTuneState(); ok {
		t.Fatal("state file should be removed after restore")
	}
}

// 同一块网卡重复进入房间不应重复改动或重复记录。
func TestTuneNetworkIdempotent(t *testing.T) {
	f := &fakeNetTune{prev: 20}
	withFakeTune(t, f)
	tuneNetwork("ztabc")
	tuneNetwork("ztabc")
	if len(f.calls) != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// 换网卡时先恢复旧网卡，再调整新网卡。
func TestTuneNetworkSwitchDevice(t *testing.T) {
	f := &fakeNetTune{prev: 30}
	withFakeTune(t, f)
	tuneNetwork("first")
	tuneNetwork("second")
	want := []string{"lower:first", "restore:first:30", "lower:second"}
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %v", f.calls)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q", i, f.calls[i], want[i])
		}
	}
	if s, _ := loadTuneState(); s.Device != "second" {
		t.Fatalf("state = %+v", s)
	}
}

// 平台返回 -1（如 macOS）表示无需调整，不应留下状态文件。
func TestTuneNetworkNoopPlatform(t *testing.T) {
	f := &fakeNetTune{prev: -1}
	withFakeTune(t, f)
	tuneNetwork("en0")
	if _, ok := loadTuneState(); ok {
		t.Fatal("no-op platform should not persist state")
	}
}

// 恢复失败时保留状态文件，供下次启动重试。
func TestRestoreFailureKeepsState(t *testing.T) {
	f := &fakeNetTune{prev: 12}
	withFakeTune(t, f)
	tuneNetwork("ztabc")
	f.restErr = os.ErrPermission
	restoreNetwork()
	if s, ok := loadTuneState(); !ok || s.Device != "ztabc" {
		t.Fatalf("state should be kept: %+v ok=%v", s, ok)
	}
	f.restErr = nil
	restoreStaleTune()
	if _, ok := loadTuneState(); ok {
		t.Fatal("stale state should be restored on next start")
	}
}

func TestTuneStateFilePermissions(t *testing.T) {
	f := &fakeNetTune{prev: 10}
	withFakeTune(t, f)
	tuneNetwork("ztabc")
	info, err := os.Stat(filepath.Join(homeDir, "nettune-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}
