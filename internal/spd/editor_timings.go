package spd

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 时序字段: 每种世代一组 (key, 名称, 单位, 读取 ns, 写入 ns)。
type timingSpec struct {
	Key    string
	Name   string
	Offset string
	Get    func(e *Editor) (float64, bool)
	Set    func(e *Editor, ns float64) error
}

// ---- DDR3/DDR4: medium+fine 双粒度 ----

func medFine(medOff, fineOff int) (func(*Editor) (float64, bool), func(*Editor, float64) error) {
	get := func(e *Editor) (float64, bool) {
		tb := DDR4Timebase(e.dump)
		v := timingNS(int(e.dump[medOff]), int(int8(e.dump[fineOff])), tb)
		return v, v > 0
	}
	set := func(e *Editor, ns float64) error {
		tb := DDR4Timebase(e.dump)
		m, f, err := encodeTiming(ns, tb)
		if err != nil {
			return err
		}
		if err := e.set(medOff, byte(m), "tCK/timing", "medium"); err != nil {
			return err
		}
		return e.set(fineOff, byte(int8(f)), "tCK/timing", "medium")
	}
	return get, set
}

// med12 处理 12 位 medium(低字节 + 高 4 位 nibble)。
func med12(loOff, nibOff int, pos int) (func(*Editor) (float64, bool), func(*Editor, float64) error) {
	get := func(e *Editor) (float64, bool) {
		tb := DDR4Timebase(e.dump)
		med := int(e.dump[loOff]) | int(subByteR(e.dump[nibOff], pos, 4))<<8
		return timingNS(med, 0, tb), med > 0
	}
	set := func(e *Editor, ns float64) error {
		tb := DDR4Timebase(e.dump)
		m, _, err := encodeTimingMax(ns, tb, 0xFFF) // 12 位 medium
		if err != nil {
			return err
		}
		if err := e.set(loOff, byte(m&0xFF), "tCK/timing", "medium"); err != nil {
			return err
		}
		return e.set(nibOff, setSubByteR(e.dump[nibOff], pos, 4, byte(m>>8)), "tCK/timing", "medium")
	}
	return get, set
}

// med16 处理 16 位 medium(两字节小端)。
func med16(loOff int) (func(*Editor) (float64, bool), func(*Editor, float64) error) {
	get := func(e *Editor) (float64, bool) {
		tb := DDR4Timebase(e.dump)
		med := int(e.dump[loOff]) | int(e.dump[loOff+1])<<8
		return timingNS(med, 0, tb), med > 0
	}
	set := func(e *Editor, ns float64) error {
		tb := DDR4Timebase(e.dump)
		m, _, err := encodeTimingMax(ns, tb, 0xFFFF)
		if err != nil {
			return err
		}
		if err := e.set(loOff, byte(m&0xFF), "tCK/timing", "medium"); err != nil {
			return err
		}
		return e.set(loOff+1, byte(m>>8), "tCK/timing", "medium")
	}
	return get, set
}

