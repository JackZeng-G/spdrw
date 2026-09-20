package smbus

import (
	"fmt"
	"strings"
	"sync"
)

// OpKind 是记录下来的 SMBus 事务类型。
type OpKind string

const (
	OpQuick         OpKind = "quick"
	OpReadByteData  OpKind = "read-byte"
	OpWriteByteData OpKind = "write-byte"
	OpWriteByte     OpKind = "write-byte-nodata"
	OpReadWordData  OpKind = "read-word"
	OpReadBlock     OpKind = "read-block"
	OpWriteBlock    OpKind = "write-block"
)

// Op 是一次 SMBus 事务的记录(Err 非空表示该事务失败)。
type Op struct {
	Kind  OpKind `json:"kind"`
	Addr  byte   `json:"addr"`
	Cmd   byte   `json:"cmd"`
	Val   byte   `json:"val"`
	Write bool   `json:"write"`
	Err   string `json:"err,omitempty"`
}

// RecordingTransport 包装任意 Transport, 按序记录全部事务, 并支持故障注入。
//
// 用于写入路径的严格离线测试: 断言"到底写了哪些字节/写了多少字节", 以及模拟
// 写保护块(NACK)、第 N 次写失败(半写中止)等真实硬件故障场景。
// 它不改变任何语义, 只做转发与记录。
type RecordingTransport struct {
	Inner Transport

	mu  sync.Mutex
	ops []Op

	// FailWriteAt > 0 时, 第 N 次数据写(byte-data/byte, 不含 quick)失败。
	FailWriteAt int
	// FailWriteFrom > 0 时, 第 N 次及之后的写全部失败(模拟持续故障/还原失败)。
	FailWriteFrom int
	// FailWriteOnce > 0 时, 只让第 N 次数据写失败一次(模拟偶发 NACK/超时) ——
	// 之后的写恢复正常, 用于验证"失败→自动回滚"能真正把内容还原。
	FailWriteOnce int
	// FailWriteCmdFilter 非 nil 时, 只有满足条件的写才计数/注入失败。
	// DDR5 用得上: MR 寄存器写(切页 MR11 等, cmd bit7=0)不算 NVM 写入,
	// 让它参与计数会把"第 N 次数据写"算错。
	FailWriteCmdFilter func(cmd byte) bool
	ErrInjected        error
	// DenyWriteFrom >= 0 时, cmd >= 该值的写事务返回 NACK(模拟写保护块)。
	DenyWriteFrom int
	// BanQuickWrite 为 true 时所有 quick 写事务返回 NACK(模拟控制器不支持快速命令)。
	BanQuickWrite bool
	// ddr5 非 nil 时用于 NVMWrites 的世代判据(默认从内层 Fake 推断)。
	ddr5 *bool
}

// NewRecording 包装一个 Transport 用于记录与故障注入。
func NewRecording(inner Transport) *RecordingTransport {
	return &RecordingTransport{Inner: inner, DenyWriteFrom: -1}
}

// NewRecordingFake 返回包装了内存 Fake 的记录器(最常用的测试组合)。
func NewRecordingFake() (*RecordingTransport, *FakeTransport) {
	f := NewFake()
	return NewRecording(f), f
}

func (r *RecordingTransport) record(op Op) {
	r.mu.Lock()
	r.ops = append(r.ops, op)
	r.mu.Unlock()
}

// Ops 返回全部事务记录的副本。
func (r *RecordingTransport) Ops() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Op, len(r.ops))
	copy(out, r.ops)
	return out
}

// Writes 返回全部写事务(quick 写 + byte 写 + byte-data 写)。
func (r *RecordingTransport) Writes() []Op {
	var out []Op
	for _, op := range r.Ops() {
		if op.Write {
			out = append(out, op)
		}
	}
	return out
}

// WriteCount 返回写事务总数。
func (r *RecordingTransport) WriteCount() int { return len(r.Writes()) }

