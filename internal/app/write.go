package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"spdrw/internal/eeprom"
	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// 写入路径的护栏(写入是唯一会"变砖"的操作, 这里承担全部前置检查):
//
//	EditApplyToDevice(界面唯一入口, 编辑器内容) / writeConfirmed(按文件路径, 包内+测试)
//	  → 确认串 ack → 自动备份当前内容 → 预检(diff/风险/保护/CRC) → 写入(干跑模式零写事务)
//
// 服务层不提供"绕过预检直接写"的导出方法(测试用同包内未导出函数)。

// FieldChange 是按字段区域聚合后的变更摘要。
type FieldChange struct {
	Region string `json:"region"`
	Risk   string `json:"risk"` // low / medium / high
	Count  int    `json:"count"`
	Ranges string `json:"ranges"` // 例: "0x000-0x003, 0x018"
}

// WritePreflight 是写入前预检结果(前端展示 diff 与风险, 并据此放行/阻断)。
type WritePreflight struct {
	Path       string `json:"path"`
	Addr       byte   `json:"addr"`
	Generation string `json:"generation"`
	DeviceSize int    `json:"deviceSize"`
	FileSize   int    `json:"fileSize"`
	SizeOK     bool   `json:"sizeOk"`

	ChangeCount      int                 `json:"changeCount"`
	CRCBytes         int                 `json:"crcBytes"`
	Changes          []eeprom.ByteChange `json:"changes"`
	ChangesTruncated bool                `json:"changesTruncated"`
	Fields           []FieldChange       `json:"fields"`
	HighRiskCount    int                 `json:"highRiskCount"`
	ProtectedBlocks  []int               `json:"protectedBlocks"`
	UnknownBlocks    []int               `json:"unknownBlocks"`
	PSWP             bool                `json:"pswp"`
	TargetGeneration string              `json:"targetGeneration"`
	TargetCRCValid   bool                `json:"targetCrcValid"`
	CurrentCRCValid  bool                `json:"currentCrcValid"`
	DryRun           bool                `json:"dryRun"`
	Warnings         []string            `json:"warnings"`
	Blocked          bool                `json:"blocked"`
	BlockReason      string              `json:"blockReason"`
	// BlockKind 区分阻断原因: size/crc = "数据本身不合格"(干跑也拒绝);
	// protected/pswp = "硬件此刻不接受写入"(干跑只是内存演算, 可以放行)。
	BlockKind string `json:"blockKind"`
}

// WriteResult 是一次写入(或干跑)的结果。
type WriteResult struct {
	DryRun     bool            `json:"dryRun"`
	Written    int             `json:"written"`
	Total      int             `json:"total"`
	BackupPath string          `json:"backupPath"`
	Verified   bool            `json:"verified"`
	Message    string          `json:"message"`
	NVMWrites  int             `json:"nvmWrites"`
	BusStats   *BusStatsResult `json:"busStats,omitempty"`
	// 写入失败/校验失败时是否已用写入前镜像自动回滚, 以及回滚结果说明。
	RolledBack   bool   `json:"rolledBack"`
	RollbackNote string `json:"rollbackNote,omitempty"`
}

// spdRegion 描述一段 SPD 字节区域。
type spdRegion struct {
	name  string
	start int
	end   int // 闭区间
	risk  string
}

var ddr4Regions = []spdRegion{
	{"头部(字节数/SPD 修订)", 0, 1, "low"},
	{"DRAM 类型/模块类型", 2, 3, "high"},
	{"密度/封装/寻址", 4, 6, "high"},
	{"保留区", 7, 14, "medium"},
	{"时间基准 MTB/FTB", 15, 15, "high"},
	{"保留区", 16, 17, "medium"},
	{"主时序", 18, 42, "medium"},
	{"保留区", 43, 116, "medium"},
	{"精细时序 FTB", 117, 125, "medium"},
	{"CRC(第 1 段)", 126, 127, "low"},
	{"模块特定/第 2 段", 128, 253, "medium"},
	{"CRC(第 2 段)", 254, 255, "low"},
	{"保留区", 256, 319, "medium"},
	{"模块厂商/生产地点", 320, 322, "low"},
	{"生产日期", 323, 324, "low"},
	{"序列号", 325, 328, "low"},
	{"部件号", 329, 348, "low"},
	{"保留区", 349, 383, "medium"},
	{"XMP 2.0", 384, 509, "medium"},
	{"保留区", 510, 511, "medium"},
}

var ddr5Regions = []spdRegion{
	{"头部(字节数/SPD 修订)", 0, 1, "low"},
	{"DRAM 类型/模块类型", 2, 3, "high"},
	{"密度/IO 位宽/bank", 4, 11, "high"},
	{"SDRAM 参数", 12, 14, "medium"},
	{"保留区", 15, 15, "medium"},
	{"电压 VDD/VDDQ/VPP", 16, 18, "high"},
	{"SDRAM 时序标志", 19, 19, "high"},
	{"JEDEC 时序", 20, 102, "medium"},
	{"保留区", 103, 191, "medium"},
	{"SPD 器件信息", 192, 197, "medium"},
	{"PMIC/温度传感器", 198, 213, "high"},
	{"保留区", 214, 229, "medium"},
	{"模块尺寸/参考卡/组织/通道位宽", 230, 235, "high"},
	{"保留区", 236, 509, "medium"},
	{"CRC(基础段)", 510, 511, "low"},
	{"模块厂商/生产地点", 512, 514, "low"},
	{"生产日期", 515, 516, "low"},
	{"序列号", 517, 520, "low"},
	{"部件号", 521, 550, "low"},
	{"模块修订/DRAM 厂商/stepping", 551, 554, "low"},
	{"厂商特定数据", 555, 639, "medium"},
	{"XMP 3.0 / EXPO", 640, 1023, "medium"},
}

