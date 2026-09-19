// Package eeprom 实现 SPD EEPROM 协议层: 分页、整片读写、校验、RSWP/PSWP 写保护。
//
// 逻辑移植自 SPD-Reader-Writer (1a2m3) 的 Eeprom.cs, SMBus 路径;
// 其中 DDR4 页切换由原版的 BYTE 协议修正为 JEDEC EE1004 标准的 Quick 写。
//
// 写保护语义(与真实硬件一致):
//   - RSWP(可逆): DDR4 quick 命令 / DDR5 MR12-MR13 位; 状态经写测试或位图读取
//   - PSWP(永久): 仅状态检测(与原版 SMBus 路径一致); 设置需硬件 HV,不在 SMBus 能力内
package eeprom

import (
	"fmt"

	"spdrw/internal/smbus"
)

// DDR4 EE1004 命令设备地址(原版 EepromCommand >> 1)。
const (
	spa0   = 0x36 // 选择页 0
	spa1   = 0x37 // 选择页 1
	cwp    = 0x33 // 清除写保护
	pswpID = 0x30 // PSWP 设备类型标识基址(PWPB<<3)
)

// swpCmds[block] = DDR4 设置 RSWP 的 quick 命令地址(SWP0-3)。
var swpCmds = [4]byte{0x31, 0x34, 0x35, 0x30}

// DDR5 SPD5118 hub 寄存器。
const (
	MR11 = 11 // Legacy Mode Device Configuration(页寄存器)
	MR12 = 12 // NVM 块写保护 [7:0]
	MR13 = 13 // NVM 块写保护 [15:8]
	MR48 = 48 // Device Status(bit2 = offline mode)

	spd5NVMReg = 0x80 // DDR5 NVM 访问位(offset%128 | 0x80)
)

// Device 是连接到一条 SMBus 总线上某个 SPD 地址的 EEPROM。
type Device struct {
	t    smbus.Transport
	addr byte
	ddr5 bool
	size int
	page int // 当前页(DDR4: 0-1; DDR5: 0-15)
}

// New 建立设备连接: 探测地址、识别 DDR5 与 SPD 大小,并复位到页 0。
func New(t smbus.Transport, addr byte) (*Device, error) {
	if addr>>3 != 0b1010 {
		return nil, fmt.Errorf("无效 EEPROM 地址 %#x (应在 0x50-0x57)", addr)
	}
	d := &Device{t: t, addr: addr}

	// DDR5 检测(原版逻辑): PMIC0 存在且 SPD byte0 == 0x51。
	ddr5 := false
	if err := t.Quick(0x48|(addr&7), false); err == nil {
		if b, err2 := t.ReadByteData(addr, 0); err2 == nil && b == 0x51 {
			ddr5 = true
		}
	}
	d.ddr5 = ddr5

	if ddr5 {
		d.size = 1024
	} else {
		ramType, err := t.ReadByteData(addr, 2)
		if err != nil {
			return nil, fmt.Errorf("读取 DRAM 类型失败(地址 %#x): %w", addr, err)
		}
		d.size = sizeByRamType(ramType)
	}

	if err := d.setPage(0); err != nil {
		return nil, fmt.Errorf("复位页失败: %w", err)
	}
	return d, nil
}

func sizeByRamType(ramType byte) int {
	switch ramType {
	case 0x0C, 0x0E, 0x0F, 0x10, 0x11: // DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X
		return 512
	case 0x12, 0x13, 0x14, 0x15: // DDR5, LPDDR5, DDR5 NVDIMM-P, LPDDR5X
		return 1024
	default: // SDRAM/DDR/DDR2/DDR3 及未知
		return 256
	}
}

// Size 返回 SPD 总大小(字节)。
func (d *Device) Size() int { return d.size }

// Addr 返回 I2C 地址。
func (d *Device) Addr() byte { return d.addr }

// IsDDR5 报告是否为 DDR5(SPD5 hub)。
func (d *Device) IsDDR5() bool { return d.ddr5 }

// pageSize 返回每页字节数。
func (d *Device) pageSize() int {
	if d.ddr5 {
		return 128
	}
	return 256
}

// pageCount 返回总页数。
func (d *Device) pageCount() int { return d.size / d.pageSize() }

