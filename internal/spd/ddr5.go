package spd

import "fmt"

// DDR5 解析(对照原版 DDR5.cs 移植; 原版不含时序字段,保持同范围)。

// DDR5ModuleTypeNames 是 DDR5 byte3 BaseModuleType 名称。
var DDR5ModuleTypeNames = map[byte]string{
	0x01: "RDIMM",
	0x02: "UDIMM",
	0x03: "SO-DIMM",
	0x04: "LRDIMM",
	0x0A: "Solder down",
	0x0B: "DDIMM",
}

// DDR5 密度表(die 容量 Gb)。
var ddr5DensityList = [...]byte{0, 4, 8, 12, 16, 24, 32, 48, 64}

// XMP 3.0 / EXPO 布局(依据 JEDEC SPD5118 与 DDR5XMPEditor/SPD-Reader-Writer 的实测布局)。
//
//	0x280-0x2BF: XMP 3.0 header(64B): magic 0x0C 0x4A, version, 启用位, 3 个 profile 名,
//	             末 2 字节(0x2BE/0x2BF)为 header CRC
//	0x2C0/0x300/0x340/0x380/0x3C0: 5 个 XMP profile 槽(每个 64B, 末 2 字节为该槽 CRC)
//	0x340-0x3BF: EXPO 区(128B, magic "EXPO", 含 2 个 profile, 末 2 字节 CRC)
//	             —— 与 XMP 槽 3(0x340) 和 User1(0x380) **互斥**
const (
	xmp30Offset    = 0x280 // XMP 3.0 header 偏移
	xmp30HeaderLen = 64    // header 长度
	expoOffset     = 0x340 // EXPO 偏移
	expoLen        = 128   // EXPO 长度
)

// XMP30ProfileOffsets 是 5 个 XMP 3.0 profile 槽的偏移(旧实现误用 0x280+i*64,
// 把 header 当成第 1 个槽, 于是漏检最后一个槽 0x3C0 且 CRC 覆盖错位)。
var XMP30ProfileOffsets = [5]int{0x2C0, 0x300, 0x340, 0x380, 0x3C0}

// DDR5 是解析后的 DDR5 SPD(1024 字节)。
type DDR5SPD struct{ raw []byte }

// NewDDR5 构造; dump 必须 1024 字节且类型为 DDR5 系。
func NewDDR5(dump []byte) (*DDR5SPD, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	if rt != DDR5 && rt != LPDDR5 && rt != DDR5NVDIMMP && rt != LPDDR5X {
		return nil, fmt.Errorf("不是 DDR5 系 SPD: %v", rt)
	}
	if len(dump) != size {
		return nil, fmt.Errorf("DDR5 dump 应为 %d 字节, got %d", size, len(dump))
	}
	return &DDR5SPD{raw: dump}, nil
}

// Raw 返回原始数据。
func (d *DDR5SPD) Raw() []byte { return d.raw }

// ModuleType 返回模块类型名。
func (d *DDR5SPD) ModuleType() string {
	if name, ok := DDR5ModuleTypeNames[subByteR(d.raw[3], 3, 4)]; ok {
		return name
	}
	return fmt.Sprintf("未知(%#x)", subByteR(d.raw[3], 3, 4))
}

// DensityPackages 返回两组 (die 数, 单 die 容量 Gb)(对称时第二组为空数据)。
func (d *DDR5SPD) DensityPackages() (dies [2]byte, densitiesGb [2]byte) {
	for i := 0; i < 2; i++ {
		b := d.raw[4+i*4]
		die := subByteR(b, 7, 4)
		if die == 0 {
			die = 1
		}
		dies[i] = die
		densitiesGb[i] = ddr5DensityList[subByteR(b, 3, 4)]
	}
	return
}

// Addressing 返回两组 (行, 列)。
func (d *DDR5SPD) Addressing() (rows, cols [2]byte) {
	for i := 0; i < 2; i++ {
		rows[i] = subByteR(d.raw[5+i*4], 4, 5) + 16
		cols[i] = subByteR(d.raw[5+i*4], 7, 3) + 10
	}
	return
}

// IOWidths 返回两组 SDRAM IO 位宽编码相关值(原始字段值 3 位)。
func (d *DDR5SPD) IOWidths() (w [2]byte) {
	for i := 0; i < 2; i++ {
		w[i] = subByteR(d.raw[6+i*4], 7, 3)
	}
	return
}

// DeviceWidth 返回首组 SDRAM 器件位宽(JEDEC: 000=x4, 001=x8, 010=x16, 011=x32)。
func (d *DDR5SPD) DeviceWidth() byte {
	return 4 << subByteR(d.raw[6], 7, 3)
}

// Banks 返回 (bank group 数, 每组 bank 数)。
func (d *DDR5SPD) Banks() (groups, perGroup byte) {
	b := d.raw[7]
	return 1 << subByteR(b, 7, 3), 1 << subByteR(b, 2, 3)
}

// Organization 返回 (非对称, rank 数)。
func (d *DDR5SPD) Organization() (asymmetric bool, ranks byte) {
	return getBit(d.raw[234], 6), subByteR(d.raw[234], 5, 3) + 1
}