// ddr4TimingSpecs 返回 DDR4 全部主/次时序字段。
func ddr4TimingSpecs() []timingSpec {
	var out []timingSpec
	add := func(key, name, offset string, g func(*Editor) (float64, bool), s func(*Editor, float64) error) {
		out = append(out, timingSpec{Key: key, Name: name, Offset: offset, Get: g, Set: s})
	}
	g, s := medFine(18, 125)
	add("ddr4.tCKAVGmin", "tCKAVGmin", "0x12/0x7D", g, s)
	g, s = medFine(19, 124)
	add("ddr4.tCKAVGmax", "tCKAVGmax", "0x13/0x7C", g, s)
	g, s = medFine(24, 123)
	add("ddr4.tAA", "tAA", "0x18/0x7B", g, s)
	g, s = medFine(25, 122)
	add("ddr4.tRCD", "tRCD", "0x19/0x7A", g, s)
	g, s = medFine(26, 121)
	add("ddr4.tRP", "tRP", "0x1A/0x79", g, s)
	g, s = med12(28, 27, 3)
	add("ddr4.tRAS", "tRAS", "0x1C/0x1B[3:0]", g, s)
	g, s = medFine(29, 120)
	add("ddr4.tRC", "tRC", "0x1D(12b)/0x78", g, s)
	// tRC 的高 4 位在 byte27[7:4]
	out[len(out)-1].Get = func(e *Editor) (float64, bool) {
		tb := DDR4Timebase(e.dump)
		med := int(e.dump[29]) | int(subByteR(e.dump[27], 7, 4))<<8
		v := timingNS(med, int(int8(e.dump[120])), tb)
		return v, v > 0
	}
	out[len(out)-1].Set = func(e *Editor, ns float64) error {
		tb := DDR4Timebase(e.dump)
		m, f, err := encodeTimingMax(ns, tb, 0xFFF)
		if err != nil {
			return err
		}
		if err := e.set(29, byte(m&0xFF), "tRC", "medium"); err != nil {
			return err
		}
		if err := e.set(27, setSubByteR(e.dump[27], 7, 4, byte(m>>8)), "tRC", "medium"); err != nil {
			return err
		}
		return e.set(120, byte(int8(f)), "tRC", "medium")
	}
	g, s = med16(30)
	add("ddr4.tRFC1", "tRFC1", "0x1E-0x1F", g, s)
	g, s = med16(32)
	add("ddr4.tRFC2", "tRFC2", "0x20-0x21", g, s)
	g, s = med16(34)
	add("ddr4.tRFC4", "tRFC4", "0x22-0x23", g, s)
	g, s = med12(37, 36, 3)
	add("ddr4.tFAW", "tFAW", "0x25/0x24[3:0]", g, s)
	g, s = medFine(38, 119)
	add("ddr4.tRRD_S", "tRRD_S", "0x26/0x77", g, s)
	g, s = medFine(39, 118)
	add("ddr4.tRRD_L", "tRRD_L", "0x27/0x76", g, s)
	g, s = medFine(40, 117)
	add("ddr4.tCCD_L", "tCCD_L", "0x28/0x75", g, s)
	g, s = med12(42, 41, 3)
	add("ddr4.tWR", "tWR", "0x2A/0x29[3:0]", g, s)
	g, s = med12(44, 43, 3)
	add("ddr4.tWTR_S", "tWTR_S", "0x2C/0x2B[3:0]", g, s)
	g, s = med12(45, 43, 7)
	add("ddr4.tWTR_L", "tWTR_L", "0x2D/0x2B[7:4]", g, s)
	return out
}

