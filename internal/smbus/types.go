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
	// ReadBlockData 是 SMBus Block Read(协议 5): 一次最多 32 字节。
	// 设备/控制器若不支持会返回错误, 调用方应回退到逐字节读。
	ReadBlockData(addr byte, cmd byte) ([]byte, error)
	// WriteBlockData 是 SMBus Block Write(协议 5): 一次写入 1..32 字节。
	// 有些 SPD5 hub 对 NVM 的写只认块写(逐字节写会被忽略), 所以写入档位也要能自适应。
	WriteBlockData(addr byte, cmd byte, data []byte) error
	ReadWordData(addr byte, cmd byte) (uint16, error)
	Close() error
}

// GenerationAware 是"知道自己连的是不是 DDR5"的传输(可选能力)。
// 写周期判定要区分 DDR5 的 MR 寄存器写与 NVM 写, 所以由设备层在识别世代后告知。
type GenerationAware interface{ SetDDR5(bool) }

// SetTransportDDR5 尽力把世代信息透给传输(含计数包装)。
func SetTransportDDR5(t Transport, ddr5 bool) {
	if g, ok := t.(GenerationAware); ok {
		g.SetDDR5(ddr5)
	}
}
