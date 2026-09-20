package spd

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 扩展信息(XMP 2.0 / XMP 3.0 / EXPO)编辑。
//
// 布局来源: Intel XMP 2.0(DD4 384 起, 2×63B 槽)、JEDEC SPD5118 XMP 3.0
// (0x280 header + 0x2C0..0x3C0 五槽)、AMD EXPO(0x340 共 128B, 与 XMP 槽 3/User1 互斥)。
// 与开源参考实现 ec-/DDR5XMPEditor 的偏移一致。

const (
	xmp2Base     = 0x180 // DDR4 XMP 2.0 header
	xmp2ProfLen  = 63
	xmp2Magic1   = 0x0C
	xmp2Magic2   = 0x4A
	xmp3Version  = 0x30
	xmp3HdrLen   = 64
	expoHdrLen   = 0x0A
	expoProfLen  = 0x28
	expoProf1Off = expoOffset + expoHdrLen
	expoProf2Off = expoProf1Off + expoProfLen
)

// pfKind 是 profile 字段的数据类型。
type pfKind string

const (
	pfPS16   pfKind = "ps16"    // 16bit 小端, 单位 ps(界面 ns)
	pfNS16   pfKind = "ns16"    // 16bit 小端, 单位 ns
	pfVolt5  pfKind = "volt5"   // DDR5 电压: (ones<<5)|(hundredths/5)
	pfMedFin pfKind = "medfine" // medium 字节 + fine 字节(字节相对偏移由 Fine 给出)
	pfNib12  pfKind = "nib12"   // 12 位: 低字节 + 另一字节的 nibble
	pfCL3    pfKind = "cl3"     // XMP2: 3 字节 CL 掩码
	pfCL5    pfKind = "cl5"     // XMP3: 5 字节 CL 掩码
	pfU8     pfKind = "u8"
	pfVolt2  pfKind = "volt2" // XMP2 电压: (bit7) + 低 7 位/100
	pfStr16  pfKind = "str16"
)

type pfSpec struct {
	Suffix string
	Name   string
	Kind   pfKind
	Off    int // 相对 profile 基址
	Aux    int // medfine 的 fine 偏移 / nib12 的高位字节偏移
	Pos    int // nib12 的 nibble 位置(3 或 7)
	Risk   string
	Note   string
	MinVal float64
	MaxVal float64
}

var xmp2ProfileSpecs = []pfSpec{
	{"voltage", "电压(V)", pfVolt2, 0x09, 0, 0, "medium", "", 0, 0},
	{"tCK", "tCKAVGmin", pfMedFin, 0x0C, 0x2F, 0, "medium", "", 0, 0},
	{"cl", "CL 支持(逗号分隔)", pfCL3, 0x0D, 0, 0, "medium", "", 0, 0},
	{"tAA", "tAA", pfMedFin, 0x11, 0x2E, 0, "medium", "", 0, 0},
	{"tRCD", "tRCD", pfMedFin, 0x12, 0x2D, 0, "medium", "", 0, 0},
	{"tRP", "tRP", pfMedFin, 0x13, 0x2C, 0, "medium", "", 0, 0},
	{"tRAS", "tRAS", pfNib12, 0x15, 0x14, 7, "medium", "", 0, 0},
	{"tRC", "tRC", pfMedFin, 0x16, 0x2B, 0, "medium", "", 0, 0},
	{"tRFC1", "tRFC1", pfPS16, 0x17, 0, 0, "medium", "", 0, 0},
	{"tRFC2", "tRFC2", pfPS16, 0x19, 0, 0, "medium", "", 0, 0},
	{"tRFC4", "tRFC4", pfPS16, 0x1B, 0, 0, "medium", "", 0, 0},
	{"tFAW", "tFAW", pfNib12, 0x1E, 0x1D, 3, "medium", "", 0, 0},
	{"tRRD_S", "tRRD_S", pfMedFin, 0x1F, 0x2A, 0, "medium", "", 0, 0},
	{"tRRD_L", "tRRD_L", pfMedFin, 0x20, 0x29, 0, "medium", "", 0, 0},
}

