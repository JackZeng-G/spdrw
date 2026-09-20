package spd

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
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

// TestRealDumpEditorFieldNoop 对语料里每份 dump 的每个字段做"设回当前值"，
// 断言字节一个都不变 —— 这同时验证了字段的读/写编码互为逆运算。
func TestRealDumpEditorFieldNoop(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	checked, skipped := 0, 0
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
			continue
		}
		orig := append([]byte{}, ed.Bytes()...)
		for _, f := range ed.Fields() {
			if f.Value == "" || f.Kind == "bool" && f.Value != "true" && f.Value != "false" {
				continue
			}
			if err := ed.SetField(f.Key, f.Value); err != nil {
				t.Errorf("%s: 设回 %s=%q 报错: %v", name, f.Key, f.Value, err)
				skipped++
				continue
			}
			checked++
		}
		got := ed.Bytes()
		for i := range got {
			if got[i] != orig[i] {
				t.Fatalf("%s: 把字段设回当前值却改了字节 @0x%03X(%02X→%02X)", name, i, orig[i], got[i])
			}
		}
	}
	t.Logf("字段幂等检查: 通过 %d 个赋值, %d 个失败", checked, skipped)
}

// TestRealDumpXMP2Profiles 在语料里的 DDR4 XMP 2.0 样本上验证扩展区解析:
// 头部 magic、启用位、电压与 tCK 必须能读出来且换算合理。
func TestRealDumpXMP2Profiles(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr4") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(dump) != 512 || dump[384] != 0x0C || dump[385] != 0x4A {
			continue
		}
		d, err := NewDDR4(dump)
		if err != nil {
			t.Fatalf("%s: NewDDR4: %v", e.Name(), err)
		}
		if !d.XMPPresence() {
			t.Fatalf("%s: XMP 应存在", e.Name())
		}
		profs := d.XMPProfiles()
		found := false
		for _, p := range profs {
			if !p.Enabled {
				continue
			}
			found = true
			ns := p.TCKmin.NanoSeconds(d.Timebase())
			if ns <= 0 || ns > 100 {
				t.Errorf("%s: XMP P%d tCK = %.3f ns 不合理", e.Name(), p.Number+1, ns)
			}
			if p.Volts <= 0.5 || p.Volts > 2.5 {
				t.Errorf("%s: XMP P%d 电压 = %.2f V 不合理", e.Name(), p.Number+1, p.Volts)
			}
			t.Logf("%s: XMP P%d = %.0f MHz %.2fV CL=%v", e.Name(), p.Number+1,
				p.TCKmin.MegaHertz(d.Timebase()), p.Volts, p.CasLat.ToArray())
		}
		if !found {
			t.Errorf("%s: XMP 存在但没有启用的 profile", e.Name())
		}
		n++
	}
	if n == 0 {
		t.Skip("语料里没有 DDR4 XMP 2.0 样本")
	}
	t.Logf("DDR4 XMP 2.0 样本: %d 份", n)
}

// TestRealDumpDDR5XMP3AndEXPO 在语料里的 DDR5 样本上验证 XMP3/EXPO 解析与 CRC 结论一致。
func TestRealDumpDDR5XMP3AndEXPO(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	var xmp3, expo, slots int
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr5") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		d, err := NewDDR5(dump)
		if err != nil {
			t.Fatalf("%s: NewDDR5: %v", e.Name(), err)
		}
		if d.XMPPresence() {
			xmp3++
			if !d.XMP30HeaderCRCOK() {
				t.Errorf("%s: XMP3 header CRC 不通过", e.Name())
			}
			enabled := dump[0x283]
			for i, present := range d.XMP30Slots() {
				if !present {
					continue
				}
				slots++
				// 已是真实 profile: 槽内 CRC 必须通过
				off := XMP30ProfileOffsets[i]
				sec := dump[off : off+64]
				if Crc16(sec[:62]) != uint16(sec[62])|uint16(sec[63])<<8 {
					t.Errorf("%s: 槽 %d(%#x) 被判定为存在但 CRC 不通过", e.Name(), i+1, off)
				}
			}
			// 启用位指向的槽通常应当存在; 个别手工构造的示例文件会出现
			// "启用位=1 但槽为空"的不一致, 这里只记录不判失败
			for i := 0; i < 3; i++ {
				if enabled&(1<<i) != 0 && !d.XMP30Slots()[i] {
					t.Logf("%s: 启用位 bit%d=1 但槽 %d 为空(文件自身不一致)", e.Name(), i, i+1)
				}
			}
		}
		if d.EXPOPresence() {
			expo++
			sec := dump[expoOffset : expoOffset+expoLen]
			if Crc16(sec[:126]) != uint16(sec[126])|uint16(sec[127])<<8 {
				t.Errorf("%s: EXPO CRC 不通过", e.Name())
			}
		}
	}
	t.Logf("DDR5 语料: XMP3 %d 份 / EXPO %d 份 / 有效 profile 槽 %d 个", xmp3, expo, slots)
}

