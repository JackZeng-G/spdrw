package spd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// 字段模型: UI 通过 Fields() 拿到全部可编辑字段(当前值/范围/风险), 通过
// SetField(key, value) 修改。这样绑定层只需要两个方法, 新增字段不必改 Wails 接口。

// idLayout 描述一个世代的身份区偏移。
type idLayout struct {
	MfgCont, MfgCode     int
	Location             int
	DateYear, DateWeek   int
	SerialOff, SerialLen int
	PNOff, PNLen         int
	RevisionOff          int // -1 = 无
	RevisionLen          int // 1(DDR4/DDR5) 或 2(DDR3)
	DramCont, DramCode   int // -1 = 无
	DramStepping         int // -1 = 无
	DateBCD              bool
}

func idLayoutFor(rt RAMType) (idLayout, error) {
	switch rt {
	case DDR4, DDR4E:
		return idLayout{320, 321, 322, 323, 324, 325, 4, 329, 20, 349, 1, 350, 351, 352, true}, nil
	case LPDDR3, LPDDR4, LPDDR4X:
		return idLayout{320, 321, 322, 323, 324, 325, 4, 329, 20, 349, 1, -1, -1, -1, true}, nil
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		return idLayout{512, 513, 514, 515, 516, 517, 4, 521, 30, 551, 1, 552, 553, 554, true}, nil
	case DDR3:
		return idLayout{117, 118, 119, 120, 121, 122, 4, 128, 18, 146, 2, 148, 149, -1, true}, nil
	default:
		return idLayout{}, fmt.Errorf("%v 暂不支持编辑", rt)
	}
}

// Identity 是身份区的当前值。
type Identity struct {
	Manufacturer     string `json:"manufacturer"`
	ManufacturerCont byte   `json:"manufacturerCont"`
	ManufacturerCode byte   `json:"manufacturerCode"`
	Location         byte   `json:"location"`
	DateYear         int    `json:"dateYear"`
	DateWeek         int    `json:"dateWeek"`
	SerialHex        string `json:"serialHex"`
	PartNumber       string `json:"partNumber"`
	Revision         uint16 `json:"revision"`
	DRAMManufacturer string `json:"dramManufacturer,omitempty"`
	DRAMCont         byte   `json:"dramCont,omitempty"`
	DRAMCode         byte   `json:"dramCode,omitempty"`
	DRAMStepping     byte   `json:"dramStepping,omitempty"`
}

// Identity 读取当前身份区。
func (e *Editor) Identity() (Identity, error) {
	l, err := idLayoutFor(e.rt)
	if err != nil {
		return Identity{}, err
	}
	cont, code := e.dump[l.MfgCont], e.dump[l.MfgCode]
	id := Identity{
		Manufacturer:     ManufacturerName(cont, code),
		ManufacturerCont: cont,
		ManufacturerCode: code,
		Location:         e.dump[l.Location],
		SerialHex:        hexOf(e.dump[l.SerialOff : l.SerialOff+l.SerialLen]),
		PartNumber:       asciiString(e.dump[l.PNOff : l.PNOff+l.PNLen]),
	}
	if l.DateBCD {
		id.DateYear, id.DateWeek = 2000+BCD(e.dump[l.DateYear]), BCD(e.dump[l.DateWeek])
	} else {
		id.DateYear, id.DateWeek = 2000+int(e.dump[l.DateYear]), int(e.dump[l.DateWeek])
	}
	if l.RevisionOff >= 0 && l.RevisionLen > 0 {
		// 修订码低字节在前(DDR3 的 byte146/147 与 DDR4/DDR5 同序)
		id.Revision = uint16(e.dump[l.RevisionOff])
		if l.RevisionLen > 1 {
			id.Revision |= uint16(e.dump[l.RevisionOff+1]) << 8
		}
	}
	if l.DramCont >= 0 {
		id.DRAMCont, id.DRAMCode = e.dump[l.DramCont], e.dump[l.DramCode]
		id.DRAMManufacturer = ManufacturerName(id.DRAMCont, id.DRAMCode)
	}
	if l.DramStepping >= 0 {
		id.DRAMStepping = e.dump[l.DramStepping]
	}
	return id, nil
}

// Fields 返回该 dump 的全部可编辑字段(常用信息 + JEDEC 时序 + 扩展信息)。
func (e *Editor) Fields() []Field {
	out := []Field{}
	out = append(out, e.identityFields()...)
	for _, s := range e.timingSpecs() {
		v, ok := s.Get(e)
		f := Field{
			Key: s.Key, Name: s.Name, Group: "JEDEC 时序", Kind: "float", Unit: "ns",
			Min: 0, Max: 100000, Step: 0.001,
			Value: timingValue(v, ok), Offset: s.Offset, Risk: "medium",
		}
		if ok && !skipClkHint(s.Key) {
			if base, bok := e.clkBaseNS(s.Key); bok {
				f.Hint = clkHint(v, base)
			}
		}
		out = append(out, f)
	}
	if f, ok := e.clMaskField(); ok {
		out = append(out, f)
	}
	out = append(out, e.profileFields()...)
	return markPrimary(out)
}