var xmp3ProfileSpecs = []pfSpec{
	{"vpp", "VPP", pfVolt5, 0x00, 0, 0, "low", "", 0, 0},
	{"vdd", "VDD", pfVolt5, 0x01, 0, 0, "low", "", 0, 0},
	{"vddq", "VDDQ", pfVolt5, 0x02, 0, 0, "low", "", 0, 0},
	{"vmemctrl", "VMEMCTRL", pfVolt5, 0x04, 0, 0, "low", "", 0, 0},
	{"tCK", "tCKAVGmin", pfPS16, 0x05, 0, 0, "medium", "", 0, 0},
	{"cl", "CL 支持(逗号分隔偶数)", pfCL5, 0x07, 0, 0, "medium", "", 0, 0},
	{"tAA", "tAA", pfPS16, 0x0D, 0, 0, "medium", "", 0, 0},
	{"tRCD", "tRCD", pfPS16, 0x0F, 0, 0, "medium", "", 0, 0},
	{"tRP", "tRP", pfPS16, 0x11, 0, 0, "medium", "", 0, 0},
	{"tRAS", "tRAS", pfPS16, 0x13, 0, 0, "medium", "", 0, 0},
	{"tRC", "tRC", pfPS16, 0x15, 0, 0, "medium", "", 0, 0},
	{"tWR", "tWR", pfPS16, 0x17, 0, 0, "medium", "", 0, 0},
	{"tRFC1", "tRFC1(ns)", pfNS16, 0x19, 0, 0, "medium", "", 0, 0},
	{"tRFC2", "tRFC2(ns)", pfNS16, 0x1B, 0, 0, "medium", "", 0, 0},
	{"tRFC", "tRFC(ns)", pfNS16, 0x1D, 0, 0, "medium", "", 0, 0},
	{"tRRD_L", "tRRD_L", pfPS16, 0x1F, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WR", "tCCD_L_WR", pfPS16, 0x22, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WR2", "tCCD_L_WR2", pfPS16, 0x25, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WTR", "tCCD_L_WTR", pfPS16, 0x28, 0, 0, "medium", "", 0, 0},
	{"tCCD_S_WTR", "tCCD_S_WTR", pfPS16, 0x2B, 0, 0, "medium", "", 0, 0},
	{"tCCD_L", "tCCD_L", pfPS16, 0x2E, 0, 0, "medium", "", 0, 0},
	{"tRTP", "tRTP", pfPS16, 0x31, 0, 0, "medium", "", 0, 0},
	{"tFAW", "tFAW", pfPS16, 0x34, 0, 0, "medium", "", 0, 0},
	{"commandRate", "Command Rate(0=未定义,1=1N,2=2N,3=3N)", pfU8, 0x3C, 0, 0, "medium", "", 0, 3},
}

var expoProfileSpecs = []pfSpec{
	{"vdd", "VDD", pfVolt5, 0x00, 0, 0, "low", "", 0, 0},
	{"vddq", "VDDQ", pfVolt5, 0x01, 0, 0, "low", "", 0, 0},
	{"vpp", "VPP", pfVolt5, 0x02, 0, 0, "low", "", 0, 0},
	{"tCK", "tCKAVGmin", pfPS16, 0x04, 0, 0, "medium", "", 0, 0},
	{"tAA", "tAA", pfPS16, 0x06, 0, 0, "medium", "", 0, 0},
	{"tRCD", "tRCD", pfPS16, 0x08, 0, 0, "medium", "", 0, 0},
	{"tRP", "tRP", pfPS16, 0x0A, 0, 0, "medium", "", 0, 0},
	{"tRAS", "tRAS", pfPS16, 0x0C, 0, 0, "medium", "", 0, 0},
	{"tRC", "tRC", pfPS16, 0x0E, 0, 0, "medium", "", 0, 0},
	{"tWR", "tWR", pfPS16, 0x10, 0, 0, "medium", "", 0, 0},
	{"tRFC1", "tRFC1(ns)", pfNS16, 0x12, 0, 0, "medium", "", 0, 0},
	{"tRFC2", "tRFC2(ns)", pfNS16, 0x14, 0, 0, "medium", "", 0, 0},
	{"tRFC", "tRFC(ns)", pfNS16, 0x16, 0, 0, "medium", "", 0, 0},
	{"tRRD_L", "tRRD_L", pfPS16, 0x18, 0, 0, "medium", "", 0, 0},
	{"tCCD_L", "tCCD_L", pfPS16, 0x1A, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WR", "tCCD_L_WR", pfPS16, 0x1C, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WR2", "tCCD_L_WR2", pfPS16, 0x1E, 0, 0, "medium", "", 0, 0},
	{"tFAW", "tFAW", pfPS16, 0x20, 0, 0, "medium", "", 0, 0},
	{"tCCD_L_WTR", "tCCD_L_WTR", pfPS16, 0x22, 0, 0, "medium", "", 0, 0},
	{"tCCD_S_WTR", "tCCD_S_WTR", pfPS16, 0x24, 0, 0, "medium", "", 0, 0},
	{"tRTP", "tRTP", pfPS16, 0x26, 0, 0, "medium", "", 0, 0},
}

// expoPresent 报告 EXPO 头("EXPO")是否存在。
func (e *Editor) expoPresent() bool {
	if len(e.dump) < expoOffset+4 {
		return false
	}
	return string(e.dump[expoOffset:expoOffset+4]) == "EXPO"
}

// profileFields 返回扩展信息(XMP/EXPO)字段。
func (e *Editor) profileFields() []Field {
	switch e.rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		return e.xmp2Fields()
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		return append(e.xmp3Fields(), e.expoFields()...)
	default:
		return nil
	}
}

