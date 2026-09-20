package spd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 解析/校验层对任意垃圾输入都不许 panic(长度、器件类型、保留编码都可以是任意值)。
func TestParsersNeverPanicOnGarbage(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 4, 8, 9, 16, 63, 64, 126, 127, 128, 255, 256, 257, 384, 511, 512, 513, 768, 1023, 1024, 1025}
	types := []byte{0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x00, 0x07, 0xFF}
	for _, n := range sizes {
		for _, ty := range types {
			for seed := 0; seed < 8; seed++ {
				d := make([]byte, n)
				for i := range d {
					d[i] = byte((i*53 + int(ty)*13 + seed*29) % 256)
				}
				if n > 2 {
					d[2] = ty
				}
				_, _, _ = Identify(d)
				_ = ValidateSpd(d)
				_, _ = CRCOK(d)
				_ = CRCRanges(d)
				_, _ = FixCRC(d)
				_ = CRCOffsets(d)
				if _, err := ParseBasic(d); err == nil {
					// 解析成功也必须能安全取字段
					_, _ = NewDDR4(d)
					_, _ = NewDDR5(d)
					_, _ = NewEditor(d)
				}
				if ed, err := NewEditor(d); err == nil {
					_ = ed.Fields()
					_, _ = ed.CRCOK(), ed.Bytes()
				}
			}
		}
	}
}

// 语料截断/翻转: 不许 panic; 长度与世代匹配时 CRC 相关 API 也不许崩。
func TestParsersNeverPanicOnCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("无语料")
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for cut := 0; cut <= len(b); cut += 11 {
			_, _ = CRCOK(b[:cut])
			_, _ = FixCRC(append([]byte{}, b[:cut]...))
		}
		for i := 0; i < len(b); i += 13 {
			c := append([]byte{}, b...)
			c[i] ^= 0xFF
			_, _ = CRCOK(c)
			if ed, err := NewEditor(c); err == nil {
				_ = ed.Fields()
				_, _ = FixCRC(append([]byte{}, c...))
			}
		}
		n++
	}
	if n == 0 {
		t.Skip("无语料")
	}
	t.Logf("截断/翻转回归: %d 份语料, 无 panic", n)
}

// 跨世代 key 必须被拒绝(审计发现的阻断项): 以前在 DDR3 的 256 字节 dump 上
// SetField("xmp3.p1.tCK", ...) 会越界 panic; 在 DDR5 上 SetField("xmp.p1.tRAS", ...)
// 会被接受并写进基础 CRC 覆盖区里的无关字节(静默改错数据)。
func TestSetFieldRejectsForeignGenerationKeys(t *testing.T) {
	d3 := make([]byte, 256)
	d3[2] = 0x0B
	ed3, err := NewEditor(d3)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"xmp3.p1.tCK", "xmp3.p2.tRFC1", "xmp.p1.tCK", "xmp.p1.tRAS", "expo.p1.tCK"} {
		if err := ed3.SetField(key, "10"); err == nil {
			t.Errorf("DDR3 上 %s 应被拒绝", key)
		}
	}
	d5 := make([]byte, 1024)
	d5[2] = 0x12
	ed5, err := NewEditor(d5)
	if err != nil {
		t.Fatal(err)
	}
	if err := ed5.SetField("xmp.p1.tRAS", "10"); err == nil {
		t.Error("DDR5 上 xmp.* 应被拒绝(否则会改动基础 CRC 覆盖区)")
	}
	// 同世代的 key 仍然可用
	if err := ed5.SetField("xmp3.p1.tCK", "0.5"); err != nil {
		t.Errorf("DDR5 上 xmp3.* 应可用: %v", err)
	}
}

// 任意 key × 任意世代都不许 panic(即使 key 合法但偏移落在 dump 之外)。
func TestSetFieldNeverPanicsOnAnyKey(t *testing.T) {
	keys := []string{
		"xmp.present", "xmp.version", "xmp.p1.tCK", "xmp.p1.tRAS", "xmp.p2.vdd",
		"xmp3.present", "xmp3.p1.tCK", "xmp3.p5.tRFC1", "xmp3.enabled3", "xmp3.name2",
		"expo.present", "expo.p1.tCK", "expo.p2.tRC", "expo.p2.enabled",
		"ddr4.cl", "ddr5.cl", "serial", "partNumber", "manufacturer", "mfgCont",
	}
	for _, n := range []int{256, 512, 1024} {
		for _, ty := range []byte{0x0B, 0x0C, 0x12} {
			d := make([]byte, n)
			d[2] = ty
			ed, err := NewEditor(d)
			if err != nil {
				continue
			}
			for _, k := range keys {
				_ = ed.SetField(k, "1") // 只要求不 panic
			}
		}
	}
}

// present 开关不许有"幽灵变更"或破坏性副作用(审计 H1):
//   - 对**已经存在**的扩展区执行 present=true 必须逐字节无变化;
//   - 关闭只清 magic, 版本/启用位/profile 内容必须保留, 再打开也只补 magic。
func TestPresentToggleIsNonDestructive(t *testing.T) {
	for _, tc := range []struct{ name, path, onKey string }{
		{"XMP2", "ddr4-micron-ballistix-elite-4000-4x8g-eloaders.spd", "xmp.present"},
		{"XMP3", "ddr5-tforce-ud5-6000-cityson.bin", "xmp3.present"},
		{"EXPO", "ddr5-gskill-f5-6000j3636f16g-cityson.bin", "expo.present"},
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "spd", tc.path))
		if err != nil {
			t.Skipf("%s: %v", tc.name, err)
		}
		ed, err := NewEditor(raw)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := editorFieldValues(ed)[tc.onKey]; got != "true" {
			t.Skipf("%s: 该样本没有扩展区", tc.name)
		}
		// 1) 已存在时 present=true 必须是无操作
		before := append([]byte{}, ed.Bytes()...)
		if err := ed.SetField(tc.onKey, "true"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for i := range before {
			if ed.Bytes()[i] != before[i] {
				t.Errorf("%s: 已存在的扩展区被 present=true 改动 @%#x(%02X→%02X)",
					tc.name, i, before[i], ed.Bytes()[i])
				break
			}
		}
		// 2) 关掉再打开: 只允许 magic 字节变化, 其余必须逐字节复原
		if err := ed.SetField(tc.onKey, "false"); err != nil {
			t.Fatalf("%s: 关闭失败: %v", tc.name, err)
		}
		if err := ed.SetField(tc.onKey, "true"); err != nil {
			t.Fatalf("%s: 打开失败: %v", tc.name, err)
		}
		for i := range before {
			if ed.Bytes()[i] != before[i] {
				t.Errorf("%s: 关→开之后 @%#x 未复原(%02X→%02X)", tc.name, i, before[i], ed.Bytes()[i])
				break
			}
		}
	}
}