var ddr3Regions = []spdRegion{
	{"头部", 0, 1, "low"},
	{"DRAM 类型/模块类型", 2, 3, "high"},
	{"密度/寻址", 4, 6, "high"},
	{"组织/总线位宽", 7, 8, "high"},
	{"时间基准/时序", 9, 33, "medium"},
	{"精细时序修正", 34, 38, "medium"},
	{"保留区", 39, 59, "medium"},
	{"模块高度/厚度/参考设计", 60, 62, "medium"},
	{"模块特定段", 63, 116, "medium"},
	{"模块厂商 JEP106", 117, 118, "low"},
	{"生产地点", 119, 119, "low"},
	{"生产日期", 120, 121, "low"},
	{"序列号", 122, 125, "low"},
	{"CRC16", 126, 127, "low"},
	{"部件号", 128, 145, "low"},
	{"模块修订", 146, 147, "low"},
	{"DRAM 厂商", 148, 149, "low"},
	{"厂商特定/客户区", 150, 255, "medium"},
}

// regionsFor 返回该世代的区域表。
func regionsFor(rt spd.RAMType, size int) []spdRegion {
	switch rt {
	case spd.DDR4, spd.DDR4E, spd.LPDDR3, spd.LPDDR4, spd.LPDDR4X:
		return ddr4Regions
	case spd.DDR5, spd.LPDDR5, spd.DDR5NVDIMMP, spd.LPDDR5X:
		return ddr5Regions
	case spd.DDR3:
		return ddr3Regions
	default:
		if size == 1024 {
			return ddr5Regions
		}
		if size == 512 {
			return ddr4Regions
		}
		return ddr3Regions
	}
}

// PreflightWrite 是**预览**: 算写入计划与风险, 但绝不写设备。
//
// 注意 probeProtection=false: DDR4/更早世代的写保护状态只能靠"取反写一字节再还原"探测,
// 而这一步在用户勾"干跑"之前就会发生(前端是先预检、再弹出面板)。预览因此不做写测试,
// 保护状态显示为"未知"; 真正写入时(WriteConfirmed)会带写测试重做一次并在受保护时拒绝。
func (a *App) preflightWrite(path string, force bool) (*WritePreflight, error) {
	defer a.lockOp()()
	return a.preflightNoLock(path, force, false)
}

// preflightNoLock 假定调用方已持操作锁(WriteConfirmed 内部用, 不能再次加锁)。
// probeProtection=true 时允许做写测试(仅在真正写入的路径上)。
func (a *App) preflightNoLock(path string, force, probeProtection bool) (*WritePreflight, error) {
	dump, err := readDumpFile(path)
	if err != nil {
		return nil, err
	}
	a.resetBusCounter()
	return a.buildPreflight(path, dump, force, probeProtection)
}

// readDumpFile 读入待写入的 dump 文件(TOCTOU: 调用方必须把**同一份字节**一路用到写完)。
func readDumpFile(path string) ([]byte, error) {
	dump, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	return dump, nil
}

