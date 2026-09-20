package spd

// 位操作与共享结构(对应上游实现 Data.cs 的 SubByte/GetBit 与 Spd.Timing)。

// subByte 取 value 的 [offset 位起, count 位] 字段(低位优先)。
func subByte(value byte, offset, count int) byte {
	if offset >= 8 {
		return 0
	}
	if offset+count > 8 {
		count = 8 - offset
	}
	return byte(value>>offset) & byte((1<<count)-1)
}

// subByteR 按上游实现 C# Data.SubByte(input, position, count) 语义取位:
// 从 position 位起向右数 count 位, 即 [position-count+1 .. position]。
// 对照字段时沿用同一套位域参数(位置/位数直接照搬文档值), 避免手工换算出错。
func subByteR(value byte, position, count int) byte {
	return subByte(value, position-count+1, count)
}

// getBit 取 value 的第 bit 位。
func getBit(value byte, bit int) bool {
	return value&(1<<bit) != 0
}

// getBit32 取 32 位值的第 bit 位。
func getBit32(value uint32, bit int) bool {
	return value&(1<<bit) != 0
}

// Timebase 是 SPD 时间基准(皮秒)。DDR4: byte 15; 125ps / 1ps。
type Timebase struct {
	Medium int
	Fine   int
}

// Timing 是 medium+fine 双粒度时序值(皮秒组合)。
type Timing struct {
	Medium int // medium 粒度计数
	Fine   int // fine 粒度计数(有符号)
}

// NanoSeconds 转换为纳秒(浮点)。
func (t Timing) NanoSeconds(tb Timebase) float64 {
	return float64(t.Medium*tb.Medium+t.Fine*tb.Fine) / 1000
}

// ClockCycles 换算为相对 tCK 的周期数(向上取整)。
func (t Timing) ClockCycles(tb Timebase, tck Timing) int {
	tckNS := tck.NanoSeconds(tb)
	if tckNS <= 0 {
		return 0
	}
	return int((t.NanoSeconds(tb) + tckNS - 1e-9) / tckNS)
}

// MegaHertz 由 tCK 换算频率。
func (t Timing) MegaHertz(tb Timebase) float64 {
	ns := t.NanoSeconds(tb)
	if ns <= 0 {
		return 0
	}
	return 1000 / ns
}

// DDR4Timebase 计算 DDR4 时间基准(byte 15)。
// DDR4Timebase 解析时间基准(byte 15)。dump 不足 16 字节时返回默认基准而不是 panic
// (导出 API, 审计 L1: 短输入会越界)。
func DDR4Timebase(dump []byte) Timebase {
	tb := Timebase{Medium: 125, Fine: 1}
	if len(dump) < 16 {
		return tb
	}
	tb = Timebase{}
	if subByteR(dump[15], 3, 2) == 0 {
		tb.Medium = 125
	}
	if subByteR(dump[15], 1, 2) == 0 {
		tb.Fine = 1
	}
	return tb
}

// CasLatencies 是 CAS 掩码数据。
type CasLatencies struct {
	Bitmask   uint32
	HighRange bool
}

// ToArray 展开为支持的 CL 值列表。
func (c CasLatencies) ToArray() []int {
	var out []int
	for i := 0; i < 29; i++ {
		if getBit32(c.Bitmask, i) {
			out = append(out, i+7+map[bool]int{true: 16, false: 0}[c.HighRange])
		}
	}
	return out
}

func (c CasLatencies) String() string {
	s := ""
	for _, l := range c.ToArray() {
		s += itoa(l) + ","
	}
	return trimRight(s, ',')
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func trimRight(s string, c byte) string {
	for len(s) > 0 && s[len(s)-1] == c {
		s = s[:len(s)-1]
	}
	return s
}