// TestCorruptSamplesHandledSafely 负样本: 不能 panic, 且结构异常要被如实报告。
func TestCorruptSamplesHandledSafely(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd", "corrupt")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无负样本目录(%v)", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n++
		// 任何长度都必须能安全走一遍识别/校验链路(不得 panic)
		rt, size, ierr := Identify(dump)
		if ierr == nil && len(dump) == size {
			ok, cerr := CRCOK(dump)
			if cerr == nil && ok {
				t.Logf("%s: 负样本却通过了 CRC(%v)", e.Name(), rt)
			}
		} else {
			t.Logf("%s: 长度/类型异常(长度 %d): %v", e.Name(), len(dump), ierr)
		}
		// 编辑器对异常样本必须优雅失败而不是 panic
		if ed, err := NewEditor(dump); err == nil {
			_ = ed.Fields()
			_, _ = ed.FixCRC()
		}
	}
	t.Logf("负样本处理: %d 份, 无 panic", n)
}

// TestRealDumpManufacturerNames 语料里的厂商 ID 必须解析成正确厂商名。
// 期望值来自 JEP106 与实物条品牌(不依赖本仓库的表, 避免"表错则测试也错")。
func TestRealDumpManufacturerNames(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	want := map[string]string{
		"ddr3-micron-8ktf51264hz-1g6e1-1600mhz-eloaders.bin":            "Micron Technology",
		"ddr3-samsung-m471b2873fhs-ch9-0x61cf1261-1333mhz-eloaders.bin": "Samsung",
		"ddr3-hynixafr-baboomerang.bin":                                 "SK Hynix",
		"ddr3-kingston-kvr13ls9s6-2-017-a00lf-eloaders.bin":             "Kingston",
		"ddr4-micron_4gib_dimm_mta9asf51272pz-2g1a2-coreboot.bin":       "Micron Technology",
		"ddr4-samsung-k4aag165wa-bctd-coreboot.bin":                     "Samsung",
		"ddr4-hynix-h5anag6namr-uh-coreboot.bin":                        "SK Hynix",
		"ddr4-gskill-flarex-3200-2x8g-samsungb-eloaders.spd":            "G.Skill Intl",
		"ddr4-micron-ballistix-elite-4000-4x8g-eloaders.spd":            "Crucial Technology",
		"ddr4-patriot-viper4-blackout-3200-2x8g-hynixcjr-eloaders.spd":  "Patriot Memory (PDP Systems)",
		"ddr5-corsair-cmk32gx5m2b5600z40-cityson.bin":                   "Corsair",
		"ddr5-crucial-ct16g56c46u5-cityson.bin":                         "Crucial Technology",
		"ddr5-geil-d5-8000-cl38-cityson.bin":                            "Golden Empire",
		"ddr5-gskill-f5-6000j3636f16g-cityson.bin":                      "G.Skill Intl",
		"ddr5-samsung-m323r1gb4pb0-cityson.bin":                         "Samsung",
		"ddr5-teamgroup-ud5-6000-omi.spd":                               "Team Group Inc",
		"ddr5-tforce-ud5-6000-cityson.bin":                              "Team Group Inc",
		"ddr5-oloy-d5u0852382b-k69-djchumpguy.spd":                      "OLOy Technology",
	}
	checked := 0
	for name, exp := range want {
		dump, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("缺少样本 %s: %v", name, err)
			continue
		}
		var cont, code byte
		switch len(dump) {
		case 256:
			cont, code = dump[117], dump[118]
		case 512:
			cont, code = dump[320], dump[321]
		case 1024:
			cont, code = dump[512], dump[513]
		}
		got := ManufacturerName(cont, code)
		if got != exp {
			t.Errorf("%s: 厂商 = %q, 期望 %q(cont=%#02x code=%#02x, 说明 %q)",
				name, got, exp, cont, code, ManufacturerIDNote(cont, code))
			continue
		}
		checked++
	}
	t.Logf("厂商名核对: %d/%d 通过", checked, len(want))
}