func (e *Editor) xmp2Fields() []Field {
	base := xmp2Base
	present := e.dump[base] == xmp2Magic1 && e.dump[base+1] == xmp2Magic2
	out := []Field{
		{Key: "xmp.present", Name: "XMP 2.0 是否存在", Group: "XMP 2.0", Kind: "bool",
			Value: boolStr(present), Offset: "0x180", Risk: "medium",
			Note: "写入 0x0C 0x4A 头即创建 XMP; 关闭只清启用位, 不清内容"},
		{Key: "xmp.version", Name: "XMP 版本(hex)", Group: "XMP 2.0", Kind: "hex",
			Value: fmt.Sprintf("%02X", e.dump[base+3]), Offset: "0x183", Risk: "medium"},
	}
	for n := 0; n < 2; n++ {
		pb := base + n*xmp2ProfLen
		pre := fmt.Sprintf("xmp.p%d", n+1)
		out = append(out, Field{
			Key: pre + ".enabled", Name: fmt.Sprintf("Profile %d 启用", n+1), Group: "XMP 2.0",
			Kind: "bool", Value: boolStr(e.dump[base+2]&(1<<n) != 0), Offset: "0x182", Risk: "medium",
		})
		for _, sp := range xmp2ProfileSpecs {
			out = append(out, e.fieldFromSpec(pre+"."+sp.Suffix, sp, pb, fmt.Sprintf("XMP 2.0 P%d", n+1), "XMP 2.0"))
		}
	}
	return out
}

