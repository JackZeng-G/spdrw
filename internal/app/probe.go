package app

import (
	"fmt"

	"spdrw/internal/eeprom"
	"spdrw/internal/spd"
)

// 写入能力探测: 用"写一个字节再还原"回答"这条 SPD 到底能不能写"。
//
// 为什么需要它: 真实写入失败时, 现象可能是 NACK, 也可能是"器件照常 ACK 但内容不变"
// (平台级 SPD 写保护、hub 的 RSWP/PSWP、WP# 引脚被拉低都会这样)。后者在日志里只表现为
// "回读与目标不一致", 用户无法判断是工具的问题还是主板/颗粒的问题。这个探测把结论说清楚。
//
// 安全性:
//   - 探测前先整片备份(内存镜像 + 文件);
//   - 目标字节优先选**不在任何 CRC 覆盖范围内**的空闲字节(厂商特定区 0x22B-0x27F),
//     这样即使探测中途断电也不会让 SPD 的 CRC 失效(该条仍能开机);
//   - 写反值 → 立即回读 → 无论结果如何都还原原值 → 最后整片复核与备份一致;
//   - 探测期间不做任何其他改动, 结束后设备内容与探测前逐字节相同。

// WriteProbeResult 是写入能力探测的结果。
type WriteProbeResult struct {
	Addr     byte   `json:"addr"`
	Offset   int    `json:"offset"`
	OffsetIn string `json:"offsetText"`
	Old      byte   `json:"old"`
	New      byte   `json:"new"`
	ReadBack byte   `json:"readBack"`
	Verdict  string `json:"verdict"` // ok / ignored / rejected
	Mode     string `json:"mode"`    // 生效的写入档位: 单字节(协议 2) / 块写(协议 5)
	Note     string `json:"note"`
	Restored bool   `json:"restored"`
	Verified bool   `json:"verified"`
	Backup   string `json:"backupPath"`
}

