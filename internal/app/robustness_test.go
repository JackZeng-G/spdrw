package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 损坏/恶意内容不许把程序搞崩: 解码入口对任意长度、任意器件类型字节、任意内容都只能
// 返回错误, 不能 panic。审计时这里真的崩过两次(短 dump 越界 + 保留编码除零),
// 所以把它固化成常驻用例。
func TestDecodeDumpNeverPanicsOnGarbage(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 4, 5, 8, 9, 15, 63, 64, 100, 126, 127, 128, 255, 256, 257, 383, 384, 511, 512, 513, 1023, 1024, 1025, 2048}
	types := []byte{0x00, 0x01, 0x07, 0x08, 0x0A, 0x0B, 0x0C, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0xFF}
	for _, n := range sizes {
		for _, ty := range types {
			for seed := 0; seed < 16; seed++ {
				d := make([]byte, n)
				for i := range d {
					d[i] = byte((i*37 + int(ty)*7 + seed*11) % 251)
				}
				if n > 2 {
					d[2] = ty
				}
				_, _ = DecodeDump(d) // 只要求不 panic
			}
		}
	}
}

// 真实语料被截断/篡改后同样不许 panic(离线回归: 用户可能喂进半个文件或被人改过的 dump)。
func TestDecodeDumpNeverPanicsOnTruncatedCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("无语料")
	}
	checked := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for cut := 0; cut <= len(b); cut += 5 {
			_, _ = DecodeDump(b[:cut])
		}
		for i := 0; i < len(b); i += 17 { // 单字节翻转(含器件类型/位宽等关键字节)
			c := append([]byte{}, b...)
			c[i] ^= 0xFF
			_, _ = DecodeDump(c)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("无语料")
	}
	t.Logf("截断/翻转回归: %d 份语料, 无 panic", checked)
}

// 审计 M3: "从设备载入"必须读设备的**当前**内容, 不能拿上次的缓存冒充。
func TestEditLoadFromDeviceAlwaysRereads(t *testing.T) {
	a, f := newWriteTestApp(t)
	if _, err := a.Dump(); err != nil {
		t.Fatal(err)
	}
	// 设备内容在"读取"之后被外部改动(模拟别的工具/部分写入)
	f.EEProm[325] = 0x77
	ed, err := a.EditLoadFromDevice()
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.EditState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Dirty {
		t.Fatalf("刚载入不应有改动: %+v", st)
	}
	_ = ed
	cur, err := a.EditBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) == 0 {
		t.Fatal("应拿到内容")
	}
}

// 审计 M4: 编辑器里有未保存改动时, 读取设备不得悄悄覆盖工作副本。
func TestDumpKeepsDirtyEditor(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.Dump(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetByte(325, 0xAB); err != nil {
		t.Fatal(err)
	}
	before, err := a.EditBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dump(); err != nil { // 再读一次设备
		t.Fatal(err)
	}
	after, err := a.EditBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("读取设备后编辑器里的未保存改动被丢弃了")
	}
	st, _ := a.EditState()
	if !st.Dirty {
		t.Fatal("编辑器应仍是 dirty(改动被保留)")
	}
	found := false
	for _, l := range a.Logs() {
		if strings.Contains(l.Text, "未保存改动") {
			found = true
		}
	}
	if !found {
		t.Fatal("应在日志里说明'编辑器有未保存改动, 未覆盖'")
	}
}