// primarySuffix 是"常用/关键"字段的 key 末段(如 ddr5.tAA → tAA、xmp3.p1.tCK → tCK)。
// 其余字段仍然可编辑, 只是默认收在"显示全部"后面 —— 一次铺满几十行没人看得下去。
var primarySuffix = map[string]bool{
	// 常用信息(无点号的 key 一律 primary, 见 markPrimary)
	"present": true, "version": true,
	// 时序
	"tCK": true, "tCKAVGmin": true, "tCKAVGmax": true, "tCKmin": true, "tCKmax": true,
	"tAA": true, "tRCD": true, "tRP": true, "tRAS": true, "tRC": true, "tWR": true,
	"tRFC": true, "tRFC1": true, "tRFC2": true, "tRFCsb": true, "tRFC4": true,
	"tRRD_L": true, "tCCD_L": true, "tFAW": true, "tRTP": true,
	// 电压与 CL 掩码
	"vdd": true, "vddq": true, "vpp": true, "cl": true,
}

// markPrimary 给字段打"常用"标记:
//   - 常用信息整组;
//   - 时序/电压/CL 等关键字段(见 primarySuffix);
//   - XMP/EXPO 的"是否存在/启用位/名称"以及**第 1 份 profile** 的关键字段
//     (第 2..5 份 profile 收在"显示全部"后面 —— 五份 profile 一次铺出来是上百行)。
func markPrimary(fields []Field) []Field {
	for i := range fields {
		f := &fields[i]
		if f.Group == "常用信息" {
			f.Primary = true
			continue
		}
		segs := strings.Split(f.Key, ".")
		base := segs[len(segs)-1]
		switch {
		case strings.HasPrefix(base, "enabled"), strings.HasPrefix(base, "name"),
			base == "present", base == "version":
			f.Primary = true
		case profileSegIndex(segs) > 1:
			// 第 2..5 份 profile: 默认收起
		case primarySuffix[base]:
			f.Primary = true
		}
	}
	return fields
}

// profileSegIndex 从 key 里取出 profile 序号(p1 → 1; 没有 profile 段 → 0)。
func profileSegIndex(segs []string) int {
	for _, s := range segs {
		if len(s) == 2 && s[0] == 'p' && s[1] >= '1' && s[1] <= '9' {
			return int(s[1] - '0')
		}
	}
	return 0
}

