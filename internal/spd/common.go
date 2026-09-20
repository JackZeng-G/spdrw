// Package spd 解析 SPD 序列化数据。纯函数,零硬件依赖。
package spd

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed data/idcodes.json
var idcodesFS embed.FS

// RAMType 是 SPD byte2 的 DRAM 类型(值与 SPD 标准/原版一致)。
type RAMType byte

const (
	Unknown     RAMType = 0x00
	SDRAM       RAMType = 0x04
	DDR         RAMType = 0x07
	DDR2        RAMType = 0x08
	DDR2FBDIMM  RAMType = 0x09
	DDR2FBDIMMP RAMType = 0x0A
	DDR3        RAMType = 0x0B
	DDR4        RAMType = 0x0C
	LPDDR3      RAMType = 0x0F
	DDR4E       RAMType = 0x0E
	LPDDR4      RAMType = 0x10
	LPDDR4X     RAMType = 0x11
	DDR5        RAMType = 0x12
	LPDDR5      RAMType = 0x13
	DDR5NVDIMMP RAMType = 0x14
	LPDDR5X     RAMType = 0x15
)

var ramTypeNames = map[RAMType]string{
	SDRAM: "SDRAM", DDR: "DDR", DDR2: "DDR2", DDR2FBDIMM: "DDR2 FB-DIMM",
	DDR2FBDIMMP: "DDR2 FB-DIMM Probe", DDR3: "DDR3", DDR4: "DDR4", DDR4E: "DDR4E",
	LPDDR3: "LPDDR3", LPDDR4: "LPDDR4", LPDDR4X: "LPDDR4X", DDR5: "DDR5",
	LPDDR5: "LPDDR5", DDR5NVDIMMP: "DDR5 NVDIMM-P", LPDDR5X: "LPDDR5X",
}

// String 返回类型的中性英文名(UI 直接展示)。
func (r RAMType) String() string {
	if n, ok := ramTypeNames[r]; ok {
		return n
	}
	return fmt.Sprintf("未知(%#x)", byte(r))
}

// SPDSize 返回该类型的 SPD 序列总大小。未知类型 256(原版同款保守值)。
func (r RAMType) SPDSize() int {
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
func Identify(dump []byte) (RAMType, int, error) {
	if len(dump) < 3 {
		return Unknown, 0, fmt.Errorf("数据过短(%d 字节)", len(dump))
	}
	rt := RAMType(dump[2])
	if _, ok := ramTypeNames[rt]; !ok {
		rt = Unknown
	}
	return rt, rt.SPDSize(), nil
}

// RAMTypeFromByte 把 SPD byte2(器件类型) 映射成 RAMType; 未知类型返回 Unknown。
// 设备侧只有这一个字节可用来判定世代, 写入前必须与待写内容的世代对照。
func RAMTypeFromByte(b byte) RAMType {
	rt := RAMType(b)
	if _, ok := ramTypeNames[rt]; !ok {
		return Unknown
	}
	return rt
}

// ValidateSpd 校验 dump 是否像一份合法 SPD(长度与类型匹配)。
func ValidateSpd(dump []byte) bool {
	if len(dump) < 256 {
		return false
	}
	rt := RAMType(dump[2])
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
	idcodesTable [][]string
	idcodesErr   error
	// sync.OnceValue: 手写的 once 布尔量在并发下有数据竞争(审计用 -race 复现:
	// 一个 goroutine 正在 json.Unmarshal 写表, 另一个已经在读 —— 可能返回空厂商名)。
	idcodesOnce = sync.OnceValue(func() error {
		raw, err := idcodesFS.ReadFile("data/idcodes.json")
		if err != nil {
			idcodesErr = err
			return err
		}
		if err := json.Unmarshal(raw, &idcodesTable); err != nil {
			idcodesErr = err
			return err
		}
		return nil
	})
)

// ManufacturerName 由 continuation code + 厂商码查询厂商名。
//
// 两个字节的 bit7 都是 **奇校验位**(JEP106): 实测 Micron/Samsung/SK Hynix 的
// continuation 字节是 0x80(计数 0 + 校验位)、Kingston 是 0x01(计数 1)、
// Crucial 0x85(计数 5)、Corsair 0x02(计数 2) —— 全都是"计数 | 校验位"。
// 不屏蔽 bit7 会把这些厂商全部查不到名字(旧实现就把 cont=0x80 当越界直接返回空串)。
func ManufacturerName(cont, code byte) string {
	_ = idcodesOnce()
	bank := cont & 0x7F
	if idcodesErr != nil || bank >= byte(len(idcodesTable)) {
		return ""
	}
	idx := int(code & 0x7F)
	if idx == 0 {
		return ""
	}
	names := idcodesTable[bank]
	if idx <= len(names) {
		return names[idx-1]
	}
	return ""
}

// FindManufacturer 按名(不区分大小写,完整匹配)查厂商,返回 continuation/码(含奇偶位,按原版规则补齐)。
func FindManufacturer(name string) (cont, code byte, ok bool) {
	_ = idcodesOnce()
	if idcodesErr != nil {
		return 0, 0, false
	}
	for i, names := range idcodesTable {
		for j, n := range names {
			if strings.EqualFold(n, name) {
				cc := byte(j + 1)
				// 厂商码与 continuation 字节都带奇校验位(实测数据一致)
				if parityOdd(cc) {
					cc |= 0x80
				}
				cont := byte(i)
				if parityOdd(cont) {
					cont |= 0x80
				}
				return cont, cc, true
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

// ManufacturerIDNote 在厂商 ID 无法解析时给出可读原因(空串表示解析成功)。
// 真实 dump 里出现过续延字节校验位不成立、银行号超范围的情况(如 KLEVV 实物条
// 写成 cont=0x18/code=0x98), 与其静默显示空白, 不如把原因告诉用户。
func ManufacturerIDNote(cont, code byte) string {
	if ManufacturerName(cont, code) != "" {
		return ""
	}
	switch {
	case cont == 0 && code == 0:
		return "厂商 ID 未写入(0x00/0x00)"
	case code&0x7F == 0:
		return fmt.Sprintf("厂商码为 0(原始 %#02x/%#02x)", cont, code)
	}
	bank := cont & 0x7F
	if !parityOK(cont) {
		return fmt.Sprintf("厂商 ID %#02x/%#02x 无法定位: 续延字节 %#02x 的奇校验不成立(应为 %#02x), 推得的银行号 %d 超出 JEP106 表范围",
			cont, code, cont, cont|0x80, bank)
	}
	return fmt.Sprintf("厂商 ID %#02x/%#02x 不在当前 JEP106 表内(银行 %d, 厂商码 %d)", cont, code, bank, code&0x7F)
}

// parityOK 报告字节是否满足 JEP106 的奇校验(1 的个数为奇数)。
func parityOK(b byte) bool {
	n := 0
	for v := b; v != 0; v &= v - 1 {
		n++
	}
	return n%2 == 1
}
