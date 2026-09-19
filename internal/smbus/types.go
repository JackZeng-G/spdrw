// Package smbus 定义 SMBus 传输抽象与 PawnIO 后端。
//
// Transport 是 EEPROM 协议层(internal/eeprom)与硬件之间的唯一边界:
// Windows 下由 PawnIO 签名模块(SmbusI801/SmbusPIIX4/SmbusIntelSkylakeIMC)实现,
// 测试下由 FakeTransport 实现。
package smbus

import "fmt"

// ControllerKind 标识 SMBus 控制器类型。
type ControllerKind string

const (
	KindI801       ControllerKind = "i801"  // Intel PCH (ICH/PCH i801 家族)
	KindPIIX4      ControllerKind = "piix4" // AMD FCH (KernCZ PIIX4)
	KindSkylakeIMC ControllerKind = "skx-imc"
)

// Controller 描述一个可用的 SMBus 总线。
type Controller struct {
	Kind    ControllerKind
	Index   int    // AMD 端口号(0/1) 或 SKX IMC 序号(0/1); i801 恒为 0
	IOBase  uint32 // 控制器 IO 基址(标识用)
	PCIIDs  uint64 // vendor|dev<<16|subsysvend<<32|subsysdev<<48
	Name    string // 人类可读名称
	NoSpdWp bool   // true = BIOS 未开启 SPD 写禁止(可写); 由 i801 ioctl_write_protection 提供
	WpKnown bool   // i801 才能得知写禁止位
}

func (c Controller) String() string {
	return fmt.Sprintf("%s (%s)", c.Name, c.Kind)
}

// Transport 是 SPD EEPROM 所在 SMBus 总线的最小读写抽象。
//
// 语义与 Linux i2c-dev/i2c-core 一致:
//   - Quick: SMBus Quick 事务(START+addr+R/W+STOP), 用于设备探测与 DDR4 页切换/写保护命令
//   - ReadByte/WriteByte: SMBus Byte Data 事务(带 8 位命令/偏移)
//   - WriteByteNoData: Byte 协议写(地址+命令字节,无数据), 原版 PSWP 检测所用
//   - ReadWord: SMBus Word Data 读
type Transport interface {
	Identity() (Controller, error)
	Quick(addr byte, write bool) error
	ReadByteData(addr byte, cmd byte) (byte, error)
	WriteByteData(addr byte, cmd byte, val byte) error
	WriteByteNoData(addr byte) error
	ReadWordData(addr byte, cmd byte) (uint16, error)
	Close() error
}