func (e *Editor) identityFields() []Field {
	l, err := idLayoutFor(e.rt)
	if err != nil {
		return nil
	}
	id, _ := e.Identity()
	out := []Field{
		{Key: "manufacturer", Name: "模块厂商", Group: "常用信息", Kind: "string",
			Value: id.Manufacturer, Offset: fmt.Sprintf("0x%03X-0x%03X", l.MfgCont, l.MfgCode),
			Risk: "low", Params: []string{"JEP106"},
			Note: "输入厂商名(可用搜索);留空 = 不改动, 想清掉 ID 请改下面两个原始码"},
		{Key: "mfgCode", Name: mfgCodeName(l), Group: "常用信息", Kind: "int",
			Value: mfgCodeValue(e, l), Min: 0, Max: 255,
			Offset: fmt.Sprintf("0x%03X", l.MfgCode), Risk: "low",
			Note: mfgCodeNote(l)},
		{Key: "mfgCont", Name: mfgContName(l), Group: "常用信息", Kind: "int",
			Value: mfgContValue(e, l), Min: 0, Max: 255,
			Offset: fmt.Sprintf("0x%03X", l.MfgCont), Risk: "low",
			Note: mfgContNote(l, id)},
		{Key: "location", Name: "生产地点", Group: "常用信息", Kind: "int",
			Value: strconv.Itoa(int(id.Location)), Min: 0, Max: 255,
			Offset: fmt.Sprintf("0x%03X", l.Location), Risk: "low"},
		{Key: "dateYear", Name: "生产年份", Group: "常用信息", Kind: "int",
			Value: strconv.Itoa(id.DateYear), Min: 2000, Max: 2099,
			Offset: fmt.Sprintf("0x%03X", l.DateYear), Risk: "low"},
		{Key: "dateWeek", Name: "生产周(0 = 未设置)", Group: "常用信息", Kind: "int",
			Value: strconv.Itoa(id.DateWeek), Min: 0, Max: 53,
			Offset: fmt.Sprintf("0x%03X", l.DateWeek), Risk: "low"},
		{Key: "serial", Name: "序列号(hex)", Group: "常用信息", Kind: "hex",
			Value: id.SerialHex, Offset: fmt.Sprintf("0x%03X-%dB", l.SerialOff, l.SerialLen), Risk: "low"},
		{Key: "partNumber", Name: "部件号", Group: "常用信息", Kind: "string",
			Value: id.PartNumber, Offset: fmt.Sprintf("0x%03X-%dB", l.PNOff, l.PNLen),
			Risk: "low", Params: []string{strconv.Itoa(l.PNLen)}},
	}
	if l.RevisionOff >= 0 && l.RevisionLen > 0 {
		out = append(out, Field{Key: "revision", Name: "模块修订码(hex, 按偏移顺序)", Group: "常用信息", Kind: "hex",
			Value:  hexOf(e.dump[l.RevisionOff : l.RevisionOff+l.RevisionLen]),
			Offset: fmt.Sprintf("0x%03X-%dB", l.RevisionOff, l.RevisionLen), Risk: "low"})
	}
	if l.DramCont >= 0 {
		out = append(out,
			Field{Key: "dramManufacturer", Name: "DRAM 厂商", Group: "常用信息", Kind: "string",
				Value: id.DRAMManufacturer, Offset: fmt.Sprintf("0x%03X-0x%03X", l.DramCont, l.DramCode), Risk: "low"},
			Field{Key: "dramMfgCode", Name: "DRAM 厂商码", Group: "常用信息", Kind: "int",
				Value: strconv.Itoa(int(id.DRAMCode)), Min: 0, Max: 255,
				Offset: fmt.Sprintf("0x%03X", l.DramCode), Risk: "low"})
	}
	if l.DramStepping >= 0 {
		out = append(out, Field{Key: "dramStepping", Name: "DRAM Stepping", Group: "常用信息", Kind: "int",
			Value: strconv.Itoa(int(id.DRAMStepping)), Min: 0, Max: 255,
			Offset: fmt.Sprintf("0x%03X", l.DramStepping), Risk: "low"})
	}
	return out
}

