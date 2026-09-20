package smbus

import "fmt"

// SMBus 协议常量(与 Linux i2c-dev I2C_SMBUS_* 一致,PawnIO 模块同样使用这套值)。
const (
	ProtoQuick     byte = 0
	ProtoByte      byte = 1
	ProtoByteData  byte = 2
	ProtoWordData  byte = 3
	ProtoBlockData byte = 5

	FlagRead  byte = 1
	FlagWrite byte = 0
)

// XferInSize / XferOutSize 是 PawnIO ioctl_smbus_xfer 的固定缓冲尺寸(cells)。
const (
	XferInSize  = 9
	XferOutSize = 5
)

// MarshalXfer 构造 ioctl_smbus_xfer 的输入 cells。
//
// data 的语义随协议而变(与 i2c_smbus_data 联合体一致):
//   - ByteData: data[0]
//   - WordData: data[0](低字节) + data[1](高字节)
//   - BlockData(写): data[0] = 长度(1..32), data[1..len] = 数据 —— 模块用
//     unpack_bytes_le(in, in_data, 33, 4*8, 0) 从第 4 个 cell 起解出这 33 字节
//   - BlockData(读): 无输入数据
//
// 越界/自相矛盾的入参**返回错误**而不是截断: 模块只会看上面前 33 字节, 静默截断会让
// "写 40 字节"变成"悄悄只写 33 字节", 而调用方以为全写进去了(审计遗留项)。
func MarshalXfer(addr byte, write bool, cmd byte, proto byte, data []byte) ([]uint64, error) {
	in := make([]uint64, XferInSize)
	in[0] = uint64(addr)
	if write {
		in[1] = uint64(FlagWrite)
	} else {
		in[1] = uint64(FlagRead)
	}
	in[2] = uint64(cmd)
	in[3] = uint64(proto)
	switch proto {
	case ProtoQuick, ProtoByte:
		// 无数据字节(写这两种协议时模块忽略 data)
	case ProtoByteData:
		if len(data) > 0 {
			in[4] = uint64(data[0])
		}
	case ProtoWordData:
		if len(data) > 0 {
			in[4] = uint64(data[0])
		}
		if len(data) > 1 {
			in[4] |= uint64(data[1]) << 8
		}
	case ProtoBlockData:
		// 长度字节 + 最多 32 字节数据, 小端按字节铺在 in[4..8](5 个 cell = 40 字节)
		if len(data) > BlockMaxPayload+1 {
			return nil, fmt.Errorf("smbus: 块写入参 %d 字节超出上限 %d(1 长度字节 + %d 数据字节)",
				len(data), BlockMaxPayload+1, BlockMaxPayload)
		}
		if len(data) > 0 && int(data[0]) != len(data)-1 {
			return nil, fmt.Errorf("smbus: 块写长度字节 %d 与实际数据 %d 字节不符", data[0], len(data)-1)
		}
		for i, b := range data {
			in[4+i/8] |= uint64(b) << (8 * uint(i%8))
		}
	default:
		return nil, fmt.Errorf("smbus: 不支持的协议 %d", proto)
	}
	return in, nil
}

// BlockMaxPayload 是 SMBus Block Write 单次最多携带的数据字节数。
const BlockMaxPayload = 32

// UnmarshalOut 从 ioctl_smbus_xfer 输出 cells 提取读取的数据。
// Byte 协议: out[0] bits0-7; Word: out[0] bits0-15。
func UnmarshalOut(cells []uint64, proto byte) ([]byte, error) {
	if len(cells) == 0 {
		return nil, fmt.Errorf("smbus: 空输出")
	}
	switch proto {
	case ProtoQuick:
		return nil, nil
	case ProtoByte, ProtoByteData:
		return []byte{byte(cells[0])}, nil
	case ProtoWordData:
		return []byte{byte(cells[0]), byte(cells[0] >> 8)}, nil
	default:
		return nil, fmt.Errorf("smbus: 不支持的读取协议 %d", proto)
	}
}