// DataWrites 返回数据写事务(排除 quick 命令: 页选择/写保护命令不算数据写入)。
// 干跑模式断言"零数据写"时用这个。
func (r *RecordingTransport) DataWrites() []Op {
	var out []Op
	for _, op := range r.Writes() {
		if op.Kind != OpQuick {
			out = append(out, op)
		}
	}
	return out
}

// NVMWrites 返回真正的 NVM 数据写事务(仅 DDR5 语义: cmd bit7=1 为 NVM 窗口;
// DDR5 的 MR 寄存器写/切页写成 bit7=0, 不属于 NVM 写入)。
// NVMWrites 返回对 SPD NVM 的数据写(测试断言用)。
//
// 判据随世代不同: DDR5 看 cmd bit7(NVM 窗口); DDR4 及更早所有 byte-data/块写都是 NVM。
// 以前无条件用 DDR5 判据, 在 DDR4 fixture 上会把 NVM 写数成 0(审计指出的少报),
// 于是"零 NVM 写"这类断言可能被虚过。世代默认从内层 Fake 推断, 也可显式设置。
func (r *RecordingTransport) NVMWrites() []Op {
	return r.NVMWritesFor(r.ddr5Mode())
}

// NVMWritesFor 按指定世代统计 NVM 写。
func (r *RecordingTransport) NVMWritesFor(ddr5 bool) []Op {
	var out []Op
	for _, op := range r.Writes() {
		if op.Kind != OpWriteByteData && op.Kind != OpWriteBlock {
			continue
		}
		if op.Addr < 0x50 || op.Addr > 0x57 {
			continue
		}
		if ddr5 {
			if op.Cmd&0x80 != 0 {
				out = append(out, op)
			}
			continue
		}
		if op.Cmd == 0 && op.Val == 0 { // quick/无数据写
			continue
		}
		out = append(out, op)
	}
	return out
}

// ddr5Mode 推断当前是否 DDR5: 显式设置优先, 否则看内层 Fake。
func (r *RecordingTransport) ddr5Mode() bool {
	if r.ddr5 != nil {
		return *r.ddr5
	}
	if f, ok := r.Inner.(*FakeTransport); ok {
		return f.DDR5
	}
	return false // 默认按 DDR4 判据(更宽松, 宁可多报)
}

// SetDDR5Mode 显式指定世代(包装的不是 Fake 时用)。
func (r *RecordingTransport) SetDDR5Mode(ddr5 bool) { r.ddr5 = &ddr5 }

// WritesAtCmd 返回对指定 cmd 的全部 byte-data 写事务。
func (r *RecordingTransport) WritesAtCmd(cmd byte) []Op {
	var out []Op
	for _, op := range r.Writes() {
		if op.Kind == OpWriteByteData && op.Cmd == cmd {
			out = append(out, op)
		}
	}
	return out
}

// Reset 清空记录(保留故障注入配置)。
func (r *RecordingTransport) Reset() {
	r.mu.Lock()
	r.ops = nil
	r.mu.Unlock()
}

// String 生成单行摘要, 便于测试失败信息。
func (r *RecordingTransport) String() string {
	ops := r.Ops()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d ops(%d writes):", len(ops), r.WriteCount())
	for i, op := range ops {
		if i >= 24 {
			fmt.Fprintf(&sb, " ...")
			break
		}
		fmt.Fprintf(&sb, " %s@%#x/%#x=%#x", op.Kind, op.Addr, op.Cmd, op.Val)
		if op.Err != "" {
			fmt.Fprintf(&sb, "(ERR)")
		}
	}
	return sb.String()
}

