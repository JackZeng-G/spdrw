package spd

import "fmt"

// DDR2/DDR3 基本信息解析(对照原版 DDR2.cs/DDR3.cs 的关键字段)。

// Basic 是旧世代(DDR2/DDR3)的基本信息。
type Basic struct {
	Type         RamType
	ModuleType   string // UDIMM/SO-DIMM/...
	BytesMib     uint64 // 模块总容量(MiB)
	BusWidthBits byte
	Ranks        byte
	DeviceWidth  byte
	PartNumber   string
	Manufacturer string
	CRCOK        bool
	TCKminNS     float64
}

var ddr23ModuleTypeNames = map[byte]string{
	0x00: "FPDIMM",
	0x01: "RDIMM",
	0x02: "UDIMM",
	0x03: "SO-DIMM",
	0x04: "Micro-DIMM",
	0x05: "Mini-RDIMM",
	0x06: "Mini-UDIMM",
	0x07: "Mini-UDIMM",
}

// ParseBasic 解析 DDR2/DDR3 dump(256 字节)。其他世代返回错误。
func ParseBasic(dump []byte) (*Basic, error) {
	rt, _, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	switch rt {
	case DDR2, DDR2FBDIMM:
		return parseDDR2(dump, rt)
	case DDR3:
		return parseDDR3(dump)
	default:
		return nil, fmt.Errorf("不支持的世代 %v (DDR4/DDR5 请用专用解析)", rt)
	}
}

func parseDDR3(dump []byte) (*Basic, error) {
	b := &Basic{Type: DDR3}
	if name, ok := ddr23ModuleTypeNames[subByteR(dump[3], 3, 4)]; ok {
		b.ModuleType = name
	} else {
		b.ModuleType = fmt.Sprintf("未知(%#x)", subByteR(dump[3], 3, 4))
	}
	b.Ranks = subByteR(dump[7], 5, 3) + 1
	b.DeviceWidth = 4 << subByteR(dump[7], 2, 3)
	_, b.BusWidthBits = b23BusWidth(dump)

	// 容量: 单 die 容量(Mb) × 总线位宽/芯片位宽 × rank (原版 TotalModuleCapacityProgrammed)
	capPerDieMb := uint64(1) << (subByteR(dump[4], 3, 4) + 8)
	b.BytesMib = capPerDieMb / 8 * uint64(b.BusWidthBits) / uint64(b.DeviceWidth) * uint64(b.Ranks)

	b.PartNumber = asciiString(dump[128:146])
	b.Manufacturer = ManufacturerName(dump[117], dump[118])

	// CRC: byte0 bit7 决定覆盖 117 或 126 字节(JEDEC DDR3)
	coverage := 126
	if getBit(dump[0], 7) {
		coverage = 117
	}
	b.CRCOK = Crc16(dump[:coverage]) == uint16(dump[126])|uint16(dump[127])<<8

	// tCKmin = byte12 × MTB(= byte10/byte11 ns) + byte34 × 1ps
	mtbNum, mtbDen := dump[10], dump[11]
	if mtbDen > 0 {
		b.TCKminNS = float64(dump[12]) * float64(mtbNum) / float64(mtbDen)
	}
	b.TCKminNS += float64(int8(dump[34])) / 1000
	return b, nil
}

func parseDDR2(dump []byte, rt RamType) (*Basic, error) {
	b := &Basic{Type: rt}
	b.Ranks = subByteR(dump[5], 2, 3) + 1
	b.DeviceWidth = ddr2Width(dump[6])
	_, b.BusWidthBits = b23BusWidth(dump)

	// 容量: 阵列位 = 2^rows × 2^cols × banks; 模块 = 阵列/8 × (总线/芯片位宽) × rank
	rows, cols := uint(dump[3]), uint(dump[4])
	banks := uint64(dump[17])
	arrayBits := (uint64(1) << rows) * (uint64(1) << cols) * banks
	totalBytes := arrayBits / 8 * uint64(b.BusWidthBits) / uint64(b.DeviceWidth) * uint64(b.Ranks)
	b.BytesMib = totalBytes / 1024 / 1024

	b.PartNumber = asciiString(dump[73:91])
	b.Manufacturer = ""
	// DDR2 无 CRC
	b.CRCOK = true
	// tCKmin = 整数 nibble + 十分位 nibble
	b.TCKminNS = float64(subByteR(dump[9], 7, 4)) + float64(subByteR(dump[9], 3, 4))/10
	return b, nil
}

func b23BusWidth(dump []byte) (ext bool, primary byte) {
	return getBit(dump[8], 3), 1 << (subByteR(dump[8], 2, 3) + 3)
}

// ddr2Width 解析 DDR2 byte6 bits0-2 的芯片位宽(4/8/16/32)。
func ddr2Width(v byte) byte {
	switch subByteR(v, 2, 3) {
	case 0:
		return 4
	case 1:
		return 8
	case 2:
		return 16
	case 3:
		return 32
	default:
		return 0
	}
}
