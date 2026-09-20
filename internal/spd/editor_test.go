package spd

import (
	"strings"
	"testing"
)

// 编辑器测试: 字段读写、可逆性、CRC 重算、扩展区创建/互斥、非法值拒绝。

func editorFor(t *testing.T, dump []byte) *Editor {
	t.Helper()
	e, err := NewEditor(dump)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	return e
}

// assertReversible 断言"改回原值后与原始 dump 逐字节相同"。
func assertReversible(t *testing.T, e *Editor, key, orig string) {
	t.Helper()
	if err := e.SetField(key, "临时值-忽略"); err == nil {
		// 值可行时再改回来
		_ = e.SetField(key, orig)
	}
	if err := e.SetField(key, orig); err != nil {
		t.Fatalf("恢复 %s=%q: %v", key, orig, err)
	}
	got := e.Bytes()
	for i := range got {
		if got[i] != e.original[i] {
			t.Fatalf("恢复后 @0x%03X 仍不同: %02X != %02X", i, got[i], e.original[i])
		}
	}
}

func TestEditorIdentityRoundTripAllGenerations(t *testing.T) {
	cases := []struct {
		name string
		dump []byte
		pn   string
	}{
		{"DDR4", makeDDR4(t), "TEST16GB-DDR4-3200"},
		{"DDR5", makeDDR5(t), "DDR5-TEST-16GB"},
		{"DDR3", makeDDR3(t), "DDR3-TEST-8GB"},
		{"DDR2", makeDDR2(t), "DDR2-TEST-1GB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := editorFor(t, c.dump)
			id, err := e.Identity()
			if err != nil {
				t.Fatalf("Identity: %v", err)
			}
			if id.PartNumber != c.pn {
				t.Fatalf("部件号 = %q, want %q", id.PartNumber, c.pn)
			}
			// 改部件号 → 解析能读回
			if err := e.SetField("partNumber", "NEW-PN-1234"); err != nil {
				t.Fatalf("SetField(partNumber): %v", err)
			}
			id2, _ := e.Identity()
			if !strings.HasPrefix(id2.PartNumber, "NEW-PN-1234") {
				t.Fatalf("部件号未生效: %q", id2.PartNumber)
			}
			// 改序列号
			if err := e.SetField("serial", "01020304"); err != nil {
				t.Fatalf("SetField(serial): %v", err)
			}
			id3, _ := e.Identity()
			if id3.SerialHex != "01020304" {
				t.Fatalf("序列号未生效: %q", id3.SerialHex)
			}
			// 改日期
			if err := e.SetField("dateYear", "2027"); err != nil {
				t.Fatalf("dateYear: %v", err)
			}
			if err := e.SetField("dateWeek", "7"); err != nil {
				t.Fatalf("dateWeek: %v", err)
			}
			id4, _ := e.Identity()
			if id4.DateYear != 2027 || id4.DateWeek != 7 {
				t.Fatalf("日期未生效: %d/%d", id4.DateYear, id4.DateWeek)
			}
			// 改厂商名(走 JEP106 反查)
			if err := e.SetField("manufacturer", "Samsung"); err != nil {
				t.Fatalf("manufacturer: %v", err)
			}
			id5, _ := e.Identity()
			if !strings.Contains(strings.ToLower(id5.Manufacturer), "samsung") {
				t.Fatalf("厂商未生效: %q", id5.Manufacturer)
			}
			// 可逆: 全部改回原始值
			orig, _ := NewEditor(c.dump)
			oid, _ := orig.Identity()
			for _, kv := range [][2]string{
				{"partNumber", oid.PartNumber},
				{"serial", oid.SerialHex},
				{"dateYear", itoaTest(oid.DateYear)},
				{"dateWeek", itoaTest(oid.DateWeek)},
				{"manufacturer", oid.Manufacturer},
			} {
				if err := e.SetField(kv[0], kv[1]); err != nil {
					t.Fatalf("恢复 %s=%q: %v", kv[0], kv[1], err)
				}
			}
			got, want := e.Bytes(), c.dump
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("恢复后 @0x%03X 不同: %02X != %02X(%s)", i, got[i], want[i], c.name)
				}
			}
		})
	}
}

