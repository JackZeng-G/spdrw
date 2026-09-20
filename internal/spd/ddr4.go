package spd

import "fmt"

// DDR4 解析(对照原版 DDR4.cs 逐字段移植)。

// DDR4ModuleTypeNames 是 DDR4 byte3 BaseModuleType 名称。
var DDR4ModuleTypeNames = map[byte]string{
	0x00: "Extended DIMM",
	0x01: "RDIMM",
	0x02: "UDIMM",
	0x03: "SO-DIMM",
	0x04: "LRDIMM",
	0x05: "Mini-RDIMM",
	0x06: "Mini-UDIMM",
	0x08: "72b-SO-RDIMM",
	0x09: "72b-SO-UDIMM",
	0x0C: "16b-SO-DIMM",
	0x0D: "32b-SO-DIMM",
}

// DDR4 是解析后的 DDR4 SPD(512 字节)。
type DDR4SPD struct{ raw []byte }

// NewDDR4 构造; dump 必须 512 字节且类型为 DDR4 系。
func NewDDR4(dump []byte) (*DDR4SPD, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	if rt != DDR4 && rt != DDR4E && rt != LPDDR3 && rt != LPDDR4 && rt != LPDDR4X {
		return nil, fmt.Errorf("不是 DDR4 系 SPD: %v", rt)
	}
	if len(dump) != size {
		return nil, fmt.Errorf("DDR4 dump 应为 %d 字节, got %d", size, len(dump))
	}
	return &DDR4SPD{raw: dump}, nil
}

// Raw 返回原始数据(修复 CRC 时可写)。
func (d *DDR4SPD) Raw() []byte { return d.raw }

// Timebase 返回时间基准(byte 15)。
func (d *DDR4SPD) Timebase() Timebase { return DDR4Timebase(d.raw) }

// ModuleType 返回模块类型名。
func (d *DDR4SPD) ModuleType() string {
	if name, ok := DDR4ModuleTypeNames[subByteR(d.raw[3], 3, 4)]; ok {
		return name
	}
	return fmt.Sprintf("未知(%#x)", subByteR(d.raw[3], 3, 4))
}

// DensityBanks 返回 bank 组数/每组合数/单 die 容量(Mb)。
func (d *DDR4SPD) DensityBanks() (bankGroup, bankAddr byte, capPerDieMb uint16) {
	bg := subByteR(d.raw[4], 7, 2) * 2
	ba := byte(1 << (subByteR(d.raw[4], 5, 2) + 2))
	capDie := subByteR(d.raw[4], 3, 4)
	var cap uint16
	if !getBit(d.raw[4], 3) {
		cap = 2 << (capDie + 7) // 256Mb-32Gb
	} else {
		cap = 3 << (capDie + 4) // 12Gb-24Gb
	}
	return bg, ba, cap
}

// Addressing 返回行/列地址数。
func (d *DDR4SPD) Addressing() (rows, cols byte) {
	return subByteR(d.raw[5], 5, 3) + 12, subByteR(d.raw[5], 2, 3) + 9
}

// PackageType 返回 (是否单片, die 数)。
func (d *DDR4SPD) PackageType() (monolithic bool, dieCount byte) {
	return !getBit(d.raw[6], 7), subByteR(d.raw[6], 6, 3) + 1
}

// Organization 返回 (对称/非对称, rank 数, 位宽)。
func (d *DDR4SPD) Organization() (asymmetric bool, ranks byte, deviceWidth byte) {
	return getBit(d.raw[12], 6), subByteR(d.raw[12], 5, 3) + 1, 4 << subByteR(d.raw[12], 2, 3)
}

// BusWidth 返回 (是否含 ECC 扩展, 主总线位宽)。
func (d *DDR4SPD) BusWidth() (extension bool, primary byte) {
	return getBit(d.raw[13], 3), 1 << (subByteR(d.raw[13], 2, 3) + 3)
}

// TotalCapacityBytes 计算模块总容量(字节, 编程值)。
func (d *DDR4SPD) TotalCapacityBytes() uint64 {
	_, dieCount := d.PackageType()
	dieFactor := uint64(1)
	mono, _ := d.PackageType()
	if !mono { // 多 die 堆叠(3DS)
		dieFactor = uint64(dieCount)
	}
	_, ranks, _ := d.Organization()
	_, bus := d.BusWidth()
	_, _, capPerDie := d.DensityBanks()
	return uint64(capPerDie) / 8 * uint64(bus) / uint64(d.deviceWidth()) * uint64(ranks) * dieFactor * 1024 * 1024
}

func (d *DDR4SPD) deviceWidth() byte {
	_, _, w := d.Organization()
	return w
}

// Timing adjustable: medium+offset + fine+offset。
func (d *DDR4SPD) timing(mediumOffset, fineOffset int) Timing {
	return Timing{Medium: int(int8(d.raw[mediumOffset])), Fine: int(int8(d.raw[fineOffset]))}
}

func (d *DDR4SPD) timingLong(medium int) Timing { return Timing{Medium: medium} }

