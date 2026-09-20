package spd

import "fmt"

// DDR3 基本信息解析(对照参考项目 DDR3.cs 的关键字段)。
// DDR2 支持已移除(太老, 无真实语料); byte2=0x08-0x0A 现在按 Unknown 处理。

// Basic 是旧世代(DDR3)的基本信息。
type Basic struct {
	Type         RAMType `json:"type"`
	ModuleType   string  `json:"moduleType"` // UDIMM/SO-DIMM/...
	BytesMib     uint64  `json:"bytesMib"`   // 模块总容量(MiB)
	BusWidthBits byte    `json:"busWidthBits"`
	Ranks        byte    `json:"ranks"`
	DeviceWidth  byte    `json:"deviceWidth"`
	PartNumber   string  `json:"partNumber"`
	Manufacturer string  `json:"manufacturer"`
	CRCOK        bool    `json:"crcOk"`
	TCKminNS     float64 `json:"tckminNs"`

	// 身份区(编辑用)
	ManufacturerCont byte   `json:"manufacturerCont"` // JEP106 continuation
	ManufacturerCode byte   `json:"manufacturerCode"` // JEP106 code(含奇校验位)
	Location         byte   `json:"location"`         // 生产地点
	DateYear         int    `json:"dateYear"`         // 生产年(4 位; 2000+BCD(byte120))
	DateWeek         int    `json:"dateWeek"`         // 生产周
	SerialHex        string `json:"serialHex"`        // 序列号(16 进制)
	Revision         uint16 `json:"revision"`         // 模块修订码
	ChecksumOK       bool   `json:"checksumOk"`       // = CRCOK(DDR3 用 CRC16)
}

var ddr3ModuleTypeNames = map[byte]string{
	0x00: "FPDIMM",
	0x01: "RDIMM",
	0x02: "UDIMM",
	0x03: "SO-DIMM",
	0x04: "Micro-DIMM",
	0x05: "Mini-RDIMM",
	0x06: "Mini-UDIMM",
	0x07: "Mini-UDIMM",
}

// ParseBasic 解析 DDR3 dump(256 字节)。其他世代返回错误。
func ParseBasic(dump []byte) (*Basic, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	// 长度必须与该世代一致: 少于 256 字节会在下面的固定偏移处越界 panic
	// (审计发现: 3 字节的 dump 只要 byte2 是 0x0B 就能让 ParseBasic 崩掉)。
	if len(dump) != size {
		return nil, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	if rt != DDR3 {
		return nil, fmt.Errorf("不支持的世代 %v (DDR4/DDR5 请用专用解析)", rt)
	}
	return parseDDR3(dump)
}

func parseDDR3(dump []byte) (*Basic, error) {
	b := &Basic{Type: DDR3}
	if name, ok := ddr3ModuleTypeNames[subByteR(dump[3], 3, 4)]; ok {
		b.ModuleType = name
	} else {
		b.ModuleType = fmt.Sprintf("未知(%#x)", subByteR(dump[3], 3, 4))
	}
	b.Ranks = subByteR(dump[7], 5, 3) + 1
	b.DeviceWidth = 4 << subByteR(dump[7], 2, 3)
	_, b.BusWidthBits = ddr3BusWidth(dump)

	// 容量: 单 die 容量(Mb) × 总线位宽/芯片位宽 × rank (参考项目 TotalModuleCapacityProgrammed)
	capPerDieMb := uint64(1) << (subByteR(dump[4], 3, 4) + 8)
	if b.DeviceWidth == 0 || b.Ranks == 0 { // 保留编码: 位宽 code>=6 会截断成 0, 别除
		b.BytesMib = 0
	} else {
		b.BytesMib = capPerDieMb / 8 * uint64(b.BusWidthBits) / uint64(b.DeviceWidth) * uint64(b.Ranks)
	}

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

func ddr3BusWidth(dump []byte) (ext bool, primary byte) {
	return getBit(dump[8], 3), 1 << (subByteR(dump[8], 2, 3) + 3)
}