func (e *Editor) xmp3Fields() []Field {
	out := []Field{}
	hdrPresent := e.dump[xmp30Offset] == xmp2Magic1 && e.dump[xmp30Offset+1] == xmp2Magic2
	out = append(out,
		Field{Key: "xmp3.present", Name: "XMP 3.0 是否存在", Group: "XMP 3.0", Kind: "bool",
			Value: boolStr(hdrPresent), Offset: "0x280", Risk: "medium",
			Note: "头 0x0C 0x4A + 版本 0x30; CRC 由编辑器重算"},
		Field{Key: "xmp3.version", Name: "版本(hex)", Group: "XMP 3.0", Kind: "hex",
			Value: fmt.Sprintf("%02X", e.dump[xmp30Offset+2]), Offset: "0x282", Risk: "medium"},
	)
	enabled := e.dump[xmp30Offset+3]
	for i := 0; i < 3; i++ {
		out = append(out, Field{
			Key: fmt.Sprintf("xmp3.enabled%d", i+1), Name: fmt.Sprintf("Profile %d 启用", i+1),
			Group: "XMP 3.0", Kind: "bool", Value: boolStr(enabled&(1<<i) != 0),
			Offset: "0x283", Risk: "medium",
		})
	}
	for i := 0; i < 3; i++ {
		nameOff := xmp30Offset + 0x0E + i*16
		out = append(out, Field{
			Key: fmt.Sprintf("xmp3.name%d", i+1), Name: fmt.Sprintf("Profile %d 名称", i+1),
			Group: "XMP 3.0", Kind: "string", Value: strings.TrimRight(string(e.dump[nameOff:nameOff+16]), "\x00 "),
			Offset: fmt.Sprintf("0x%03X-16B", nameOff), Risk: "low",
		})
	}
	expo := e.expoPresent()
	for i, off := range XMP30ProfileOffsets {
		if expo && (i == 2 || i == 3) {
			continue // EXPO 占用了槽 3 与 User1
		}
		pre := fmt.Sprintf("xmp3.p%d", i+1)
		for _, sp := range xmp3ProfileSpecs {
			out = append(out, e.fieldFromSpec(pre+"."+sp.Suffix, sp, off, fmt.Sprintf("XMP 3.0 P%d", i+1), "XMP 3.0"))
		}
	}
	// 用户槽 4/5 只在未启用 EXPO 时暴露(与 EXPO 重叠)
	return out
}

func (e *Editor) expoFields() []Field {
	hdrPresent := e.expoPresent()
	out := []Field{
		{Key: "expo.present", Name: "EXPO 是否存在", Group: "EXPO", Kind: "bool",
			Value: boolStr(hdrPresent), Offset: "0x340", Risk: "medium",
			Note: "EXPO 占 0x340-0x3BF, 与 XMP 槽 3 / User1 互斥"},
		{Key: "expo.revision", Name: "EXPO 版本(hex)", Group: "EXPO", Kind: "hex",
			Value: fmt.Sprintf("%02X", e.dump[expoOffset+4]), Offset: "0x344", Risk: "medium",
			Note: "公开资料有限, 常见 0x10"},
		{Key: "expo.enabledMask", Name: "启用位掩码", Group: "EXPO", Kind: "int",
			Value: strconv.Itoa(int(e.dump[expoOffset+5])), Min: 0, Max: 255,
			Offset: "0x345", Risk: "medium"},
	}
	for n := 0; n < 2; n++ {
		pb := expoProf1Off + n*expoProfLen
		pre := fmt.Sprintf("expo.p%d", n+1)
		out = append(out, Field{
			Key: pre + ".enabled", Name: fmt.Sprintf("Profile %d 启用", n+1), Group: "EXPO",
			Kind: "bool", Value: boolStr(e.dump[expoOffset+5]&(1<<n) != 0), Offset: "0x345", Risk: "medium",
		})
		for _, sp := range expoProfileSpecs {
			out = append(out, e.fieldFromSpec(pre+"."+sp.Suffix, sp, pb, fmt.Sprintf("EXPO P%d", n+1), "EXPO"))
		}
	}
	return out
}

