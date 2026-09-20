//go:build windows

package smbus

import (
	"fmt"
	"sort"
	"time"

	"spdrw/internal/assets"
)

// 模块公开的 ioctl 函数名(PawnIO.Modules DEFINE_IOCTL)。
const (
	fnSmbusXfer       = "ioctl_smbus_xfer"
	fnIdentity        = "ioctl_identity"
	fnWriteProtection = "ioctl_write_protection"
	fnPiix4PortSel    = "ioctl_piix4_port_sel"
	fnSmbusIndex      = "ioctl_smbus_index"
	fnSetSleepMode    = "ioctl_set_sleep_mode"
)

// SleepMode AlwaysSleep: 模块在长等待时真正休眠,降低 CPU 占用(OpenRGB 同款默认)。
const sleepModeAlwaysSleep = 2

// pawnioTransport 是绑定到一条 SMBus 总线(一个已加载模块会话)的 Transport。
type pawnioTransport struct {
	session   *pawnioSession
	ctrl      Controller
	piix4Port int // -1 = 非 PIIX4 会话
}

// Discover 枚举本机全部可用 SMBus 总线。
//
// 顺序: Intel i801(1 条) → AMD PIIX4(端口 0/1 各一) → Intel Skylake IMC(index 0/1 各一)。
// 每条总线是独立的 PawnIO 会话(与 OpenRGB 相同做法)。
func Discover() ([]Transport, error) {
	var result []Transport

	try := func(bin string, kind ControllerKind, index int, piix4Port int, selectFn func(*pawnioSession) error) {
		s, err := pawnioOpen()
		if err != nil {
			return // 该控制器不存在/不支持
		}
		if err := s.load(assets.MustBytes(bin)); err != nil {
			s.close()
			return
		}
		_, _ = s.execute(fnSetSleepMode, []uint64{sleepModeAlwaysSleep}, nil)
		if selectFn != nil {
			if err := selectFn(s); err != nil {
				s.close()
				return
			}
		}
		ctrl, err := readIdentity(s, kind, index)
		if err != nil {
			s.close()
			return
		}
		result = append(result, &pawnioTransport{session: s, ctrl: ctrl, piix4Port: piix4Port})
	}

	try("SmbusI801.bin", KindI801, 0, -1, nil)
	try("SmbusPIIX4.bin", KindPIIX4, 0, 0, func(s *pawnioSession) error { return piix4SelectPort(s, 0) })
	try("SmbusPIIX4.bin", KindPIIX4, 1, 1, func(s *pawnioSession) error { return piix4SelectPort(s, 1) })
	try("SmbusIntelSkylakeIMC.bin", KindSkylakeIMC, 0, -1, func(s *pawnioSession) error { return skxSelectIndex(s, 0) })
	try("SmbusIntelSkylakeIMC.bin", KindSkylakeIMC, 1, -1, func(s *pawnioSession) error { return skxSelectIndex(s, 1) })

	sort.Slice(result, func(i, j int) bool {
		ni, _ := result[i].Identity()
		nj, _ := result[j].Identity()
		return ni.Name < nj.Name
	})
	return result, nil
}

func piix4SelectPort(s *pawnioSession, port int) error {
	out := make([]uint64, 1)
	_, err := s.execute(fnPiix4PortSel, []uint64{uint64(port)}, out)
	return err
}

func skxSelectIndex(s *pawnioSession, idx int) error {
	_, err := s.execute(fnSmbusIndex, []uint64{uint64(idx)}, nil)
	return err
}

// readIdentity 调用 ioctl_identity 填充 Controller 元信息。
func readIdentity(s *pawnioSession, kind ControllerKind, index int) (Controller, error) {
	out := make([]uint64, 3)
	_, err := s.execute(fnIdentity, []uint64{0}, out)
	if err != nil {
		return Controller{}, err
	}
	c := Controller{Kind: kind, Index: index}
	c.PCIIDs = out[2]
	ioBase := uint32(out[1])
	_ = ioBase
	// out[0] 为模块名前 8 字符打包的 'i801\0..' / 'PIIX4\0'
	name := decodeName(out[0])
	kindLabel := map[ControllerKind]string{
		KindI801:       "Intel PCH",
		KindPIIX4:      "AMD FCH",
		KindSkylakeIMC: "Skylake-X IMC",
	}[kind]
	suffix := ""
	if index > 0 || kind == KindPIIX4 || kind == KindSkylakeIMC {
		suffix = fmt.Sprintf(" #%d", index)
	}
	c.IOBase = ioBase
	c.Name = fmt.Sprintf("%s %s%s (IO:%04X)", kindLabel, name, suffix, ioBase)

	// i801 提供 BIOS SPD 写禁止状态。
	if kind == KindI801 {
		out1 := make([]uint64, 1)
		if _, err := s.execute(fnWriteProtection, []uint64{0}, out1); err == nil {
			c.WpKnown = true
			c.NoSpdWp = out1[0] == 0
		}
	}
	return c, nil
}

