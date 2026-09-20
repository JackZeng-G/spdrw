// Package spd 的编辑层: 对一份 dump 做纯内存编辑(零 I/O), 生成差异列表。
//
// 设计原则:
//   - 编辑只改内存字节, 绝不触发任何 I/O; 写设备是 app 层的独立动作(走预检/备份/确认)
//   - 每个字段都有键(key)、单位、合法范围与风险级别, 前端据此生成表单
//   - CRC/校验和永远由编辑器重算(编辑后 FixCRC), 不会让用户手填
package spd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// EditChange 是一个字节级变更(编辑结果)。
type EditChange struct {
	Offset int    `json:"offset"`
	Old    byte   `json:"old"`
	New    byte   `json:"new"`
	Field  string `json:"field"`
	Risk   string `json:"risk"`
}

// Field 是一个可编辑字段(供 UI 生成表单)。
type Field struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Group  string  `json:"group"`
	Kind   string  `json:"kind"` // int / float / string / hex / bool / select
	Unit   string  `json:"unit,omitempty"`
	Min    float64 `json:"min,omitempty"`
	Max    float64 `json:"max,omitempty"`
	Step   float64 `json:"step,omitempty"`
	Value  string  `json:"value"`
	Offset string  `json:"offset"`
	Risk   string  `json:"risk"`
	// Primary 标记"常用/关键"字段: 界面默认只显示这些, 其余收在"显示全部"后面,
	// 免得一次铺满几十行(用户反馈: JEDEC 时序优先显示重要的)。
	Primary bool     `json:"primary,omitempty"`
	Note    string   `json:"note,omitempty"`
	Params  []string `json:"params,omitempty"` // 附加参数(如 XMP profile 名/槽位)
}

// Editor 是对一份 dump 的可编辑视图。
type Editor struct {
	original []byte
	dump     []byte
	rt       RamType
	changes  map[int]EditChange // offset → 变更(保留首次的 Old)
	order    []int
}