// fieldFromSpec 依据 kind 读取当前值并生成 Field。
func (e *Editor) fieldFromSpec(key string, sp pfSpec, base int, prefix, group string) Field {
	f := Field{
		Key: key, Name: prefix + " " + sp.Name, Group: group, Risk: sp.Risk, Note: sp.Note,
		Offset: fmt.Sprintf("0x%03X", base+sp.Off), Min: sp.MinVal, Max: sp.MaxVal,
	}
	switch sp.Kind {
	case pfPS16, pfNS16:
		v := int(e.dump[base+sp.Off]) | int(e.dump[base+sp.Off+1])<<8
		f.Kind, f.Unit, f.Step = "float", "ns", 0.001
		ns := float64(v) / 1000 // ps
		if sp.Kind == pfNS16 {
			ns = float64(v) // 已是 ns(如 XMP3/EXPO 的 tRFC*)
		}
		f.Value = timingValue(ns, v > 0)
	case pfVolt5:
		f.Kind, f.Unit, f.Step = "float", "V", 0.005
		if b := e.dump[base+sp.Off]; b != 0 {
			f.Value = strconv.FormatFloat(volt5ToV(b), 'f', 3, 64)
		}
	case pfMedFin:
		tb := DDR4Timebase(e.dump)
		ns := timingNS(int(e.dump[base+sp.Off]), int(int8(e.dump[base+sp.Aux])), tb)
		f.Kind, f.Unit, f.Step = "float", "ns", 0.001
		f.Value = timingValue(ns, ns > 0)
	case pfNib12:
		tb := DDR4Timebase(e.dump)
		med := int(e.dump[base+sp.Off]) | int(subByteR(e.dump[base+sp.Aux], sp.Pos, 4))<<8
		ns := timingNS(med, 0, tb)
		f.Kind, f.Unit, f.Step = "float", "ns", 0.001
		f.Value = timingValue(ns, ns > 0)
	case pfVolt2:
		v := e.dump[base+sp.Off]
		f.Kind, f.Unit, f.Step = "float", "V", 0.01
		if v != 0 {
			volts := float64(v>>7) + float64(v&0x7F)/100
			f.Value = strconv.FormatFloat(volts, 'f', 2, 64)
		}
	case pfCL3, pfCL5:
		f.Kind = "string"
		f.Value = clMaskString(e.dump, base+sp.Off, map[pfKind]int{pfCL3: 3, pfCL5: 5}[sp.Kind], sp.Kind == pfCL5)
	case pfU8:
		f.Kind = "int"
		f.Value = strconv.Itoa(int(e.dump[base+sp.Off]))
	case pfStr16:
		f.Kind = "string"
		f.Value = strings.TrimRight(string(e.dump[base+sp.Off:base+sp.Off+16]), "\x00 ")
	}
	return f
}