// ddr3TimingSpecs 返回 DDR3 主要时序(byte 9-38, MTB/FTB 见 byte9-11)。
func ddr3TimingSpecs() []timingSpec {
	var out []timingSpec
	add := func(key, name, offset string, g func(*Editor) (float64, bool), s func(*Editor, float64) error) {
		out = append(out, timingSpec{Key: key, Name: name, Offset: offset, Get: g, Set: s})
	}
	// DDR3 的 MTB/FTB 在 byte9(FTB)/10-11(MTB), 与 DDR4 的 byte15 不同
	medFine3 := func(medOff, fineOff int) (func(*Editor) (float64, bool), func(*Editor, float64) error) {
		get := func(e *Editor) (float64, bool) {
			tb := ddr3Timebase(e.dump)
			v := timingNS(int(e.dump[medOff]), int(int8(e.dump[fineOff])), tb)
			return v, v > 0
		}
		set := func(e *Editor, ns float64) error {
			tb := ddr3Timebase(e.dump)
			m, f, err := encodeTiming(ns, tb)
			if err != nil {
				return err
			}
			if err := e.set(medOff, byte(m), "tCK/timing", "medium"); err != nil {
				return err
			}
			return e.set(fineOff, byte(int8(f)), "tCK/timing", "medium")
		}
		return get, set
	}
	g, s := medFine3(12, 34)
	add("ddr3.tCKmin", "tCKmin", "0x0C/0x22", g, s)
	g, s = medFine3(16, 35)
	add("ddr3.tAA", "tAA", "0x10/0x23", g, s)
	g, s = medFine3(18, 36)
	add("ddr3.tRCD", "tRCD", "0x12/0x24", g, s)
	g, s = medFine3(20, 37)
	add("ddr3.tRP", "tRP", "0x14/0x25", g, s)
	// tRC = byte23 + nibble21[3:0]<<8, fine byte38
	out = append(out, timingSpec{
		Key: "ddr3.tRC", Name: "tRC", Offset: "0x17/0x15[3:0]/0x26",
		Get: func(e *Editor) (float64, bool) {
			tb := ddr3Timebase(e.dump)
			med := int(e.dump[23]) | int(subByteR(e.dump[21], 3, 4))<<8
			v := timingNS(med, int(int8(e.dump[38])), tb)
			return v, v > 0
		},
		Set: func(e *Editor, ns float64) error {
			tb := ddr3Timebase(e.dump)
			m, f, err := encodeTimingMax(ns, tb, 0xFFF)
			if err != nil {
				return err
			}
			if err := e.set(23, byte(m&0xFF), "ddr3.tRC", "medium"); err != nil {
				return err
			}
			if err := e.set(21, setSubByteR(e.dump[21], 3, 4, byte(m>>8)), "ddr3.tRC", "medium"); err != nil {
				return err
			}
			return e.set(38, byte(int8(f)), "ddr3.tRC", "medium")
		},
	})
	// tRAS = byte22 + nibble21[7:4]<<8
	out = append(out, timingSpec{
		Key: "ddr3.tRAS", Name: "tRAS", Offset: "0x16/0x15[7:4]",
		Get: func(e *Editor) (float64, bool) {
			tb := ddr3Timebase(e.dump)
			med := int(e.dump[22]) | int(subByteR(e.dump[21], 7, 4))<<8
			return timingNS(med, 0, tb), med > 0
		},
		Set: func(e *Editor, ns float64) error {
			tb := ddr3Timebase(e.dump)
			m, _, err := encodeTimingMax(ns, tb, 0xFFF)
			if err != nil {
				return err
			}
			if err := e.set(22, byte(m&0xFF), "ddr3.tRAS", "medium"); err != nil {
				return err
			}
			return e.set(21, setSubByteR(e.dump[21], 7, 4, byte(m>>8)), "ddr3.tRAS", "medium")
		},
	})
	med16Simple := func(key, name string, loOff int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X-0x%02X", loOff, loOff+1),
			Get: func(e *Editor) (float64, bool) {
				tb := ddr3Timebase(e.dump)
				med := int(e.dump[loOff]) | int(e.dump[loOff+1])<<8
				return timingNS(med, 0, tb), true
			},
			Set: func(e *Editor, ns float64) error {
				m, _, err := encodeTimingMax(ns, ddr3Timebase(e.dump), 0xFFFF)
				if err != nil {
					return err
				}
				if err := e.set(loOff, byte(m&0xFF), key, "medium"); err != nil {
					return err
				}
				return e.set(loOff+1, byte(m>>8), key, "medium")
			},
		})
	}
	med16Simple("ddr3.tRFC", "tRFC", 24)
	med16Simple("ddr3.tFAW", "tFAW", 28)
	// 单字节 MTB 粒度字段
	byteSpec := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X", off),
			Get: func(e *Editor) (float64, bool) {
				v := timingNS(int(e.dump[off]), 0, ddr3Timebase(e.dump))
				return v, v > 0
			},
			Set: func(e *Editor, ns float64) error {
				m, _, err := encodeTiming(ns, ddr3Timebase(e.dump))
				if err != nil {
					return err
				}
				return e.set(off, byte(m), key, "medium")
			},
		})
	}
	byteSpec("ddr3.tWR", "tWR", 17)
	byteSpec("ddr3.tRRD", "tRRD", 19)
	byteSpec("ddr3.tWTR", "tWTR", 26)
	byteSpec("ddr3.tRTP", "tRTP", 27)
	byteSpec("ddr3.tCCD", "tCCD", 30)
	return out
}