// ChannelBusWidth 返回 (通道数, 扩展位宽, 每通道主位宽)。
func (d *DDR5SPD) ChannelBusWidth() (channels, extension, primary byte) {
	b := d.raw[235]
	channels = 1 << subByteR(b, 6, 2)
	extension = subByteR(b, 4, 2) * 4
	primary = (1 << (subByteR(b, 2, 3) + 3)) & 0xF8
	return
}

// TotalCapacityBytes 计算模块总容量(单位 GiB, 原版公式直接出 GiB)。非对称返回 0。
func (d *DDR5SPD) TotalCapacityBytes() uint64 {
	asym, ranks := d.Organization()
	if asym {
		return 0
	}
	channels, _, primary := d.ChannelBusWidth()
	dies, densities := d.DensityPackages()
	ioW := d.IOWidths()
	ioWidth := byte(4) << ioW[0] // 原版 IoWidth 未展示容量换算,按 JEDEC: 4<<code(0→4bit... code=0→4)
	// 原版容量公式: channels * (primary/ioWidth) * dies * densityGb/8 * ranks
	return uint64(channels) * uint64(primary/ioWidth) * uint64(dies[0]) * uint64(densities[0]) / 8 * uint64(ranks)
}

// Manufacturer 返回 (厂商名, continuation, code)(bytes 512-513)。
func (d *DDR5SPD) Manufacturer() (string, byte, byte) {
	return ManufacturerName(d.raw[512], d.raw[513]), d.raw[512], d.raw[513]
}

// DateCode 返回 (年, 周)(bytes 515-516)。
func (d *DDR5SPD) DateCode() (year, week int) {
	return 2000 + BCD(d.raw[515]), BCD(d.raw[516])
}

// SerialNumber 返回序列号(bytes 517-520)。
func (d *DDR5SPD) SerialNumber() [4]byte {
	return [4]byte{d.raw[517], d.raw[518], d.raw[519], d.raw[520]}
}

// PartNumber 返回部件号(bytes 521-550)。
func (d *DDR5SPD) PartNumber() string {
	return asciiString(d.raw[521 : 521+30])
}

// XMPPresence 检测 XMP 3.0 头(0x0C 0x4A @ 640)。
func (d *DDR5SPD) XMPPresence() bool {
	return d.raw[xmp30Offset] == 0x0C && d.raw[xmp30Offset+1] == 0x4A
}

// EXPOPresence 检测 AMD EXPO 头("EXPO" @ 832)。
func (d *DDR5SPD) EXPOPresence() bool {
	return string(d.raw[expoOffset:expoOffset+4]) == "EXPO"
}

// XMP30Slots 返回各 XMP 3.0 槽位是否存在(非空白: 既非全 0x00 也非全 0xFF)。
// EXPO 存在时 0x340/0x380 两槽被 EXPO 占用, 一律报 false。
func (d *DDR5SPD) XMP30Slots() [5]bool {
	var out [5]bool
	expo := d.EXPOPresence()
	for i, off := range XMP30ProfileOffsets {
		if expo && (i == 2 || i == 3) {
			continue
		}
		out[i] = !isBlank(d.raw[off : off+62])
	}
	return out
}

// XMP30HeaderCRCOK 校验 XMP 3.0 header 段 CRC(覆盖 0x280-0x2BD, CRC 在 0x2BE/0x2BF)。
func (d *DDR5SPD) XMP30HeaderCRCOK() bool {
	if !d.XMPPresence() {
		return false
	}
	sec := d.raw[xmp30Offset : xmp30Offset+xmp30HeaderLen]
	return Crc16(sec[:62]) == uint16(sec[62])|uint16(sec[63])<<8
}

// CRCOK 校验基础段 CRC(bytes 0-509, CRC 在 510-511)、XMP 3.0 header 与各存在的
// profile 槽 CRC、EXPO 段 CRC。空白槽位跳过(未使用的槽常为 0x00/0xFF)。
func (d *DDR5SPD) CRCOK() bool {
	sec := d.raw[0:512]
	if Crc16(sec[:510]) != uint16(sec[510])|uint16(sec[511])<<8 {
		return false
	}
	if d.XMPPresence() {
		if !d.XMP30HeaderCRCOK() {
			return false
		}
		slots := d.XMP30Slots()
		for i, off := range XMP30ProfileOffsets {
			if !slots[i] {
				continue
			}
			s := d.raw[off : off+64]
			if Crc16(s[:62]) != uint16(s[62])|uint16(s[63])<<8 {
				return false
			}
		}
	}
	if d.EXPOPresence() {
		sec := d.raw[expoOffset : expoOffset+expoLen]
		if Crc16(sec[:126]) != uint16(sec[126])|uint16(sec[127])<<8 {
			return false
		}
	}
	return true
}

// isBlank 判断字节段是否全 0x00 或全 0xFF(未使用的区域)。
func isBlank(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	all00, allFF := true, true
	for _, v := range b {
		if v != 0x00 {
			all00 = false
		}
		if v != 0xFF {
			allFF = false
		}
	}
	return all00 || allFF
}