// tCKAVGmin 最小周期时间(byte 18 / fine 125)。
func (d *DDR4SPD) TCKAVGmin() Timing { return d.timing(18, 125) }

// tCKAVGmax 最大周期时间(byte 19 / fine 124)。
func (d *DDR4SPD) TCKAVGmax() Timing { return d.timing(19, 124) }

// CasLatencies CAS 掩码(byte 20-23)。
func (d *DDR4SPD) CasLatencies() CasLatencies {
	return CasLatencies{
		Bitmask:   uint32(d.raw[20]) | uint32(d.raw[21])<<8 | uint32(d.raw[22])<<16 | uint32(d.raw[23])<<24,
		HighRange: getBit(d.raw[23], 7),
	}
}

// TAAmin CAS 到数据输出延迟(byte 24 / fine 123)。
func (d *DDR4SPD) TAAmin() Timing { return d.timing(24, 123) }

// TRCDmin RAS 到 CAS 延迟(byte 25 / fine 122)。
func (d *DDR4SPD) TRCDmin() Timing { return d.timing(25, 122) }

// TRPmin 行预充电延迟(byte 26 / fine 121)。
func (d *DDR4SPD) TRPmin() Timing { return d.timing(26, 121) }

// TRASmin 行激活到预充电(byte 28 | nibble(27,3,4)<<8)。
func (d *DDR4SPD) TRASmin() Timing {
	return d.timingLong(int(uint16(d.raw[28]) | uint16(subByteR(d.raw[27], 3, 4))<<8))
}

// TRCmin 行周期时间(byte 29 | nibble(27,7,4)<<8 / fine 120)。
func (d *DDR4SPD) TRCmin() Timing {
	return Timing{
		Medium: int(uint16(d.raw[29]) | uint16(subByteR(d.raw[27], 7, 4))<<8),
		Fine:   int(int8(d.raw[120])),
	}
}

// TRFC1 刷新恢复 1(byte 30-31)。
func (d *DDR4SPD) TRFC1() Timing { return d.timingLong(int(uint16(d.raw[30]) | uint16(d.raw[31])<<8)) }

// TRFC2 刷新恢复 2(byte 32-33)。
func (d *DDR4SPD) TRFC2() Timing { return d.timingLong(int(uint16(d.raw[32]) | uint16(d.raw[33])<<8)) }

// TRFC4 刷新恢复 4(byte 34-35)。
func (d *DDR4SPD) TRFC4() Timing { return d.timingLong(int(uint16(d.raw[34]) | uint16(d.raw[35])<<8)) }

// TFAW 四激活窗口(byte 37 | nibble(36,3,4)<<8)。
func (d *DDR4SPD) TFAW() Timing {
	return d.timingLong(int(uint16(d.raw[37]) | uint16(subByteR(d.raw[36], 3, 4))<<8))
}

// TRRDS 不同 bank group 的 RRD(byte 38 / fine 119)。
func (d *DDR4SPD) TRRDS() Timing { return d.timing(38, 119) }

// TRRDL 同 bank group 的 RRD(byte 39 / fine 118)。
func (d *DDR4SPD) TRRDL() Timing { return d.timing(39, 118) }

// TCCDL 同 bank group 的 CCD(byte 40 / fine 117)。
func (d *DDR4SPD) TCCDL() Timing { return d.timing(40, 117) }

// TWR 写恢复(byte 42 | nibble(41,3,4)<<8)。
func (d *DDR4SPD) TWR() Timing {
	return d.timingLong(int(uint16(d.raw[42]) | uint16(subByteR(d.raw[41], 3, 4))<<8))
}

// TWTRS 写到读-不同组(byte 44 | nibble(43,3,4)<<8)。
func (d *DDR4SPD) TWTRS() Timing {
	return d.timingLong(int(uint16(d.raw[44]) | uint16(subByteR(d.raw[43], 3, 4))<<8))
}

// TWTRL 写到读-同组(byte 45 | nibble(43,7,4)<<8)。
func (d *DDR4SPD) TWTRL() Timing {
	return d.timingLong(int(uint16(d.raw[45]) | uint16(subByteR(d.raw[43], 7, 4))<<8))
}

// crcSections 返回两段 CRC 区(各 128 字节,CRC 位于每段末 2 字节,LSB 在前)。
func (d *DDR4SPD) crcOK() bool {
	for i := 0; i < 2; i++ {
		sec := d.raw[i*128 : (i+1)*128]
		if Crc16(sec[:126]) != uint16(sec[126])|uint16(sec[127])<<8 {
			return false
		}
	}
	return true
}

// CRCOK 报告双段 CRC 是否校验通过。
func (d *DDR4SPD) CRCOK() bool { return d.crcOK() }

// FixCRC 重算并写入两段 CRC; 返回修复后是否通过。
func (d *DDR4SPD) FixCRC() bool {
	for i := 0; i < 2; i++ {
		sec := d.raw[i*128 : (i+1)*128]
		crc := Crc16(sec[:126])
		sec[126] = byte(crc)
		sec[127] = byte(crc >> 8)
	}
	return d.crcOK()
}

