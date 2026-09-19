// Package spd 解析 SPD 序列化数据。纯函数,零硬件依赖。
package spd

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data/idcodes.json
var idcodesFS embed.FS

// RamType 是 SPD byte2 的 DRAM 类型(值与 SPD 标准/原版一致)。
type RamType byte

const (
	Unknown      RamType = 0x00
	SDRAM        RamType = 0x04
	DDR          RamType = 0x07
	DDR2         RamType = 0x08
	DDR2FBDIMM   RamType = 0x09
	DDR2FBDIMMP  RamType = 0x0A
	DDR3         RamType = 0x0B
	DDR4         RamType = 0x0C
	LPDDR3       RamType = 0x0F
	DDR4E        RamType = 0x0E
	LPDDR4       RamType = 0x10
	LPDDR4X      RamType = 0x11
	DDR5         RamType = 0x12
	LPDDR5       RamType = 0x13
	DDR5NVDIMMP  RamType = 0x14
	LPDDR5X      RamType = 0x15
)

var ramTypeNames = map[RamType]string{
	SDRAM: "SDRAM", DDR: "DDR", DDR2: "DDR2", DDR2FBDIMM: "DDR2 FB-DIMM",
	DDR2FBDIMMP: "DDR2 FB-DIMM Probe", DDR3: "DDR3", DDR4: "DDR4", DDR4E: "DDR4E",
	LPDDR3: "LPDDR3", LPDDR4: "LPDDR4", LPDDR4X: "LPDDR4X", DDR5: "DDR5",
	LPDDR5: "LPDDR5", DDR5NVDIMMP: "DDR5 NVDIMM-P", LPDDR5X: "LPDDR5X",
}

// String 返回类型的中性英文名(UI 直接展示)。
func (r RamType) String() string {
	if n, ok := ramTypeNames[r]; ok {
		return n
	}
	return fmt.Sprintf("未知(%#x)", byte(r))
}

// SPDSize 返回该类型的 SPD 序列总大小。未知类型 256(原版同款保守值)。
func (r RamType) SPDSize() int {
	switch r {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		return 512
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		return 1024
	default:
		return 256
	}
}

// Identify 从 dump 识别 RAM 类型与 SPD 大小。
// dump 至少 3 字节; 返回的类型字节无效时 Unknown。
func Identify(dump []byte) (RamType, int, error) {
	if len(dump) < 3 {
		return Unknown, 0, fmt.Errorf("数据过短(%d 字节)", len(dump))
	}
	rt := RamType(dump[2])
	if _, ok := ramTypeNames[rt]; !ok {
		rt = Unknown
	}
	return rt, rt.SPDSize(), nil
}

// ValidateSpd 校验 dump 是否像一份合法 SPD(长度与类型匹配)。
func ValidateSpd(dump []byte) bool {
	if len(dump) < 256 {
		return false
	}
	rt := RamType(dump[2])
	switch rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		return len(dump) == 512
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		return len(dump) == 1024
	case SDRAM, DDR, DDR2, DDR2FBDIMM, DDR2FBDIMMP, DDR3:
		return len(dump) == 256
	default:
		return false
	}
}

// Crc16 计算 SPD CRC16(CCITT 0x1021, 初值 0,无反射)——SPD DDR3/4/5 标准算法。
func Crc16(data []byte) uint16 {
	crc := uint16(0)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// BCD 将 BCD 码字节转十进制(模块日期用)。
func BCD(b byte) int { return int(b>>4)*10 + int(b&0x0F) }

// asciiString 提取定长 ASCII 字段并去尾部空白。
func asciiString(b []byte) string {
	return strings.TrimRight(string(b), " \x00")
}

// ---------------- JEDEC 厂商表 ----------------

var (
	idcodesOnce  func() // 延迟加载
	idcodesTable [][]string
	idcodesErr   error
)

func init() { idcodesOnce = syncOnce() }

func syncOnce() func() {
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		raw, err := idcodesFS.ReadFile("data/idcodes.json")
		if err != nil {
			idcodesErr = err
			return
		}
		if err := json.Unmarshal(raw, &idcodesTable); err != nil {
			idcodesErr = err
		}
	}
}

// ManufacturerName 由 continuation code + 厂商码查询厂商名。
// 忽略 MSB 奇偶位; 码 0 或越界返回空串。
func ManufacturerName(cont, code byte) string {
	idcodesOnce()
	if idcodesErr != nil || cont >= byte(len(idcodesTable)) {
		return ""
	}
	idx := int(code & 0x7F)
	if idx == 0 {
		return ""
	}
	names := idcodesTable[cont]
	if idx <= len(names) {
		return names[idx-1]
	}
	return ""
}

// FindManufacturer 按名(不区分大小写,完整匹配)查厂商,返回 continuation/码(含奇偶位,按原版规则补齐)。
func FindManufacturer(name string) (cont, code byte, ok bool) {
	idcodesOnce()
	if idcodesErr != nil {
		return 0, 0, false
	}
	for i, names := range idcodesTable {
		for j, n := range names {
			if strings.EqualFold(n, name) {
				cc := byte(j + 1)
				// 原版: code | (奇校验 << 7)
				if parityOdd(cc) {
					cc |= 0x80
				}
				return byte(i), cc, true
			}
		}
	}
	return 0, 0, false
}

// parityOdd 返回为使 1 的个数为奇数,MSB 是否应置 1(原版 GetParity(Odd) 语义)。
func parityOdd(b byte) bool {
	n := 0
	for v := b; v != 0; v &= v - 1 {
		n++
	}
	return n%2 == 0 // 1 的个数已是奇数则 MSB=0; 偶数则 MSB=1
}
