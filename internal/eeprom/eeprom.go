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
	"time"

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
	MR29 = 29 // I2C/寄存器写保护相关(原始值诊断, 语义因芯片而异)
	MR48 = 48 // Device Status(bit2 = offline mode)
	MR52 = 52 // Device Status 扩展(bit6 = 写受保护块被忽略)

	spd5NVMReg = 0x80 // DDR5 NVM 访问位(offset%128 | 0x80)
)

// Device 是连接到一条 SMBus 总线上某个 SPD 地址的 EEPROM。
type Device struct {
	t         smbus.Transport
	addr      byte
	ddr5      bool
	size      int
	page      int  // 当前页(DDR4: 0-1; DDR5: 0-7)
	pageKnown bool // false = 未知(HUB 的 MR11 可能有 BIOS 残留值), 首次切页前须回读
}

// New 建立设备连接: 探测地址、识别 DDR5 与 SPD 大小。
// 不假设初始页状态 —— DDR5 HUB 的 MR11 可能残留任意页(对齐 RAMSPDToolkit:
// 每次 NVM 访问前回读 MR11 决定是否切页)。
func New(t smbus.Transport, addr byte) (*Device, error) {
	if addr>>3 != 0b1010 {
		return nil, fmt.Errorf("无效 EEPROM 地址 %#x (应在 0x50-0x57)", addr)
	}
	d := &Device{t: t, addr: addr}

	// DDR5 检测(对齐 RAMSPDToolkit DDR5Accessor.IsAvailable):
	// 读 MR0(Device Type, cmd=0, bit7=0 → 寄存器区) == 0x51 即 DDR5。
	// MR0 是寄存器不受 MR11 页残留影响, 比读 NVM 字节更可靠。
	ddr5 := false
	if b, err2 := t.ReadByteData(addr, 0); err2 == nil && b == 0x51 {
		ddr5 = true
	}
	d.ddr5 = ddr5

	if ddr5 {
		d.size = 1024
		// 回读 MR11 确认初始页(不写!有些模块写保护, 只读同步缓存)
		if cur, err := t.ReadByteData(addr, MR11); err == nil {
			d.page = int(cur & 0x07)
			d.pageKnown = true
		}
	} else {
		ramType, err := t.ReadByteData(addr, 2)
		if err != nil {
			return nil, fmt.Errorf("读取 DRAM 类型失败(地址 %#x): %w", addr, err)
		}
		d.size = sizeByRamType(ramType)
	}

	// 注意: 探测阶段不做任何写操作(页复位/页切换推迟到真正读写时由
	// physOffset 执行) —— 避免探测阶段对未知设备触发写事务。
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

// setPage 切换 EEPROM 页。DDR5 在写之后回读 MR11 校验(对齐 RAMSPDToolkit
// GetPage/SetPage): HUB 对页写的实际生效与控制器返回值未必一致。
func (d *Device) setPage(p int) error {
	if p < 0 || p >= d.pageCount() {
		return fmt.Errorf("页 %d 越界(共 %d 页)", p, d.pageCount())
	}
	var err error
	if d.ddr5 {
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				time.Sleep(2 * time.Millisecond)
			}
			if err = d.t.WriteByteData(d.addr, MR11, byte(p)); err != nil {
				continue
			}
			// 回读校验(只读, 不落 NVM)
			if cur, rerr := d.t.ReadByteData(d.addr, MR11); rerr == nil && int(cur&0x07) == p {
				d.page, d.pageKnown = p, true
				return nil
			}
			err = fmt.Errorf("页寄存器校验失败(期望 %d)", p)
		}
		if err != nil {
			return err
		}
		d.page, d.pageKnown = p, true
		return nil
	}
	// DDR4 页切换: 首选 Quick 写(EE1004 SPA), 失败回退 BYTE 写
	// (与原版一致的退化路径, 部分控制器对 Quick 支持不佳)
	err = d.t.Quick(byte(spa0+p), true)
	if err != nil {
		err = d.t.WriteByteNoData(byte(spa0 + p))
	}
	if err != nil {
		return err
	}
	d.page, d.pageKnown = p, true
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
	if p != d.page || !d.pageKnown {
		if err := d.setPage(p); err != nil {
			return 0, 0, err
		}
	}
	return byte(p), phys, nil
}

