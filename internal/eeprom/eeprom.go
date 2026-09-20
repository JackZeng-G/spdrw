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
	"errors"
	"fmt"
	"sort"
	"time"

	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// DDR4 EE1004 命令设备地址(原版 EepromCommand >> 1)。
const (
	spa0 = 0x36 // 选择页 0
	spa1 = 0x37 // 选择页 1
	cwp  = 0x33 // 清除写保护
	// pswpProbeAddr 是探测 PSWP 用的设备地址基址((PWPB<<3)|SA)。
	//
	// 注意: 它和 swpCmds[3] 的数值都是 0x30, 但语义完全不同 —— 这里是**地址**
	// (PSWP 器件不再应答这个地址), 那边是 DDR4 SWP3 的**命令**。当前靠
	// PSWPApplicable() 只在 256B(DDR2/DDR3)器件上探测才没串味, 改名以免误读。
	pswpProbeAddr = 0x30
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

// ErrDryRunNoWriteTest 表示"干跑模式跳过写测试, 保护状态未知"。
// 调用方应把它当作"不知道"而不是"受保护"。
var ErrDryRunNoWriteTest = errors.New("干跑模式不做写测试(保护状态未知)")

// Device 是连接到一条 SMBus 总线上某个 SPD 地址的 EEPROM。
type Device struct {
	t         smbus.Transport
	addr      byte
	ddr5      bool
	size      int
	ramType   byte // SPD byte2(DDR2=0x08/0x09/0x0A, DDR3=0x0B, DDR4=0x0C...)
	typeKnown bool // byte2 是否落在已知器件类型里(未知 → 拒绝写入)
	page      int  // 当前页(DDR4: 0-1; DDR5: 0-7)
	pageKnown bool // false = 未知(HUB 的 MR11 可能有 BIOS 残留值), 首次切页前须回读

	// 干跑模式: 写只落在 shadow(整片镜像), 总线上一字节都不写。
	dryRun bool
	shadow []byte

	// 块读加速: SMBus Block Read(协议 5) 一次 32 字节, 把整片读取从事务数=字节数
	// 降到 1/32。设备不一定支持(EE1004 规范无块读、SPD5 HUB 视固件而定), 所以
	// 首次使用要探测, 失败自动回退到逐字节读并记住结论。
	fastRead      bool   // 是否允许尝试块读(可由界面开关)
	blockOK       *bool  // nil=未探测; true=可用; false=不可用(回退)
	blockFallback int    // 回退到逐字节读的字节数(用于日志)
	blockBytes    int    // 走块读读到的字节数
	readTx        int    // 读事务计数(字节读=1, 块读=1)
	blockNote     string // 块读失败原因(诊断)
	wordRead      bool   // 是否允许字读(默认开)
	wordOK        *bool  // 字读(2 字节/事务)是否可用
	wordBytes     int
	readStats     ReadStats
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
	// 探测阶段**只读**: 不做任何"选页 0 再重读"的补救写。
	//
	// 曾经想按 linux spd5118.c 的做法(页寄存器非 0 时先选页 0 再重读 MR0)来救
	// "MR0 读作 0"的 hub, 但那在**非 DDR5** 器件上 cmd 0x0B 就是 NVM 字节 11 ——
	// 探测本身就把别人的 SPD 写坏了(实测: DDR4 镜像 byte 0x0B 被写成 0)。
	// 误判的后果由别处兜住: 待写内容的世代/长度/CRC 门禁(gateDump)会拒绝把
	// DDR5 的 1024B 内容写进一个被判成 256B 的设备, 类型未知时直接拒绝写入。
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
		d.ramType = ramType
		d.size = sizeByRamType(ramType)
		d.typeKnown = spd.RamTypeFromByte(ramType) != spd.Unknown
	}

	// 注意: 探测阶段不做任何写操作(页复位/页切换推迟到真正读写时由
	// physOffset 执行) —— 避免探测阶段对未知设备触发写事务。
	d.fastRead = true
	d.wordRead = true
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

// RamType 返回设备自己识别出的 SPD 世代。
//
// DDR5 系由探测得出(器件类型字节在 MR 区, 读不到), 其余按 byte2 映射。
// 写入前必须用它和待写内容的世代对照: 长度相同的世代不止一个(256/512/1024 各有多个),
// 光比长度会把 DDR3 的镜像写进 DDR2 条里。
func (d *Device) RamType() spd.RamType {
	if d.ddr5 {
		return spd.DDR5
	}
	return spd.RamTypeFromByte(d.ramType)
}

// WPBlockCount 返回写保护块数(DDR5 16×64B / DDR4 4×128B / DDR3 1×128B)。
func (d *Device) WPBlockCount() int {
	n, err := d.blockCount()
	if err != nil {
		return 0
	}
	return n
}

// TypeKnown 报告设备自己报出的器件类型是否可识别(不可识别时禁止写入)。
func (d *Device) TypeKnown() bool { return d.ddr5 || d.typeKnown }

// NeedsWriteTest 报告该器件的写保护探测是否需要"取反写一字节再还原"的真实写测试
// (DDR5 读 MR12/MR13 位图即可, 不需要写)。需要写测试的路径必须先备份。
func (d *Device) NeedsWriteTest() bool { return !d.ddr5 }

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
			// 读-改-写: MR11 只写整字节会清掉 bit7:4 与 bit3(内核 spd5118.c 用
			// selector_mask=GENMASK(2,0) 并保留 bit3 —— 那是寻址模式位, 清掉它
			// 会在个别 hub 上静默切换寻址模式)。读不到就退回只写低 3 位。
			val := byte(p & 0x07)
			if cur, rerr := d.t.ReadByteData(d.addr, MR11); rerr == nil {
				val = (cur & 0xF8) | byte(p&0x07)
			}
			if err = d.t.WriteByteData(d.addr, MR11, val); err != nil {
				continue
			}
			// 回读校验(只读, 不落 NVM): 低 3 位必须是目标页
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

// ReadDelay 是逐字节读之间的间隔。默认 1ms(对齐 RAMSPDToolkit 的 SPD_IO_DELAY,
// DDR5 HUB 对背靠背事务敏感); 测试可临时置 0 把整片读取从秒级降到毫秒级。
// 块读路径不加这个间隔(32 字节一次事务, 间隔没有意义)。
var ReadDelay = time.Millisecond

// SetFastRead 开关块读加速(默认开)。关闭后一律逐字节读, 用于对照排查。
func (d *Device) SetFastRead(on bool) { d.fastRead = on }

// FastRead 报告是否允许块读。
func (d *Device) FastRead() bool { return d.fastRead }

// ReadStats 报告上一次整片读取的方式与事务数。
type ReadStats struct {
	Bytes          int    `json:"bytes"`
	Transactions   int    `json:"transactions"`
	BlockBytes     int    `json:"blockBytes"`
	FallbackBytes  int    `json:"fallbackBytes"`
	BlockReadOK    bool   `json:"blockReadOK"`
	BlockReadKnown bool   `json:"blockReadKnown"`
	WordReadOK     bool   `json:"wordReadOK"`
	WordBytes      int    `json:"wordBytes"`
	WordReadKnown  bool   `json:"wordReadKnown"`
	Mode           string `json:"mode"`
	ElapsedMS      int64  `json:"elapsedMs"`
	SleepMS        int64  `json:"sleepMs"`
	Note           string `json:"note,omitempty"`
}

// ReadStats 返回读取统计(用于界面显示"快/慢"与排查)。
func (d *Device) ReadStats() ReadStats {
	st := d.readStats
	st.Transactions = d.readTx
	st.BlockBytes = d.blockBytes
	st.FallbackBytes = d.blockFallback
	st.BlockReadKnown = d.blockOK != nil
	st.WordReadKnown = d.wordOK != nil
	if d.wordOK != nil {
		st.WordReadOK = *d.wordOK
	}
	st.WordBytes = d.wordBytes
	if d.blockNote != "" {
		st.Note = d.blockNote
	}
	if d.blockOK != nil {
		st.BlockReadOK = *d.blockOK
	}
	return st
}

// resetReadStats 在每次整片读取前清零。
func (d *Device) resetReadStats() {
	d.readTx, d.blockBytes, d.blockFallback, d.wordBytes = 0, 0, 0, 0
	d.readStats = ReadStats{}
}

// SetWordRead 开关字读加速(2 字节/事务)。块读不可用时它是第二档。
func (d *Device) SetWordRead(on bool) { d.wordRead = on }

// Read 读取 n 字节(n 为 0 时报错)。
//
// 优先走 **SMBus Block Read**(协议 5, 一次最多 32 字节): 真机上单次事务约 30ms,
// 逐字节读 1024B 要 30 多秒, 块读能降到 1~2 秒。设备不一定支持(EE1004 规范没有块读、
// SPD5 HUB 视固件而定), 因此首次使用会**探测**, 失败立即回退逐字节并记住结论。
// 逐字节路径保留 ReadDelay(默认 1ms, 对齐 RAMSPDToolkit)间隔。
func (d *Device) Read(off uint16, n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("读取长度必须为正")
	}
	if int(off)+n > d.size {
		return nil, fmt.Errorf("读取范围 %#x+%#x 越界", off, n)
	}
	out := make([]byte, n)
	start := time.Now()
	for i := 0; i < n; {
		cur := off + uint16(i)
		if d.fastRead && d.blockReadUsable() {
			got, err := d.readBlockChunk(cur, n-i)
			if err == nil && len(got) > 0 {
				copy(out[i:], got)
				i += len(got)
				d.blockBytes += len(got)
				d.readTx++
				continue
			}
			d.markBlockUnusable(err)
		}
		if d.fastRead && d.wordRead && d.wordReadUsable() && n-i >= 2 && int(cur)%d.pageSize() != d.pageSize()-1 {
			if b0, b1, err := d.readWordPair(cur); err == nil {
				out[i], out[i+1] = b0, b1
				i += 2
				d.wordBytes += 2
				d.readTx++
				continue
			}
			d.markWordUnusable()
		}
		b, err := d.readOne(cur)
		if err != nil {
			return nil, err
		}
		out[i] = b
		i++
		d.blockFallback++
		d.readTx++
	}
	d.readStats.ElapsedMS = time.Since(start).Milliseconds()
	switch {
	case d.blockOK != nil && *d.blockOK:
		d.readStats.Mode = "块读(32 字节/事务)"
	case d.wordOK != nil && *d.wordOK:
		d.readStats.Mode = "字读(2 字节/事务)"
	default:
		d.readStats.Mode = "逐字节(1 字节/事务)"
	}
	return out, nil
}