func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestEditorTimingRoundTrip(t *testing.T) {
	// DDR4: medium+fine 双粒度
	d4 := editorFor(t, makeDDR4(t))
	if err := d4.SetField("ddr4.tCKAVGmin", "0.750"); err != nil {
		t.Fatalf("DDR4 tCK: %v", err)
	}
	if err := d4.SetField("ddr4.tRCD", "13.75"); err != nil {
		t.Fatalf("DDR4 tRCD: %v", err)
	}
	if err := d4.SetField("ddr4.tRFC1", "350.000"); err != nil {
		t.Fatalf("DDR4 tRFC1: %v", err)
	}
	fields := map[string]string{}
	for _, f := range d4.Fields() {
		fields[f.Key] = f.Value
	}
	if got := fields["ddr4.tRCD"]; got != "13.75" {
		t.Fatalf("DDR4 tRCD 回读 = %q, want 13.75", got)
	}
	if got := fields["ddr4.tRFC1"]; got != "350" {
		t.Fatalf("DDR4 tRFC1 回读 = %q", got)
	}

	// DDR5: 16bit ps
	d5 := editorFor(t, makeDDR5(t))
	if err := d5.SetField("ddr5.tAA", "16.25"); err != nil {
		t.Fatalf("DDR5 tAA: %v", err)
	}
	if err := d5.SetField("ddr5.tRFC1", "295"); err != nil {
		t.Fatalf("DDR5 tRFC1: %v", err)
	}
	v5 := map[string]string{}
	for _, f := range d5.Fields() {
		v5[f.Key] = f.Value
	}
	if v5["ddr5.tAA"] != "16.25" {
		t.Fatalf("DDR5 tAA 回读 = %q", v5["ddr5.tAA"])
	}
	if v5["ddr5.tRFC1"] != "295" {
		t.Fatalf("DDR5 tRFC1 回读 = %q", v5["ddr5.tRFC1"])
	}

	// DDR3: MTB/FTB(byte9-11)
	d3 := editorFor(t, makeDDR3(t))
	if err := d3.SetField("ddr3.tCKmin", "1.250"); err != nil {
		t.Fatalf("DDR3 tCK: %v", err)
	}
	v3 := map[string]string{}
	for _, f := range d3.Fields() {
		v3[f.Key] = f.Value
	}
	if v3["ddr3.tCKmin"] != "1.25" {
		t.Fatalf("DDR3 tCKmin 回读 = %q", v3["ddr3.tCKmin"])
	}
}

func TestEditorCRCAutoRecalc(t *testing.T) {
	d := makeDDR4(t)
	e := editorFor(t, d)
	if !e.CRCOK() {
		t.Fatal("初始 CRC 应通过")
	}
	if err := e.SetField("ddr4.tAA", "15.0"); err != nil { // 第 1 段(byte24)
		t.Fatal(err)
	}
	if e.CRCOK() {
		t.Fatal("改数据后 CRC 应不通过(尚未重算)")
	}
	n, err := e.FixCRC()
	if err != nil {
		t.Fatalf("FixCRC: %v", err)
	}
	if n == 0 {
		t.Fatal("FixCRC 应至少改动 CRC 字节")
	}
	if !e.CRCOK() {
		t.Fatal("FixCRC 后应通过")
	}
	// 变更列表里能看出 CRC 被改动
	found := false
	for _, c := range e.Changes() {
		if c.Offset == 126 || c.Offset == 127 {
			found = true
		}
	}
	if !found {
		t.Fatalf("变更列表应包含 CRC 字节: %+v", e.Changes())
	}
}

func TestEditorRawHexEdit(t *testing.T) {
	e := editorFor(t, makeDDR4(t))
	if err := e.SetByte(500, 0xAB); err != nil {
		t.Fatal(err)
	}
	b, _ := e.GetByte(500)
	if b != 0xAB {
		t.Fatalf("原始编辑未生效: %#x", b)
	}
	if err := e.SetByte(512, 1); err == nil {
		t.Fatal("越界应报错")
	}
	if err := e.SetBytes(510, []byte{1, 2}); err != nil {
		t.Fatalf("SetBytes: %v", err)
	}
	if err := e.SetBytes(511, []byte{1, 2}); err == nil {
		t.Fatal("SetBytes 越界应报错")
	}
	// Reset 回到初始
	e.Reset()
	if e.IsDirty() || !e.CRCOK() {
		t.Fatal("Reset 后应无变更且 CRC 通过")
	}
}