// beginDryRunIfRequested 在"用户要求干跑"时, **先**把设备切到干跑模式再走预检。
//
// 这一点很关键: DDR4/更早世代的保护状态查询靠"取反写一个字节再还原"的写测试,
// 若先跑预检再切干跑, 那么"干跑"这个动作本身就已经真的写过总线了。
// 返回的 restore 用于在操作结束后恢复(用户本来就开着干跑时不关)。
func (a *App) beginDryRunIfRequested(dryRun bool) (func(), error) {
	noop := func() {}
	if !dryRun {
		return noop, nil
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	if dev.DryRun() {
		return noop, nil // 用户显式开着干跑, 由用户自己关
	}
	if err := dev.SetDryRun(true); err != nil {
		return nil, fmt.Errorf("开启干跑模式失败: %w", err)
	}
	a.logf("干跑模式: 已开启(本次操作不会向 SPD 写入任何字节)")
	return func() { _ = dev.SetDryRun(false) }, nil
}

// controllerInfo 返回当前控制器的信息(用于 BIOS SPD 写禁止位这类门禁)。
func (a *App) controllerInfo() (smbus.Controller, error) {
	a.mu.Lock()
	ctl := a.ctrl
	a.mu.Unlock()
	if ctl.Name == "" {
		return smbus.Controller{}, fmt.Errorf("尚未连接控制器")
	}
	return ctl, nil
}

// resetBusCounter 清零当前控制器的总线计数(若已套计数包装)。
func (a *App) resetBusCounter() {
	if c, ok := a.activeTransport().(*smbus.CountingTransport); ok {
		c.Reset()
	}
}

// activeTransport 返回当前选中的传输(用于取总线计数包装)。
func (a *App) activeTransport() smbus.Transport {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}

// buildPreflight 对一份内存 dump 做写入前检查(编辑器与文件写入共用)。
// probeProtection=false 时临时让设备进入干跑, 使写保护探测不产生任何总线写。
func (a *App) buildPreflight(label string, dump []byte, force, probeProtection bool) (*WritePreflight, error) {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	pf := &WritePreflight{
		Path: label, Addr: dev.Addr(), Generation: dev.Generation(),
		DeviceSize: dev.Size(), FileSize: len(dump), DryRun: dev.DryRun(),
	}
	pf.SizeOK = len(dump) == dev.Size()
	if !pf.SizeOK {
		pf.Blocked, pf.BlockKind = true, "size"
		pf.BlockReason = fmt.Sprintf(
			"文件 %d 字节与 %s SPD 大小 %d 字节不一致(不支持截断/补齐写入)", len(dump), pf.Generation, dev.Size())
		return pf, nil
	}

	// 世代必须一致: 长度相同的世代不止一个(512: DDR4/LPDDR4; 1024: DDR5/LPDDR5),
	// 写错世代(如 LPDDR4 的镜像写 DDR4 条)基本等于该条不 POST。256B 现在只剩 DDR3。
	// (DDR2 支持已移除: byte2=0x08-0x0A 的目标会在这里按"类型未知"拒绝。)
	// 这一条只靠 dump 自己的 byte2 判定, 必须与设备自己识别出的世代对照。
	if !dev.TypeKnown() {
		pf.Blocked, pf.BlockKind = true, "generation"
		pf.BlockReason = "无法识别该设备的 SPD 器件类型(byte2 不在已知类型里): " +
			"为避免写错世代, 拒绝写入; 请确认这条 SPD 是否被工具正确识别"
	} else if rt, _, ierr := spd.Identify(dump); ierr != nil {
		pf.Blocked, pf.BlockKind = true, "generation"
		pf.BlockReason = fmt.Sprintf("无法识别目标内容的 SPD 世代: %v", ierr)
	} else if drt := dev.RAMType(); drt != rt {
		pf.Blocked, pf.BlockKind = true, "generation"
		pf.BlockReason = fmt.Sprintf(
			"世代不一致: 目标是 %v, 设备是 %v —— 长度相同也不允许跨世代写入", rt, drt)
	} else {
		pf.TargetGeneration = rt.String()
	}

	// 目标 dump 必须自身 CRC 有效: 写入一份 CRC 不符的 SPD 等于让主板按错时序启动
	if ok, cerr := spd.CRCOK(dump); cerr != nil {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf("目标文件无法校验: %v", cerr))
	} else {
		pf.TargetCRCValid = ok
	}
	if !pf.TargetCRCValid && !pf.Blocked {
		pf.Blocked, pf.BlockKind = true, "crc"
		pf.BlockReason = "目标文件 CRC 校验不通过(先用编辑器修复 CRC 或重新生成 dump)"
	}

	// BIOS 的 SPD Write Disable 打开时, 所有写入都会被拦在控制器一级 —— 提前说清楚,
	// 别让用户走到"备份→写→失败→回滚"才看到原因。
	if ctl, cerr := a.controllerInfo(); cerr == nil && ctl.WpKnown && !ctl.NoSpdWp {
		pf.Blocked, pf.BlockKind = true, "bios"
		pf.BlockReason = fmt.Sprintf(
			"BIOS 的 SPD Write Disable 处于打开状态(控制器 %s): 请在 BIOS 中关闭后再写入", ctl.Name)
	}

	// 当前内容 CRC(仅提示, 不阻断)
	if cur, rerr := dev.ReadAll(); rerr == nil {
		if ok, _ := spd.CRCOK(cur); ok {
			pf.CurrentCRCValid = true
		} else {
			pf.Warnings = append(pf.Warnings, "设备当前内容 CRC 已不通过(此前可能被部分写入)")
		}
	}

	// 保护状态
	// 预览(probeProtection=false)必须先进入干跑: DDR4 及更早的写保护探测是**真实写测试**。
	// 干跑影子建立失败时绝不能继续探测 —— 旧实现吞掉 SetDryRun 的错误, 于是"预览"在
	// 一个连整片都读不出来的设备上照样下发了 8 次数据写(审计 M5)。
	if !probeProtection && !dev.DryRun() {
		if err := dev.SetDryRun(true); err != nil {
			return nil, fmt.Errorf("无法进入干跑模式(需要先整片读取建立影子): %w; 预览已中止, 未下发任何写测试", err)
		}
		defer func() { _ = dev.SetDryRun(false) }()
	}
	det, werr := dev.WPStatusDetail()
	if werr != nil {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf("写保护状态查询失败: %v", werr))
	} else {
		for i := range det.Protected {
			switch {
			case det.Protected[i] && det.Known[i]:
				pf.ProtectedBlocks = append(pf.ProtectedBlocks, i)
			case !det.Known[i]:
				pf.UnknownBlocks = append(pf.UnknownBlocks, i)
			}
		}
		pf.PSWP = det.PSWP
		if len(pf.UnknownBlocks) > 0 {
			pf.Warnings = append(pf.Warnings, fmt.Sprintf(
				"块 %v 写保护状态未知(写测试失败), 若这些块有变更, 写入会在回读校验处中止", pf.UnknownBlocks))
		}
	}

	// 写入计划
	changes, perr := dev.PlanWrite(dump, force)
	if perr != nil {
		return nil, perr
	}
	pf.ChangeCount = len(changes)
	pf.Fields = summarizeFields(changes, dump)
	for _, f := range pf.Fields {
		if f.Risk == "high" {
			pf.HighRiskCount += f.Count
		}
	}
	for _, c := range changes {
		if c.IsCRC {
			pf.CRCBytes++
		}
	}
	const maxShow = 300
	if len(changes) > maxShow {
		pf.Changes = changes[:maxShow]
		pf.ChangesTruncated = true
	} else {
		pf.Changes = changes
	}
	if pf.Changes == nil {
		pf.Changes = []eeprom.ByteChange{}
	}
	pf.Fields = nonNilFields(pf.Fields)

	// 受保护块与变更块相交 → 拒绝
	changedBlocks := map[int]bool{}
	for _, c := range changes {
		changedBlocks[c.Block] = true
	}
	var hit []int
	for _, b := range pf.ProtectedBlocks {
		if changedBlocks[b] {
			hit = append(hit, b)
		}
	}
	if len(hit) > 0 && !pf.Blocked {
		pf.Blocked, pf.BlockKind = true, "protected"
		pf.BlockReason = fmt.Sprintf("变更涉及受写保护的块 %v(可先执行 RSWP 清除, 若可逆)", hit)
	}
	// M2: 保护状态"未知"(写测试失败)的块一旦被改动, 也必须拒绝**真实写入** ——
	// 未知不等于可写; 否则会在状态不明的块上写, 中途回读失败再回滚(半写风险)。
	// 注意: 只在真正探测过的路径(probeProtection=true)上这么判 —— 预览路径为了保证
	// "预览不写设备"会让写测试返回"未知", 那是预期行为, 不能因此阻断预览。
	if probeProtection && !pf.Blocked && len(pf.UnknownBlocks) > 0 {
		var unknownHit []int
		for _, b := range pf.UnknownBlocks {
			if changedBlocks[b] {
				unknownHit = append(unknownHit, b)
			}
		}
		if len(unknownHit) > 0 {
			pf.Blocked, pf.BlockKind = true, "unknown"
			pf.BlockReason = fmt.Sprintf(
				"变更涉及的块 %v 写保护状态未知(写测试失败): 为避免半写, 已拒绝写入; 可重试或换一根条", unknownHit)
		}
	}
	if pf.PSWP && !pf.Blocked {
		pf.Blocked, pf.BlockKind = true, "pswp"
		pf.BlockReason = "该条已处于 PSWP 永久写保护, 无法写入"
	}
	if pf.HighRiskCount > 0 {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf(
			"变更包含 %d 个高危字节(容量/组织/电压/PMIC 等), 写错可能导致无法开机", pf.HighRiskCount))
	}
	if force && pf.ChangeCount > 0 {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf("强制模式: 将写入全部 %d 字节(含相同值)", len(dump)))
	}
	if pf.ChangeCount == 0 {
		pf.Warnings = append(pf.Warnings, "设备内容已与文件一致, 无需写入")
	}
	if pf.DryRun {
		pf.Warnings = append(pf.Warnings, "当前为干跑模式: 不会向 SPD 写入任何字节")
	}
	return pf, nil
}