// wordReadUsable 探测字读(2 字节/事务)是否可用: 读一个字并与两次字节读对照。
func (d *Device) wordReadUsable() bool {
	if d.wordOK != nil {
		return *d.wordOK
	}
	ok := new(bool)
	d.wordOK = ok
	b0, b1, err := d.readWordPair(0)
	if err != nil {
		return false
	}
	r0, err0 := d.readOne(0)
	r1, err1 := d.readOne(1)
	if err0 != nil || err1 != nil || r0 != b0 || r1 != b1 {
		return false
	}
	*ok = true
	return true
}

func (d *Device) markWordUnusable() {
	if d.wordOK == nil {
		d.wordOK = new(bool)
	}
	*d.wordOK = false
}

// readWordPair 用 SMBus Word Read 读两个连续字节(小端)。
func (d *Device) readWordPair(off uint16) (byte, byte, error) {
	_, phys, err := d.physOffset(off)
	if err != nil {
		return 0, 0, err
	}
	v, err := d.t.ReadWordData(d.addr, phys)
	if err != nil {
		return 0, 0, err
	}
	return byte(v), byte(v >> 8), nil
}

// blockReadUsable 报告当前是否走块读; 未探测时先探测一次(只读, 不写)。
func (d *Device) blockReadUsable() bool {
	if d.blockOK != nil {
		return *d.blockOK
	}
	// 探测: 取一块, 与逐字节读的结果比对; 不一致或报错都视为不支持。
	got, err := d.readBlockChunk(0, 32)
	if err != nil || len(got) == 0 {
		d.blockOK = new(bool)
		*d.blockOK = false
		return false
	}
	ref, err := d.readOne(0)
	if err != nil || ref != got[0] {
		d.blockOK = new(bool)
		*d.blockOK = false
		return false
	}
	d.blockOK = new(bool)
	*d.blockOK = true
	return true
}