// Read 读取 n 字节(n 为 0 时报错)。每字节读后间隔 1ms(对齐 RAMSPDToolkit
// SPD_IO_DELAY): DDR5 HUB 对背靠背事务敏感, 连发会导致数据错位。
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
		time.Sleep(time.Millisecond)
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

// WriteTest 对指定偏移做写保护测试: 写入取反值 → 回读确认 → 还原 → 回读确认还原。
//
// 返回 (writable, err):
//   - writable=false, err=nil: 设备拒绝/忽略该写 → 该块受写保护
//   - writable=true,  err=nil: 写入成功且已确认还原(内容不变)
//   - err != nil: 状态无法判定, 且字节值可能已被改写(错误信息会指出偏移与原值)
//
// 与原始实现的关键差异: **还原后必须回读确认**, 失败重试 3 次仍不成功则报错。
// 原实现只写不校验, 还原写失败会永久改掉该字节且无任何提示(静默数据损坏)。
func (d *Device) WriteTest(off uint16) (bool, error) {
	b, err := d.Read(off, 1)
	if err != nil {
		return false, fmt.Errorf("写测试读取 %#x: %w", off, err)
	}
	orig := b[0]
	flipped := orig ^ 0xFF

	if err := d.WriteByteAt(off, flipped); err != nil {
		if isNACK(err) {
			return false, nil // 设备拒绝写入 → 受保护
		}
		return false, fmt.Errorf("写测试写入 %#x: %w", off, err)
	}

	// 确认写是否真的生效: 有的 HUB/颗粒会静默忽略受保护块的写(不 NACK)。
	back, err := d.Read(off, 1)
	if err != nil {
		return false, fmt.Errorf("写测试回读 %#x: %w", off, err)
	}
	if back[0] != flipped {
		if back[0] == orig {
			return false, nil // 写被忽略 → 受保护(内容未变, 无需还原)
		}
		// 出现了既非原值也非目标值的异常值: 尽力还原后再报错
		_, rerr := d.restoreByte(off, orig)
		return false, fmt.Errorf("写测试回读异常 @ %#x: 原 %#x 写 %#x 读 %#x(还原错误: %v)",
			off, orig, flipped, back[0], rerr)
	}

	if ok, rerr := d.restoreByte(off, orig); !ok {
		return false, fmt.Errorf("写测试还原失败 @ %#x(原值 %#x): %w; 该字节可能已被改写, 请立即用备份恢复",
			off, orig, rerr)
	}
	return true, nil
}

// restoreByte 把 off 处的值写回 want 并回读确认, 最多重试 3 次。
func (d *Device) restoreByte(off uint16, want byte) (bool, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		if lastErr = d.WriteByteAt(off, want); lastErr != nil {
			continue
		}
		cur, err := d.Read(off, 1)
		if err != nil {
			lastErr = err
			continue
		}
		if cur[0] == want {
			return true, nil
		}
		lastErr = fmt.Errorf("还原后回读仍为 %#x(期望 %#x)", cur[0], want)
	}
	return false, lastErr
}