func TestEditorXMP2(t *testing.T) {
	e := editorFor(t, makeDDR4(t))
	if err := e.SetField("xmp.p1.enabled", "true"); err != nil {
		t.Fatalf("enabled: %v", err)
	}
	if err := e.SetField("xmp.p1.voltage", "1.35"); err != nil {
		t.Fatalf("voltage: %v", err)
	}
	if err := e.SetField("xmp.p1.tCK", "0.625"); err != nil {
		t.Fatalf("tCK: %v", err)
	}
	if err := e.SetField("xmp.p1.cl", "16,18,20"); err != nil {
		t.Fatalf("cl: %v", err)
	}
	vals := map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if vals["xmp.p1.voltage"] != "1.35" {
		t.Fatalf("XMP2 电压回读 = %q", vals["xmp.p1.voltage"])
	}
	if vals["xmp.p1.tCK"] != "0.625" {
		t.Fatalf("XMP2 tCK 回读 = %q", vals["xmp.p1.tCK"])
	}
	if vals["xmp.p1.cl"] != "" && !strings.Contains(vals["xmp.p1.cl"], "18") {
		t.Fatalf("XMP2 CL 回读 = %q", vals["xmp.p1.cl"])
	}
	// 非法 CL(超出 7-35)
	if err := e.SetField("xmp.p1.cl", "60"); err == nil {
		t.Fatal("XMP2 CL 超出 7-35 应被拒")
	}
	if err := e.SetField("xmp.p1.voltage", "3.5"); err == nil {
		t.Fatal("XMP2 电压必须在 1.00-2.27V")
	}
}

func TestEditorXMP3AndEXPO(t *testing.T) {
	d := makeDDR5(t)
	e := editorFor(t, d)

	// 创建 XMP3 头
	if err := e.SetField("xmp3.present", "true"); err != nil {
		t.Fatalf("xmp3 create: %v", err)
	}
	if err := e.SetField("xmp3.enabled1", "true"); err != nil {
		t.Fatalf("enabled1: %v", err)
	}
	if err := e.SetField("xmp3.name1", "My Profile"); err != nil {
		t.Fatalf("name1: %v", err)
	}
	if err := e.SetField("xmp3.p1.vdd", "1.35"); err != nil {
		t.Fatalf("vdd: %v", err)
	}
	if err := e.SetField("xmp3.p1.tCK", "0.416"); err != nil {
		t.Fatalf("tCK: %v", err)
	}
	if err := e.SetField("xmp3.p1.cl", "30,32,34"); err != nil {
		t.Fatalf("cl: %v", err)
	}
	if err := e.SetField("xmp3.p1.commandRate", "2"); err != nil {
		t.Fatalf("commandRate: %v", err)
	}
	if _, err := e.FixCRC(); err != nil {
		t.Fatalf("FixCRC: %v", err)
	}
	if !e.CRCOK() {
		t.Fatal("XMP3 编辑后 CRC 应通过(含 header 段)")
	}
	vals := map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if vals["xmp3.p1.vdd"] != "1.350" && vals["xmp3.p1.vdd"] != "1.35" {
		t.Fatalf("XMP3 VDD 回读 = %q", vals["xmp3.p1.vdd"])
	}
	if vals["xmp3.name1"] != "My Profile" {
		t.Fatalf("XMP3 名称回读 = %q", vals["xmp3.name1"])
	}
	if !strings.Contains(vals["xmp3.p1.cl"], "32") {
		t.Fatalf("XMP3 CL 回读 = %q", vals["xmp3.p1.cl"])
	}
	// 非法 CL(奇数)
	if err := e.SetField("xmp3.p1.cl", "31"); err == nil {
		t.Fatal("XMP3 奇数 CL 应被拒")
	}

	// EXPO:占用 0x340-0x3BF, 与 XMP3 槽 3/User1 互斥
	if err := e.SetField("expo.present", "true"); err != nil {
		t.Fatalf("expo create: %v", err)
	}
	if err := e.SetField("expo.p1.vdd", "1.40"); err != nil {
		t.Fatalf("expo vdd: %v", err)
	}
	if err := e.SetField("expo.p1.tCK", "0.5"); err != nil {
		t.Fatalf("expo tCK: %v", err)
	}
	if err := e.SetField("expo.p1.tRTP", "7.5"); err != nil {
		t.Fatalf("expo tRTP: %v", err)
	}
	if err := e.SetField("xmp3.p3.tCK", "1.0"); err == nil {
		t.Fatal("EXPO 存在时应拒绝写 XMP3 槽 3")
	}
	if _, err := e.FixCRC(); err != nil {
		t.Fatalf("FixCRC: %v", err)
	}
	if !e.CRCOK() {
		t.Fatal("EXPO 编辑后 CRC 应通过")
	}
	vals = map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if !strings.HasPrefix(vals["expo.p1.vdd"], "1.4") {
		t.Fatalf("EXPO VDD 回读 = %q", vals["expo.p1.vdd"])
	}
	if vals["expo.p1.tCK"] != "0.5" {
		t.Fatalf("EXPO tCK 回读 = %q", vals["expo.p1.tCK"])
	}
	// EXPO 存在时不应暴露 XMP3 槽 3 字段
	for _, f := range e.Fields() {
		if strings.HasPrefix(f.Key, "xmp3.p3.") {
			t.Fatalf("EXPO 存在时不应暴露 %s", f.Key)
		}
	}
}