// summarizeFields 把变更按区域聚合。
func summarizeFields(changes []eeprom.ByteChange, dump []byte) []FieldChange {
	rt, _, _ := spd.Identify(dump)
	regions := regionsFor(rt, len(dump))
	type agg struct {
		region spdRegion
		offs   []int
	}
	byRegion := map[string]*agg{}
	var order []string
	for _, c := range changes {
		reg := spdRegion{name: "未定义区域", start: c.Offset, end: c.Offset, risk: "medium"}
		for _, r := range regions {
			if c.Offset >= r.start && c.Offset <= r.end {
				reg = r
				break
			}
		}
		a := byRegion[reg.name]
		if a == nil {
			a = &agg{region: reg}
			byRegion[reg.name] = a
			order = append(order, reg.name)
		}
		a.offs = append(a.offs, c.Offset)
	}
	out := make([]FieldChange, 0, len(order))
	for _, name := range order {
		a := byRegion[name]
		sort.Ints(a.offs)
		out = append(out, FieldChange{
			Region: a.region.name, Risk: a.region.risk,
			Count: len(a.offs), Ranges: compactRanges(a.offs),
		})
	}
	return out
}

func nonNilFields(f []FieldChange) []FieldChange {
	if f == nil {
		return []FieldChange{}
	}
	return f
}

// compactRanges 把偏移列表压缩成 "0x000-0x003, 0x018" 形式。
func compactRanges(offs []int) string {
	if len(offs) == 0 {
		return ""
	}
	var parts []string
	start, prev := offs[0], offs[0]
	flush := func() {
		if start == prev {
			parts = append(parts, fmt.Sprintf("0x%03X", start))
		} else {
			parts = append(parts, fmt.Sprintf("0x%03X-0x%03X", start, prev))
		}
	}
	for _, o := range offs[1:] {
		if o == prev+1 {
			prev = o
			continue
		}
		flush()
		start, prev = o, o
	}
	flush()
	if len(parts) > 12 {
		return strings.Join(parts[:12], ", ") + fmt.Sprintf(" …(共 %d 段)", len(parts))
	}
	return strings.Join(parts, ", ")
}

