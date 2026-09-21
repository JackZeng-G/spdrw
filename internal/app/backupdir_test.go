package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 备份目录 = 可执行文件(测试二进制)同级的 backup\
func TestBackupDirIsNextToExecutable(t *testing.T) {
	dir, err := backupDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "backup" {
		t.Fatalf("目录名 = %q, want backup", filepath.Base(dir))
	}
	exe, _ := os.Executable()
	if want := filepath.Join(filepath.Dir(exe), "backup"); dir != want {
		t.Fatalf("backupDir = %q, want %q", dir, want)
	}
	// 真实走一遍备份流程: 目录应被自动创建, 备份文件落在里面
	a, _ := newWriteTestApp(t)
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		t.Fatal("无活动设备")
	}
	if _, _, err := a.backupIfNeeded(dev, false); err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".bin" {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("backup 目录里没有 .bin 备份: %s", dir)
	}
}