// markBlockUnusable 记录块读不可用(探测阶段失败或运行中失败都视为不可用)。
func (d *Device) markBlockUnusable(err error) {
	if d.blockOK != nil && !*d.blockOK {
		return
	}
	d.blockOK = new(bool)
	*d.blockOK = false
	if err != nil {
		d.blockNote = fmt.Sprintf("%v", err)
	}
}

// readBlockChunk 读一块: 不超过 32 字节, 且不跨越页边界(设备页内自增, 跨页会读到别的页)。
func (d *Device) readBlockChunk(off uint16, max int) ([]byte, error) {
	_, phys, err := d.physOffset(off)
	if err != nil {
		return nil, err
	}
	ps := d.pageSize()
	pageRemain := ps - int(off)%ps
	want := smbus.ProtoBlockMax
	if max < want {
		want = max
	}
	if pageRemain < want {
		want = pageRemain
	}
	if want <= 0 {
		return nil, fmt.Errorf("块读长度非法")
	}
	got, err := d.t.ReadBlockData(d.addr, phys)
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, fmt.Errorf("块读返回空数据")
	}
	if len(got) > want {
		got = got[:want]
	}
	return got, nil
}

// ReadOneByte 对外暴露"逐字节读一个字节"(强制走字节读, 不经过块读/字读)。
// 探测与单字节校验用它, 避免"写进去的位置"和"读回来的位置"经过不同的读路径。
func (d *Device) ReadOneByte(off uint16) (byte, error) { return d.readOne(off) }

