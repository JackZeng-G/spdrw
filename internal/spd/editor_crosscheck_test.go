package spd

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 编辑器的独立性校验。
//
// 编辑层(editor_*.go)与解析层(ddr4.go/ddr5.go/ddr23.go)是两套**各自独立实现**的
// 偏移/编码逻辑: 同一根条的同一个时序, 解析器从字节读出来、编辑器也从字节读出来。
// 如果编辑器把某个字段指错了字节, 两边就会给出不同的值 —— 这是唯一能在没有硬件、
// 也没有人工逐字段核对的情况下发现"偏移表写错"的办法。
//
// 语料见 testdata/spd/MANIFEST.md。

func corpusDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "spd")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	return dir
}

func corpusFiles(t *testing.T) []string {
	t.Helper()
	dir := corpusDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("读取语料目录失败(%v)", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		if strings.HasSuffix(e.Name(), ".bin") || strings.HasSuffix(e.Name(), ".spd") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// editorFieldValues 把编辑器字段表转成 map(key→值字符串)。
func editorFieldValues(e *Editor) map[string]string {
	out := map[string]string{}
	for _, f := range e.Fields() {
		out[f.Key] = f.Value
	}
	return out
}

func fieldNS(t *testing.T, e *Editor, key string) float64 {
	t.Helper()
	v := editorFieldValues(e)[key]
	if v == "" {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("字段 %s = %q 不是数值", key, v)
	}
	return f
}

func closeNS(a, b float64) bool { return math.Abs(a-b) < 0.0011 } // 1ps 粒度

// TestEditorAgreesWithParser 逐世代比对"编辑器读到的值"与"解析器读到的值"。
func TestEditorAgreesWithParser(t *testing.T) {
	checked := 0
	for _, path := range corpusFiles(t) {
		name := filepath.Base(path)
		dump, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rt, size, err := Identify(dump)
		if err != nil || len(dump) != size {
			continue
		}
		e, err := NewEditor(dump)
		if err != nil {
			continue
		}
		fields := editorFieldValues(e)
		id, _ := e.Identity()

		switch rt {
		case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
			d, err := NewDDR4(dump)
			if err != nil {
				t.Errorf("%s: NewDDR4: %v", name, err)
				continue
			}
			tb := d.Timebase()
			pairs := []struct {
				key string
				got float64
			}{
				{"ddr4.tCKAVGmin", d.TCKAVGmin().NanoSeconds(tb)},
				{"ddr4.tCKAVGmax", d.TCKAVGmax().NanoSeconds(tb)},
				{"ddr4.tAA", d.TAAmin().NanoSeconds(tb)},
				{"ddr4.tRCD", d.TRCDmin().NanoSeconds(tb)},
				{"ddr4.tRP", d.TRPmin().NanoSeconds(tb)},
				{"ddr4.tRAS", d.TRASmin().NanoSeconds(tb)},
				{"ddr4.tRC", d.TRCmin().NanoSeconds(tb)},
				{"ddr4.tRFC1", d.TRFC1().NanoSeconds(tb)},
				{"ddr4.tRFC2", d.TRFC2().NanoSeconds(tb)},
				{"ddr4.tRFC4", d.TRFC4().NanoSeconds(tb)},
				{"ddr4.tFAW", d.TFAW().NanoSeconds(tb)},
				{"ddr4.tRRD_S", d.TRRDS().NanoSeconds(tb)},
				{"ddr4.tRRD_L", d.TRRDL().NanoSeconds(tb)},
				{"ddr4.tCCD_L", d.TCCDL().NanoSeconds(tb)},
				{"ddr4.tWR", d.TWR().NanoSeconds(tb)},
				{"ddr4.tWTR_S", d.TWTRS().NanoSeconds(tb)},
				{"ddr4.tWTR_L", d.TWTRL().NanoSeconds(tb)},
			}
			for _, p := range pairs {
				ev := fieldNS(t, e, p.key)
				// 字段为 0 时编辑器视为"未设置"(空值), 解析器给出 0 —— 等价
				if math.IsNaN(ev) {
					if p.got != 0 {
						t.Errorf("%s: 编辑器 %s 为空, 解析器 = %.4f ns", name, p.key, p.got)
					}
					continue
				}
				if math.Abs(ev-p.got) > 0.0011 {
					t.Errorf("%s: 编辑器 %s = %.4f ns, 解析器 = %.4f ns", name, p.key, ev, p.got)
				}
			}
			if want := d.PartNumber(); id.PartNumber != want {
				t.Errorf("%s: 部件号 编辑器 %q vs 解析器 %q", name, id.PartNumber, want)
			}
			sn := d.SerialNumber()
			if want := hexOf(sn[:]); id.SerialHex != want {
				t.Errorf("%s: 序列号 编辑器 %q vs 解析器 %q", name, id.SerialHex, want)
			}
			y, w := d.DateCode()
			if id.DateYear != y || id.DateWeek != w {
				t.Errorf("%s: 日期 编辑器 %d/%d vs 解析器 %d/%d", name, id.DateYear, id.DateWeek, y, w)
			}
			mfg, _, _ := d.Manufacturer()
			if id.Manufacturer != mfg {
				t.Errorf("%s: 厂商 编辑器 %q vs 解析器 %q", name, id.Manufacturer, mfg)
			}
			if d.XMPPresence() {
				profs := d.XMPProfiles()
				for i, p := range profs {
					pre := "xmp.p" + strconv.Itoa(i+1)
					enabled := fields[pre+".enabled"] == "true"
					if enabled != p.Enabled {
						t.Errorf("%s: %s.enabled 编辑器 %v vs 解析器 %v", name, pre, enabled, p.Enabled)
					}
					if !p.Enabled {
						continue
					}
					want := p.TCKmin.NanoSeconds(tb)
					if got := fieldNS(t, e, pre+".tCK"); !closeNS(got, want) {
						t.Errorf("%s: %s.tCK 编辑器 %.4f vs 解析器 %.4f", name, pre, got, want)
					}
					want = p.TAAmin.NanoSeconds(tb)
					if got := fieldNS(t, e, pre+".tAA"); !closeNS(got, want) {
						t.Errorf("%s: %s.tAA 编辑器 %.4f vs 解析器 %.4f", name, pre, got, want)
					}
					want = p.TRCD.NanoSeconds(tb)
					if got := fieldNS(t, e, pre+".tRCD"); !closeNS(got, want) {
						t.Errorf("%s: %s.tRCD 编辑器 %.4f vs 解析器 %.4f", name, pre, got, want)
					}
					want = p.TRP.NanoSeconds(tb)
					if got := fieldNS(t, e, pre+".tRP"); !closeNS(got, want) {
						t.Errorf("%s: %s.tRP 编辑器 %.4f vs 解析器 %.4f", name, pre, got, want)
					}
					// 电压: 编辑器按 (1 + 低7位/100) 解码
					v := dump[0x180+i*63+0x09]
					wantV := float64(v>>7) + float64(v&0x7F)/100
					if math.Abs(p.Volts-wantV) > 0.005 {
						t.Errorf("%s: 解析器电压 %.2f 与原始字节 %.2f 不符", name, p.Volts, wantV)
					}
					if got := fields[pre+".voltage"]; got != "" {
						gv, _ := strconv.ParseFloat(got, 64)
						if math.Abs(gv-wantV) > 0.005 {
							t.Errorf("%s: %s.voltage 编辑器 %.2f vs 原始字节 %.2f", name, pre, gv, wantV)
						}
					}
				}
			}

		case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
			d, err := NewDDR5(dump)
			if err != nil {
				t.Errorf("%s: NewDDR5: %v", name, err)
				continue
			}
			tm := d.Timings()
			for key, ps := range map[string]int{
				"ddr5.tCKAVGmin": tm.TCKMinPS, "ddr5.tCKAVGmax": tm.TCKMaxPS,
				"ddr5.tAA": tm.TAA, "ddr5.tRCD": tm.TRCD, "ddr5.tRP": tm.TRP,
				"ddr5.tRAS": tm.TRAS, "ddr5.tRC": tm.TRC, "ddr5.tWR": tm.TWR,
				"ddr5.tRRD_L": tm.TRRDL, "ddr5.tCCD_L": tm.TCCDL,
				"ddr5.tCCD_L_WR": tm.TCCDLWR, "ddr5.tCCD_L_WR2": tm.TCCDLWR2,
				"ddr5.tFAW": tm.TFAW, "ddr5.tCCD_L_WTR": tm.TCCDLWTR,
				"ddr5.tCCD_S_WTR": tm.TCCDSWTR, "ddr5.tRTP": tm.TRTP,
				"ddr5.tCCD_M": tm.TCCDM, "ddr5.tCCD_M_WR": tm.TCCDMWR,
				"ddr5.tCCD_M_WTR": tm.TCCDMWTR,
			} {
				got := fieldNS(t, e, key)
				if ps == 0 {
					if !math.IsNaN(got) {
						t.Errorf("%s: 解析器 %s = 0(未设置) 但编辑器 = %.4f", name, key, got)
					}
					continue
				}
				if !closeNS(got, float64(ps)/1000) {
					t.Errorf("%s: 编辑器 %s = %.4f ns, 解析器 = %.4f ns", name, key, got, float64(ps)/1000)
				}
			}
			for key, ns := range map[string]int{
				"ddr5.tRFC1": tm.RFC1SLR, "ddr5.tRFC2": tm.RFC2SLR, "ddr5.tRFCsb": tm.RFCSbSLR,
				"ddr5.tRFC1DLR": tm.RFC1DLR, "ddr5.tRFC2DLR": tm.RFC2DLR, "ddr5.tRFCsbDLR": tm.RFCSbDLR,
			} {
				if ns == 0 {
					continue
				}
				if got := fieldNS(t, e, key); !closeNS(got, float64(ns)) {
					t.Errorf("%s: 编辑器 %s = %.4f ns, 解析器 = %.4f ns", name, key, got, float64(ns))
				}
			}
			if len(tm.CL) > 0 {
				got := fields["ddr5.cl"]
				for _, cl := range tm.CL {
					if !strings.Contains(got, strconv.Itoa(cl)) {
						t.Errorf("%s: 编辑器 CL=%q 缺少解析器给出的 CL%d", name, got, cl)
						break
					}
				}
			}
			if want := d.PartNumber(); id.PartNumber != want {
				t.Errorf("%s: 部件号 编辑器 %q vs 解析器 %q", name, id.PartNumber, want)
			}
			y, w := d.DateCode()
			if id.DateYear != y || id.DateWeek != w {
				t.Errorf("%s: 日期 编辑器 %d/%d vs 解析器 %d/%d", name, id.DateYear, id.DateWeek, y, w)
			}
			// 解析器认定存在的槽, 编辑器必须给出对应字段
			// (反之不成立: 编辑器会为"空槽"也给字段, 否则用户无法新建 profile)
			slots := d.XMP30Slots()
			for i := 0; i < 5; i++ {
				if d.EXPOPresence() && (i == 2 || i == 3) {
					continue
				}
				key := "xmp3.p" + strconv.Itoa(i+1) + ".tCK"
				if _, hasField := fields[key]; slots[i] && !hasField {
					t.Errorf("%s: 解析器认为槽 %d 存在, 编辑器却没有字段", name, i+1)
				}
			}
			if d.XMPPresence() {
				// XMP3 profile 1 的 VDD 与原始字节必须一致
				if slots[0] {
					raw := dump[XMP30ProfileOffsets[0]+1]
					wantV := float64(raw>>5) + float64(raw&0x1F)*5/100
					if got := fields["xmp3.p1.vdd"]; got != "" {
						gv, _ := strconv.ParseFloat(got, 64)
						if math.Abs(gv-wantV) > 0.006 {
							t.Errorf("%s: xmp3.p1.vdd 编辑器 %.3f vs 原始字节 %.3f", name, gv, wantV)
						}
					}
				}
			}

		case DDR3:
			b, err := ParseBasic(dump)
			if err != nil {
				t.Errorf("%s: ParseBasic: %v", name, err)
				continue
			}
			// tCKmin 解析器用 byte12×MTB + byte34(ps), 编辑器走同一套 timebase
			if got := fieldNS(t, e, "ddr3.tCKmin"); !closeNS(got, b.TCKminNS) {
				t.Errorf("%s: ddr3.tCKmin 编辑器 %.4f vs 解析器 %.4f", name, got, b.TCKminNS)
			}
			if id.PartNumber != b.PartNumber {
				t.Errorf("%s: 部件号 编辑器 %q vs 解析器 %q", name, id.PartNumber, b.PartNumber)
			}
			if id.Manufacturer != b.Manufacturer {
				t.Errorf("%s: 厂商 编辑器 %q vs 解析器 %q", name, id.Manufacturer, b.Manufacturer)
			}
			if id.DateYear != b.DateYear || id.DateWeek != b.DateWeek {
				t.Errorf("%s: 日期 编辑器 %d/%d vs 解析器 %d/%d", name, id.DateYear, id.DateWeek, b.DateYear, b.DateWeek)
			}
			if id.SerialHex != b.SerialHex {
				t.Errorf("%s: 序列号 编辑器 %q vs 解析器 %q", name, id.SerialHex, b.SerialHex)
			}

		case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
			b, err := ParseBasic(dump)
			if err != nil {
				continue
			}
			if id.PartNumber != b.PartNumber {
				t.Errorf("%s: 部件号 编辑器 %q vs 解析器 %q", name, id.PartNumber, b.PartNumber)
			}
			if id.Manufacturer != b.Manufacturer {
				t.Errorf("%s: 厂商 编辑器 %q vs 解析器 %q", name, id.Manufacturer, b.Manufacturer)
			}
			if got := fieldNS(t, e, "ddr2.tCKmin"); !closeNS(got, b.TCKminNS) {
				t.Errorf("%s: ddr2.tCKmin 编辑器 %.4f vs 解析器 %.4f", name, got, b.TCKminNS)
			}
		}
		checked++
	}
	t.Logf("编辑器↔解析器一致性: %d 份真实 dump 逐字段核对完成", checked)
}

// perturbValue 为一个字段构造"不同的合法值"(失败返回空串表示跳过)。
func perturbValue(f Field) string {
	switch f.Kind {
	case "bool":
		if f.Value == "true" {
			return "false"
		}
		return "true"
	case "float":
		v, err := strconv.ParseFloat(f.Value, 64)
		if err != nil || v == 0 {
			return ""
		}
		if f.Unit == "V" {
			return strconv.FormatFloat(v+0.05, 'f', 3, 64)
		}
		return strconv.FormatFloat(v*1.05+0.01, 'f', 3, 64)
	case "int":
		v, err := strconv.Atoi(f.Value)
		if err != nil {
			return ""
		}
		nv := v + 1
		if f.Max > 0 && float64(nv) > f.Max {
			nv = v - 1
		}
		if nv < 0 || (f.Min > 0 && float64(nv) < f.Min) {
			return ""
		}
		return strconv.Itoa(nv)
	case "hex":
		switch {
		case strings.Contains(f.Key, "serial"):
			return "A1B2C3D4"
		case strings.Contains(f.Key, "revision"):
			if strings.Contains(f.Offset, "1B") {
				return "AA"
			}
			return "AABB"
		}
		return ""
	case "string":
		switch {
		case strings.Contains(f.Key, "cl"):
			if strings.HasPrefix(f.Key, "ddr5") || strings.HasPrefix(f.Key, "xmp3") || strings.HasPrefix(f.Key, "expo") {
				return "30,32,34"
			}
			if strings.HasPrefix(f.Key, "xmp.") {
				return "16,18"
			}
			return "9,11,13" // DDR4 JEDEC(低段)
		case strings.Contains(f.Key, "partNumber"):
			return "PERTURB"
		case strings.Contains(f.Key, "manufacturer"):
			return "Samsung"
		case strings.Contains(f.Key, "name"):
			return "PERTURB-NAME"
		}
		return ""
	}
	return ""
}

// fieldMaxWidth 给出该字段一次写入允许影响的字节数上限。
func fieldMaxWidth(f Field) int {
	switch {
	case strings.Contains(f.Key, "partNumber"):
		return 30
	case strings.Contains(f.Key, "serial"):
		return 4
	case strings.Contains(f.Key, "revision"):
		return 2
	case strings.Contains(f.Key, "cl"):
		return 5
	case strings.Contains(f.Key, "manufacturer"):
		return 8
	case strings.Contains(f.Key, "name"):
		return 16
	case strings.HasSuffix(f.Key, ".present"):
		// present 开关的职责: 写自己那段 magic, 并在该区**还是空白**时顺手初始化
		// 版本/启用位(否则"新建一份 profile"不成立)。EXPO 的 magic 是 4 字节
		// ("EXPO")+版本+启用位 = 6; XMP 是 magic 2 + 版本 + 启用位 = 4。
		// 上限据此设定, 仍能抓住"字段写到别人区域"的越界。
		if strings.Contains(f.Key, "expo") {
			return 6
		}
		return 4
	case f.Kind == "float" || f.Kind == "int" || f.Kind == "bool":
		return 2
	case f.Kind == "hex":
		return 2
	}
	return 4
}

// TestEditorFieldWritesStayInBounds 不变量: 任何一个字段的写入
//  1. 绝不能落在 CRC/校验和字节上(除了 CRC 本身由 FixCRC 负责);
//  2. 影响的字节数不得超过该字段自身的宽度(否则说明偏移表串到了别的字段);
//  3. 改回原值后必须逐字节复原。
func TestEditorFieldWritesStayInBounds(t *testing.T) {
	checked, skipped := 0, 0
	for _, path := range corpusFiles(t) {
		name := filepath.Base(path)
		dump, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		e, err := NewEditor(dump)
		if err != nil {
			continue
		}
		crcSet := map[int]bool{}
		for _, off := range CRCOffsets(dump) {
			crcSet[off] = true
		}
		var keys []string
		for _, f := range e.Fields() {
			keys = append(keys, f.Key)
		}
		for _, key := range keys {
			// 每轮都重新取当前字段(前面的字段可能已经改动了相邻状态)
			cur, f := "", Field{}
			for _, cf := range e.Fields() {
				if cf.Key == key {
					cur, f = cf.Value, cf
					break
				}
			}
			if cur == "" {
				skipped++
				continue
			}
			f.Value = cur
			newVal := perturbValue(f)
			if newVal == "" || newVal == f.Value {
				skipped++
				continue
			}
			before := append([]byte{}, e.Bytes()...)
			if err := e.SetField(f.Key, newVal); err != nil {
				skipped++ // 值不可表示(如超出编码范围)属正常
				continue
			}
			var changed []int
			for i, b := range e.Bytes() {
				if b != before[i] {
					changed = append(changed, i)
				}
			}
			if len(changed) == 0 {
				t.Errorf("%s: 字段 %s 改成 %q 后没有任何字节变化", name, f.Key, newVal)
			}
			for _, off := range changed {
				if crcSet[off] {
					t.Errorf("%s: 字段 %s 写到了 CRC 字节 %#x", name, f.Key, off)
				}
			}
			if max := fieldMaxWidth(f); len(changed) > max {
				t.Errorf("%s: 字段 %s 影响了 %d 字节(上限 %d): %v", name, f.Key, len(changed), max, changed)
			}
			// 复原
			if err := e.SetField(f.Key, f.Value); err != nil {
				t.Errorf("%s: 恢复 %s=%q 失败: %v", name, f.Key, f.Value, err)
				continue
			}
			if strings.HasSuffix(f.Key, ".present") {
				// 关闭扩展区只清启用位、保留内容(有意的非破坏语义), 因此按字段值判定
				if got := editorFieldValues(e)[f.Key]; got != f.Value {
					t.Errorf("%s: 字段 %s 复原后 = %q, 期望 %q", name, f.Key, got, f.Value)
				}
				continue
			}
			if f.Kind == "float" {
				// 时序: 同一时间可能有等价但字节不同的编码(floor+正 fine vs ceil+负 fine),
				// 因此按"时间等价"判定(容差 = 显示精度)
				want, _ := strconv.ParseFloat(f.Value, 64)
				if got := fieldNS(t, e, f.Key); math.Abs(got-want) > 6e-4 {
					t.Errorf("%s: 字段 %s 复原后 = %.4f, 期望 %.4f", name, f.Key, got, want)
				}
				continue
			}
			for i, b := range e.Bytes() {
				if b != before[i] {
					t.Fatalf("%s: 字段 %s 改回原值后 @%#x 未复原(%02X→%02X)", name, f.Key, i, before[i], b)
				}
			}
			checked++
		}
	}
	t.Logf("字段写入边界检查: %d 次改写通过, %d 次跳过(值不可表示/无差异)", checked, skipped)
	if checked < 500 {
		t.Fatalf("只检查了 %d 次改写, 覆盖面不足", checked)
	}
}