// Manufacturer 返回 (厂商名, continuation, code)。
func (d *DDR4SPD) Manufacturer() (string, byte, byte) {
	return ManufacturerName(d.raw[320], d.raw[321]), d.raw[320], d.raw[321]
}

// DateCode 返回 (年, 周) BCD。
func (d *DDR4SPD) DateCode() (year, week int) {
	return 2000 + BCD(d.raw[323]), BCD(d.raw[324]) // 原版显示层 +2000
}

// SerialNumber 返回序列号 4 字节。
func (d *DDR4SPD) SerialNumber() [4]byte {
	return [4]byte{d.raw[325], d.raw[326], d.raw[327], d.raw[328]}
}

// PartNumber 返回部件号(bytes 329-348)。
func (d *DDR4SPD) PartNumber() string {
	return asciiString(d.raw[329 : 329+20])
}

// XMPPresence 检测 XMP 2.0 头(magic 0x0C 0x4A @ 384)。
func (d *DDR4SPD) XMPPresence() bool {
	return len(d.raw) > 385 && d.raw[384] == 0x0C && d.raw[385] == 0x4A
}

// XMPProfile 是一份 XMP 2.0 profile(2 份,offset = 384 + n*63)。
type XMPProfile struct {
	Number   byte
	Enabled  bool
	Version  byte
	TCKmin   Timing
	TAAmin   Timing
	CasLat   CasLatencies
	TRCD     Timing
	TRP      Timing
	TRAS     Timing
	TRC      Timing
	TFAW     Timing
	TRRDS    Timing
	TRRDL    Timing
	TRFC1    Timing
	TRFC2    Timing
	TRFC4    Timing
	Volts    float64
	Channels byte
}

// XMPProfiles 返回两份 profile(未启用/不存在时 Enabled=false 的空数据)。
func (d *DDR4SPD) XMPProfiles() []XMPProfile {
	out := make([]XMPProfile, 2)
	for n := 0; n < 2; n++ {
		off := n * 63
		p := XMPProfile{Number: byte(n)}
		p.Enabled = getBit(d.raw[386], n)
		p.Version = d.raw[387]
		base := 384 + off
		p.TCKmin = d.timing(base+0x0C, base+0x2F)
		p.TAAmin = d.timing(base+0x11, base+0x2E)
		// CL 掩码: +0x0D 起的 24 位小端, bit i → CL i+7(实测: G.Skill 3200 的 0x80 = CL14,
		// 原实现按大端读会把 CL 表整体读错)
		p.CasLat = CasLatencies{
			Bitmask: uint32(d.raw[base+0x0D]) | uint32(d.raw[base+0x0E])<<8 | uint32(d.raw[base+0x0F])<<16,
		}
		p.TRCD = d.timing(base+0x12, base+0x2D)
		p.TRP = d.timing(base+0x13, base+0x2C)
		p.TRAS = d.timingLong(int(uint16(d.raw[base+0x15]) | uint16(subByteR(d.raw[base+0x14], 7, 4))<<8))
		p.TRC = Timing{
			Medium: int(uint16(d.raw[base+0x16]) | uint16(subByteR(d.raw[base+0x14], 3, 4))<<8),
			Fine:   int(int8(d.raw[base+0x2B])),
		}
		p.TFAW = d.timingLong(int(uint16(d.raw[base+0x1E]) | uint16(subByteR(d.raw[base+0x1D], 3, 4))<<8))
		p.TRRDS = d.timing(base+0x1F, base+0x2A)
		p.TRRDL = d.timing(base+0x20, base+0x29)
		p.TRFC1 = d.timingLong(int(uint16(d.raw[base+0x17]) | uint16(d.raw[base+0x18])<<8))
		p.TRFC2 = d.timingLong(int(uint16(d.raw[base+0x19]) | uint16(d.raw[base+0x1A])<<8))
		p.TRFC4 = d.timingLong(int(uint16(d.raw[base+0x1B]) | uint16(d.raw[base+0x1C])<<8))
		v := d.raw[base+0x09]
		p.Volts = float64(b2i(getBit(v, 7))) + float64(subByteR(v, 6, 7))/100
		p.Channels = subByteR(d.raw[base+0x02], 3, 2) + 1
		out[n] = p
	}
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// XMPString 格式化一份 profile 为 "3200 MHz 16-18-18-38 1.35V" 形式(原版 ToString)。
func XMPString(p XMPProfile, tb Timebase) string {
	if !p.Enabled {
		return ""
	}
	return fmt.Sprintf("%.0f MHz %d-%d-%d-%d %.2fV",
		p.TCKmin.MegaHertz(tb),
		p.TAAmin.ClockCycles(tb, p.TCKmin),
		p.TRCD.ClockCycles(tb, p.TCKmin),
		p.TRP.ClockCycles(tb, p.TCKmin),
		p.TRAS.ClockCycles(tb, p.TCKmin),
		p.Volts)
}