// NTSTATUS 状态码(模块经 pawnio_execute 返回的 HRESULT 已由 DLL 层转换为 HRESULT;
// 但 ioctl 函数的 NTSTATUS 返回值同样会映射进 HRESULT 的高位。这里按常见值归类)。
const (
	ntStatusSuccess          = 0x00000000
	ntStatusDeviceBusyWarn   = 0x80000011 // STATUS_DEVICE_BUSY
	ntStatusNoSuchDevice     = 0xC000000E // STATUS_NO_SUCH_DEVICE
	ntStatusIOTimeout        = 0xC00000B5 // STATUS_IO_TIMEOUT
	ntStatusIODeviceError    = 0xC0000185 // STATUS_IO_DEVICE_ERROR
	ntStatusNotSupported     = 0xC00000BB // STATUS_NOT_SUPPORTED
	ntStatusNotImplemented   = 0xC0000002 // STATUS_NOT_IMPLEMENTED
	ntStatusInvalidParam     = 0xC000000D // STATUS_INVALID_PARAMETER
	ntStatusDeviceProtocolEr = 0xC0000186 // STATUS_DEVICE_PROTOCOL_ERROR
	ntStatusRetry            = 0xC000022D // STATUS_RETRY
)

// StatusToError 将 pawnio_execute / 模块函数的返回码(HRESULT 形式,含 NTSTATUS 按位或 0x80070000 的情形)
// 映射为用户可读错误。成功返回 nil。
func StatusToError(hr uint32) error {
	if hr == ntStatusSuccess {
		return nil
	}
	// HRESULT_FROM_NTSTATUS: 0x80070000 | (ntstatus & 0xFFFF)? 实际是 保留 HRESULT 0x8007xxxx 为 Win32;
	// NTSTATUS 会以 0xCxxxxxxx 原样出现在 DLL 的 HRESULT 中。两种形态都归一处理。
	code := hr
	if hr&0xFFFF0000 == 0x80070000 { // HRESULT_FROM_WIN32
		return win32Err(uint16(hr & 0xFFFF))
	}
	switch code {
	case ntStatusDeviceBusyWarn:
		return fmt.Errorf("SMBus 忙(0x%08X)", hr)
	case ntStatusNoSuchDevice:
		return fmt.Errorf("设备无响应 NACK(0x%08X)", hr)
	case ntStatusIOTimeout:
		return fmt.Errorf("SMBus 事务超时(0x%08X)", hr)
	case ntStatusIODeviceError:
		return fmt.Errorf("SMBus 事务失败(总线错误)(0x%08X)", hr)
	case ntStatusDeviceProtocolEr:
		return fmt.Errorf("设备协议错误(0x%08X)", hr)
	case ntStatusRetry:
		return fmt.Errorf("仲裁丢失,需重试(0x%08X)", hr)
	case ntStatusNotSupported, ntStatusNotImplemented:
		return fmt.Errorf("控制器不支持该事务(0x%08X)", hr)
	case ntStatusInvalidParam:
		return fmt.Errorf("无效参数(0x%08X)", hr)
	default:
		return fmt.Errorf("SMBus 操作失败(0x%08X)", hr)
	}
}

func win32Err(code uint16) error {
	switch code {
	case 2: // ERROR_FILE_NOT_FOUND
		return fmt.Errorf("未安装 PawnIO 或驱动未运行")
	case 5: // ERROR_ACCESS_DENIED
		return fmt.Errorf("访问被拒绝: 请以管理员身份运行")
	case 32: // ERROR_SHARING_VIOLATION
		return fmt.Errorf("SMBus 正被其他程序占用")
	default:
		return fmt.Errorf("SMBus 操作失败(Win32 错误 %d)", code)
	}
}

// ProtoBlockMax 是 SMBus Block 传输的最大数据字节数(协议限定 32)。
const ProtoBlockMax = 32

// UnmarshalBlockRead 解包块读的返回: 模块把 out_data[33](首字节为长度, 其后为数据)
// 按字节小端打包进输出 cells, 所以 out 至少要有 5 格(33 字节)。
func UnmarshalBlockRead(cells []uint64) ([]byte, error) {
	var raw [ProtoBlockMax + 1]byte
	for i := range raw {
		cell := i / 8
		if cell >= len(cells) {
			return nil, fmt.Errorf("smbus: 块读输出不足(%d 格)", len(cells))
		}
		raw[i] = byte(cells[cell] >> (8 * uint(i%8)))
	}
	n := int(raw[0])
	if n <= 0 || n > ProtoBlockMax {
		return nil, fmt.Errorf("smbus: 块读长度非法(%d)", n)
	}
	out := make([]byte, n)
	copy(out, raw[1:1+n])
	return out, nil
}