// backupKeep 是备份目录保留的份数(超出后按文件名里的时间戳删最旧的)。
// 名字形如 spd-0x50-20260920-153000.123456789.bin, 字典序即时序。
const backupKeep = 20

// backupCurrent 把设备当前整片内容存到备份目录, 返回**镜像内容**与文件路径。
// 镜像要一直留在内存里: 写入失败时它就是回滚源(不能只依赖磁盘文件, 免得盘满/权限问题)。
//
// 落盘要求(审计遗留项):
//   - **原子**: 先写同目录的临时文件 + fsync, 再 rename。中断只会留下 *.tmp, 不会留下
//     半份"看起来像备份"的文件 —— 回滚时按它写回去才是真的灾难。
//   - **回读校验**: rename 之后读回来逐字节比对, 不一致就报错拒绝写入。
//   - **有界**: 只保留最近 backupKeep 份, 顺带清掉中断留下的 *.tmp。
func (a *App) backupCurrent(dev *eeprom.Device) ([]byte, string, error) {
	img, err := dev.ReadAll()
	if err != nil {
		return nil, "", fmt.Errorf("备份失败(读取当前内容): %w", err)
	}
	if len(img) == 0 {
		return nil, "", fmt.Errorf("备份失败: 设备返回 0 字节")
	}
	dir, err := backupDir()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("创建备份目录 %s: %w", dir, err)
	}
	// 纳秒 + 地址: 同一秒内连续两次真实写入不能互相覆盖(第一次的备份才是"原始内容")
	name := fmt.Sprintf("spd-%#x-%s.bin", dev.Addr(), time.Now().Format("20060102-150405.000000000"))
	path := filepath.Join(dir, name)
	if err := writeFileAtomic(dir, path, img); err != nil {
		return nil, "", fmt.Errorf("写备份 %s: %w", path, err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("备份回读 %s: %w", path, err)
	}
	if len(back) != len(img) {
		return nil, "", fmt.Errorf("备份回读长度不符(%d != %d): %s", len(back), len(img), path)
	}
	for i := range img {
		if back[i] != img[i] {
			return nil, "", fmt.Errorf("备份回读内容不符(@0x%03X: %02X != %02X): %s", i, back[i], img[i], path)
		}
	}
	a.mu.Lock()
	a.lastBackupPath = path
	a.mu.Unlock()
	a.logf("已备份当前 SPD: %s (%d 字节)", path, len(img))
	if n := pruneBackups(dir, backupKeep); n > 0 {
		a.logf("已清理 %d 份较早的备份(保留最近 %d 份)", n, backupKeep)
	}
	return img, path, nil
}

// writeFileAtomic 在 dir 内写临时文件 → fsync → rename 到 path。
func writeFileAtomic(dir, path string, data []byte) error {
	f, err := os.CreateTemp(dir, "spd-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil { // 崩溃/断电时不留半份内容
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pruneBackups 只保留最近的 keep 份 spd-*.bin, 并清掉中断留下的 *.tmp; 返回删除份数。
func pruneBackups(dir string, keep int) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	var bins []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		switch {
		case strings.HasPrefix(n, "spd-") && strings.HasSuffix(n, ".bin"):
			bins = append(bins, n)
		case strings.HasSuffix(n, ".tmp"):
			// 中断留下的临时文件: 永远不是有效备份
			if os.Remove(filepath.Join(dir, n)) == nil {
				removed++
			}
		}
	}
	if len(bins) <= keep {
		return removed
	}
	sort.Strings(bins) // 名字里带纳秒时间戳 → 字典序即时序
	for _, n := range bins[:len(bins)-keep] {
		if os.Remove(filepath.Join(dir, n)) == nil {
			removed++
		}
	}
	return removed
}

// restoreImage 用给定镜像覆盖设备内容并整片校验(回滚与测试共用)。
func (a *App) restoreImage(dev *eeprom.Device, img []byte) (int, error) {
	changes, err := dev.PlanWrite(img, false)
	if err != nil {
		return 0, fmt.Errorf("无法生成回滚计划: %w", err)
	}
	if len(changes) == 0 {
		return 0, nil // 内容已经和镜像一致(失败可能发生在第一个字节之前)
	}
	if err := dev.ApplyWrite(img, changes, nil); err != nil {
		return 0, err
	}
	if err := dev.VerifyByteWise(img); err != nil {
		return len(changes), fmt.Errorf("回滚后整片逐字节复核不通过: %w", err)
	}
	return len(changes), nil
}

// backupIfNeeded 在**任何可能写设备的动作之前**做整片备份(内存镜像 + 文件)。
//
// B1(审计阻断项): DDR4 及更早世代的"写保护探测"本身就是真实 NVM 写(块首取反写→还原)。
// 旧顺序是"先探测、后备份", 于是还原写一旦失败: byte0 被永久改坏、备份里存的也是坏值、
// 之后回滚拿坏值当"原始内容"比对自己, 还报告"无需回滚"。备份必须排在最前。
func (a *App) backupIfNeeded(dev *eeprom.Device, dryRun bool) ([]byte, string, error) {
	if dryRun {
		return nil, "", nil // 干跑全程零写, 不需要回滚源
	}
	return a.backupCurrent(dev)
}

