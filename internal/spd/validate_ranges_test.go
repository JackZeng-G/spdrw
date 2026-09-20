package spd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 校验区表(CRCRanges)必须与校验器(CRCOK)完全一致 —— 这张表是编辑器"改了要不要
// 重算 CRC"的唯一依据, 一旦与校验器脱节, 界面就会给出错误的结论。
//
// 判定方式(逐字节、逐位翻转):
//   - 翻转"参与校验"的字节(覆盖区内的数据, 或 CRC 值本身)→ CRCOK 必须变成 false
//   - 翻转其他任何字节(例如 DDR4/DDR5 的厂商/序列号/日期/部件号)→ CRCOK 必须仍为 true
//
// 极少数"存在性魔数"字节翻转后会改变"该扩展区是否存在"的判定(区域被整体跳过),
// 因此不适用第一条 —— 这些字节在下面显式列出, 将来改动必须重新审视。
func TestCRCRangesMatchValidator(t *testing.T) {
	dumps := corpusDumps(t)
	if len(dumps) == 0 {
		t.Skip("无语料")
	}
	checked, skipped := 0, 0
	for name, dump := range dumps {
		ranges := CRCRanges(dump)
		if len(ranges) == 0 {
			t.Errorf("%s: 没有校验区(世代 %v)", name, mustType(t, dump))
			continue
		}
		ok, err := CRCOK(dump)
		if err != nil || !ok {
			t.Fatalf("%s: 语料本身校验不通过(err=%v ok=%v)", name, err, ok)
		}
		rt0 := mustType(t, dump)
		for off := range dump {
			for _, bit := range []byte{0x01, 0x80} {
				mod := append([]byte{}, dump...)
				mod[off] ^= bit
				// 少数字节决定"这是哪一代 SPD"(如 DDR3 byte2 = 器件类型): 翻转后
				// 世代判定本身变了(甚至变成另一代且侥幸校验通过), 无从比较 —— 跳过。
				if rt2, _, err := Identify(mod); err != nil || rt2 != rt0 {
					skipped++
					continue
				}
				got, err := CRCOK(mod)
				if err != nil {
					skipped++
					continue
				}
				if presenceDecisionByte(dump, off) {
					continue // 决定"这段是否参与校验"的开关字节: 见函数注释
				}
				affects := AffectsChecksum(ranges, off)
				if affects && got {
					t.Errorf("%s@%#x: 该校验区内的字节翻转后 CRCOK 仍为 true(校验区表与校验器不一致)", name, off)
				}
				if !affects && !got {
					t.Errorf("%s@%#x: 不在任何校验区的字节翻转却让 CRCOK 失败(校验区表漏了一段)", name, off)
				}
			}
			checked++
		}
	}
	if checked < 10000 {
		t.Fatalf("样本量太小(%d 个字节), 检查语料是否缺失", checked)
	}
	t.Logf("已逐字节核对 %d 个偏移(跳过 %d 个翻转后世代无法识别的字节, 共 %d 份 dump)", checked, skipped, len(dumps))
}

// presenceDecisionByte 报告该偏移是否为"是否参与校验"的开关字节:
//   - XMP 3.0 头魔数(0x280/0x281): 没有它整段 XMP(头 + 各槽)都不校验;
//   - EXPO 魔数(0x340-0x343): 有它则 0x340/0x380 两槽按 EXPO 段校验;
//   - 各 XMP 槽首字节(VPP): 翻转可能把"空槽"变成"有效 profile", 从而开启该槽校验。
//
// 这些字节翻转后会改变判定本身, 因此不适用"区内翻转必然失败"的预期。
func presenceDecisionByte(dump []byte, off int) bool {
	rt, _, err := Identify(dump)
	if err != nil {
		return false
	}
	switch rt {
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		if off == xmp30Offset || off == xmp30Offset+1 {
			return true
		}
		if off >= expoOffset && off < expoOffset+4 {
			return true
		}
		for _, slot := range XMP30ProfileOffsets {
			if off == slot {
				return true
			}
		}
	}
	return false
}

