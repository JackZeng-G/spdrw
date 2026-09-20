package spd

import "fmt"

// 校验层: 按世代校验 dump 的 CRC。纯函数, 无 I/O。
// 写入前预检与编辑器都依赖它 —— 写一份 CRC 不符的 SPD 会让主板拒绝该条或
// 按错误的时序启动。

// CRCOK 校验 dump 的 CRC。返回 (是否通过, 错误)。
// 长度与类型不匹配、或该世代无 CRC 定义时返回错误。
//
// 覆盖范围:
//   - DDR4/LPDDR4 系(512B): 两段各 128B, CRC 在每段末 2 字节
//   - DDR5/LPDDR5 系(1024B): 基础段 0-509(CRC 510/511) + XMP 3.0 header/各槽 + EXPO
//   - DDR3(256B): 单段, 覆盖 126 或 117 字节(byte0 bit7 决定), CRC 在 126/127
//   - DDR2(256B): 8 位校验和, byte63 = sum(bytes 0..62)
func CRCOK(dump []byte) (bool, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return false, err
	}
	if len(dump) != size {
		return false, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	switch rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		d, err := NewDDR4(dump)
		if err != nil {
			return false, err
		}
		return d.CRCOK(), nil
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		d, err := NewDDR5(dump)
		if err != nil {
			return false, err
		}
		return d.CRCOK(), nil
	case DDR3:
		coverage := 126
		if getBit(dump[0], 7) {
			coverage = 117
		}
		return Crc16(dump[:coverage]) == uint16(dump[126])|uint16(dump[127])<<8, nil
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		// DDR2 没有 CRC16, 而是 byte63 = sum(bytes 0..62) & 0xFF
		return ddr2Checksum(dump) == dump[63], nil
	default:
		return false, fmt.Errorf("%v 无 CRC 定义, 无法校验", rt)
	}
}

// CRCOffsets 返回该 dump 中各 CRC 字节的偏移(写入时应最后写这些字节)。
// 只返回"对应区域非空白"的 CRC —— 未使用的扩展区不写。
//
// 注意与 CRCRanges 的**判据差异**(有意保留):
//   - CRCOffsets(这里)按"槽内有数据"(XMP30SlotHasData)判定, 因为半填充槽也要被
//     修好 CRC(用户可能刚建了一份 profile);
//   - CRCRanges 按校验器 CRCOK 的判据("是不是一份有效 profile" XMP30SlotPresent)
//     判定, 因为它回答的是"改这个字节会不会让校验失败"。
//
// 前者是后者的超集; TestCRCOffsetsMatchesRanges 锁住这个包含关系。
func CRCOffsets(dump []byte) []int {
	rt, size, err := Identify(dump)
	if err != nil || len(dump) != size {
		return nil
	}
	var out []int
	switch rt {
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		out = append(out, 63)
	case DDR3:
		out = append(out, 126, 127)
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		out = append(out, 126, 127, 254, 255)
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		out = append(out, 510, 511)
		if len(dump) > xmp30Offset+1 && dump[xmp30Offset] == 0x0C && dump[xmp30Offset+1] == 0x4A {
			out = append(out, xmp30Offset+62, xmp30Offset+63)
		}
		expo := len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
		for i := range XMP30ProfileOffsets {
			if expo && (i == 2 || i == 3) {
				continue
			}
			// 用"有数据"而不是"有 VPP": 只填了时序的空槽也必须重算 CRC
			if XMP30SlotHasData(dump, i) {
				out = append(out, XMP30ProfileOffsets[i]+62, XMP30ProfileOffsets[i]+63)
			}
		}
		if expo {
			out = append(out, expoOffset+126, expoOffset+127)
		}
	}
	return out
}