// setPage 切换 EEPROM 页。
func (d *Device) setPage(p int) error {
	if p < 0 || p >= d.pageCount() {
		return fmt.Errorf("页 %d 越界(共 %d 页)", p, d.pageCount())
	}
	var err error
	if d.ddr5 {
		err = d.t.WriteByteData(d.addr, MR11, byte(p))
	} else {
		err = d.t.Quick(byte(spa0+p), true)
	}
	if err != nil {
		return err
	}
	d.page = p
	return nil
}

// physOffset 将逻辑偏移映射到(页, 物理地址)并按需切页。
func (d *Device) physOffset(off uint16) (byte, byte, error) {
	if int(off) >= d.size {
		return 0, 0, fmt.Errorf("偏移 %#x 越界(大小 %d)", off, d.size)
	}
	ps := d.pageSize()
	var p int
	var phys byte
	if d.ddr5 {
		p = int(off) / ps
		phys = byte(int(off)%ps) | spd5NVMReg
	} else {
		p = int(off) >> 8
		phys = byte(off)
	}
	if p != d.page {
		if err := d.setPage(p); err != nil {
			return 0, 0, err
		}
	}
	return byte(p), phys, nil
}

// Read 读取 n 字节(n 为 0 时报错)。
func (d *Device) Read(off uint16, n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("读取长度必须为正")
	}
	if int(off)+n > d.size {
		return nil, fmt.Errorf("读取范围 %#x+%#x 越界", off, n)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		_, phys, err := d.physOffset(off + uint16(i))
		if err != nil {
			return nil, err
		}
		b, err := d.t.ReadByteData(d.addr, phys)
		if err != nil {
			return nil, fmt.Errorf("读取 %#x 失败: %w", off+uint16(i), err)
		}
		out[i] = b
	}
	return out, nil
}

// ReadAll 整片读取。
func (d *Device) ReadAll() ([]byte, error) {
	return d.Read(0, d.size)
}

// WriteByteAt 向指定逻辑偏移写一个字节。(避开 vet 对 io.WriteByte 惯例的检查)
func (d *Device) WriteByteAt(off uint16, val byte) error {
	_, phys, err := d.physOffset(off)
	if err != nil {
		return err
	}
	if err := d.t.WriteByteData(d.addr, phys, val); err != nil {
		return fmt.Errorf("写入 %#x 失败: %w", off, err)
	}
	return nil
}

// Write 将 dump 写入 EEPROM。
//
// force=false: update 模式,跳过与当前值相同的字节(原版 Update);
// force=true: 全量强制写(原版 /writeforce)。
// 每字节写入后立即回读校验。progress(已写字节数) 可为 nil。
func (d *Device) Write(dump []byte, force bool, progress func(written int)) error {
	if len(dump) == 0 {
		return fmt.Errorf("空数据")
	}
	if len(dump) > d.size {
		return fmt.Errorf("数据 %d 字节超过 SPD 大小 %d", len(dump), d.size)
	}
	cur, err := d.Read(0, len(dump))
	if err != nil {
		return fmt.Errorf("读取当前内容失败: %w", err)
	}
	written := 0
	for i := 0; i < len(dump); i++ {
		if force || cur[i] != dump[i] {
			off := uint16(i)
			if err := d.WriteByteAt(off, dump[i]); err != nil {
				return fmt.Errorf("写入 0x%02X: %w", off, err)
			}
			back, err := d.Read(off, 1)
			if err != nil {
				return fmt.Errorf("回读 0x%02X: %w", off, err)
			}
			if back[0] != dump[i] {
				return fmt.Errorf("校验失败 @ 0x%02X: 写 %#x 读 %#x", off, dump[i], back[0])
			}
			written++
			if progress != nil {
				progress(written)
			}
		}
	}
	if progress != nil {
		progress(written)
	}
	return nil
}

// Verify 比对 EEPROM 内容与 dump。
func (d *Device) Verify(dump []byte) error {
	if len(dump) == 0 || len(dump) > d.size {
		return fmt.Errorf("数据长度 %d 无效", len(dump))
	}
	cur, err := d.Read(0, len(dump))
	if err != nil {
		return err
	}
	for i := range cur {
		if cur[i] != dump[i] {
			return fmt.Errorf("内容不一致 @ 0x%02X: 设备 %#x 文件 %#x", i, cur[i], dump[i])
		}
	}
	return nil
}

// WriteTest 对指定偏移做写保护测试(原版同款: 取反写回再还原)。
func (d *Device) WriteTest(off uint16) bool {
	b, err := d.Read(off, 1)
	if err != nil {
		return false
	}
	orig := b[0]
	if err := d.WriteByteAt(off, orig^0xFF); err != nil {
		return false
	}
	if err := d.WriteByteAt(off, orig); err != nil {
		return false
	}
	return true
}