// 世代相关的"哪些身份字段不参与校验"必须与 JEDEC 布局一致: 这是用户实际遇到的
// 问题 —— 改序列号/日期提示"记得重算 CRC"是错的, 它们根本不在校验范围内。
func TestUnprotectedIdentityAreas(t *testing.T) {
	cases := []struct {
		name  string
		dump  []byte
		free  []string // 必须报告为"不参与校验"
		bound []string // 必须报告为"参与校验"
	}{
		{"DDR5", makeDDR5(t), []string{"序列号", "生产日期", "部件号", "厂商"}, nil},
		{"DDR4", makeDDR4(t), []string{"序列号", "生产日期", "部件号", "厂商"}, nil},
		{"DDR3", makeDDR3(t), []string{"部件号", "修订版本"}, []string{"序列号", "生产日期", "厂商"}},
		{"DDR2", makeDDR2(t), []string{"序列号", "生产日期", "部件号", "厂商"}, nil},
	}
	for _, c := range cases {
		rt, _, err := Identify(c.dump)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		free := map[string]bool{}
		for _, a := range UnprotectedIdentityAreas(rt, CRCRanges(c.dump)) {
			free[a.Name] = true
		}
		// 其余身份字段(有名字的)应被判为"参与校验"
		all := map[string]bool{}
		for _, a := range IdentityAreas(rt) {
			all[a.Name] = true
		}
		for _, n := range c.free {
			if !free[n] {
				t.Errorf("%s: %s 应报告为不参与校验(实际 free=%v)", c.name, n, free)
			}
		}
		for _, n := range c.bound {
			if free[n] {
				t.Errorf("%s: %s 在覆盖范围内, 不应报告为不参与校验", c.name, n)
			}
		}
		for _, a := range IdentityAreas(rt) {
			if a.Start < 0 || a.End > len(c.dump) {
				t.Errorf("%s: %s 区间越界 %d-%d", c.name, a.Name, a.Start, a.End)
			}
		}
	}
}

// 具体布局断言: 数字写死, 防止"表算错了但自洽"。
func TestCRCRangesLayout(t *testing.T) {
	d5 := CRCRanges(makeDDR5(t))
	if len(d5) == 0 || d5[0].Start != 0 || d5[0].End != 510 || d5[0].CRCOff != 510 || d5[0].CRCLen != 2 {
		t.Fatalf("DDR5 基础段应为 0-509(CRC 510/511): %+v", d5)
	}
	// 0x200-0x27F(厂商/序列号/日期/部件号)与 0x200 之后的空隙都不参与校验
	for _, off := range []int{512, 515, 517, 521, 551} {
		if AffectsChecksum(d5, off) {
			t.Errorf("DDR5 %#x 不应参与校验", off)
		}
	}
	if !AffectsChecksum(d5, 0) || !AffectsChecksum(d5, 509) || !AffectsChecksum(d5, 510) {
		t.Errorf("DDR5 0/509/510 必须参与校验(510 是 CRC 值本身)")
	}

	d4 := CRCRanges(makeDDR4(t))
	if len(d4) != 2 || d4[0].Start != 0 || d4[0].End != 126 || d4[1].Start != 128 || d4[1].End != 254 {
		t.Fatalf("DDR4 应为 0-125 与 128-253 两段: %+v", d4)
	}
	for _, off := range []int{126, 127, 254, 255} {
		if !AffectsChecksum(d4, off) {
			t.Errorf("DDR4 %#x 是 CRC 值本身, 必须视为影响校验", off)
		}
	}
	for _, off := range []int{320, 323, 325, 329, 384, 500} {
		if AffectsChecksum(d4, off) {
			t.Errorf("DDR4 %#x 不在校验区(身份区/XMP 2.0), 不应被判为影响校验", off)
		}
	}

	d3 := CRCRanges(makeDDR3(t))
	if len(d3) != 1 || d3[0].End != 126 {
		t.Fatalf("DDR3 单段 0-125: %+v", d3)
	}
	if !AffectsChecksum(d3, 122) {
		t.Error("DDR3 序列号(0x7A-0x7D)在 126 字节覆盖范围内, 应判为影响校验")
	}
	if AffectsChecksum(d3, 128) {
		t.Error("DDR3 部件号(0x80+)不在覆盖范围内")
	}

	d2 := CRCRanges(makeDDR2(t))
	if len(d2) != 1 || !d2[0].Checksum || d2[0].End != 63 || d2[0].CRCOff != 63 || d2[0].CRCLen != 1 {
		t.Fatalf("DDR2 应为 0-62 的 8 位校验和(值在 63): %+v", d2)
	}
	if AffectsChecksum(d2, 64) {
		t.Error("DDR2 身份区(0x40+)不参与校验和")
	}
}