// readOne 逐字节读一个字节(带 ReadDelay 间隔)。
func (d *Device) readOne(off uint16) (byte, error) {
	_, phys, err := d.physOffset(off)
	if err != nil {
		return 0, err
	}
	b, err := d.t.ReadByteData(d.addr, phys)
	if err != nil {
		return 0, fmt.Errorf("读取 %#x 失败: %w", off, err)
	}
	if ReadDelay > 0 {
		time.Sleep(ReadDelay)
		d.readStats.SleepMS += ReadDelay.Milliseconds()
	}
	return b, nil
}

// ReadAll 整片读取(并清零读取统计)。
func (d *Device) ReadAll() ([]byte, error) {
	d.resetReadStats()
	b, err := d.Read(0, d.size)
	if err == nil {
		d.readStats.Bytes = len(b)
	}
	return b, err
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

// ByteChange 是一个待写字节的变更(写入计划的最小单位)。
type ByteChange struct {
	Offset int  `json:"offset"`
	Old    byte `json:"old"`
	New    byte `json:"new"`
	Block  int  `json:"block"`
	IsCRC  bool `json:"isCRC"` // CRC/校验字节: 排在计划最后写
}

// ProgressFunc 报告写入进度(已写字节数 / 计划总字节数)。
type ProgressFunc func(written, total int)

// WriteError 描述一次中止的写入: 已经写了多少、卡在哪个偏移。
// 半写状态是真实风险(SPD 可能开不了机), 所以必须把"已写/未写"和恢复建议讲清楚。
type WriteError struct {
	Offset  int
	Written int
	Total   int
	Err     error
	Stage   string // "read" / "write" / "verify"
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("写入中止(%s)@ 0x%03X: 已写 %d/%d 字节, 未写 %d 字节: %v;"+
		" 建议立即用备份重写该条 SPD(或离线恢复)", e.Stage, e.Offset, e.Written, e.Total, e.Total-e.Written, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }

// checkDumpLen 严格校验写入长度: 必须与 SPD 大小完全一致。
// 旧实现用 dump[:size] 截断, 长度不足时会切片越界 panic, 过长时静默丢弃尾部。
func (d *Device) checkDumpLen(dump []byte) error {
	switch {
	case len(dump) == 0:
		return fmt.Errorf("空数据")
	case len(dump) != d.size:
		return fmt.Errorf("数据长度 %d 字节与 %s SPD 大小 %d 字节不一致(不支持截断或补齐写入)",
			len(dump), d.Generation(), d.size)
	}
	return nil
}

// current 读取当前整片内容(干跑模式下读影子)。
func (d *Device) current() ([]byte, error) {
	if d.dryRun && d.shadow != nil {
		out := make([]byte, len(d.shadow))
		copy(out, d.shadow)
		return out, nil
	}
	return d.ReadAll()
}

// CRCOffsets 返回各校验(CRC)字节的逻辑偏移: 这些字节在写入计划里排到最后。
//
// DDR4: 两段 CRC(每 128B 段末 2 字节); DDR5: 基础段 CRC(510/511)、XMP 3.0 header
// CRC(0x2BE/0x2BF)、5 个 profile 槽的 CRC(槽末 2 字节)、EXPO CRC(0x3BE/0x3BF)。
// 只返回"目标 dump 里该区域非空白"的校验字节 —— 空白(全 0/全 0xFF)的槽位不写。
func (d *Device) CRCOffsets(dump []byte) []int {
	if dump == nil {
		dump = d.shadow
	}
	// 统一由 spd 层判定(两处各写一套判定会漂移: 实测对同一份 dump 会给出不同的槽位集合)
	if dump != nil {
		if offs := spd.CRCOffsets(dump); len(offs) > 0 {
			return offs
		}
	}
	var out []int
	nonBlank := func(start, n int) bool {
		if start < 0 || start+n > len(dump) {
			return false
		}
		allFF, all00 := true, true
		for _, b := range dump[start : start+n] {
			if b != 0xFF {
				allFF = false
			}
			if b != 0x00 {
				all00 = false
			}
		}
		return !allFF && !all00
	}
	switch d.size {
	case 256:
		// DDR2 是 8 位和校验(byte63 = sum(0..62)); DDR3 是 CRC16(126/127)
		if isDDR2Type(d.ramType) {
			out = append(out, 63)
		} else {
			out = append(out, 126, 127)
		}
	case 512:
		out = append(out, 126, 127, 254, 255)
	default: // DDR5 1024
		out = append(out, 510, 511)
		if len(dump) >= 0x282 && dump[0x280] == 0x0C && dump[0x281] == 0x4A {
			out = append(out, 0x2BE, 0x2BF) // XMP 3.0 header CRC
		}
		for _, slot := range []int{0x2C0, 0x300, 0x340, 0x380, 0x3C0} {
			if nonBlank(slot, 62) {
				out = append(out, slot+62, slot+63)
			}
		}
		if nonBlank(0x340, 126) { // EXPO 与 XMP profile3/user1 区重叠, 按内容判定
			out = append(out, 0x3BE, 0x3BF)
		}
	}
	return out
}

// PlanWrite 计算写入计划(不写任何字节, 可安全调用)。
//
// force=false: update 模式, 只列出与当前内容不同的字节;
// force=true: 全量写入。
// 计划排序: 数据字节在前(按偏移升序), CRC 字节全部排在最后 —— 万一写入中断,
// 设备上留下的是"CRC 与数据不符"的 SPD(BIOS 会拒绝), 而不是"校验通过但内容错"的 SPD。
func (d *Device) PlanWrite(dump []byte, force bool) ([]ByteChange, error) {
	if err := d.checkDumpLen(dump); err != nil {
		return nil, err
	}
	cur, err := d.current()
	if err != nil {
		return nil, fmt.Errorf("读取当前内容失败: %w", err)
	}
	crcSet := map[int]bool{}
	for _, off := range d.CRCOffsets(dump) {
		crcSet[off] = true
	}
	changes := make([]ByteChange, 0, len(dump))
	for i := range dump {
		if !force && cur[i] == dump[i] {
			continue
		}
		changes = append(changes, ByteChange{
			Offset: i, Old: cur[i], New: dump[i], Block: i / d.blockSize(), IsCRC: crcSet[i],
		})
	}
	sort.SliceStable(changes, func(a, b int) bool {
		if changes[a].IsCRC != changes[b].IsCRC {
			return !changes[a].IsCRC
		}
		return changes[a].Offset < changes[b].Offset
	})
	return changes, nil
}

// ApplyWrite 执行写入计划并逐字节回读校验。
// 干跑模式下不产生任何总线写事务, 只把结果落到内存影子(用于零风险验证全流程)。
func (d *Device) ApplyWrite(dump []byte, changes []ByteChange, progress ProgressFunc) error {
	if err := d.checkDumpLen(dump); err != nil {
		return err
	}
	total := len(changes)
	if progress != nil {
		progress(0, total)
	}
	for i, ch := range changes {
		if err := d.writeOne(uint16(ch.Offset), ch.New); err != nil {
			return &WriteError{Offset: ch.Offset, Written: i, Total: total, Err: err, Stage: "write"}
		}
		// 注意计数: 到这里字节**已经写下去了**, 所以"已写"是 i+1(旧实现按 i 少算 1)
		back, err := d.readBack(uint16(ch.Offset))
		if err != nil {
			return &WriteError{Offset: ch.Offset, Written: i + 1, Total: total, Err: err, Stage: "read"}
		}
		if back != ch.New {
			return &WriteError{
				Offset: ch.Offset, Written: i + 1, Total: total, Stage: "verify",
				Err: fmt.Errorf("回读 %#x 与目标 %#x 不一致(该块可能受写保护, 写被忽略)", back, ch.New),
			}
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return nil
}

// writeOne 写一个字节(干跑模式改为写影子)。
func (d *Device) writeOne(off uint16, val byte) error {
	if d.dryRun {
		if d.shadow == nil {
			return fmt.Errorf("干跑模式影子未初始化")
		}
		if int(off) >= len(d.shadow) {
			return fmt.Errorf("干跑偏移 %#x 越界", off)
		}
		d.shadow[off] = val
		return nil
	}
	return d.WriteByteAt(off, val)
}

// readBack 回读一个字节(干跑模式读影子)。
func (d *Device) readBack(off uint16) (byte, error) {
	if d.dryRun {
		if d.shadow == nil || int(off) >= len(d.shadow) {
			return 0, fmt.Errorf("干跑影子不可用")
		}
		return d.shadow[off], nil
	}
	b, err := d.Read(off, 1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// Write 将 dump 写入 EEPROM: PlanWrite + ApplyWrite(update 模式跳过相同字节)。
func (d *Device) Write(dump []byte, force bool, progress ProgressFunc) error {
	changes, err := d.PlanWrite(dump, force)
	if err != nil {
		return err
	}
	return d.ApplyWrite(dump, changes, progress)
}

// Verify 比对 EEPROM 内容与 dump(长度必须完全一致)。
func (d *Device) Verify(dump []byte) error {
	if err := d.checkDumpLen(dump); err != nil {
		return err
	}
	cur, err := d.ReadAll()
	if err != nil {
		return err
	}
	for i := range cur {
		if cur[i] != dump[i] {
			return fmt.Errorf("内容不一致 @ 0x%03X: 设备 %#x 文件 %#x", i, cur[i], dump[i])
		}
	}
	return nil
}

// PhysDesc 返回某个逻辑偏移的物理访问描述(纯计算, 不碰总线), 用于错误报告:
// DDR5 = "页 1, cmd 0x88"; DDR4 及更早 = "页 0, cmd 0x88"。
func (d *Device) PhysDesc(off uint16) string {
	ps := d.pageSize()
	if d.ddr5 {
		return fmt.Sprintf("页 %d, cmd %#02x", int(off)/ps, byte(int(off)%ps)|spd5NVMReg)
	}
	return fmt.Sprintf("页 %d(SPA%d), cmd %#02x", int(off)>>8, int(off)>>8, byte(off))
}

// MRSnapshot 只读地抓一份 DDR5 hub 的关键寄存器现场(非 DDR5 返回空串)。
//
// 写入失败时把它附在错误里: MR12/MR13 是 RSWP 块位图, MR52 bit6 是"最近有写受保护块
// 被忽略", MR48 bit3 与 NVM 访问许可相关 —— 有这几个值就能判断"到底是谁拒绝了写入"。
func (d *Device) MRSnapshot() string {
	if !d.ddr5 {
		return ""
	}
	read := func(reg byte) string {
		v, err := d.t.ReadByteData(d.addr, reg)
		if err != nil {
			return "??"
		}
		return fmt.Sprintf("%#02x", v)
	}
	return fmt.Sprintf("MR11=%s MR12=%s MR13=%s MR29=%s MR48=%s MR52=%s",
		read(MR11), read(MR12), read(MR13), read(MR29), read(MR48), read(MR52))
}

// VerifyChangedByteWise 用**最原始的逐字节读法**复核改动过的字节。
//
// 为什么需要: 每字节回读与整片 Verify 都走同一条读路径(优先块读/字读), 一旦该路径
// 有系统性偏差, 两者会一起"同意"而报成功。改动字节通常只有几个, 用逐字节读法再核一遍
// 几乎不花时间(真机上每字节一次事务, 几个字节 = 几十毫秒)。
func (d *Device) VerifyChangedByteWise(dump []byte, changes []ByteChange) error {
	if err := d.checkDumpLen(dump); err != nil {
		return err
	}
	offs := make([]int, 0, len(changes))
	for _, ch := range changes {
		offs = append(offs, ch.Offset)
	}
	return d.verifyByteWiseAt(dump, offs)
}

// VerifyByteWise 用逐字节读法复核**整片**内容。
//
// 比"只复核改动字节"更强: SP5 的分页/窗口写错位、外部工具同时动总线这类问题会让
// 计划之外的字节发生变化, 只有整片独立读一遍才能发现。逐字节读 1024 字节在忙等模式下
// 约 1 秒(旧实现里整片逐字节读要 32 秒, 所以当时只复核改动字节)。
func (d *Device) VerifyByteWise(dump []byte) error {
	if err := d.checkDumpLen(dump); err != nil {
		return err
	}
	offs := make([]int, len(dump))
	for i := range offs {
		offs[i] = i
	}
	return d.verifyByteWiseAt(dump, offs)
}

// verifyByteWiseAt 关掉块读/字读, 逐个偏移读回来比对(读完全程再恢复设置)。
func (d *Device) verifyByteWiseAt(dump []byte, offs []int) error {
	fast, word := d.fastRead, d.wordRead
	d.fastRead, d.wordRead = false, false
	defer func() { d.fastRead, d.wordRead = fast, word }()
	for _, off := range offs {
		got, err := d.readOne(uint16(off))
		if err != nil {
			return fmt.Errorf("逐字节复核 %#x 失败: %w", off, err)
		}
		if got != dump[off] {
			return fmt.Errorf("逐字节复核不一致 @ 0x%03X: 设备 %#x 目标 %#x", off, got, dump[off])
		}
	}
	return nil
}

// ---------------- 干跑(dry-run) ----------------

// SetDryRun 开启/关闭干跑模式。开启时先读取整片建立内存影子, 之后所有"写"
// 只落在影子并从未初始化的总线上消失 —— 用于在真机上零风险跑通写入全流程
// (计划/diff/CRC 顺序/回读校验), 一个字节都不会发到 SPD。
func (d *Device) SetDryRun(v bool) error {
	if v {
		img, err := d.ReadAll()
		if err != nil {
			return fmt.Errorf("干跑模式需先读取整片建立影子: %w", err)
		}
		d.shadow = img
		d.dryRun = true
		return nil
	}
	d.dryRun = false
	d.shadow = nil
	return nil
}

// DryRun 报告当前是否处于干跑模式。
func (d *Device) DryRun() bool { return d.dryRun }

// ShadowImage 返回干跑模式下的整片镜像(未开启返回 nil)。
func (d *Device) ShadowImage() []byte {
	if d.shadow == nil {
		return nil
	}
	out := make([]byte, len(d.shadow))
	copy(out, d.shadow)
	return out
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
	if d.dryRun {
		// 干跑是"零风险"承诺: 写测试要真的取反写一个字节再还原, 一旦掉电/总线异常/
		// 还原失败就会永久改坏该字节。因此干跑下直接报"状态未知", 一个字节都不写。
		return false, ErrDryRunNoWriteTest
	}
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
			if errors.Is(err, ErrDryRunNoWriteTest) {
				// 干跑: 状态未知(不写任何字节), 但也不能说成"受保护"
				det.Protected[b] = false
				continue
			}
			det.Protected[b] = true
			det.Warnings = append(det.Warnings, fmt.Sprintf("块 %d(0x%03X)状态未知: %v", b, b*det.BlockSize, err))
			continue
		}
		det.Known[b] = true
		det.Protected[b] = !writable
	}
	if d.dryRun {
		det.Warnings = append(det.Warnings, "干跑模式: 跳过 DDR4/更早世代的块首写测试, 保护状态未知(不写任何字节)")
	} else {
		det.Warnings = append(det.Warnings,
			fmt.Sprintf("%s 无写保护状态寄存器: 状态由块首写测试得出(每块写入取反值再还原)", d.Generation()))
	}

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
	_, err := d.t.ReadByteData(pswpProbeAddr|(d.addr&7), 0)
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

// isDDR2Type 判断 SPD byte2 是否 DDR2 系(0x08 DDR2 / 0x09 FB-DIMM / 0x0A FB-DIMM Probe)。
func isDDR2Type(ramType byte) bool {
	return ramType == 0x08 || ramType == 0x09 || ramType == 0x0A
}
