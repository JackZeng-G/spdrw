package spd

import (
	"encoding/binary"
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

func TestEditorIdentityRoundTripAllGenerations(t *testing.T) {
	cases := []struct {
		name string
		dump []byte
		pn   string
	}{
		{"DDR4", makeDDR4(t), "TEST16GB-DDR4-3200"},
		{"DDR5", makeDDR5(t), "DDR5-TEST-16GB"},
		{"DDR3", makeDDR3(t), "DDR3-TEST-8GB"},
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
	// 版本字节: 空白头创建 XMP 时应初始化为 0x20(XMP 2.0; 语料 3 份真实头全是
	// 0x20), 不是 DDR3 时代 XMP 1.2 的 0x12(审计 L3)
	e2 := editorFor(t, makeDDR4(t))
	if err := e2.SetField("xmp.present", "是"); err != nil {
		t.Fatalf("xmp.present 开: %v", err)
	}
	if got := e2.Bytes()[xmp2Base+3]; got != 0x20 {
		t.Fatalf("新建 XMP 头的版本字节 = %#02x, want 0x20", got)
	}
	// 中途非法的 CL 列表: 必须报错且**一字节不动**(审计 L1: 旧实现先清零再逐项
	// 解析, "16,abc" 会把掩码清掉/写一半才报错)
	before := append([]byte{}, e.Bytes()...)
	if err := e.SetField("xmp.p1.cl", "16,abc"); err == nil {
		t.Fatal("中途非法的 CL 列表应被拒")
	}
	if err := e.SetField("xmp.p1.cl", "16,99"); err == nil {
		t.Fatal("中途超界的 CL 列表应被拒")
	}
	for i := range before {
		if e.Bytes()[i] != before[i] {
			t.Fatalf("失败的 CL 编辑却改动了 @%#x(%02X→%02X)", i, before[i], e.Bytes()[i])
			break
		}
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
		"DDR4": makeDDR4(t), "DDR5": makeDDR5(t), "DDR3": makeDDR3(t),
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
		if name == "DDR3" {
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

func TestDDR3SingleByteMediumRejectsOversize(t *testing.T) {
	e := editorFor(t, makeDDR3(t))
	orig := editorFieldValues(e)
	for _, key := range []string{"ddr3.tCKmin", "ddr3.tAA", "ddr3.tRCD", "ddr3.tRP"} {
		before := append([]byte{}, e.Bytes()...)
		// 40ns 需要 medium=320 > 255: 必须报错, 且一个字节都不能动
		if err := e.SetField(key, "40"); err == nil {
			t.Errorf("%s=40ns 应报超出范围(单字节 medium 上限 255×125ps=31.875ns)", key)
		}
		_ = before
		for i, b := range e.Bytes() {
			if b != before[i] {
				t.Fatalf("%s=40ns 被拒后仍改了字节 @%#x", key, i)
			}
		}
		// 写回当前值必须是彻底的无操作(原值为空 = 未设置, 跳过)
		if orig[key] == "" {
			continue
		}
		if err := e.SetField(key, orig[key]); err != nil {
			t.Errorf("%s 写回当前值 %q: %v", key, orig[key], err)
		}
	}
	for i, b := range e.Bytes() {
		if b != before0(t, makeDDR3(t))[i] {
			t.Fatalf("写回当前值后字节应复原, @%#x 不同", i)
		}
	}
	if e.IsDirty() {
		t.Fatal("应为无变更")
	}
}

// before0 返回一份全新夹具(用于比对"未改过"的字节)。
func before0(t *testing.T, d []byte) []byte { return d }

// ---- 时序按周期(clk)显示与输入 ----

func fieldHint(t *testing.T, fields []Field, key string) string {
	t.Helper()
	for _, f := range fields {
		if f.Key == key {
			return f.Hint
		}
	}
	t.Fatalf("字段 %s 不存在", key)
	return ""
}

func setFieldNS(t *testing.T, e *Editor, key, value string) {
	t.Helper()
	if err := e.SetField(key, value); err != nil {
		t.Fatalf("SetField(%s, %s): %v", key, value, err)
	}
}

func TestTimingClkInputDDR4(t *testing.T) {
	e := editorFor(t, makeDDR4(t))
	base, ok := e.TCKminNS()
	if !ok || base != 0.75 {
		t.Fatalf("TCKminNS = %v %v, want 0.75ns", base, ok)
	}
	// hint: tRCD=1.5ns @0.75ns → 2 clk; tCK 自身不显示 hint
	if h := fieldHint(t, e.Fields(), "ddr4.tRCD"); h != "2 clk" {
		t.Fatalf("tRCD hint = %q, want %q", h, "2 clk")
	}
	if h := fieldHint(t, e.Fields(), "ddr4.tCKAVGmin"); h != "" {
		t.Fatalf("tCKAVGmin 不应有 hint, got %q", h)
	}
	// 按 ns 与按 clk 等价(字节一致), 且都确实改了字节
	eNS := editorFor(t, makeDDR4(t))
	setFieldNS(t, eNS, "ddr4.tRCD", "12")
	eClk := editorFor(t, makeDDR4(t))
	setFieldNS(t, eClk, "ddr4.tRCD", "16clk")
	eClk2 := editorFor(t, makeDDR4(t))
	setFieldNS(t, eClk2, "ddr4.tRCD", "16 CLK") // 大小写与空格都收
	if string(eNS.Bytes()[25]) != string(eClk.Bytes()[25]) || string(eNS.Bytes()[122]) != string(eClk.Bytes()[122]) {
		t.Fatalf("按 ns(12) 与按 clk(16×0.75) 编码不一致: med=%d/%d fine=%d/%d",
			eNS.Bytes()[25], eClk.Bytes()[25], eNS.Bytes()[122], eClk.Bytes()[122])
	}
	if string(eClk.Bytes()[25]) != string(eClk2.Bytes()[25]) {
		t.Fatalf("大小写/空格解析不一致")
	}
	// 半周期: 2.5clk = 1.875ns
	eHalf := editorFor(t, makeDDR4(t))
	setFieldNS(t, eHalf, "ddr4.tRCD", "2.5clk")
	f := eHalf.Fields()
	var got string
	for _, x := range f {
		if x.Key == "ddr4.tRCD" {
			got = x.Value
		}
	}
	if got != "1.875" {
		t.Fatalf("2.5clk → %s ns, want 1.875", got)
	}
	// 非法输入
	for _, bad := range []string{"abc", "-1clk", "0clk", "", "1sec"} {
		if err := e.SetField("ddr4.tRCD", bad); err == nil {
			t.Fatalf("SetField(%q) 应报错", bad)
		}
	}
}

func TestTimingClkDDR5AndProfiles(t *testing.T) {
	d := makeDDR5(t)
	binary.LittleEndian.PutUint16(d[20:22], 625) // tCKmin = 625ps = 0.625ns (DDR5-3200)
	binary.LittleEndian.PutUint16(d[30:32], 8125)
	e := editorFor(t, d)
	if base, ok := e.TCKminNS(); !ok || base != 0.625 {
		t.Fatalf("TCKminNS = %v %v", base, ok)
	}
	if h := fieldHint(t, e.Fields(), "ddr5.tAA"); h != "13 clk" {
		t.Fatalf("tAA hint = %q, want 13 clk", h)
	}
	// JEDEC 字段按周期写入(tRCD 原为 0)
	setFieldNS(t, e, "ddr5.tRCD", "16clk")
	var trcd string
	for _, x := range e.Fields() {
		if x.Key == "ddr5.tRCD" {
			trcd = x.Value
		}
	}
	if trcd != "10" {
		t.Fatalf("ddr5.tRCD(16clk) = %s ns, want 10", trcd)
	}
	// XMP3 profile: tCK 与 JEDEC 可以不同; 周期向上取整要保守(实际 ≥ 请求值)
	e5 := editorFor(t, makeDDR5(t))
	setFieldNS(t, e5, "xmp3.present", "是")
	setFieldNS(t, e5, "xmp3.p1.tCK", "0.625")
	setFieldNS(t, e5, "xmp3.p1.tAA", "13.5clk") // 8.4375ns → ps 编码 8438(≥8437.5)
	var v, h string
	for _, x := range e5.Fields() {
		if x.Key == "xmp3.p1.tAA" {
			v, h = x.Value, x.Hint
		}
	}
	if v != "8.438" {
		t.Fatalf("xmp3.p1.tAA(13.5clk) = %s ns, want 8.438", v)
	}
	if h != "14 clk" { // 8.438/0.625 = 13.5008 → 14(向上取整, 保守)
		t.Fatalf("xmp3.p1.tAA hint = %q, want 14 clk", h)
	}
	// XMP2(DDR4): profile tCK 与 JEDEC 不同步的情况
	e4 := editorFor(t, makeDDR4(t))
	setFieldNS(t, e4, "xmp.present", "是")
	setFieldNS(t, e4, "xmp.p1.tCK", "0.625") // 3200 的 profile tCK ≠ JEDEC 0.75
	setFieldNS(t, e4, "xmp.p1.tRCD", "18clk")
	var pv, ph string
	for _, x := range e4.Fields() {
		if x.Key == "xmp.p1.tRCD" {
			pv, ph = x.Value, x.Hint
		}
	}
	if pv != "11.25" { // 18 × 0.625
		t.Fatalf("xmp.p1.tRCD(18clk) = %s ns, want 11.25(按 profile tCK)", pv)
	}
	if ph != "18 clk" {
		t.Fatalf("xmp.p1.tRCD hint = %q, want 18 clk", ph)
	}
	// 白名单: 电压字段不接受 clk(否则换算结果会被当伏特写下去)
	if err := e4.SetField("xmp.p1.voltage", "2clk"); err == nil {
		t.Fatal("电压字段带 clk 输入应报错")
	}
}

func TestParseTimingInput(t *testing.T) {
	cases := []struct {
		in    string
		ns    float64
		byClk bool
		ok    bool
	}{
		{"10", 10, false, true},
		{" 10.5 ", 10.5, false, true},
		{"10ns", 10, false, true},
		{"10NS", 10, false, true},
		{"16clk", 16, true, true},
		{"16 clk", 16, true, true},
		{"16CLK", 16, true, true},
		{"2.5clk", 2.5, true, true},
		{"abc", 0, false, false},
		{"-1clk", 0, false, false},
		{"0clk", 0, false, false},
		{"", 0, false, false},
		{"1sec", 0, false, false},
	}
	for _, c := range cases {
		ns, byClk, err := parseTimingInput(c.in)
		if c.ok && (err != nil || ns != c.ns || byClk != c.byClk) {
			t.Fatalf("parseTimingInput(%q) = %v %v %v, want ns=%v clk=%v", c.in, ns, byClk, err, c.ns, c.byClk)
		}
		if !c.ok && err == nil {
			t.Fatalf("parseTimingInput(%q) 应报错", c.in)
		}
	}
}