// afterProbeCheck 在写保护探测之后、真正写入之前, 复核设备内容仍与备份一致。
// 不一致说明探测的"还原写"失败了 —— 立即回滚并中止本次写入(继续写只会更糟)。
func (a *App) afterProbeCheck(dev *eeprom.Device, img []byte) error {
	if img == nil {
		return nil
	}
	// 判定"探测是否改动过设备"必须用最原始的逐字节读法: 块读/字读路径一旦有缓存或偏差,
	// 这里会把"byte0 已被探测写坏"误判成"内容一致 → 无需回滚"(审计验证过的假阴性)。
	err := dev.VerifyByteWise(img)
	if err == nil {
		return nil
	}
	a.logf("警告: 写保护探测后设备内容与备份不一致(%v), 正在用备份回滚…", err)
	n, rerr := a.restoreImage(dev, img)
	if rerr != nil {
		return fmt.Errorf("写保护探测改动了设备内容且回滚失败: %v; "+
			"请立即用备份文件重写该条 SPD, 不要继续写入", rerr)
	}
	if n == 0 {
		return fmt.Errorf("写保护探测的还原写失败(设备内容读回异常), 但重新比对已一致; " +
			"为安全起见本次写入已中止, 请重试或换个时段再试")
	}
	a.logf("已回滚写保护探测造成的改动(%d 字节, 校验通过)", n)
	return fmt.Errorf("写保护探测期间还原失败, 设备内容一度被改动(已自动回滚 %d 字节并通过校验); "+
		"本次写入已中止 —— 总线不稳定, 请重试", n)
}

// gateDump 复检待写内容: 长度、世代、CRC 全部合格才允许写入。
//
// writeWithPreflight 不信任调用方传来的 pf(M4: 预检与实际写入之间文件可能被替换),
// 所以在这里独立重算一遍 —— 没有这道门, 传一份 CRC 已破坏的字节进来也会"写成功"。
func gateDump(dev *eeprom.Device, dump []byte) error {
	if len(dump) != dev.Size() {
		return fmt.Errorf("待写内容 %d 字节与设备 %d 字节不一致", len(dump), dev.Size())
	}
	rt, size, err := spd.Identify(dump)
	if err != nil {
		return fmt.Errorf("无法识别待写内容的 SPD 世代: %w", err)
	}
	if len(dump) != size {
		return fmt.Errorf("待写内容长度 %d 与 %v 的 SPD 大小 %d 不一致", len(dump), rt, size)
	}
	if !dev.TypeKnown() {
		return fmt.Errorf("无法识别设备的 SPD 器件类型, 拒绝写入")
	}
	if drt := dev.RAMType(); drt != rt {
		return fmt.Errorf("世代不一致: 待写内容是 %v, 设备是 %v", rt, drt)
	}
	if ok, cerr := spd.CRCOK(dump); cerr != nil || !ok {
		return fmt.Errorf("待写内容 CRC 校验不通过(拒绝写入): %v", cerr)
	}
	return nil
}

// rollbackAfterFailure 写入/校验失败后用写入前镜像自动回滚。
//
// 为什么必须是自动的: SPD 被写了一半(尤其校验字节还没写)时, 主板可能直接拒绝该条,
// 用户面对的是一根"开不了机"的内存条。失败即回滚才是默认行为, 不能指望用户手动补。
func (a *App) rollbackAfterFailure(dev *eeprom.Device, img []byte, backup string, cause error) (bool, string) {
	a.logf("写入未完成(%v), 正在用写入前镜像回滚…", cause)
	n, err := a.restoreImage(dev, img)
	switch {
	case err != nil:
		note := fmt.Sprintf("自动回滚失败: %v; 请立刻用备份 %s 重新写入该条 SPD(或离线恢复)", err, backup)
		a.logf("%s", note)
		return false, note
	case n == 0:
		note := fmt.Sprintf("内容已与写入前一致(失败发生在改动之前), 无需回滚; 备份 %s", backup)
		a.logf("%s", note)
		return true, note
	default:
		note := fmt.Sprintf("已自动回滚到写入前内容(%d 字节, 读回校验通过); 备份 %s", n, backup)
		a.logf("%s", note)
		return true, note
	}
}

func backupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("无法确定用户目录: %v", err)
	}
	return filepath.Join(home, ".spdrw", "backups"), nil
}