// TestManufacturerNoteForMalformedID 续延字节校验位不成立时给出可读原因(而非静默空白)。
func TestManufacturerNoteForMalformedID(t *testing.T) {
	// KLEVV 实物条: cont=0x18(奇校验不成立) / code=0x98
	if n := ManufacturerIDNote(0x18, 0x98); n == "" {
		t.Fatal("异常厂商 ID 应给出说明")
	} else if !strings.Contains(n, "奇校验") {
		t.Fatalf("说明应提到校验位: %q", n)
	}
	if n := ManufacturerIDNote(0x00, 0x00); !strings.Contains(n, "未写入") {
		t.Fatalf("0/0 应说明未写入: %q", n)
	}
	if n := ManufacturerIDNote(0x80, 0xCE); n != "" {
		t.Fatalf("可解析的 ID 不应有说明: %q", n)
	}
}

// TestRealDumpXMP2TimingsMatchVendorSpec 用厂商公布的 XMP 规格核对 XMP 2.0 时序解析。
//
// 这条测试是为了锁住一个真实缺陷: 上游实现 C# 把 XMP profile 里 byte+0x14 的高位 nibble
// 位序写反了(tRAS/tRC 互换), 于是 Viper4 3200 的 tRAS 被读成 87 周期(真实 36),
// 而 tRC 只剩低字节(12.8 周期, 真实 64)。编辑器↔解析器的交叉校验抓不到这种错 ——
// 两边用的是同一个错误约定 —— 只有拿厂商规格做外部参照才行。
func TestRealDumpXMP2TimingsMatchVendorSpec(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	cases := []struct {
		file string
		tRAS int // 周期
		tRC  int
	}{
		{"ddr4-patriot-viper4-blackout-3200-2x8g-hynixcjr-eloaders.spd", 36, 64},
		{"ddr4-micron-ballistix-elite-4000-4x8g-eloaders.spd", 39, 64},
		{"ddr4-gskill-flarex-3200-2x8g-samsungb-eloaders.spd", 34, 48},
	}
	for _, c := range cases {
		dump, err := os.ReadFile(filepath.Join(dir, c.file))
		if err != nil {
			t.Errorf("缺少样本 %s: %v", c.file, err)
			continue
		}
		d, err := NewDDR4(dump)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		tb := d.Timebase()
		prof := d.XMPProfiles()[0]
		if !prof.Enabled {
			t.Errorf("%s: XMP profile 1 应启用", c.file)
			continue
		}
		gotRAS := prof.TRAS.ClockCycles(tb, prof.TCKmin)
		gotRC := prof.TRC.ClockCycles(tb, prof.TCKmin)
		if gotRAS != c.tRAS || gotRC != c.tRC {
			t.Errorf("%s: XMP tRAS/tRC = %d/%d 周期, 期望 %d/%d(厂商规格)",
				c.file, gotRAS, gotRC, c.tRAS, c.tRC)
		}
		// 编辑器字段必须与解析器给出同一个值
		ed, err := NewEditor(dump)
		if err != nil {
			t.Fatal(err)
		}
		fields := editorFieldValues(ed)
		for key, want := range map[string]int{"xmp.p1.tRAS": c.tRAS, "xmp.p1.tRC": c.tRC} {
			ns, err := strconv.ParseFloat(fields[key], 64)
			if err != nil {
				t.Errorf("%s: 编辑器 %s = %q: %v", c.file, key, fields[key], err)
				continue
			}
			cyc := int(ns/prof.TCKmin.NanoSeconds(tb) + 0.001)
			if cyc != want {
				t.Errorf("%s: 编辑器 %s = %d 周期, 期望 %d", c.file, key, cyc, want)
			}
		}
		// 写回同值不得改动字节(tRC 的高位必须被正确保留)
		before := append([]byte{}, ed.Bytes()...)
		for _, key := range []string{"xmp.p1.tRAS", "xmp.p1.tRC"} {
			if err := ed.SetField(key, fields[key]); err != nil {
				t.Errorf("%s: 设回 %s=%s: %v", c.file, key, fields[key], err)
			}
		}
		for i, b := range ed.Bytes() {
			if b != before[i] {
				t.Fatalf("%s: 设回 XMP 时序却改了字节 @%#x", c.file, i)
			}
		}
	}
}