// CRCRange 描述一段被校验覆盖的区域: [Start,End) 内的字节参与校验, 校验值本身
// 存在 CRCOff 起的 CRCLen 字节里。
//
// 编辑器用这张表区分"改了必须重算校验"与"改了不影响校验":落到所有区间之外的
// 字节(例如 DDR4/DDR5 的厂商/序列号/日期/部件号区)改多少都不影响任何校验和。
type CRCRange struct {
	Name     string `json:"name"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	CRCOff   int    `json:"crcOff"`
	CRCLen   int    `json:"crcLen"`
	Checksum bool   `json:"checksum,omitempty"` // true = 8 位校验和(DDR2), 否则 CRC16
}

// CRCRanges 返回 dump 中**真正生效**的校验区(与 CRCOK/CRCOffsets 同一套判定:
// 未使用的扩展槽不参与校验, 也就不会出现在这里)。
func CRCRanges(dump []byte) []CRCRange {
	rt, size, err := Identify(dump)
	if err != nil || len(dump) != size {
		return nil
	}
	var out []CRCRange
	add := func(name string, start, end, crcOff, crcLen int) {
		out = append(out, CRCRange{Name: name, Start: start, End: end, CRCOff: crcOff, CRCLen: crcLen})
	}
	switch rt {
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		r := CRCRange{Name: "基础段(0x00-0x3E)", Start: 0, End: 63, CRCOff: 63, CRCLen: 1, Checksum: true}
		return append(out, r)
	case DDR3:
		coverage := 126
		if getBit(dump[0], 7) {
			coverage = 117
		}
		add("基础段(0x00-0x"+fmt.Sprintf("%02X", coverage-1)+")", 0, coverage, 126, 2)
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		add("块 1(0x000-0x07D)", 0, 126, 126, 2)
		add("块 2(0x080-0x0FD)", 128, 254, 254, 2)
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		add("基础段(0x000-0x1FD)", 0, 510, 510, 2)
		expo := len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
		// 与 CRCOK 同判据: 没有 XMP 头魔数时整段(头 + 各槽)都不参与校验,
		// 槽位按"是不是一份有效 profile"(VPP)判定, EXPO 占用 0x340/0x380 两槽。
		if len(dump) > xmp30Offset+1 && dump[xmp30Offset] == 0x0C && dump[xmp30Offset+1] == 0x4A {
			add("XMP 3.0 头(0x280-0x2BD)", xmp30Offset, xmp30Offset+62, xmp30Offset+62, 2)
			for i, off := range XMP30ProfileOffsets {
				if expo && (i == 2 || i == 3) {
					continue
				}
				if XMP30SlotPresent(dump, i) {
					add(fmt.Sprintf("XMP 3.0 槽 %d(0x%03X-0x%03X)", i+1, off, off+61), off, off+62, off+62, 2)
				}
			}
		}
		if expo {
			add("EXPO(0x340-0x3BD)", expoOffset, expoOffset+126, expoOffset+126, 2)
		}
	}
	return out
}

// AffectsChecksum 判断改动某个字节是否会影响校验结果:落在校验区内(数据变了 →
// 校验值必须跟着变),或者本身就是校验值字节(直接改它 = 校验不通过)。
func AffectsChecksum(ranges []CRCRange, off int) bool {
	for _, r := range ranges {
		if off >= r.CRCOff && off < r.CRCOff+r.CRCLen {
			return true
		}
		if off >= r.Start && off < r.End {
			return true
		}
	}
	return false
}

// CRCBytes 返回全部校验值字节的偏移(写入时这些字节排在最后)。
func CRCBytes(ranges []CRCRange) []int {
	var out []int
	for _, r := range ranges {
		for i := 0; i < r.CRCLen; i++ {
			out = append(out, r.CRCOff+i)
		}
	}
	return out
}

// Area 是一段有名字的字节区间(用于向用户解释"哪些区域改了不用重算校验")。
type Area struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// IdentityAreas 返回该世代身份区各字段的偏移区间(厂商/生产位置/日期/序列号/
// 部件号/修订)。这些字段里哪些真正参与校验随世代而不同 —— DDR3 的厂商与序列号
// 在 126 字节覆盖范围内, DDR4/DDR5 的身份区(0x140+/0x200+)则完全不在校验范围内。
func IdentityAreas(rt RamType) []Area {
	l, err := idLayoutFor(rt)
	if err != nil {
		return nil
	}
	var out []Area
	add := func(name string, off, n int) {
		if off < 0 || n <= 0 {
			return
		}
		out = append(out, Area{Name: name, Start: off, End: off + n})
	}
	add("厂商", l.MfgCont, 2)
	add("生产位置", l.Location, 1)
	add("生产日期", l.DateYear, l.DateWeek-l.DateYear+1)
	add("序列号", l.SerialOff, l.SerialLen)
	add("部件号", l.PNOff, l.PNLen)
	add("修订版本", l.RevisionOff, l.RevisionLen)
	if l.DramCont >= 0 {
		add("DRAM 厂商", l.DramCont, 2)
	}
	return out
}

// UnprotectedIdentityAreas 返回身份区里**不参与校验**的字段(编辑器据此提示
// "这些改动不用重算 CRC")。
func UnprotectedIdentityAreas(rt RamType, ranges []CRCRange) []Area {
	var out []Area
	for _, a := range IdentityAreas(rt) {
		covered := false
		for off := a.Start; off < a.End; off++ {
			if AffectsChecksum(ranges, off) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, a)
		}
	}
	return out
}

// FixCRC 重算并写回 dump 中所有 CRC 字节(CRC 不符时用于修复)。
// 只改 CRC 字节, 返回被修改的偏移列表。
func FixCRC(dump []byte) ([]int, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	if len(dump) != size {
		return nil, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	switch rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		d, err := NewDDR4(dump)
		if err != nil {
			return nil, err
		}
		if !d.FixCRC() {
			return nil, fmt.Errorf("DDR4 CRC 修复后仍校验失败")
		}
		return []int{126, 127, 254, 255}, nil
	case DDR3:
		coverage := 126
		if getBit(dump[0], 7) {
			coverage = 117
		}
		crc := Crc16(dump[:coverage])
		dump[126], dump[127] = byte(crc), byte(crc>>8)
		return []int{126, 127}, nil
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		dump[63] = ddr2Checksum(dump)
		return []int{63}, nil
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		var touched []int
		base := Crc16(dump[:510])
		dump[510], dump[511] = byte(base), byte(base>>8)
		touched = append(touched, 510, 511)
		if len(dump) > xmp30Offset+63 && dump[xmp30Offset] == 0x0C && dump[xmp30Offset+1] == 0x4A {
			sec := dump[xmp30Offset : xmp30Offset+xmp30HeaderLen]
			h := Crc16(sec[:62])
			sec[62], sec[63] = byte(h), byte(h>>8)
			touched = append(touched, xmp30Offset+62, xmp30Offset+63)
		}
		expo := len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
		for i, off := range XMP30ProfileOffsets {
			if expo && (i == 2 || i == 3) {
				continue
			}
			if !XMP30SlotHasData(dump, i) {
				continue
			}
			s := dump[off : off+64]
			c := Crc16(s[:62])
			s[62], s[63] = byte(c), byte(c>>8)
			touched = append(touched, off+62, off+63)
		}
		if expo {
			sec := dump[expoOffset : expoOffset+expoLen]
			c := Crc16(sec[:126])
			sec[126], sec[127] = byte(c), byte(c>>8)
			touched = append(touched, expoOffset+126, expoOffset+127)
		}
		return touched, nil
	default:
		return nil, fmt.Errorf("%v 无 CRC 定义", rt)
	}
}

// ddr2Checksum 计算 DDR2 SPD 校验和: byte63 = sum(bytes 0..62) & 0xFF。
func ddr2Checksum(dump []byte) byte {
	sum := byte(0)
	for _, v := range dump[:63] {
		sum += v
	}
	return sum
}