// RSWPStatus 返回各块的可逆写保护状态(保守语义: 无法判定的块按"受保护"处理)。
// DDR5: 16 块(MR12/MR13 位图); DDR4: 4 块; 更早: 1 块。
func (d *Device) RSWPStatus() ([]bool, error) {
	if d.ddr5 {
		mr12, err := d.t.ReadByteData(d.addr, MR12)
		if err != nil {
			return nil, fmt.Errorf("读 MR12: %w", err)
		}
		mr13, err := d.t.ReadByteData(d.addr, MR13)
		if err != nil {
			return nil, fmt.Errorf("读 MR13: %w", err)
		}
		result := make([]bool, 16)
		for i := 0; i < 8; i++ {
			result[i] = mr12&(1<<i) != 0
			result[i+8] = mr13&(1<<i) != 0
		}
		return result, nil
	}
	blocks, err := d.blockCount()
	if err != nil {
		return nil, err
	}
	result := make([]bool, blocks)
	for b := 0; b < blocks; b++ {
		writable, err := d.WriteTest(uint16(b * d.blockSize()))
		result[b] = err != nil || !writable // 状态未知按受保护处理, 保证写入前检查保守
	}
	return result, nil
}

// blockCount 返回可保护块数。
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

// blockSize 返回每块字节数: DDR5 按 64B 块(MR12/MR13 位图共 16 块 = 1024B);
// DDR4/更早按 128B 块(EE1004 SWP 语义)。
func (d *Device) blockSize() int {
	if d.ddr5 {
		return 64
	}
	return 128
}

// WPStatusDetail 是写保护状态的完整快照(RSWP 位图 + 原始寄存器 + 永久保护/离线)。
//
// 设计要点(修掉旧实现的假阳性):
//   - DDR5 不存在 EE1004 的 PSWP 设备类型(0110b), 旧实现去读 0x30|SA 必然 NACK,
//     于是把每根 DDR5 都误报成"PSWP 永久保护已生效"。现在 DDR5 直接标 PSWPApplicable=false。
//   - 只有 DDR2/DDR3 世代(256B SPD)才用 PWPB(0x30|SA)探测永久保护:
//     器件一旦被永久保护就不再应答 0110b 设备类型([AT34C02D 手册 7.5.1](https://onlinedocs.microchip.com/oxy/GUID-CBD9956C-D3D9-444B-A2AE-BA0049287CAB-en-US-2/GUID-8DF2B692-DDB1-476B-8550-34CD1F325B20.html))。
//   - DDR4/更早的 RSWP 状态无寄存器可读, 只能用写测试; 写测试失败/无法判定时
//     该块标 Known=false, 绝不谎报状态。
type WPStatusDetail struct {
	DDR5           bool     `json:"ddr5"`
	Blocks         int      `json:"blocks"`
	BlockSize      int      `json:"blockSize"`
	Protected      []bool   `json:"protected"` // RSWP 位图(未知按受保护处理)
	Known          []bool   `json:"known"`     // 每块状态是否确知
	MR11           byte     `json:"mr11"`
	MR12           byte     `json:"mr12"`
	MR13           byte     `json:"mr13"`
	MR29           byte     `json:"mr29"`
	MR48           byte     `json:"mr48"`
	MR52           byte     `json:"mr52"`
	ProtectionHit  bool     `json:"protectionHit"` // MR52[6]: 近期有写受保护块被忽略
	Offline        bool     `json:"offline"`       // DDR5 MR48[2]
	PSWPApplicable bool     `json:"pswpApplicable"`
	PSWP           bool     `json:"pswp"`
	RegsPresent    bool     `json:"regsPresent"`
	Warnings       []string `json:"warnings"`
}

