package smbus

import (
	"fmt"
	"sync"
)

// FakeTransport 是内存中的 Transport 实现,用于 eeprom/app 的离线单测。
// 模拟一片 EEPROM(默认 1024 字节,填 0xFF),并记录 Quick/WriteByte 命令历史。
type FakeTransport struct {
	mu       sync.Mutex
	Ctrl     Controller
	EEProm   []byte
	QuickLog []QuickOp
	WriteLog []WriteOp
	// Present 决定哪些地址在 Quick 探测时 ACK; 为 nil 时全部 ACK。
	Present map[byte]bool
	// ProtectedFrom >= 0 时模拟写保护: 对该偏移及以上的写操作 NACK(真实 EE1004 行为)。
	ProtectedFrom int
	// IgnoreFrom >= 0 时模拟"写了但不生效"(不报错也不落值): 用来触发回读校验失败。
	IgnoreFrom int
	// IgnoreByteDataFrom >= 0 时, 只让**逐字节写(协议 2)**在该偏移及以上"写了不生效",
	// 块写(协议 5)照常生效 —— 用来验证写入档位能自动切到块写。
	IgnoreByteDataFrom int
	// FailReads 为 true 时所有读事务报错(模拟总线读不出来)。
	FailReads bool
	// BlockReadOverride 非 nil 时, 块读返回这份镜像的内容而不是 EEProm 里的真实值 ——
	// 用来模拟"读路径本身有系统性偏差"(逐字节读仍读真值)。
	BlockReadOverride []byte
	// BlockReadUnsupported 为 true 时块读返回 NACK(模拟不支持块读的设备, 用于测回退)。
	BlockReadUnsupported bool
	// WordReadUnsupported 为 true 时字读返回 NACK(模拟连字读都不支持的设备)。
	WordReadUnsupported bool
	Closed              bool

	// DDR5 为 true 时按 DDR5 分页(MR11, 128 字节页, 读命令 |0x80); 否则按 DDR4(SPA quick, 256 字节页)。
	DDR5 bool

	// MR 覆盖表(仅 DDR5, cmd bit7=0 的寄存器读): 测试注入 MR12/13/48 等状态用。
	// 未覆盖的寄存器按默认模拟(MR0=0x51, MR11=当前页, 其余 0)。
	MR map[byte]byte

	// 页切换状态模拟。
	page     int
	pageSpan int
}

type QuickOp struct {
	Addr  byte
	Write bool
}

type WriteOp struct {
	Addr byte
	Cmd  byte
	Val  byte
}

func NewFake() *FakeTransport {
	return &FakeTransport{
		MR:                 map[byte]byte{},
		ProtectedFrom:      -1,
		IgnoreFrom:         -1,
		IgnoreByteDataFrom: -1,
		Ctrl:               Controller{Kind: KindI801, Index: 0, IOBase: 0xEFA0, Name: "Fake"},
		EEProm:             make([]byte, 1024),
		page:               0,
		pageSpan:           256,
	}
}

// SetDDR5 切换 DDR5 分页语义(128 字节页, MR11 切页)。
func (f *FakeTransport) SetDDR5(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DDR5 = v
	if v {
		f.pageSpan = 128
	} else {
		f.pageSpan = 256
	}
	f.page = 0
}

func (f *FakeTransport) Fill(v byte) {
	for i := range f.EEProm {
		f.EEProm[i] = v
	}
}

func (f *FakeTransport) Identity() (Controller, error) { return f.Ctrl, nil }