// WriteProbe 探测当前设备能否真正写入 SPD(单字节, 写后立即还原)。
func (a *App) WriteProbe() (*WriteProbeResult, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	if dev.DryRun() {
		return nil, fmt.Errorf("设备处于干跑模式, 探测无意义(干跑的写只落内存); 请先关闭干跑模式")
	}

	img, backup, err := a.backupCurrent(dev)
	if err != nil {
		return nil, fmt.Errorf("探测前备份失败, 已中止: %w", err)
	}
	off, old, err := probeTarget(dev, img)
	if err != nil {
		return nil, err
	}
	res := &WriteProbeResult{
		Addr: dev.Addr(), Offset: off, OffsetIn: fmt.Sprintf("0x%03X", off),
		Old: old, New: old ^ 0xFF, Backup: backup,
	}
	a.logf("写入能力探测: 先备份当前内容(%s), 然后对 %s 写反值 %#02x 再还原(该字节不在 CRC 覆盖范围内)",
		backup, res.OffsetIn, res.New)

	// 1) 先按默认档位写反值: 逐字节写(协议 2)
	useBlock := false
	readBack := func() (byte, error) { return dev.ReadOneByte(uint16(off)) }
	if werr := dev.WriteByteAt(uint16(off), res.New); werr != nil {
		res.Verdict = "rejected"
		res.Note = fmt.Sprintf("逐字节写被控制器/器件拒绝: %v —— 该平台或该条不允许写入 SPD(如 BIOS 的 SPD 写保护、硬件 WP 引脚)", werr)
	} else if back, rerr := readBack(); rerr != nil {
		res.Verdict = "rejected"
		res.Note = fmt.Sprintf("写入后回读失败: %v", rerr)
	} else if back == res.New {
		res.ReadBack, res.Verdict, res.Mode = back, "ok", "单字节(协议 2)"
		res.Note = fmt.Sprintf("逐字节写生效: %s 从 %#02x 变成 %#02x(该条可写)", res.OffsetIn, old, back)
		useBlock = false
	} else {
		// 2) 逐字节写没生效 → 再试块写(协议 5): 有些 SPD5 hub 对 NVM 的写只认块写
		a.logf("写入能力探测: 逐字节写在 %s 未生效(回读 %#02x), 改试块写(协议 5)…", res.OffsetIn, back)
		if berr := dev.WriteBlockAt(uint16(off), []byte{res.New}); berr == nil {
			if b2, rerr := readBack(); rerr == nil && b2 == res.New {
				res.ReadBack, res.Verdict, res.Mode, useBlock = b2, "ok", "块写(协议 5)", true
				res.Note = fmt.Sprintf("逐字节写被忽略, 但**块写(协议 5)生效**: %s 从 %#02x 变成 %#02x"+
					" —— 写入必须用块写档位(本工具写入时会自动切换)", res.OffsetIn, old, b2)
			}
		}
		if res.Verdict == "" {
			res.ReadBack = back
			res.Verdict = "ignored"
			res.Note = fmt.Sprintf("逐字节写与块写都被忽略: 目标值 %#02x 发出后, 回读仍是 %#02x —— "+
				"器件接受了事务但没有改内容。常见原因: 平台级 SPD 写保护(BIOS 里的 SPD Write Protect)、"+
				"该块已被 RSWP/PSWP 保护、或 WP# 引脚被拉低", res.New, back)
		}
	}

	// 3) 还原(无论上面结果如何都要把原值写回去; 用刚才生效的档位)
	restore := func(v byte) error {
		if useBlock {
			return dev.WriteBlockAt(uint16(off), []byte{v})
		}
		return dev.WriteByteAt(uint16(off), v)
	}
	if rerr := restore(old); rerr != nil {
		res.Note += fmt.Sprintf("; 还原写入失败: %v —— 请用备份 %s 重写该条", rerr, backup)
		a.logf("写入能力探测: 还原失败(%v), 正在用备份整片回滚…", rerr)
		if n, ferr := a.restoreImage(dev, img); ferr != nil {
			res.Note += fmt.Sprintf("; 整片回滚也失败: %v", ferr)
		} else {
			res.Note += fmt.Sprintf("; 已用备份整片回滚(%d 字节)", n)
		}
	} else {
		res.Restored = true
	}

	// 4) 整片复核: 设备必须与探测前逐字节一致
	if verr := dev.Verify(img); verr != nil {
		res.Verified = false
		res.Note += fmt.Sprintf("; 探测后设备内容与备份不一致(%v), 正在回滚…", verr)
		if n, ferr := a.restoreImage(dev, img); ferr != nil {
			res.Note += fmt.Sprintf("; 回滚失败(%v) —— 请用备份 %s 重写该条", ferr, backup)
		} else {
			res.Note += fmt.Sprintf("; 已回滚(%d 字节)并校验通过", n)
		}
	} else {
		res.Verified = true
	}
	a.logf("写入能力探测结果: %s(%s; 已还原=%v, 整片复核=%v)", res.Verdict, res.Note, res.Restored, res.Verified)
	return res, nil
}

// probeTarget 选一个"探测字节": 优先不在任何 CRC 覆盖范围内的空闲字节(0x22B-0x27F 一类),
// 其次才是保留区; 目标必须是 0x00/0xFF 这种空闲值。
func probeTarget(dev *eeprom.Device, img []byte) (int, byte, error) {
	if len(img) != dev.Size() {
		return 0, 0, fmt.Errorf("镜像长度 %d 与设备 %d 不一致", len(img), dev.Size())
	}
	rt := dev.RamType()
	ranges := spd.CRCRanges(img)
	regions := regionsFor(rt, len(img))
	empty := func(b byte) bool { return b == 0x00 || b == 0xFF }
	// 先看不参与校验的区域(即使断电也不会破坏 CRC), 再看任意保留区
	for _, wantUncovered := range []bool{true, false} {
		for _, r := range regions {
			if r.name != "保留区" && r.name != "厂商特定数据" {
				continue
			}
			for off := r.start; off <= r.end && off < len(img); off++ {
				if !empty(img[off]) {
					continue
				}
				if wantUncovered && spd.AffectsChecksum(ranges, off) {
					continue
				}
				if !wantUncovered && !spd.AffectsChecksum(ranges, off) {
					continue
				}
				return off, img[off], nil
			}
		}
	}
	return 0, 0, fmt.Errorf("找不到合适的探测字节(没有空闲的保留字节) —— 为避免动到有效数据, 已放弃探测")
}