// TestRealDumpDDR5ProfilesMatchVendorSpec 用厂商型号里公开的规格核对 DDR5 的
// JEDEC/XMP3/EXPO 解析。型号本身就把规格写在名字里, 因此这是**外部参照**:
// 代码与它不一致就是代码错(与 XMP 2.0 那条 nibble 位序 bug 同一类)。
func TestRealDumpDDR5ProfilesMatchVendorSpec(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	cases := []struct {
		file       string
		mtPerS     float64 // 数据率 MT/s
		vdd        float64
		wantCL     int
		wantEXPO   bool
		expoMTPerS float64
	}{
		// Corsair CMK32GX5M2B5600Z40: DDR5-5600, CL40, 1.25V(XMP3 + EXPO 都有)
		{"ddr5-corsair-cmk32gx5m2b5600z40-cityson.bin", 5600, 1.25, 40, true, 5600},
		// GeIL D5-8000 CL38: DDR5-8000, CL38, 1.45V
		{"ddr5-geil-d5-8000-cl38-cityson.bin", 8000, 1.45, 38, true, 8000},
		// G.Skill F5-6000J3636F16G: DDR5-6000, 1.35V(只有 EXPO)
		{"ddr5-gskill-f5-6000j3636f16g-cityson.bin", 0, 1.35, 0, true, 6000},
		// TEAMGROUP UD5-6000: XMP3 两个 profile(6000 CL38 / 5600 CL40) + 对应 EXPO
		{"ddr5-teamgroup-ud5-6000-omi.spd", 6000, 1.25, 38, true, 6000},
	}
	for _, c := range cases {
		dump, err := os.ReadFile(filepath.Join(dir, c.file))
		if err != nil {
			t.Errorf("缺少样本 %s: %v", c.file, err)
			continue
		}
		ed, err := NewEditor(dump)
		if err != nil {
			t.Fatalf("%s: NewEditor: %v", c.file, err)
		}
		fields := editorFieldValues(ed)
		parse := func(key string) float64 {
			v, _ := strconv.ParseFloat(fields[key], 64)
			return v
		}
		if c.mtPerS > 0 {
			tck := parse("xmp3.p1.tCK")
			if tck <= 0 {
				t.Errorf("%s: xmp3.p1.tCK = %q", c.file, fields["xmp3.p1.tCK"])
			} else if got := 2000 / tck; math.Abs(got-c.mtPerS) > 20 {
				t.Errorf("%s: XMP3 频率 = %.0f MT/s, 期望 %.0f", c.file, got, c.mtPerS)
			}
			if vdd := parse("xmp3.p1.vdd"); math.Abs(vdd-c.vdd) > 0.006 {
				t.Errorf("%s: XMP3 VDD = %.3f V, 期望 %.2f", c.file, vdd, c.vdd)
			}
			if cl := fields["xmp3.p1.cl"]; !strings.Contains(cl, strconv.Itoa(c.wantCL)) {
				t.Errorf("%s: XMP3 CL 列表 %q 应含 CL%d", c.file, cl, c.wantCL)
			}
		}
		if c.wantEXPO {
			if !EXPOPresenceOf(dump) {
				t.Errorf("%s: 应存在 EXPO", c.file)
			}
			tck := parse("expo.p1.tCK")
			if tck <= 0 {
				t.Errorf("%s: expo.p1.tCK = %q", c.file, fields["expo.p1.tCK"])
			} else if got := 2000 / tck; math.Abs(got-c.expoMTPerS) > 20 {
				t.Errorf("%s: EXPO 频率 = %.0f MT/s, 期望 %.0f", c.file, got, c.expoMTPerS)
			}
			if vdd := parse("expo.p1.vdd"); math.Abs(vdd-c.vdd) > 0.006 {
				t.Errorf("%s: EXPO VDD = %.3f V, 期望 %.2f", c.file, vdd, c.vdd)
			}
		}
	}
}

// EXPOPresenceOf 是给测试用的小helper(避免测试里再构造解析器)。
func EXPOPresenceOf(dump []byte) bool {
	return len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
}
