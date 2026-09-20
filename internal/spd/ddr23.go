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

	// 身份区(编辑用)
	ManufacturerCont byte   // JEP106 continuation
	ManufacturerCode byte   // JEP106 code(含奇校验位)
	Location         byte   // 生产地点
	DateYear         int    // 生产年(4 位; DDR2 = 2000+byte93, DDR3 = 2000+BCD(byte120))
	DateWeek         int    // 生产周
	SerialHex        string // 序列号(16 进制)
	Revision         uint16 // 模块修订码
	ChecksumOK       bool   // DDR2: byte63 = sum(0..62); DDR3: CRC16(见 CRCOK)
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

	// 身份区(JEDEC DDR3 SPD Annex K)
	b.ManufacturerCont, b.ManufacturerCode = dump[117], dump[118]
	b.Manufacturer = ManufacturerName(b.ManufacturerCont, b.ManufacturerCode)
	b.Location = dump[119]
	b.DateYear, b.DateWeek = 2000+BCD(dump[120]), BCD(dump[121])
	b.SerialHex = fmt.Sprintf("%02X%02X%02X%02X", dump[122], dump[123], dump[124], dump[125])
	b.Revision = uint16(dump[146]) | uint16(dump[147])<<8
	b.PartNumber = asciiString(dump[128:146])

	// CRC: byte0 bit7 决定覆盖 117 或 126 字节(JEDEC DDR3)
	coverage := 126
	if getBit(dump[0], 7) {
		coverage = 117
	}
	b.CRCOK = Crc16(dump[:coverage]) == uint16(dump[126])|uint16(dump[127])<<8
	b.ChecksumOK = b.CRCOK

	// tCKmin = byte12 × MTB(= byte10/byte11 ns) + byte34 × 1ps
	mtbNum, mtbDen := dump[10], dump[11]
	if mtbDen > 0 {
		b.TCKminNS = float64(dump[12]) * float64(mtbNum) / float64(mtbDen)
	}
	// byte34 是 FTB 粒度的修正量, FTB 可能是分数(如 5/2 = 2.5ps)
	_, finePS := ddr3TimebaseF(dump)
	b.TCKminNS += float64(int8(dump[34])) * finePS / 1000

	// byte34-38 是 FTB 粒度的修正量(单位 ps), 上面已把 byte34 计入 tCKmin
	return b, nil
}

// parseDDR2 解析 DDR2 SPD。
//
// 注意: 原版 C# 移植前的 Go 版本把 byte8(接口电压)当总线位宽、byte6(模块数据宽度)
// 当芯片位宽, 容量算错。依据 JEDEC DDR2 SPD:
// byte6 = 模块数据宽度(总线, 含 ECC 的低 4 位需屏蔽), byte13 = 芯片位宽,
// byte63 = 校验和(sum of bytes 0-62), byte64-71 = JEP106 厂商 ID(0x7F 为续延码),
// byte72 = 生产地点, byte73-90 = 部件号, byte91-92 = 修订码,
// byte93 = 年(自 2000), byte94 = 周, byte95-98 = 序列号。
func parseDDR2(dump []byte, rt RamType) (*Basic, error) {
	b := &Basic{Type: rt}
	b.Ranks = subByteR(dump[5], 2, 3) + 1
	b.BusWidthBits = dump[6] & 0xF0 // 屏蔽 ECC 扩展低 4 位
	b.DeviceWidth = dump[13]

	// 容量: 阵列位 = 2^rows × 2^cols × banks; 模块 = 阵列/8 × (总线/芯片位宽) × rank
	rows, cols := uint(dump[3]), uint(dump[4])
	banks := uint64(dump[17])
	if rows > 0 && rows < 32 && cols > 0 && cols < 32 && b.DeviceWidth > 0 {
		arrayBits := (uint64(1) << rows) * (uint64(1) << cols) * banks
		totalBytes := arrayBits / 8 * uint64(b.BusWidthBits) / uint64(b.DeviceWidth) * uint64(b.Ranks)
		b.BytesMib = totalBytes / 1024 / 1024
	}

	// JEP106: 连续的 0x7F 是 continuation(校验位已含在码里)
	for i := 64; i <= 71; i++ {
		if dump[i] == 0x7F {
			b.ManufacturerCont++
			continue
		}
		b.ManufacturerCode = dump[i]
		break
	}
	b.Manufacturer = ManufacturerName(b.ManufacturerCont, b.ManufacturerCode)
	b.Location = dump[72]
	b.PartNumber = asciiString(dump[73:91])
	b.Revision = uint16(dump[92]) | uint16(dump[91])<<8
	b.DateYear, b.DateWeek = 2000+int(dump[93]), int(dump[94])
	b.SerialHex = fmt.Sprintf("%02X%02X%02X%02X", dump[95], dump[96], dump[97], dump[98])

	// DDR2 校验和: byte63 = sum(bytes 0..62) & 0xFF(不是 CRC16)
	sum := byte(0)
	for _, v := range dump[:63] {
		sum += v
	}
	b.CRCOK = sum == dump[63]
	b.ChecksumOK = b.CRCOK

	// tCKmin = 整数 nibble + 十分位 nibble
	b.TCKminNS = float64(subByteR(dump[9], 7, 4)) + float64(subByteR(dump[9], 3, 4))/10
	return b, nil
}

func b23BusWidth(dump []byte) (ext bool, primary byte) {
	return getBit(dump[8], 3), 1 << (subByteR(dump[8], 2, 3) + 3)
}