// WriteConfirmed 执行写入。必须带确认串: 真实写入要求 "WRITE", 干跑要求 "DRYRUN"。
//
// 顺序(每一步都是有原因的, 别调换):
//  1. 确认串;
//  2. 读文件**一次** —— 预检与写入必须用同一份字节(M4);
//  3. **备份**(真实写入时) —— 必须早于任何探测, 因为 DDR4 的写保护探测是真写(B1);
//  4. 预检(含写保护探测);
//  5. 探测后整片复核, 与备份不一致 → 回滚 + 中止;
//  6. 写入(CRC 最后) → 逐字节回读 → 整片校验 → 失败自动回滚。
func (a *App) writeConfirmed(path string, force, dryRun bool, ack string) (*WriteResult, error) {
	defer a.lockOp()()
	want := "WRITE"
	if dryRun {
		want = "DRYRUN"
	}
	if strings.ToUpper(strings.TrimSpace(ack)) != want {
		return nil, fmt.Errorf("确认串不正确(应输入 %s)", want)
	}
	restore, err := a.beginDryRunIfRequested(dryRun)
	if err != nil {
		return nil, err
	}
	defer restore()
	dump, err := readDumpFile(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	// M1: 数据本身不合格(长度/世代/CRC)时**先拒绝** —— 不要在"注定被拒"的目标上
	// 先备份再跑写保护探测(DDR4 及更早的探测是真实写测试, 8 次 NVM 写)。
	if err := gateDump(dev, dump); err != nil {
		return nil, fmt.Errorf("写入被拒绝: %w", err)
	}
	img, backup, err := a.backupIfNeeded(dev, dryRun)
	if err != nil {
		return nil, fmt.Errorf("写入前备份失败, 已中止: %w", err)
	}
	a.resetBusCounter() // 统计窗口覆盖预检(与干跑报告一致)
	pf, err := a.buildPreflight(path, dump, force, true)
	if err != nil {
		return nil, err
	}
	if err := a.afterProbeCheck(dev, img); err != nil {
		return nil, err
	}
	return a.writeWithPreflight(pf, dump, force, dryRun, img, backup)
}

// writeWithPreflight 在预检通过后执行写入(文件写入、编辑器写入与测试共用)。
//
// img/backup: 写入前的镜像与备份路径。真实写入必须由调用方**在探测之前**准备好
// (B1); 直接调用本函数(测试)时 img 为 nil 会在这里补做备份。干跑不需要。
func (a *App) writeWithPreflight(pf *WritePreflight, dump []byte, force, dryRun bool, img []byte, backup string) (*WriteResult, error) {
	// 注意: 不在这里清零总线计数 —— 统计窗口从"预检开始"算起,
	// 这样干跑报告里的数字覆盖了预检(含可能发生的写保护探测), 不会漏报。

	if pf.Blocked {
		// 干跑只是内存演算, 不碰硬件: 只有"数据本身不合格"(长度/世代/CRC)才拒绝,
		// 写保护/PSWP/BIOS 这类"硬件此刻不接受"的原因允许继续(结果里带警示)。
		hardwareGate := pf.BlockKind == "protected" || pf.BlockKind == "pswp" ||
			pf.BlockKind == "bios" || pf.BlockKind == "unknown"
		if !(dryRun && hardwareGate) {
			return nil, fmt.Errorf("写入被拒绝: %s", pf.BlockReason)
		}
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	// M4: 不信任调用方 —— 长度/世代/CRC 在这里独立复检一遍(预检与实际写入之间
	// 文件可能已被替换, 而这条路径会真的写设备)。
	if err := gateDump(dev, dump); err != nil {
		return nil, fmt.Errorf("写入被拒绝: %w", err)
	}
	res := &WriteResult{DryRun: dryRun}
	counter, _ := a.activeTransport().(*smbus.CountingTransport)

	// 干跑: 不改动设备状态, 只把结果算进影子
	if dryRun {
		if !dev.DryRun() {
			if err := dev.SetDryRun(true); err != nil {
				return nil, err
			}
			defer func() { _ = dev.SetDryRun(false) }()
		}
		changes, err := dev.PlanWrite(dump, force)
		if err != nil {
			return nil, err
		}
		if err := dev.ApplyWrite(dump, changes, func(w, t int) { a.emit("write:progress", w, t) }); err != nil {
			return nil, err
		}
		// 干跑只演算内存影子: 不能声称"已校验设备"(res.Verified 语义是设备已核对)
		res.Total, res.Written, res.Verified = len(changes), len(changes), false
		res.Message = fmt.Sprintf("干跑完成: 将写入 %d 字节(未对设备做任何校验)", len(changes))
		if counter != nil {
			st := counter.Stats()
			nvm := counter.NVMWriteCount()
			res.BusStats = &BusStatsResult{
				Generation: dev.Generation(), Reads: st.Reads, QuickWrites: st.QuickWrites,
				ByteDataWrites: st.ByteDataWrites, ByteWrites: st.ByteWrites, NVMWrites: nvm,
			}
			res.NVMWrites = nvm
			// 说明: DDR5 的切页是 MR11 寄存器写(记在"字节写"里), DDR4 的切页是 Quick 写;
			// 两者都不是 NVM 数据写 —— 所以这里把"非 NVM 写"合并成一项, 避免误读。
			res.Message += fmt.Sprintf("; 期间总线事务: 读 %d 次 / 非 NVM 写 %d 次(切页/寄存器) / 其中对 SPD NVM 的数据写 %d 次",
				st.Reads, st.QuickWrites+st.ByteWrites+st.ByteDataWrites-nvm, nvm)
			if nvm == 0 {
				res.Message += "(零写入)"
			} else {
				res.Message += " —— 警告: 干跑模式出现了 NVM 写, 请勿在真机使用!"
			}
		} else {
			res.Message += "(总线上零写事务)"
		}
		if pf.Blocked {
			res.Message += "; 注意: " + pf.BlockReason
		}
		a.logf("%s", res.Message)
		return res, nil
	}

	// 真实写入: 计划 → (必要时)备份 → 写(CRC 排在最后) → 逐字节回读 → 整片校验
	if dev.DryRun() {
		return nil, fmt.Errorf("设备处于干跑模式, 本次不会真正写入; 请先关闭干跑模式再执行真实写入")
	}
	if img == nil {
		// 调用方(测试)没给镜像: 这里先备份再算计划 —— 顺序上不会出现"先计划后备份"
		var berr error
		img, backup, berr = a.backupCurrent(dev)
		if berr != nil {
			return nil, fmt.Errorf("写入前备份失败, 已中止: %w", berr)
		}
	}
	changes, err := dev.PlanWrite(dump, force)
	if err != nil {
		return nil, err
	}
	res.Total = len(changes)
	if len(changes) == 0 {
		// 设备内容已与目标一致: 不写(计划为空时备份是浪费; 备份文件仍在, 只是没用到)
		res.Message = "设备内容已与目标一致, 无需写入"
		a.logf("%s", res.Message)
		return res, nil
	}
	res.BackupPath = backup

	var fail error
	if err := dev.ApplyWrite(dump, changes, func(w, t int) { a.emit("write:progress", w, t) }); err != nil {
		fail = err
	} else if err := dev.Verify(dump); err != nil {
		fail = &eeprom.WriteError{Stage: "verify", Written: len(changes), Total: len(changes), Err: err}
	} else if err := dev.VerifyByteWise(dump); err != nil {
		// L11: 逐字节回读与整片校验共用同一条(块读/字读)读路径, 系统性读偏差(或分页写错位、
		// 外部工具同时动总线)会让两者一起"同意"。这里关掉块读/字读, 用最原始的逐字节读法把
		// **整片**再核一遍 —— 忙等模式下 1024 字节约 1 秒, 完全付得起。
		fail = &eeprom.WriteError{Stage: "verify", Written: len(changes), Total: len(changes), Err: err}
	}

	if fail != nil {
		res.Written = writtenOf(fail, len(changes))
		// 现场: 失败字节的物理位置 + hub 寄存器(MR12/13=RSWP 位图, MR52 bit6=写受保护块被忽略)。
		// 真机排障时这一行就能回答"是平台拒绝、块保护还是别的原因"。
		var we *eeprom.WriteError
		if errors.As(fail, &we) {
			scene := fmt.Sprintf("现场: 0x%03X(%s)", we.Offset, dev.PhysDesc(uint16(we.Offset)))
			if snap := dev.MRSnapshot(); snap != "" {
				scene += "; " + snap
			}
			a.logf("%s", scene)
			fail = fmt.Errorf("%w [%s]", fail, scene)
		}
		res.RolledBack, res.RollbackNote = a.rollbackAfterFailure(dev, img, backup, fail)
		a.attachBusStats(res, dev, counter)
		res.Message = fmt.Sprintf("写入未完成: %v; %s", fail, res.RollbackNote)
		a.logf("%s", res.Message)
		// 用包装错误返回: 消息里带"回滚结果", 但 errors.As 仍能取到 *eeprom.WriteError
		return res, &writeFailure{cause: fail, note: res.RollbackNote}
	}

	res.Written, res.Verified = len(changes), true
	if note := dev.WriteModeNote(); note != "" {
		a.logf("写入档位: %s", note)
	}
	res.Message = fmt.Sprintf(
		"写入并校验通过: %d 字节(备份 %s; 写入档位 %s); 校验: 每个字节写完即回读 %d/%d + 整片 %d 字节比对 + 整片逐字节复核(关掉块读/字读, 独立读法)",
		len(changes), backup, dev.WriteMode(), len(changes), len(changes), dev.Size())
	if note := dev.WriteModeNote(); note != "" {
		res.Message += "; " + note
	}
	a.attachBusStats(res, dev, counter)
	a.logf("%s", res.Message)
	return res, nil
}

// writeFailure 把"原始写入错误"与"回滚结果"一起报给用户, 同时保留错误链
// (上层/测试仍可用 errors.As 取到 *eeprom.WriteError 里的已写/未写计数)。
type writeFailure struct {
	cause error
	note  string
}

func (e *writeFailure) Error() string { return fmt.Sprintf("%v; %s", e.cause, e.note) }
func (e *writeFailure) Unwrap() error { return e.cause }

// writtenOf 从错误里取出"已经写了多少字节"。
func writtenOf(err error, fallback int) int {
	var we *eeprom.WriteError
	if errors.As(err, &we) {
		return we.Written
	}
	return fallback
}

// attachBusStats 把本次操作(含可能的回滚写)的总线计数填进结果与日志文案。
func (a *App) attachBusStats(res *WriteResult, dev *eeprom.Device, counter *smbus.CountingTransport) {
	if counter == nil {
		return
	}
	st := counter.Stats()
	nvm := counter.NVMWriteCount() // 独立累计, 不受写日志上限截断(M7)
	res.NVMWrites = nvm
	res.BusStats = &BusStatsResult{
		Generation: dev.Generation(), Reads: st.Reads, QuickWrites: st.QuickWrites,
		ByteDataWrites: st.ByteDataWrites, ByteWrites: st.ByteWrites, NVMWrites: nvm,
	}
	res.Message += fmt.Sprintf("; 总线: 读 %d / 字节写 %d(其中 NVM %d)", st.Reads, st.ByteDataWrites, nvm)
}