// setProfileField 处理 XMP/EXPO 字段写入。
func (e *Editor) setProfileField(key, value string) error {
	switch {
	case key == "xmp.present":
		if !parseBool(value) {
			// 只清启用位(保留内容, 便于再次启用)
			if e.dump[xmp2Base] == xmp2Magic1 && e.dump[xmp2Base+1] == xmp2Magic2 {
				return e.set(xmp2Base+2, 0, "XMP 启用位", "medium")
			}
			return nil
		}
		if err := e.set(xmp2Base, xmp2Magic1, "XMP 头", "medium"); err != nil {
			return err
		}
		if err := e.set(xmp2Base+1, xmp2Magic2, "XMP 头", "medium"); err != nil {
			return err
		}
		return e.set(xmp2Base+3, 0x12, "XMP 版本", "medium")
	case key == "xmp.version":
		return e.setHexBytes(xmp2Base+3, 1, value, "XMP 版本", "medium")
	case key == "xmp3.present":
		if !parseBool(value) {
			return e.set(xmp30Offset+3, 0, "XMP3 启用位", "medium")
		}
		if err := e.set(xmp30Offset, xmp2Magic1, "XMP3 头", "medium"); err != nil {
			return err
		}
		if err := e.set(xmp30Offset+1, xmp2Magic2, "XMP3 头", "medium"); err != nil {
			return err
		}
		if err := e.set(xmp30Offset+2, xmp3Version, "XMP3 版本", "medium"); err != nil {
			return err
		}
		return nil
	case key == "xmp3.version":
		return e.setHexBytes(xmp30Offset+2, 1, value, "XMP3 版本", "medium")
	case key == "expo.present":
		if !parseBool(value) {
			return e.set(expoOffset+5, 0, "EXPO 启用位", "medium")
		}
		for i, ch := range []byte("EXPO") {
			if err := e.set(expoOffset+i, ch, "EXPO 头", "medium"); err != nil {
				return err
			}
		}
		if err := e.set(expoOffset+4, 0x10, "EXPO 版本", "medium"); err != nil {
			return err
		}
		return nil
	case key == "expo.revision":
		return e.setHexBytes(expoOffset+4, 1, value, "EXPO 版本", "medium")
	case key == "expo.enabledMask":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 || v > 255 {
			return fmt.Errorf("掩码必须是 0-255 的整数")
		}
		return e.set(expoOffset+5, byte(v), "EXPO 启用位", "medium")
	}

	// 前缀派发
	type target struct {
		specs []pfSpec
		base  int
		group string
	}
	var (
		t      target
		suffix string
		bit    = -1 // 启用位所在的 header 位
		hdrOff = -1
	)
	switch {
	case strings.HasPrefix(key, "xmp.p"):
		n, err := profileIndex(key, "xmp.p", 2)
		if err != nil {
			return err
		}
		t = target{xmp2ProfileSpecs, xmp2Base + n*xmp2ProfLen, "XMP 2.0"}
		suffix = key[strings.LastIndex(key, ".")+1:]
		hdrOff, bit = xmp2Base+2, n
	case strings.HasPrefix(key, "xmp3.enabled"):
		n, err := profileIndex(key, "xmp3.enabled", 3)
		if err != nil {
			return err
		}
		b := parseBool(value)
		cur := e.dump[xmp30Offset+3]
		if b {
			cur |= 1 << n
		} else {
			cur &^= 1 << n
		}
		return e.set(xmp30Offset+3, cur, "XMP3 启用位", "medium")
	case strings.HasPrefix(key, "xmp3.name"):
		n, err := profileIndex(key, "xmp3.name", 3)
		if err != nil {
			return err
		}
		return e.setASCII(xmp30Offset+0x0E+n*16, 16, value, "XMP3 Profile 名称", "low")
	case strings.HasPrefix(key, "xmp3.p"):
		n, err := profileIndex(key, "xmp3.p", 5)
		if err != nil {
			return err
		}
		if e.expoPresent() && (n == 2 || n == 3) {
			return fmt.Errorf("该槽位被 EXPO 占用(EXPO 与 XMP 槽 3/User1 互斥); 请先清除 EXPO")
		}
		t = target{xmp3ProfileSpecs, XMP30ProfileOffsets[n], "XMP 3.0"}
		suffix = key[strings.LastIndex(key, ".")+1:]
	case strings.HasPrefix(key, "expo.p"):
		n, err := profileIndex(key, "expo.p", 2)
		if err != nil {
			return err
		}
		t = target{expoProfileSpecs, expoProf1Off + n*expoProfLen, "EXPO"}
		suffix = key[strings.LastIndex(key, ".")+1:]
		hdrOff, bit = expoOffset+5, n
	default:
		return fmt.Errorf("未知字段 %q", key)
	}

	if suffix == "enabled" {
		if hdrOff < 0 || bit < 0 {
			return fmt.Errorf("%q 不支持启用位编辑", key)
		}
		cur := e.dump[hdrOff]
		if parseBool(value) {
			cur |= 1 << bit
		} else {
			cur &^= 1 << bit
		}
		return e.set(hdrOff, cur, t.group+" 启用位", "medium")
	}
	for _, sp := range t.specs {
		if sp.Suffix != suffix {
			continue
		}
		return e.applySpec(t.base, sp, value, t.group)
	}
	return fmt.Errorf("未知字段 %q", key)
}