// SetField 按 key 修改字段。value 为表单字符串。
func (e *Editor) SetField(key, value string) error {
	l, err := idLayoutFor(e.rt)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	asInt := func(name string, min, max int) (int, error) {
		v, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s 必须是整数: %q", name, value)
		}
		if v < min || v > max {
			return 0, fmt.Errorf("%s 必须在 %d-%d 之间(当前 %d)", name, min, max, v)
		}
		return v, nil
	}
	switch key {
	case "manufacturer":
		if value == "" {
			// 空串 = 不改动(字段当前值可能本来就是空, 因为原条没写厂商 ID);
			// 想清掉写错的厂商 ID 就改 mfgCont/mfgCode 两个原始字段。
			return nil
		}
		cont, code, ok := FindManufacturer(value)
		if !ok {
			return fmt.Errorf("厂商表中找不到 %q(可用厂商码手动指定)", value)
		}
		if err := e.set(l.MfgCont, cont, "模块厂商", "low"); err != nil {
			return err
		}
		return e.set(l.MfgCode, code, "模块厂商", "low")
	case "dramManufacturer":
		if l.DramCont < 0 {
			return fmt.Errorf("%v 无 DRAM 厂商字段", e.rt)
		}
		if value == "" {
			return nil // 空串 = 不改动
		}
		cont, code, ok := FindManufacturer(value)
		if !ok {
			return fmt.Errorf("厂商表中找不到 %q", value)
		}
		if err := e.set(l.DramCont, cont, "DRAM 厂商", "low"); err != nil {
			return err
		}
		return e.set(l.DramCode, code, "DRAM 厂商", "low")
	case "mfgCont":
		v, err := asInt("续延码", 0, 255)
		if err != nil {
			return err
		}
		return e.set(l.MfgCont, byte(v), "模块厂商", "low")
	case "mfgCode":
		v, err := asInt("厂商码", 0, 255)
		if err != nil {
			return err
		}
		return e.set(l.MfgCode, byte(v), "模块厂商", "low")
	case "dramMfgCode":
		if l.DramCode < 0 {
			return fmt.Errorf("%v 无 DRAM 厂商字段", e.rt)
		}
		v, err := asInt("DRAM 厂商码", 0, 255)
		if err != nil {
			return err
		}
		return e.set(l.DramCode, byte(v), "DRAM 厂商", "low")
	case "location":
		v, err := asInt("生产地点", 0, 255)
		if err != nil {
			return err
		}
		return e.set(l.Location, byte(v), "生产地点", "low")
	case "dateYear":
		v, err := asInt("生产年份", 2000, 2099)
		if err != nil {
			return err
		}
		y := v - 2000
		if l.DateBCD {
			return e.setBCD(l.DateYear, y, "生产日期", "low")
		}
		return e.set(l.DateYear, byte(y), "生产日期", "low")
	case "dateWeek":
		v, err := asInt("生产周", 0, 53)
		if err != nil {
			return err
		}
		if l.DateBCD {
			return e.setBCD(l.DateWeek, v, "生产日期", "low")
		}
		return e.set(l.DateWeek, byte(v), "生产日期", "low")
	case "serial":
		return e.setHexBytes(l.SerialOff, l.SerialLen, value, "序列号", "low")
	case "partNumber":
		return e.setASCII(l.PNOff, l.PNLen, value, "部件号", "low")
	case "revision":
		if l.RevisionOff < 0 || l.RevisionLen <= 0 {
			return fmt.Errorf("%v 无模块修订码字段", e.rt)
		}
		return e.setHexBytes(l.RevisionOff, l.RevisionLen, value, "模块修订", "low")
	case "dramStepping":
		if l.DramStepping < 0 {
			return fmt.Errorf("%v 无 DRAM stepping 字段", e.rt)
		}
		v, err := asInt("DRAM stepping", 0, 255)
		if err != nil {
			return err
		}
		return e.set(l.DramStepping, byte(v), "DRAM stepping", "low")
	case "raw":
		return fmt.Errorf("原始字节请用 SetByte(offset, value)")
	}
	// 时序 / XMP / EXPO 字段
	for _, s := range e.timingSpecs() {
		if s.Key != key {
			continue
		}
		n, byClk, perr := parseTimingInput(value)
		if perr != nil {
			return fmt.Errorf("%s 必须是数值(纳秒; 或写 \"16clk\" 按周期): %q", s.Name, value)
		}
		ns := n
		if byClk {
			base, bok := e.clkBaseNS(key)
			if !bok || base <= 0 {
				return fmt.Errorf("%s: 当前 dump 没有有效的 tCK 基准, 不能按周期输入", s.Name)
			}
			ns = n * base
		}
		// 时间没变就不动字节: 同一时间可能有等价但字节不同的编码
		// (厂商常用 medium 向下取整 + 正 fine, 与 JEDEC 建议的向上取整 + 负 fine 等价)。
		// 容差取半个皮秒 = 界面上显示精度(3 位小数)可能引入的最大偏差。
		if cur, ok := s.Get(e); ok && math.Abs(cur-ns) < 6e-4 {
			return nil
		}
		return s.Set(e, ns)
	}
	if key == "ddr4.cl" || key == "ddr5.cl" {
		return e.setJEDECCLMask(value)
	}
	// profile 时序按周期输入: 先换算成 ns 再交给原有管线(applyClkInput 只对
	// 时序后缀生效, 电压/命令率等带 clk 输入会走原样解析并被单位校验拦下)
	if err := e.applyClkInput(&key, &value); err != nil {
		return err
	}
	if err := e.setProfileField(key, value); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "未知字段") {
		return err
	}
	return fmt.Errorf("未知字段 %q", key)
}

// MfgEntry 是厂商表条目(供界面搜索)。
type MfgEntry struct {
	Name string `json:"name"`
	Cont byte   `json:"cont"`
	Code byte   `json:"code"`
}

// SearchManufacturers 在 JEP106 表中按名称子串搜索(不区分大小写)。
func SearchManufacturers(query string, limit int) []MfgEntry {
	idcodesOnce()
	if idcodesErr != nil {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(query)
	var out []MfgEntry
	for cont, names := range idcodesTable {
		for i, n := range names {
			if q != "" && !strings.Contains(strings.ToLower(n), q) {
				continue
			}
			code := byte(i + 1)
			if parityOdd(code) {
				code |= 0x80
			}
			out = append(out, MfgEntry{Name: n, Cont: byte(cont), Code: code})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Cont < out[j].Cont
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func mfgContName(l idLayout) string {
	return "厂商续延码(bit7 = 奇校验位)"
}

func mfgCodeName(l idLayout) string {
	return "厂商码(含奇校验位)"
}

func mfgContValue(e *Editor, l idLayout) string {
	id, _ := e.Identity()
	return strconv.Itoa(int(id.ManufacturerCont))
}

func mfgCodeValue(e *Editor, l idLayout) string {
	id, _ := e.Identity()
	return strconv.Itoa(int(id.ManufacturerCode))
}

func mfgContNote(l idLayout, id Identity) string {
	return "厂商表按 cont & 0x7F 查表; 常见 bank0 写成 0x80(计数 0 + 校验位)"
}

func mfgCodeNote(l idLayout) string {
	return ""
}
