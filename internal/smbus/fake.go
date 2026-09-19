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
	Closed        bool
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
		ProtectedFrom: -1,
		Ctrl:   Controller{Kind: KindI801, Index: 0, IOBase: 0xEFA0, Name: "Fake"},
		EEProm: make([]byte, 1024),
	}
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
	// DDR4 SPA/命令地址走快速命令; Fake 无需行为,只记录。
	return nil
}

func (f *FakeTransport) ReadByteData(addr byte, cmd byte) (byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Present != nil && !f.Present[addr] {
		return 0, fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	if int(cmd) >= len(f.EEProm) {
		return 0, fmt.Errorf("越界 %#x", cmd)
	}
	return f.EEProm[cmd], nil
}

func (f *FakeTransport) WriteByteData(addr byte, cmd byte, val byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ProtectedFrom >= 0 && int(cmd) >= f.ProtectedFrom {
		f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
		return fmt.Errorf("设备无响应 NACK(0xC000000E)")
	}
	f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
	f.EEProm[cmd] = val
	return nil
}

func (f *FakeTransport) WriteByteNoData(addr byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.WriteLog = append(f.WriteLog, WriteOp{Addr: addr, Cmd: 0, Val: 0})
	return nil
}

func (f *FakeTransport) ReadWordData(addr byte, cmd byte) (uint16, error) {
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