func TestEditorRejectsInvalidInput(t *testing.T) {
	e := editorFor(t, makeDDR5(t))
	for _, tc := range [][2]string{
		{"serial", "ZZZZ"},
		{"dateWeek", "60"},
		{"dateYear", "1990"},
		{"partNumber", strings.Repeat("X", 40)},
		{"ddr5.tCKAVGmin", "-1"},
		{"ddr5.tCKAVGmin", "abc"},
		{"ddr5.tCKAVGmin", "70000"},
		{"manufacturer", "不存在的厂商名"},
		{"no.such.field", "1"},
		{"expo.enabledMask", "999"},
	} {
		if err := e.SetField(tc[0], tc[1]); err == nil {
			t.Fatalf("SetField(%q, %q) 应报错", tc[0], tc[1])
		}
	}
}

func TestEditorFieldsAllGenerations(t *testing.T) {
	for name, dump := range map[string][]byte{
		"DDR4": makeDDR4(t), "DDR5": makeDDR5(t), "DDR3": makeDDR3(t), "DDR2": makeDDR2(t),
	} {
		e := editorFor(t, dump)
		fs := e.Fields()
		if len(fs) < 8 {
			t.Fatalf("%s 字段过少: %d", name, len(fs))
		}
		keys := map[string]bool{}
		for _, f := range fs {
			if f.Key == "" || f.Name == "" || f.Group == "" {
				t.Fatalf("%s 字段缺少 key/name/group: %+v", name, f)
			}
			if keys[f.Key] {
				t.Fatalf("%s 字段 key 重复: %s", name, f.Key)
			}
			keys[f.Key] = true
		}
		if name == "DDR2" || name == "DDR3" {
			if keys["ddr4.tAA"] || keys["ddr5.tAA"] {
				t.Fatalf("%s 不应出现其它世代的时序字段", name)
			}
		}
	}
}

func TestSearchManufacturers(t *testing.T) {
	got := SearchManufacturers("micron", 5)
	if len(got) == 0 {
		t.Fatal("应能搜到 Micron")
	}
	for _, m := range got {
		if !strings.Contains(strings.ToLower(m.Name), "micron") {
			t.Fatalf("搜索结果不符: %+v", m)
		}
	}
	if SearchManufacturers("zzzzzzzz", 5) != nil {
		t.Fatal("无匹配应返回空")
	}
}

func TestEditorDDR2Timings(t *testing.T) {
	e := editorFor(t, makeDDR2(t))
	// tCKmin 是 BCD "ns.十分位"
	if err := e.SetField("ddr2.tCKmin", "2.5"); err != nil {
		t.Fatalf("ddr2.tCKmin: %v", err)
	}
	vals := map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if vals["ddr2.tCKmin"] != "2.5" {
		t.Fatalf("DDR2 tCKmin 回读 = %q", vals["ddr2.tCKmin"])
	}
	// BCD 扩展码: 0.25
	if err := e.SetField("ddr2.tCKmax", "3.25"); err != nil {
		t.Fatalf("ddr2.tCKmax(0.25): %v", err)
	}
	vals = map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if vals["ddr2.tCKmax"] != "3.25" {
		t.Fatalf("DDR2 tCKmax 回读 = %q", vals["ddr2.tCKmax"])
	}
	// 不可表示的十分位应被拒
	if err := e.SetField("ddr2.tCKmin", "2.05"); err == nil {
		t.Fatal("DDR2 不可表示的十分位应被拒")
	}
	// 1/4ns 粒度
	if err := e.SetField("ddr2.tRP", "3.75"); err != nil {
		t.Fatalf("ddr2.tRP: %v", err)
	}
	vals = map[string]string{}
	for _, f := range e.Fields() {
		vals[f.Key] = f.Value
	}
	if vals["ddr2.tRP"] != "3.75" {
		t.Fatalf("DDR2 tRP 回读 = %q", vals["ddr2.tRP"])
	}
	// 整数 ns 字段
	if err := e.SetField("ddr2.tRFC", "128"); err != nil {
		t.Fatalf("ddr2.tRFC: %v", err)
	}
	if _, err := e.FixCRC(); err != nil {
		t.Fatalf("DDR2 校验和重算: %v", err)
	}
	if !e.CRCOK() {
		t.Fatal("DDR2 改动后校验和应可重算")
	}
}
