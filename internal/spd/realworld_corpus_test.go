package spd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 真实 dump 语料回归: 遍历 testdata/spd 下的样本, 断言"能识别/能解析/不 panic",
// 并统计 CRC 情况。语料来自公开仓库(见 testdata/spd/MANIFEST.md), 不参与构建。
//
// 运行: go test ./internal/spd/ -run TestRealDumpCorpus -v
func TestRealDumpCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	var total, crcOK, crcBad, parsed int
	byGen := map[string]int{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") && !strings.HasSuffix(name, ".spd") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		total++
		rt, size, err := Identify(dump)
		if err != nil {
			t.Errorf("%s: Identify 失败: %v", name, err)
			continue
		}
		want := ""
		switch {
		case strings.HasPrefix(name, "ddr2"):
			want = "DDR2"
		case strings.HasPrefix(name, "ddr3"):
			want = "DDR3"
		case strings.HasPrefix(name, "ddr4"):
			want = "DDR4"
		case strings.HasPrefix(name, "ddr5"):
			want = "DDR5"
		}
		if want != "" && !strings.HasPrefix(rt.String(), want) {
			t.Errorf("%s: 类型识别为 %v, 期望 %s", name, rt, want)
		}
		if len(dump) != size {
			t.Errorf("%s: 长度 %d 与 %v 的 %d 不符", name, len(dump), rt, size)
			continue
		}
		byGen[rt.String()]++

		ok, cerr := CRCOK(dump)
		if cerr != nil {
			t.Errorf("%s: CRCOK 报错: %v", name, cerr)
		} else if ok {
			crcOK++
		} else {
			crcBad++
			t.Logf("%s: CRC 不通过", name)
		}

		// 解析不能 panic, 且关键字段要能读出
		switch rt {
		case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
			d, err := NewDDR4(dump)
			if err != nil {
				t.Errorf("%s: NewDDR4: %v", name, err)
				continue
			}
			_ = d.ModuleType()
			_ = d.TotalCapacityBytes()
			_ = d.PartNumber()
			_ = d.CRCOK()
			_ = d.XMPProfiles()
		case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
			d, err := NewDDR5(dump)
			if err != nil {
				t.Errorf("%s: NewDDR5: %v", name, err)
				continue
			}
			_ = d.ModuleType()
			_ = d.TotalCapacityBytes()
			_ = d.PartNumber()
			_ = d.Timings()
			_ = d.XMP30Slots()
			_ = d.CRCOK()
		default:
			if _, err := ParseBasic(dump); err != nil {
				t.Errorf("%s: ParseBasic: %v", name, err)
				continue
			}
		}
		parsed++
	}
	if total == 0 {
		t.Skip("语料为空")
	}
	t.Logf("语料统计: 共 %d 份(解析成功 %d), CRC 通过 %d / 不通过 %d, 世代分布 %v",
		total, parsed, crcOK, crcBad, byGen)
}

// TestRealDumpEditorRoundTrip 对语料做"编辑→改回→逐字节一致"的可逆性检查,
// 这是编辑器最核心的不变量。
func TestRealDumpEditorRoundTrip(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") && !strings.HasSuffix(name, ".spd") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		ed, err := NewEditor(dump)
		if err != nil {
			continue // 语料里的片段/异常文件跳过
		}
		id, err := ed.Identity()
		if err != nil {
			t.Errorf("%s: Identity: %v", name, err)
			continue
		}
		// 改部件号再改回
		if err := ed.SetField("partNumber", "ROUNDTRIP-CHECK"); err != nil {
			t.Errorf("%s: 设置部件号: %v", name, err)
			continue
		}
		if err := ed.SetField("partNumber", id.PartNumber); err != nil {
			t.Errorf("%s: 恢复部件号: %v", name, err)
			continue
		}
		got := ed.Bytes()
		for i := range got {
			if got[i] != dump[i] {
				t.Fatalf("%s: 可逆性破坏 @0x%03X: %02X != %02X", name, i, got[i], dump[i])
			}
		}
		n++
	}
	t.Logf("可逆性检查通过 %d 份", n)
}

// TestRealDumpCRCFixRepairsBadSamples 断言"重算 CRC"能修好语料里 CRC 不过的样本。
func TestRealDumpCRCFixRepairsBadSamples(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	fixed, failed := 0, 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") && !strings.HasSuffix(name, ".spd") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		rt, size, err := Identify(dump)
		if err != nil || len(dump) != size {
			continue
		}
		if rt == DDR2 || rt == DDR2FBDIMM || rt == DDR2FBDIMMP {
			continue
		}
		if ok, _ := CRCOK(dump); ok {
			continue
		}
		cp := append([]byte{}, dump...)
		if _, err := FixCRC(cp); err != nil {
			failed++
			t.Errorf("%s: FixCRC 报错: %v", name, err)
			continue
		}
		if ok, _ := CRCOK(cp); !ok {
			failed++
			t.Errorf("%s: FixCRC 之后 CRC 仍不通过", name)
			continue
		}
		fixed++
		t.Logf("%s: CRC 已可修复", name)
	}
	t.Logf("CRC 修复: 成功 %d, 失败 %d", fixed, failed)
}