// ddr5TimingSpecs 返回 DDR5 JEDEC 时序(16bit ps; tRFC* 为 1ns)。
func ddr5TimingSpecs() []timingSpec {
	var out []timingSpec
	psSpec := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X-0x%02X(ps)", off, off+1),
			Get: func(e *Editor) (float64, bool) {
				v := int(e.dump[off]) | int(e.dump[off+1])<<8
				return float64(v) / 1000, v > 0
			},
			Set: func(e *Editor, ns float64) error {
				ps := int(math.Round(ns * 1000))
				if ps < 0 || ps > 0xFFFF {
					return fmt.Errorf("%.3f ns 超出 16 位 ps 范围", ns)
				}
				if err := e.set(off, byte(ps&0xFF), key, "medium"); err != nil {
					return err
				}
				return e.set(off+1, byte(ps>>8), key, "medium")
			},
		})
	}
	nsSpec := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X-0x%02X(ns)", off, off+1),
			Get: func(e *Editor) (float64, bool) {
				v := int(e.dump[off]) | int(e.dump[off+1])<<8
				return float64(v), v > 0
			},
			Set: func(e *Editor, ns float64) error {
				v := int(math.Round(ns))
				if v < 0 || v > 0xFFFF {
					return fmt.Errorf("%.0f ns 超出 16 位范围", ns)
				}
				if err := e.set(off, byte(v&0xFF), key, "medium"); err != nil {
					return err
				}
				return e.set(off+1, byte(v>>8), key, "medium")
			},
		})
	}
	psSpec("ddr5.tCKAVGmin", "tCKAVGmin", ddr5OffTCKMin)
	psSpec("ddr5.tCKAVGmax", "tCKAVGmax", ddr5OffTCKMax)
	psSpec("ddr5.tAA", "tAA", ddr5OffTAA)
	psSpec("ddr5.tRCD", "tRCD", ddr5OffTRCD)
	psSpec("ddr5.tRP", "tRP", ddr5OffTRP)
	psSpec("ddr5.tRAS", "tRAS", ddr5OffTRAS)
	psSpec("ddr5.tRC", "tRC", ddr5OffTRC)
	psSpec("ddr5.tWR", "tWR", ddr5OffTWR)
	nsSpec("ddr5.tRFC1", "tRFC1(SLR)", ddr5OffTRFC1SLR)
	nsSpec("ddr5.tRFC2", "tRFC2(SLR)", ddr5OffTRFC2SLR)
	nsSpec("ddr5.tRFCsb", "tRFCsb(SLR)", ddr5OffTRFCSbSLR)
	nsSpec("ddr5.tRFC1DLR", "tRFC1(DLR)", ddr5OffTRFC1DLR)
	nsSpec("ddr5.tRFC2DLR", "tRFC2(DLR)", ddr5OffTRFC2DLR)
	nsSpec("ddr5.tRFCsbDLR", "tRFCsb(DLR)", ddr5OffTRFCSbDLR)
	psSpec("ddr5.tRRD_L", "tRRD_L", ddr5OffTRRDL)
	psSpec("ddr5.tCCD_L", "tCCD_L", ddr5OffTCCDL)
	psSpec("ddr5.tCCD_L_WR", "tCCD_L_WR", ddr5OffTCCDLWR)
	psSpec("ddr5.tCCD_L_WR2", "tCCD_L_WR2", ddr5OffTCCDLWR2)
	psSpec("ddr5.tFAW", "tFAW", ddr5OffTFAW)
	psSpec("ddr5.tCCD_L_WTR", "tCCD_L_WTR", ddr5OffTCCDLWTR)
	psSpec("ddr5.tCCD_S_WTR", "tCCD_S_WTR", ddr5OffTCCDSWTR)
	psSpec("ddr5.tRTP", "tRTP", ddr5OffTRTP)
	psSpec("ddr5.tCCD_M", "tCCD_M", ddr5OffTCCDM)
	psSpec("ddr5.tCCD_M_WR", "tCCD_M_WR", ddr5OffTCCDMWR)
	psSpec("ddr5.tCCD_M_WTR", "tCCD_M_WTR", ddr5OffTCCDMWTR)
	return out
}