func decodeName(v uint64) string {
	b := make([]byte, 0, 8)
	for i := 0; i < 8; i++ {
		ch := byte(v >> (8 * i))
		if ch == 0 {
			break
		}
		b = append(b, ch)
	}
	return string(b)
}

func (p *pawnioTransport) Identity() (Controller, error) { return p.ctrl, nil }

// piix4Port 记录本会话绑定的 AMD 端口; >=0 表示每次事务前重设端口选择,
// 因为 KernCZ 的端口选择寄存器是全局硬件状态, 多会话(端口0/1)会互相覆盖。
func (p *pawnioTransport) xfer(addr byte, write bool, cmd byte, proto byte, data []byte, wantOut bool) ([]uint64, error) {
	in := MarshalXfer(addr, write, cmd, proto, data)
	out := make([]uint64, XferOutSize)
	unlock := lockSMBus()
	if unlock == nil {
		return nil, fmt.Errorf("SMBus 正被其他程序占用(等待 2 秒超时), 请关闭 Thaiphoon/厂家工具后重试")
	}
	defer unlock()

	// AMD KernCZ: 每次事务前把端口选择切回本会话的端口
	if p.piix4Port >= 0 {
		if _, err := p.session.execute(fnPiix4PortSel, []uint64{uint64(p.piix4Port)}, nil); err != nil {
			return nil, fmt.Errorf("设置 PIIX4 端口 %d 失败: %w", p.piix4Port, err)
		}
	}

	// 失败自动重试(对齐原版工具的宽容时序): PawnIO 模块单次事务 64ms 硬超时,
	// 而 DDR5 SPD5 HUB/I3C 桥在空闲后的首次访问可能更慢; 原版轮询 1000ms 所以
	// "慢但能读"。这里对状态错误(NACK/超时)重试, 多数 HUB 重试一次即可恢复。
	var ret uint64
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Millisecond)
			for i := range out {
				out[i] = 0
			}
		}
		ret, err = p.session.execute(fnSmbusXfer, in, out)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	n := int(ret)
	if n > len(out) {
		n = len(out)
	}
	if wantOut && n < 1 {
		return nil, fmt.Errorf("SMBus 读取返回空数据")
	}
	return out[:n], nil
}

func (p *pawnioTransport) Quick(addr byte, write bool) error {
	_, err := p.xfer(addr, write, 0, ProtoQuick, nil, false)
	return err
}

func (p *pawnioTransport) ReadByteData(addr byte, cmd byte) (byte, error) {
	out, err := p.xfer(addr, false, cmd, ProtoByteData, nil, true)
	if err != nil {
		return 0, err
	}
	b, err := UnmarshalOut(out, ProtoByteData)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// eepromWriteDelay 与原版一致: 对 EEPROM 地址的写事务后固定等待 25ms
// (EE1004/SPD5 写周期), 是原版工具"慢但稳"的关键来源之一。
const eepromWriteDelay = 25 * time.Millisecond

func isEepromAddr(addr byte) bool { return addr >= 0x50 && addr <= 0x57 }

func (p *pawnioTransport) WriteByteData(addr byte, cmd byte, val byte) error {
	_, err := p.xfer(addr, true, cmd, ProtoByteData, []byte{val}, false)
	if err == nil && isEepromAddr(addr) {
		time.Sleep(eepromWriteDelay)
	}
	return err
}

func (p *pawnioTransport) WriteByteNoData(addr byte) error {
	_, err := p.xfer(addr, true, 0, ProtoByte, nil, false)
	if err == nil && isEepromAddr(addr) {
		time.Sleep(eepromWriteDelay)
	}
	return err
}

func (p *pawnioTransport) ReadWordData(addr byte, cmd byte) (uint16, error) {
	out, err := p.xfer(addr, false, cmd, ProtoWordData, nil, true)
	if err != nil {
		return 0, err
	}
	b, err := UnmarshalOut(out, ProtoWordData)
	if err != nil {
		return 0, err
	}
	return uint16(b[0]) | uint16(b[1])<<8, nil
}

func (p *pawnioTransport) Close() error {
	p.session.close()
	return nil
}