func (f *FakeTransport) Quick(addr byte, write bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.QuickLog = append(f.QuickLog, QuickOp{Addr: addr, Write: write})
	if f.Present != nil && !f.Present[addr] && addr < 0x30 {
		return fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	// DDR4 EE1004 SPA 页切换: quick 写 0x36=SPA0 / 0x37=SPA1。
	if write && !f.DDR5 && addr == 0x36 {
		f.page, f.pageSpan = 0, 256
	} else if write && !f.DDR5 && addr == 0x37 {
		f.page, f.pageSpan = 1, 256
	}
	return nil
}

// idx 计算当前页下的物理偏移。DDR5: cmd bit7=1 访问 NVM 页(掩 0x7F 得页内偏移),
// bit7=0 访问 MR 寄存器区(模拟 MR0=0x51 DeviceType, MR11=当前页, 其余 0)。
func (f *FakeTransport) idx(cmd byte) (int, error) {
	c := int(cmd)
	if f.DDR5 {
		c &= 0x7F
	}
	i := f.page*f.pageSpan + c
	if i >= len(f.EEProm) {
		return 0, fmt.Errorf("越界 页=%d cmd=%#x", f.page, cmd)
	}
	return i, nil
}

// mrRead 模拟 DDR5 MR 寄存器读(cmd bit7=0)。
func (f *FakeTransport) mrRead(cmd byte) (byte, bool) {
	if !f.DDR5 || cmd&0x80 != 0 {
		return 0, false
	}
	if v, ok := f.MR[cmd&0x7F]; ok {
		return v, true
	}
	switch int(cmd & 0x7F) {
	case 0: // MR0: Device Type = 0x51
		return 0x51, true
	case 11: // MR11: 当前 NVM 页
		return byte(f.page), true
	default:
		return 0x00, true
	}
}

func (f *FakeTransport) ReadByteData(addr byte, cmd byte) (byte, error) {
	if f.FailReads {
		return 0, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Present != nil && !f.Present[addr] {
		return 0, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if v, ok := f.mrRead(cmd); ok {
		return v, nil
	}
	i, err := f.idx(cmd)
	if err != nil {
		return 0, err
	}
	return f.EEProm[i], nil
}

func (f *FakeTransport) WriteByteData(addr byte, cmd byte, val byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// DDR5 页切换: 写 MR11(reg 11) 选择 NVM 页, 不落数组。
	if f.DDR5 && cmd == 11 {
		f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
		f.page, f.pageSpan = int(val), 128
		return nil
	}
	// DDR5 寄存器写(MR12/13/48 等): 记录到 MR 表, 不落 NVM 数组。
	if f.DDR5 && cmd&0x80 == 0 {
		f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
		if f.MR == nil {
			f.MR = map[byte]byte{}
		}
		f.MR[cmd] = val
		return nil
	}
	if f.ProtectedFrom >= 0 && int(cmd) >= f.ProtectedFrom {
		f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
		return fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
	i, err := f.idx(cmd)
	if err != nil {
		return err
	}
	if f.IgnoreFrom >= 0 && i >= f.IgnoreFrom {
		return nil // 模拟"写被忽略": 不落值也不报错 → 回读校验必须发现
	}
	if f.IgnoreByteDataFrom >= 0 && i >= f.IgnoreByteDataFrom {
		return nil // 只忽略逐字节写: 块写仍可生效(用于测写入档位自动切换)
	}
	f.EEProm[i] = val
	return nil
}

// WriteBlockData 模拟 SMBus Block Write(协议 5): 从 cmd 起写 len 个字节, 页内自增不跨页。
func (f *FakeTransport) WriteBlockData(addr byte, cmd byte, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Present != nil && !f.Present[addr] {
		return fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if len(data) == 0 || len(data) > BlockMaxPayload {
		return fmt.Errorf("块写长度 %d 非法(1..%d)", len(data), BlockMaxPayload)
	}
	start, err := f.idx(cmd)
	if err != nil {
		return err
	}
	e := f.page * f.pageSpan
	end := e + f.pageSpan
	if start+len(data) > end {
		return fmt.Errorf("块写跨页 cmd=%#x len=%d", cmd, len(data))
	}
	if f.ProtectedFrom >= 0 && start >= f.ProtectedFrom {
		return fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: byte(len(data))})
	if f.IgnoreFrom >= 0 && start >= f.IgnoreFrom {
		return nil
	}
	copy(f.EEProm[start:start+len(data)], data)
	return nil
}

func (f *FakeTransport) WriteByteNoData(addr byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: 0, Val: 0})
	return nil
}

// ReadBlockData 模拟 SMBus Block Read: 从 cmd 开始最多 32 字节, 页内自增, 不跨页。
// 超出页尾时按设备行为只返回页内剩余字节(真实 EE1004/SPD5 也一样)。
func (f *FakeTransport) ReadBlockData(addr byte, cmd byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Present != nil && !f.Present[addr] {
		return nil, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if f.FailReads {
		return nil, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if f.BlockReadUnsupported {
		return nil, fmt.Errorf("设备不支持块读(0xC000000E)")
	}
	start, err := f.idx(cmd)
	if err != nil {
		return nil, err
	}
	e := f.page * f.pageSpan
	end := e + f.pageSpan
	n := ProtoBlockMax
	if start+n > end {
		n = end - start
	}
	if n <= 0 {
		return nil, fmt.Errorf("块读越界 页=%d cmd=%#x", f.page, cmd)
	}
	src := f.EEProm
	if f.BlockReadOverride != nil && start+n <= len(f.BlockReadOverride) {
		src = f.BlockReadOverride
	}
	out := make([]byte, n)
	copy(out, src[start:start+n])
	return out, nil
}

func (f *FakeTransport) ReadWordData(addr byte, cmd byte) (uint16, error) {
	if f.FailReads {
		return 0, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if f.WordReadUnsupported {
		return 0, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	lo, err := f.ReadByteData(addr, cmd)
	if err != nil {
		return 0, err
	}
	hi, err := f.ReadByteData(addr, cmd+1)
	if err != nil {
		return 0, err
	}
	return uint16(lo) | uint16(hi)<<8, nil
}

func (f *FakeTransport) Close() error {
	f.Closed = true
	return nil
}