// writeGuard 判断当前这次数据写(byte-data/byte, 不含 quick)是否应被注入拦截。
// n 从 1 开始计数。
func (r *RecordingTransport) writeGuard(cmd byte) (int, error) {
	r.mu.Lock()
	filter := r.FailWriteCmdFilter
	n := 0
	for _, op := range r.ops {
		if !op.Write || op.Kind == OpQuick {
			continue
		}
		if filter != nil && !filter(op.Cmd) {
			continue
		}
		n++
	}
	if filter != nil && !filter(cmd) {
		r.mu.Unlock()
		return n, nil // 不参与计数与注入
	}
	n++ // 当前这次
	failAt, failFrom, deny, inj := r.FailWriteAt, r.FailWriteFrom, r.DenyWriteFrom, r.ErrInjected
	once := r.FailWriteOnce
	if once > 0 && n == once {
		r.FailWriteOnce = 0 // 只失败这一次, 后续写(含回滚)必须能成功
	}
	r.mu.Unlock()

	if inj == nil {
		inj = fmt.Errorf("注入的写失败(第 %d 次写)", n)
	}
	if once > 0 && n == once {
		return n, inj
	}
	if failAt > 0 && n >= failAt {
		return n, inj
	}
	if failFrom > 0 && n >= failFrom {
		return n, inj
	}
	if deny >= 0 && int(cmd) >= deny {
		return n, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	return n, nil
}

func (r *RecordingTransport) Identity() (Controller, error) { return r.Inner.Identity() }

func (r *RecordingTransport) Quick(addr byte, write bool) error {
	op := Op{Kind: OpQuick, Addr: addr, Write: write}
	var err error
	if write && r.BanQuickWrite {
		err = fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	if err != nil {
		return err
	}
	return r.Inner.Quick(addr, write)
}

func (r *RecordingTransport) ReadByteData(addr byte, cmd byte) (byte, error) {
	b, err := r.Inner.ReadByteData(addr, cmd)
	op := Op{Kind: OpReadByteData, Addr: addr, Cmd: cmd, Val: b}
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return b, err
}

func (r *RecordingTransport) WriteByteData(addr byte, cmd byte, val byte) error {
	op := Op{Kind: OpWriteByteData, Addr: addr, Cmd: cmd, Val: val, Write: true}
	if _, err := r.writeGuard(cmd); err != nil {
		op.Err = err.Error()
		r.record(op)
		return err
	}
	err := r.Inner.WriteByteData(addr, cmd, val)
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return err
}

func (r *RecordingTransport) WriteByteNoData(addr byte) error {
	op := Op{Kind: OpWriteByte, Addr: addr, Write: true}
	if _, err := r.writeGuard(0); err != nil {
		op.Err = err.Error()
		r.record(op)
		return err
	}
	err := r.Inner.WriteByteNoData(addr)
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return err
}

// ReadBlockData 记录一次块读(计入读事务)。
func (r *RecordingTransport) ReadBlockData(addr byte, cmd byte) ([]byte, error) {
	b, err := r.Inner.ReadBlockData(addr, cmd)
	op := Op{Kind: OpReadBlock, Addr: addr, Cmd: cmd}
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return b, err
}

// WriteBlockData 记录并转发 SMBus Block Write(协议 5)。
func (r *RecordingTransport) WriteBlockData(addr byte, cmd byte, data []byte) error {
	// Write: true 必须设置 —— 否则 Writes()/WriteCount()/NVMWrites()/DataWrites() 与
	// 故障注入(writeGuard)都会把块写当成"非写事务"漏掉, "干跑零写入"的断言就可能被骗过。
	op := Op{Kind: OpWriteBlock, Addr: addr, Cmd: cmd, Write: true}
	// 故障注入(与逐字节写共用同一套计数/过滤): 块写的"值"取数据首字节便于日志阅读
	if _, werr := r.writeGuard(cmd); werr != nil {
		op.Err = werr.Error()
		r.record(op)
		return werr
	}
	if len(data) > 0 {
		op.Val = data[0]
	}
	err := r.Inner.WriteBlockData(addr, cmd, data)
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return err
}

func (r *RecordingTransport) ReadWordData(addr byte, cmd byte) (uint16, error) {
	v, err := r.Inner.ReadWordData(addr, cmd)
	op := Op{Kind: OpReadWordData, Addr: addr, Cmd: cmd}
	if err != nil {
		op.Err = err.Error()
	}
	r.record(op)
	return v, err
}

func (r *RecordingTransport) Close() error { return r.Inner.Close() }

// InnerTuner 把"总线调优"能力从内层透出来(包装器自己不实现 Tuner)。
func (r *RecordingTransport) InnerTuner() (Tuner, bool) { return TunerOf(r.Inner) }