// ddr2TimingSpecs 返回 DDR2 主要时序。
//
// DDR2 的编码与 DDR3/DDR4 完全不同(JEDEC DDR2 SPD):
//   - byte9/43: tCKmin/tCKmax 是 BCD "纳秒.十分位", 且十分位有扩展码
//     (A=0.25 B=0.33 C=0.66 D=0.75 E=0.875)
//   - byte27/28/29/36/37/38: tRP/tRRD/tRCD/tWR/tWTR/tRTP, 单位 1/4 ns(整数计数)
//   - byte30/41/42: tRAS/tRC/tRFC 整数纳秒; tRC/tRFC 的分数位在 byte40
//
// 只支持整数纳秒部分(分数位保持原样), 避免猜测过深。
func ddr2TimingSpecs() []timingSpec {
	var out []timingSpec
	bcdSpec := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X(BCD)", off),
			Get: func(e *Editor) (float64, bool) {
				v := e.dump[off]
				// DDR2 tCK: 高 nibble = 纳秒整数(0-15), 低 nibble = 十分位(含扩展码)
				whole := float64(v >> 4)
				frac := bcdFraction(v & 0x0F)
				if frac < 0 {
					return whole, true
				}
				return whole + frac, true
			},
			Set: func(e *Editor, ns float64) error {
				if ns < 0 || ns > 15 {
					return fmt.Errorf("%s 必须在 0-15 ns 之间", name)
				}
				whole := int(ns)
				frac := ns - float64(whole)
				nib := fracToBCD(frac)
				if nib < 0 {
					return fmt.Errorf("%.3f ns 的十分位无法用 DDR2 BCD 表示(可用 0/0.1-0.9/0.25/0.33/0.66/0.75/0.875)", ns)
				}
				return e.set(off, byte((whole<<4)&0xF0)|byte(nib), key, "medium")
			},
		})
	}
	quarter := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X(1/4ns)", off),
			Get: func(e *Editor) (float64, bool) {
				return float64(e.dump[off]) / 4, e.dump[off] > 0
			},
			Set: func(e *Editor, ns float64) error {
				v := int(math.Round(ns * 4))
				if v < 1 || v > 63 {
					return fmt.Errorf("%s 必须在 0.25-15.75 ns 之间", name)
				}
				return e.set(off, byte(v), key, "medium")
			},
		})
	}
	intNS := func(key, name string, off int) {
		out = append(out, timingSpec{
			Key: key, Name: name, Offset: fmt.Sprintf("0x%02X(ns)", off),
			Get: func(e *Editor) (float64, bool) {
				return float64(e.dump[off]), e.dump[off] > 0
			},
			Set: func(e *Editor, ns float64) error {
				v := int(math.Round(ns))
				if v < 1 || v > 255 {
					return fmt.Errorf("%s 必须在 1-255 ns 之间", name)
				}
				return e.set(off, byte(v), key, "medium")
			},
		})
	}
	bcdSpec("ddr2.tCKmin", "tCKmin", 9)
	bcdSpec("ddr2.tCKmax", "tCKmax", 43)
	quarter("ddr2.tRP", "tRP", 27)
	quarter("ddr2.tRRD", "tRRD", 28)
	quarter("ddr2.tRCD", "tRCD", 29)
	quarter("ddr2.tWR", "tWR", 36)
	quarter("ddr2.tWTR", "tWTR", 37)
	quarter("ddr2.tRTP", "tRTP", 38)
	intNS("ddr2.tRAS", "tRAS", 30)
	intNS("ddr2.tRC", "tRC", 41)
	intNS("ddr2.tRFC", "tRFC", 42)
	return out
}

// bcdFraction 把 DDR2 BCD 扩展码的十分位 nibble 转成小数。-1 = 保留值。
func bcdFraction(nib byte) float64 {
	switch nib & 0x0F {
	case 0xA:
		return 0.25
	case 0xB:
		return 0.33
	case 0xC:
		return 0.66
	case 0xD:
		return 0.75
	case 0xE:
		return 0.875
	case 0xF:
		return -1
	}
	return float64(nib&0x0F) / 10
}

// fracToBCD 把小数部分转成 DDR2 BCD 十分位 nibble。-1 = 不可表示。
func fracToBCD(frac float64) int {
	for nib := 0; nib <= 9; nib++ {
		if math.Abs(frac-float64(nib)/10) < 1e-6 {
			return nib
		}
	}
	for nib, v := range map[int]float64{0xA: 0.25, 0xB: 0.33, 0xC: 0.66, 0xD: 0.75, 0xE: 0.875} {
		if math.Abs(frac-v) < 1e-6 {
			return nib
		}
	}
	return -1
}

// timingSpecs 返回该世代的时序字段。
func (e *Editor) timingSpecs() []timingSpec {
	switch e.rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		return ddr4TimingSpecs()
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		return ddr5TimingSpecs()
	case DDR3:
		return ddr3TimingSpecs()
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		return ddr2TimingSpecs()
	default:
		return nil
	}
}

// ddr3Timebase 计算 DDR3 时间基准: byte9 高 4 位/低 4 位 = FTB 皮秒分子/分母,
// byte10/byte11 = MTB 纳秒分子/分母。
func ddr3Timebase(dump []byte) Timebase {
	tb := Timebase{Fine: 1, Medium: 125}
	fnum := int(subByteR(dump[9], 7, 4))
	fden := int(subByteR(dump[9], 3, 4))
	if fden > 0 && fnum > 0 {
		tb.Fine = fnum / fden
	}
	if tb.Fine <= 0 {
		tb.Fine = 1
	}
	if dump[10] > 0 && dump[11] > 0 {
		tb.Medium = int(dump[10]) * 1000 / int(dump[11])
	}
	if tb.Medium <= 0 {
		tb.Medium = 125
	}
	return tb
}

// timingValue 把 ns 值格式化为表单字符串。
func timingValue(ns float64, ok bool) string {
	if !ok {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(ns, 'f', 3, 64), "0"), ".")
}