// RSWPStatus 返回各块的可逆写保护状态。
// DDR5: 16 块(MR12/MR13 位图); DDR4: 4 块; 更早: 1 块。
func (d *Device) RSWPStatus() ([]bool, error) {
	blocks, err := d.blockCount()
	if err != nil {
		return nil, err
	}
	result := make([]bool, blocks)
	if d.ddr5 {
		mr12, err := d.t.ReadByteData(d.addr, MR12)
		if err != nil {
			return nil, fmt.Errorf("读 MR12: %w", err)
		}
		mr13, err := d.t.ReadByteData(d.addr, MR13)
		if err != nil {
			return nil, fmt.Errorf("读 MR13: %w", err)
		}
		for i := 0; i < 8; i++ {
			result[i] = mr12&(1<<i) != 0
			result[i+8] = mr13&(1<<i) != 0
		}
		return result, nil
	}
	for b := 0; b < blocks; b++ {
		result[b] = !d.WriteTest(uint16(b * 128))
	}
	return result, nil
}

func (d *Device) blockCount() (int, error) {
	switch {
	case d.ddr5:
		return 16, nil
	case d.size == 512:
		return 4, nil
	case d.size == 256:
		return 1, nil
	default:
		return 0, fmt.Errorf("SPD 大小 %d 不支持写保护操作", d.size)
	}
}

// RSWPSet 启用指定块的可逆写保护。
func (d *Device) RSWPSet(block byte) error {
	blocks, err := d.blockCount()
	if err != nil {
		return err
	}
	if int(block) >= blocks {
		return fmt.Errorf("块 %d 越界(共 %d 块)", block, blocks)
	}
	if d.ddr5 {
		memReg := byte(MR12)
		if block >= 8 {
			memReg = byte(MR13)
		}
		cur, err := d.t.ReadByteData(d.addr, memReg)
		if err != nil {
			return fmt.Errorf("读 MR%d: %w", memReg, err)
		}
		return d.t.WriteByteData(d.addr, memReg, cur|(1<<(block&7)))
	}
	return d.t.Quick(swpCmds[block], true)
}

// RSWPClear 清除全部可逆写保护(DDR4 CWP / DDR5 MR12-MR13 置 0)。
func (d *Device) RSWPClear() error {
	if d.ddr5 {
		if err := d.t.WriteByteData(d.addr, MR12, 0); err != nil {
			return fmt.Errorf("清 MR12: %w", err)
		}
		if err := d.t.WriteByteData(d.addr, MR13, 0); err != nil {
			return fmt.Errorf("清 MR13: %w", err)
		}
		mr12, err := d.t.ReadByteData(d.addr, MR12)
		if err != nil {
			return err
		}
		mr13, err := d.t.ReadByteData(d.addr, MR13)
		if err != nil {
			return err
		}
		if mr12 != 0 || mr13 != 0 {
			return fmt.Errorf("清除后 MR12=%#x MR13=%#x, 保护可能不可逆", mr12, mr13)
		}
		return nil
	}
	return d.t.Quick(cwp, true)
}

// PSWPStatus 检测永久写保护状态(BYTE 协议读 0x30|(addr&7))。
// true = 已永久保护(设备 NACK)。与原版 SMBus 路径行为一致。
func (d *Device) PSWPStatus() (bool, error) {
	_, err := d.t.ReadByteData(pswpID|(d.addr&7), 0)
	if err == nil {
		return false, nil
	}
	// 仅 NACK 视为已保护; 其他错误(超时/总线)原样返回。
	if isNACK(err) {
		return true, nil
	}
	return false, err
}

// OfflineMode 报告 DDR5 hub 是否处于离线模式(MR48 bit2)。仅 DDR5。
func (d *Device) OfflineMode() (bool, error) {
	if !d.ddr5 {
		return false, fmt.Errorf("仅 DDR5 支持离线模式查询")
	}
	b, err := d.t.ReadByteData(d.addr, MR48)
	if err != nil {
		return false, err
	}
	return b&0x04 != 0, nil
}

// Close 断开设备(当前无独占资源,保留接口对称性)。
func (d *Device) Close() {}

// isNACK 判断错误是否为设备无响应(NACK)。Fake 与 PawnIO 后端的文案保持一致。
func isNACK(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "NACK") || contains(err.Error(), "无响应")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