// NewEditor 构造编辑器(dump 会被复制, 原数据不被修改)。
func NewEditor(dump []byte) (*Editor, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	if len(dump) != size {
		return nil, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	if rt == Unknown {
		return nil, fmt.Errorf("未知的 SPD 类型(byte2 = %#x)", dump[2])
	}
	cp := make([]byte, len(dump))
	copy(cp, dump)
	return &Editor{original: cp, dump: append([]byte{}, cp...), rt: rt, changes: map[int]EditChange{}}, nil
}

// RamType 返回 SPD 世代。
func (e *Editor) RamType() RamType { return e.rt }

// Size 返回 SPD 字节数。
func (e *Editor) Size() int { return len(e.dump) }

// Bytes 返回工作副本(调用方不应修改)。
func (e *Editor) Bytes() []byte { return e.dump }

// Reset 放弃全部编辑, 回到初始内容。
func (e *Editor) Reset() {
	e.dump = append([]byte{}, e.original...)
	e.changes = map[int]EditChange{}
	e.order = nil
}

// IsDirty 报告是否有未保存的编辑。
func (e *Editor) IsDirty() bool { return len(e.changes) > 0 }

// Changes 返回全部变更(按偏移排序)。
func (e *Editor) Changes() []EditChange {
	out := make([]EditChange, 0, len(e.changes))
	for _, c := range e.changes {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// GetByte 读取一个字节。
func (e *Editor) GetByte(off int) (byte, error) {
	if off < 0 || off >= len(e.dump) {
		return 0, fmt.Errorf("偏移 %d 越界(共 %d 字节)", off, len(e.dump))
	}
	return e.dump[off], nil
}

// SetByte 原始编辑: 直接改一个字节(对应"hex 任意字节编辑")。
func (e *Editor) SetByte(off int, v byte) error {
	return e.set(off, v, "raw", "high")
}

// SetBytes 原始编辑: 连续写一段字节。
func (e *Editor) SetBytes(off int, vals []byte) error {
	if off < 0 || off+len(vals) > len(e.dump) {
		return fmt.Errorf("范围 %d+%d 越界(共 %d 字节)", off, len(vals), len(e.dump))
	}
	for i, v := range vals {
		if err := e.SetByte(off+i, v); err != nil {
			return err
		}
	}
	return nil
}

// set 记录一次字节变更(内部统一入口, 保证 changes 与 dump 同步)。
func (e *Editor) set(off int, v byte, field, risk string) error {
	if off < 0 || off >= len(e.dump) {
		return fmt.Errorf("偏移 %d 越界(共 %d 字节)", off, len(e.dump))
	}
	old := e.dump[off]
	if old == v {
		return nil
	}
	// 改回原始值 = 没有变更: 必须把变更项删掉, 否则界面会谎报"有改动",
	// 点写入还会得到"写入并校验通过: 0 字节"这种假成功。
	if v == e.original[off] {
		delete(e.changes, off)
		for i, o := range e.order {
			if o == off {
				e.order = append(e.order[:i], e.order[i+1:]...)
				break
			}
		}
		e.dump[off] = v
		return nil
	}
	if _, seen := e.changes[off]; !seen {
		e.changes[off] = EditChange{Offset: off, Old: old, New: v, Field: field, Risk: risk}
		e.order = append(e.order, off)
	} else {
		c := e.changes[off]
		c.New = v
		// 同一字节被多个字段改动时, 保留更高风险/更具体的字段名
		if c.Field == "raw" && field != "raw" {
			c.Field, c.Risk = field, risk
		}
		e.changes[off] = c
	}
	e.dump[off] = v
	return nil
}

// setSubByteR 与解析层的 subByteR 对称: 把 b 的 [pos-count+1..pos] 位设为 v。
func setSubByteR(b byte, pos, count int, v byte) byte {
	shift := pos - count + 1
	mask := byte((1<<count)-1) << shift
	return (b &^ mask) | ((v << shift) & mask)
}

// encodeTiming 把纳秒值编码为 medium+fine 双粒度(用于 DDR3/DDR4 单字节 medium 字段)。
func encodeTiming(ns float64, tb Timebase) (medium, fine int, err error) {
	return encodeTimingMax(ns, tb, 255)
}

// encodeTimingMax 同上, 但允许 16 位 medium 字段(如 tRFC1/tRFC2/tRFC4)。
func encodeTimingMax(ns float64, tb Timebase, maxMedium int) (medium, fine int, err error) {
	if ns <= 0 || math.IsNaN(ns) || math.IsInf(ns, 0) {
		return 0, 0, fmt.Errorf("时间必须为正数")
	}
	if tb.Medium <= 0 {
		return 0, 0, fmt.Errorf("时间基准 MTB 无效")
	}
	totalPS := ns * 1000
	// JEDEC 约定: medium 向上取整, 修正量(FTB)多为负值或 0
	// ("the medium time base number is usually rounded up and the correction is negative")
	// 用向下取整会得到 (medium-1, 正 fine) 的等价但不同的字节, 破坏"设回原值不变"。
	medium = int(math.Ceil(totalPS / float64(tb.Medium)))
	rem := totalPS - float64(medium*tb.Medium)
	if tb.Fine > 0 {
		fine = int(math.Round(rem / float64(tb.Fine)))
		for fine > 127 {
			medium++
			fine -= tb.Medium / tb.Fine
		}
		for fine < -128 {
			medium--
			fine += tb.Medium / tb.Fine
		}
	}
	if medium < 0 || medium > maxMedium {
		return 0, 0, fmt.Errorf("%.3f ns 超出该字段可表示范围(medium=%d, 上限 %d)", ns, medium, maxMedium)
	}
	return medium, fine, nil
}

// timingNS 由 medium+fine 还原纳秒。
func timingNS(medium, fine int, tb Timebase) float64 {
	return float64(medium*tb.Medium+fine*tb.Fine) / 1000
}

// ---------------- CRC ----------------

// FixCRC 重算全部 CRC/校验和, 返回被修改的偏移数。
func (e *Editor) FixCRC() (int, error) {
	touched, err := FixCRC(e.dump)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, off := range touched {
		if off < 0 || off >= len(e.dump) {
			continue
		}
		old := e.original[off]
		if old != e.dump[off] {
			n++
		}
	}
	e.recomputeChanges()
	return n, nil
}

// CRCOK 报告当前内容 CRC 是否通过。
func (e *Editor) CRCOK() bool {
	ok, err := CRCOK(e.dump)
	return err == nil && ok
}

// recomputeChanges 依据 original↔dump 的差异重建变更表(CRC 重算后调用)。
func (e *Editor) recomputeChanges() {
	prev := e.changes
	e.changes = map[int]EditChange{}
	e.order = nil
	for i := range e.dump {
		if e.dump[i] == e.original[i] {
			continue
		}
		c := EditChange{Offset: i, Old: e.original[i], New: e.dump[i], Field: "CRC/校验和", Risk: "low"}
		if pc, ok := prev[i]; ok && pc.Old == e.original[i] {
			c.Field, c.Risk = pc.Field, pc.Risk
		}
		e.changes[i] = c
		e.order = append(e.order, i)
	}
}

// ---------------- 通用字符串/数值工具 ----------------

// setASCII 写入定长 ASCII 字段(超长报错)。
// 填充策略: 新值之后的字节沿用原始 dump 的填充字符(0x00 或 0x20), 其余补空格 ——
// 这样"改成新值再改回原值"能回到逐字节一致, 不会把 0x00 填充改成 0x20。
func (e *Editor) setASCII(off, n int, s string, field, risk string) error {
	if len(s) > n {
		return fmt.Errorf("%s 最多 %d 个字符(当前 %d)", field, n, len(s))
	}
	buf := make([]byte, n)
	for i := range buf {
		pad := byte(' ')
		if off+i < len(e.original) && (e.original[off+i] == 0x00 || e.original[off+i] == 0x20) {
			pad = e.original[off+i]
		}
		buf[i] = pad
	}
	copy(buf, []byte(s))
	for i := 0; i < n; i++ {
		if err := e.set(off+i, buf[i], field, risk); err != nil {
			return err
		}
	}
	return nil
}

// setHexBytes 写入十六进制字符串表示的字节序列(如序列号 "DEADBEEF")。
func (e *Editor) setHexBytes(off, n int, s, field, risk string) error {
	clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	clean = strings.ReplaceAll(clean, " ", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, ":", "")
	if clean == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if len(clean)%2 != 0 {
		clean = "0" + clean
	}
	if len(clean)/2 > n {
		return fmt.Errorf("%s 最多 %d 字节(当前 %d)", field, n, len(clean)/2)
	}
	vals := make([]byte, n)
	for i := 0; i < n; i++ {
		vals[i] = 0
	}
	for i := 0; i < len(clean)/2; i++ {
		v, err := strconv.ParseUint(clean[i*2:i*2+2], 16, 8)
		if err != nil {
			return fmt.Errorf("%s 不是合法的十六进制: %q", field, s)
		}
		vals[i] = byte(v)
	}
	for i := 0; i < n; i++ {
		if err := e.set(off+i, vals[i], field, risk); err != nil {
			return err
		}
	}
	return nil
}

// setBCD 写入一个 BCD 字节(如日期)。
func (e *Editor) setBCD(off int, v int, field, risk string) error {
	if v < 0 || v > 99 {
		return fmt.Errorf("%s 必须在 0-99 之间(BCD)", field)
	}
	return e.set(off, byte((v/10)<<4|(v%10)), field, risk)
}

// hexOf 把字节序列格式化为十六进制字符串。
func hexOf(b []byte) string {
	var sb strings.Builder
	for _, v := range b {
		fmt.Fprintf(&sb, "%02X", v)
	}
	return sb.String()
}