// applySpec 按 kind 写入一个 profile 字段。
func (e *Editor) applySpec(base int, sp pfSpec, value, group string) error {
	// 数值型时序: 时间没变就不动字节(等价编码可能不同)
	switch sp.Kind {
	case pfPS16, pfNS16, pfMedFin, pfNib12:
		if ns, err := strconv.ParseFloat(value, 64); err == nil {
			if cur, ok := e.specValue(base, sp); ok && math.Abs(cur-ns) < 1e-9 {
				return nil
			}
		}
	}
	switch sp.Kind {
	case pfPS16, pfNS16:
		ns, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s 必须是数值(ns): %q", sp.Name, value)
		}
		v := int(math.Round(ns * 1000))
		if sp.Kind == pfNS16 {
			v = int(math.Round(ns))
		}
		if v < 0 || v > 0xFFFF {
			return fmt.Errorf("%s(%.3f) 超出 16 位范围", sp.Name, ns)
		}
		if err := e.set(base+sp.Off, byte(v&0xFF), group+" "+sp.Suffix, sp.Risk); err != nil {
			return err
		}
		return e.set(base+sp.Off+1, byte(v>>8), group+" "+sp.Suffix, sp.Risk)
	case pfVolt5:
		if strings.TrimSpace(value) == "0" {
			return e.set(base+sp.Off, 0, group+" "+sp.Suffix, sp.Risk)
		}
		v, err := parseVoltage(value, 5)
		if err != nil {
			return fmt.Errorf("%s: %w", sp.Name, err)
		}
		return e.set(base+sp.Off, voltTo5(v), group+" "+sp.Suffix, sp.Risk)
	case pfVolt2:
		v, err := parseVoltage(value, 10)
		if err != nil {
			return fmt.Errorf("%s: %w", sp.Name, err)
		}
		if v < 1.0 || v > 2.27 {
			return fmt.Errorf("电压必须在 1.00-2.27V(XMP 2.0 编码)")
		}
		ones := 1
		hundredths := int(math.Round((v - 1.0) * 100))
		if hundredths < 0 || hundredths > 99 {
			return fmt.Errorf("电压超出范围")
		}
		return e.set(base+sp.Off, byte(ones<<7|hundredths), group+" 电压", sp.Risk)
	case pfMedFin:
		ns, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s 必须是数值(ns): %q", sp.Name, value)
		}
		tb := DDR4Timebase(e.dump)
		m, f, err := encodeTiming(ns, tb)
		if err != nil {
			return err
		}
		if err := e.set(base+sp.Off, byte(m), group+" "+sp.Suffix, sp.Risk); err != nil {
			return err
		}
		return e.set(base+sp.Aux, byte(int8(f)), group+" "+sp.Suffix, sp.Risk)
	case pfNib12:
		ns, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s 必须是数值(ns): %q", sp.Name, value)
		}
		tb := DDR4Timebase(e.dump)
		m, _, err := encodeTimingMax(ns, tb, 0xFFF)
		if err != nil {
			return fmt.Errorf("%s: %w", sp.Name, err)
		}
		if err := e.set(base+sp.Off, byte(m&0xFF), group+" "+sp.Suffix, sp.Risk); err != nil {
			return err
		}
		return e.set(base+sp.Aux, setSubByteR(e.dump[base+sp.Aux], sp.Pos, 4, byte(m>>8)), group+" "+sp.Suffix, sp.Risk)
	case pfCL3, pfCL5:
		n := 3
		if sp.Kind == pfCL5 {
			n = 5
		}
		return e.setCLMask(base+sp.Off, n, value, sp.Kind == pfCL5, group)
	case pfU8:
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 || v > 255 {
			return fmt.Errorf("%s 必须是 0-255 的整数", sp.Name)
		}
		return e.set(base+sp.Off, byte(v), group+" "+sp.Suffix, sp.Risk)
	}
	return fmt.Errorf("字段 %s 类型 %s 暂不支持写入", sp.Name, sp.Kind)
}

// ---------------- 小工具 ----------------

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "启用", "是":
		return true
	}
	return false
}

// profileIndex 从形如 "xmp.p2.tCK" 的 key 里取出 0 基槽位号。
func profileIndex(key, prefix string, max int) (int, error) {
	rest := strings.TrimPrefix(key, prefix)
	dot := strings.Index(rest, ".")
	numStr := rest
	if dot >= 0 {
		numStr = rest[:dot]
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 1 || n > max {
		return 0, fmt.Errorf("%q 的槽位号无效(1-%d)", key, max)
	}
	return n - 1, nil
}

// volt5ToV 解码 DDR5 电压字节。
func volt5ToV(b byte) float64 {
	ones := int(b >> 5)
	hundredths := int(b&0x1F) * 5
	return float64(ones) + float64(hundredths)/100
}

func voltTo5(v float64) byte {
	ones := int(v)
	hundredths := int(math.Round((v - float64(ones)) * 100))
	return byte(ones<<5 | (hundredths / 5))
}

func parseVoltage(s string, decimals int) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "V"), 64)
	if err != nil {
		return 0, fmt.Errorf("电压必须是数值(V): %q", s)
	}
	_ = decimals
	if v <= 0 || v > 5 {
		return 0, fmt.Errorf("电压必须在 0-5V 之间: %.3f", v)
	}
	return v, nil
}