// WPStatusDetail 读取完整写保护状态。只读操作(DDR4 的写测试会写入 1 字节再还原)。
func (d *Device) WPStatusDetail() (WPStatusDetail, error) {
	det := WPStatusDetail{DDR5: d.ddr5}
	blocks, err := d.blockCount()
	if err != nil {
		return det, err
	}
	det.Blocks, det.BlockSize = blocks, d.blockSize()
	det.Protected = make([]bool, blocks)
	det.Known = make([]bool, blocks)

	if d.ddr5 {
		mr11, e11 := d.t.ReadByteData(d.addr, MR11)
		mr12, e12 := d.t.ReadByteData(d.addr, MR12)
		mr13, e13 := d.t.ReadByteData(d.addr, MR13)
		if e12 != nil || e13 != nil {
			return det, fmt.Errorf("读 MR12/MR13 失败: %v / %v", e12, e13)
		}
		det.MR11, det.MR12, det.MR13 = mr11, mr12, mr13
		det.RegsPresent = e11 == nil
		for i := 0; i < 8; i++ {
			det.Protected[i] = mr12&(1<<i) != 0
			det.Protected[i+8] = mr13&(1<<i) != 0
			det.Known[i], det.Known[i+8] = true, true
		}
		if b, err := d.t.ReadByteData(d.addr, MR48); err == nil {
			det.MR48 = b
			det.Offline = b&0x04 != 0
		}
		if b, err := d.t.ReadByteData(d.addr, MR29); err == nil {
			det.MR29 = b
		}
		if b, err := d.t.ReadByteData(d.addr, MR52); err == nil {
			det.MR52 = b
			det.ProtectionHit = b&0x40 != 0
		}
		if mr12 != 0 || mr13 != 0 {
			det.Warnings = append(det.Warnings,
				"MR12/MR13 已置位: 按 JEDEC SPD5118 正常运行时不可清零, 需进入离线模式(MR48 bit2)或断电后由主板解除")
		}
		if det.ProtectionHit {
			det.Warnings = append(det.Warnings, "MR52[6]=1: 检测到对受保护块的写被忽略")
		}
		return det, nil
	}

	// DDR4 及更早: 无状态寄存器, 逐块写测试(写入 1 字节后还原)
	for b := 0; b < blocks; b++ {
		writable, err := d.WriteTest(uint16(b * det.BlockSize))
		if err != nil {
			det.Known[b] = false
			det.Protected[b] = true
			det.Warnings = append(det.Warnings, fmt.Sprintf("块 %d(0x%03X)状态未知: %v", b, b*det.BlockSize, err))
			continue
		}
		det.Known[b] = true
		det.Protected[b] = !writable
	}
	det.Warnings = append(det.Warnings,
		fmt.Sprintf("%s 无写保护状态寄存器: 状态由块首写测试得出(每块写入取反值再还原)", d.Generation()))

	det.PSWPApplicable = d.PSWPApplicable()
	if det.PSWPApplicable {
		pswp, err := d.PSWPStatus()
		if err != nil {
			det.Warnings = append(det.Warnings, fmt.Sprintf("PSWP 探测失败: %v", err))
		} else {
			det.PSWP = pswp
		}
	}
	return det, nil
}

// Generation 返回展示用的世代名。
func (d *Device) Generation() string {
	switch {
	case d.ddr5:
		return "DDR5"
	case d.size == 512:
		return "DDR4"
	case d.size == 256:
		return "DDR2/DDR3"
	default:
		return "未知世代"
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

// PSWPApplicable 报告该世代是否存在可用 SMBus 探测的永久写保护设备类型。
// 只有 DDR2/DDR3 一代(256B SPD, AT34C02 类器件)定义了 PWPB(0110b)设备类型;
// DDR4(EE1004)与 DDR5(SPD5118)没有它 —— 对它们探测必然 NACK, 会把"无设备"
// 误判成"已永久保护", 所以必须直接判为不适用。
func (d *Device) PSWPApplicable() bool { return !d.ddr5 && d.size == 256 }

// PSWPStatus 检测永久写保护状态: BYTE_DATA 读 0x30|(addr&7)(PWPB 设备类型 0110b)。
// 器件被永久保护后不再应答该设备类型(NACK)→ true。仅 DDR2/DDR3 适用。
func (d *Device) PSWPStatus() (bool, error) {
	if !d.PSWPApplicable() {
		return false, fmt.Errorf("%s 不支持 PSWP 探测(仅 DDR2/DDR3 定义 PWPB 设备类型)", d.Generation())
	}
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
