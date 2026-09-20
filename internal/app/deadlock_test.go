package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// stubDialogs 注入"用户取消"的对话框, 让对话框类入口能跑到加锁路径而不是直接返回。
func stubDialogs(a *App) {
	a.SaveDialog = func(title, defaultName string) (string, error) { return "", nil }
	a.OpenDialog = func(title string) (string, error) { return "", nil }
}

// TestNoSelfDeadlockInBoundMethods 机械守卫: 每个"持操作锁"的对外入口都必须在
// 限定时间内返回。它们内部若再调用同样加锁的方法, 就会自锁死 —— 这个错误我犯过一次
// (WriteConfirmed 持锁后又调 PreflightWrite), 当时只有 go test -race 跑到超时才暴露。
func TestNoSelfDeadlockInBoundMethods(t *testing.T) {
	cases := []struct {
		name string
		call func(a *App) error
	}{
		{"ListControllers", func(a *App) error { _, err := a.ListControllers(); return err }},
		{"Connect", func(a *App) error { return a.Connect(0) }},
		{"Scan", func(a *App) error { _, err := a.Scan(); return err }},
		{"Select", func(a *App) error { return a.Select(0x50) }},
		{"Dump", func(a *App) error { _, err := a.Dump(); return err }},
		{"WPStatus", func(a *App) error { _, err := a.WPStatus(); return err }},
		{"WPSet", func(a *App) error { return a.WPSet([]int{2}, "RSWP") }},
		{"WPClear", func(a *App) error { return a.WPClear("CLEAR") }},
		{"BusStats", func(a *App) error { _, err := a.BusStats(); return err }},
		{"ResetBusStats", func(a *App) error { return a.ResetBusStats() }},
		{"SetDryRun", func(a *App) error { _, err := a.SetDryRun(true); return err }},
		{"PreflightWrite", func(a *App) error {
			_, err := a.PreflightWrite("/nonexistent-file", false)
			return err
		}},
		{"WriteConfirmed", func(a *App) error {
			_, err := a.WriteConfirmed("/nonexistent-file", false, false, "WRITE")
			return err
		}},
		{"EditLoadFromDevice", func(a *App) error { _, err := a.EditLoadFromDevice(); return err }},
		{"EditState", func(a *App) error { _, err := a.EditState(); return err }},
		{"EditFields", func(a *App) error { _, err := a.EditFields(); return err }},
		{"EditSetField", func(a *App) error { _, err := a.EditSetField("partNumber", "DL"); return err }},
		{"EditSetByte", func(a *App) error { _, err := a.EditSetByte(500, 0x5A); return err }},
		{"EditFixCRC", func(a *App) error { _, err := a.EditFixCRC(); return err }},
		{"EditDiff", func(a *App) error { _, err := a.EditDiff(); return err }},
		{"EditBytes", func(a *App) error { _, err := a.EditBytes(); return err }},
		{"EditVerifyFile", func(a *App) error { _, err := a.EditVerifyFile(); return err }},
		{"EditReset", func(a *App) error { _, err := a.EditReset(); return err }},
		{"CRCStatus", func(a *App) error { _, err := a.CRCStatus([]int{0, 1, 2}); return err }},
		{"BusTuning", func(a *App) error { _, err := a.BusTuning(); return err }},
		{"WriteProbe", func(a *App) error { _, err := a.WriteProbe(); return err }},
		{"SetSleepMode", func(a *App) error { _, err := a.SetSleepMode(0); return err }},
		{"SetFastRead", func(a *App) error { _, err := a.SetFastRead(true); return err }},
		{"ReadStats", func(a *App) error { _, err := a.ReadStats(); return err }},
		{"Decode", func(a *App) error { _, err := a.Decode(nil); return err }},
		{"SaveDump", func(a *App) error { return a.SaveDump(filepath.Join(t.TempDir(), "x.bin")) }},
		{"VerifyFile", func(a *App) error { return a.VerifyFile(filepath.Join(t.TempDir(), "x.bin")) }},
		{"DecodeFile", func(a *App) error { _, err := a.DecodeFile(filepath.Join(t.TempDir(), "x.bin")); return err }},
		{"EditLoadPath", func(a *App) error { _, err := a.EditLoadPath(filepath.Join(t.TempDir(), "x.bin")); return err }},
		{"EditApplyToDevice", func(a *App) error { _, err := a.EditApplyToDevice(false, true, "DRYRUN"); return err }},
		{"MfgSearch", func(a *App) error { _, err := a.MfgSearch("Micron", 5); return err }},
		{"ReadFileBytes", func(a *App) error { _, err := a.ReadFileBytes(filepath.Join(t.TempDir(), "x.bin")); return err }},
		{"Logs", func(a *App) error { _ = a.Logs(); return nil }},
		{"CloseDevice", func(a *App) error { a.CloseDevice(); return nil }},
		{"AutoConnectAll", func(a *App) error { _, err := a.AutoConnectAll(); return err }},
		{"Close", func(a *App) error { a.Close(); return nil }},
		// 对话框类入口: 注入"用户取消"的桩, 保证走完加锁路径
		{"SaveDumpDialog", func(a *App) error { stubDialogs(a); return a.SaveDumpDialog() }},
		{"VerifyFileDialog", func(a *App) error { stubDialogs(a); _, err := a.VerifyFileDialog(); return err }},
		{"DecodeFileDialog", func(a *App) error { stubDialogs(a); _, err := a.DecodeFileDialog(); return err }},
		{"EditLoadFileDialog", func(a *App) error { stubDialogs(a); _, err := a.EditLoadFileDialog(); return err }},
		{"EditExportDialog", func(a *App) error { stubDialogs(a); _, err := a.EditExportDialog(); return err }},
		{"PickWriteFile", func(a *App) error { stubDialogs(a); _, err := a.PickWriteFile(); return err }},
		{"SaveDumpData", func(a *App) error { return a.SaveDumpData(filepath.Join(t.TempDir(), "x.bin"), []byte{1, 2, 3}) }},
		{"SetContext", func(a *App) error { a.SetContext(context.Background()); return nil }},
		{"LogVersion", func(a *App) error { a.LogVersion(); return nil }},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			a, _ := newWriteTestApp(t)
			done := make(chan error, 1)
			go func() { done <- c.call(a) }()
			select {
			case <-done:
				// 返回错误没关系(例如文件不存在), 关键是不能卡住
			case <-time.After(5 * time.Second):
				t.Fatalf("%s 5 秒未返回 —— 很可能内部重复加了操作锁(自锁死)", c.name)
			}
		})
	}
}