// clMaskString 把 CL 掩码展开成 "20,22,24" 形式。
// ddr5Mask=true 时按 JEDEC DDR5/XMP3 规则(20..98 偶数, 5 字节)。
func clMaskString(dump []byte, off, n int, ddr5Mask bool) string {
	var cls []int
	if ddr5Mask {
		for bi := 0; bi < n; bi++ {
			v := dump[off+bi]
			for bit := 0; bit < 8; bit++ {
				if v&(1<<bit) != 0 {
					cls = append(cls, 20+2*(bi*8+bit))
				}
			}
		}
	} else {
		mask := uint32(dump[off]) | uint32(dump[off+1])<<8 | uint32(dump[off+2])<<16
		high := mask&0x80000000 != 0
		for i := 0; i < 29; i++ {
			if mask&(1<<i) != 0 {
				base := i + 7
				if high {
					base += 16
				}
				cls = append(cls, base)
			}
		}
	}
	var sb strings.Builder
	for i, c := range cls {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(c))
	}
	return sb.String()
}

// setCLMask 解析 "20,22,24" 并写入 CL 掩码。
func (e *Editor) setCLMask(off, n int, value string, ddr5Mask bool, group string) error {
	value = strings.TrimSpace(value)
	for i := 0; i < n; i++ {
		if err := e.set(off+i, 0, group+" CL 掩码", "medium"); err != nil {
			return err
		}
	}
	if value == "" {
		return nil
	}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cl, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("CL 值必须是整数: %q", part)
		}
		if ddr5Mask {
			if cl < 20 || cl > 98 || cl%2 != 0 {
				return fmt.Errorf("CL %d 非法(DDR5/XMP3 只支持 20-98 的偶数)", cl)
			}
			bit := (cl - 20) / 2
			bi, b := bit/8, bit%8
			if err := e.set(off+bi, e.dump[off+bi]|(1<<b), group+" CL 掩码", "medium"); err != nil {
				return err
			}
			continue
		}
		// XMP 2.0(3 字节掩码): bit i → CL i+7, 支持 7..35
		if cl < 7 || cl > 35 {
			return fmt.Errorf("CL %d 非法(XMP 2.0 支持 7-35)", cl)
		}
		idx := cl - 7
		bi, b := idx/8, idx%8
		if err := e.set(off+bi, e.dump[off+bi]|(1<<b), group+" CL 掩码", "medium"); err != nil {
			return err
		}
	}
	return nil
}

// specValue 读取 profile 字段当前的数值(仅数值型 kind)。
func (e *Editor) specValue(base int, sp pfSpec) (float64, bool) {
	switch sp.Kind {
	case pfPS16:
		v := int(e.dump[base+sp.Off]) | int(e.dump[base+sp.Off+1])<<8
		return float64(v) / 1000, v > 0
	case pfNS16:
		v := int(e.dump[base+sp.Off]) | int(e.dump[base+sp.Off+1])<<8
		return float64(v), v > 0
	case pfMedFin:
		tb := DDR4Timebase(e.dump)
		v := timingNS(int(e.dump[base+sp.Off]), int(int8(e.dump[base+sp.Aux])), tb)
		return v, v > 0
	case pfNib12:
		tb := DDR4Timebase(e.dump)
		med := int(e.dump[base+sp.Off]) | int(subByteR(e.dump[base+sp.Aux], sp.Pos, 4))<<8
		v := timingNS(med, 0, tb)
		return v, med > 0
	}
	return 0, false
}