// CRCOffsets(写/修复用, "槽内有数据"判据)必须**覆盖** CRCBytes(校验器判据):
// 校验器会校验的 CRC 字节一定也要被写入/修复, 反过来允许更多(半填充槽也会被修)。
func TestCRCOffsetsMatchesRanges(t *testing.T) {
	dumps := corpusDumps(t)
	if len(dumps) == 0 {
		t.Skip("无语料")
	}
	extra := 0
	for name, dump := range dumps {
		want := map[int]bool{}
		for _, off := range CRCBytes(CRCRanges(dump)) {
			want[off] = true
		}
		got := map[int]bool{}
		for _, off := range CRCOffsets(dump) {
			got[off] = true
		}
		for off := range want {
			if !got[off] {
				t.Errorf("%s: 校验器校验 %#x, 但 CRCOffsets 不会写/修它", name, off)
			}
		}
		extra += len(got) - len(want)
	}
	t.Logf("CRCOffsets 比校验区多覆盖 %d 个 CRC 字节(半填充槽/无 XMP 头的残留槽)", extra)
}

// corpusDumps 读入 testdata/spd 下所有真实 dump(跳过负样本与错误长度)。
func corpusDumps(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".bin") && !strings.HasSuffix(e.Name(), ".spd") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("读取 %s: %v", e.Name(), err)
		}
		if _, _, err := Identify(b); err != nil {
			continue
		}
		if ok, err := CRCOK(b); err != nil || !ok {
			continue // 只拿校验通过的语料做"翻转后必然失败"的对照
		}
		out[e.Name()] = b
	}
	return out
}

func mustType(t *testing.T, dump []byte) RamType {
	t.Helper()
	rt, _, err := Identify(dump)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	return rt
}

// 字段优先级: "常用/关键"字段必须有标记, 且各分组里都留了默认显示的字段 ——
// 界面默认只显示 primary, 否则一屏铺不下(用户反馈: JEDEC 时序优先显示重要的)。
func TestFieldsMarkPrimary(t *testing.T) {
	dumps := corpusDumps(t)
	if len(dumps) == 0 {
		t.Skip("无语料")
	}
	checked := 0
	for name, dump := range dumps {
		if !strings.HasPrefix(name, "ddr5") && !strings.HasPrefix(name, "ddr4") {
			continue
		}
		ed, err := NewEditor(dump)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		byGroup := map[string][2]int{} // [primary, total]
		for _, f := range ed.Fields() {
			c := byGroup[f.Group]
			c[1]++
			if f.Primary {
				c[0]++
			}
			byGroup[f.Group] = c
		}
		for g, c := range byGroup {
			if c[0] == 0 {
				t.Errorf("%s: 分组 %q 没有任何常用字段(默认会显示空白)", name, g)
			}
			limit := 24
			if g == "XMP 3.0" || g == "XMP 2.0" || g == "EXPO" {
				limit = 40 // profile 组: 第一份 profile 的关键字段 + 启用位/名称
			}
			if c[0] > limit {
				t.Errorf("%s: 分组 %q 常用字段过多(%d/%d), 起不到收敛作用", name, g, c[0], c[1])
			}
		}
		// 关键时序必须是常用字段(仅限 JEDEC 组与第 1 份 profile: 其余 profile 默认收起)
		for _, f := range ed.Fields() {
			base := f.Key
			if j := strings.LastIndex(base, "."); j >= 0 {
				base = base[j+1:]
			}
			isFirstProfile := !strings.Contains(f.Key, ".p2.") && !strings.Contains(f.Key, ".p3.") &&
				!strings.Contains(f.Key, ".p4.") && !strings.Contains(f.Key, ".p5.")
			switch base {
			case "tAA", "tRCD", "tRP", "tRAS", "tRC", "cl":
				if isFirstProfile && !f.Primary {
					t.Errorf("%s: %s(%s) 应标记为常用字段", name, f.Key, f.Name)
				}
			}
		}
		checked++
	}
	if checked == 0 {
		t.Skip("无 DDR4/DDR5 语料")
	}
	t.Logf("字段优先级检查: %d 份 dump", checked)
}
