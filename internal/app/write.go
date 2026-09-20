package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"spdrw/internal/eeprom"
	"spdrw/internal/spd"
)

// 写入路径的护栏(写入是唯一会"变砖"的操作, 这里承担全部前置检查):
//
//	PickWriteFile → PreflightWrite(diff/风险/保护/CRC 预检) → WriteConfirmed(ack)
//	                                                    └─ 自动备份当前内容
//	                                                    └─ 干跑模式零写事务
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
	TargetCRCValid   bool                `json:"targetCrcValid"`
	CurrentCRCValid  bool                `json:"currentCrcValid"`
	DryRun           bool                `json:"dryRun"`
	Warnings         []string            `json:"warnings"`
	Blocked          bool                `json:"blocked"`
	BlockReason      string              `json:"blockReason"`
}

// WriteResult 是一次写入(或干跑)的结果。
type WriteResult struct {
	DryRun     bool   `json:"dryRun"`
	Written    int    `json:"written"`
	Total      int    `json:"total"`
	BackupPath string `json:"backupPath"`
	Verified   bool   `json:"verified"`
	Message    string `json:"message"`
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
	{"时序", 9, 30, "medium"},
	{"保留区", 31, 61, "medium"},
	{"模块特定", 62, 116, "medium"},
	{"模块厂商", 117, 118, "low"},
	{"生产日期", 120, 121, "low"},
	{"序列号", 122, 125, "low"},
	{"CRC", 126, 127, "low"},
	{"部件号", 128, 145, "low"},
	{"保留区", 146, 255, "medium"},
}

var ddr2Regions = []spdRegion{
	{"头部/行列", 0, 3, "high"},
	{"密度/位宽", 4, 6, "high"},
	{"总线位宽", 8, 8, "high"},
	{"时序", 9, 30, "medium"},
	{"bank 数", 17, 17, "high"},
	{"保留区", 31, 64, "medium"},
	{"模块组织", 65, 72, "medium"},
	{"部件号", 73, 90, "low"},
	{"模块特定/保留区", 91, 255, "medium"},
}

// regionsFor 返回该世代的区域表。
func regionsFor(rt spd.RamType, size int) []spdRegion {
	switch rt {
	case spd.DDR4, spd.DDR4E, spd.LPDDR3, spd.LPDDR4, spd.LPDDR4X:
		return ddr4Regions
	case spd.DDR5, spd.LPDDR5, spd.DDR5NVDIMMP, spd.LPDDR5X:
		return ddr5Regions
	case spd.DDR3:
		return ddr3Regions
	case spd.DDR2, spd.DDR2FBDIMM, spd.DDR2FBDIMMP:
		return ddr2Regions
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

// PickWriteFile 只弹打开对话框并返回路径(不写入), 供前端先做预检。
func (a *App) PickWriteFile() (string, error) {
	if err := a.dialogGuard(); err != nil {
		return "", err
	}
	path, err := a.OpenDialog("选择要写入的 dump 文件")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("已取消")
	}
	return path, nil
}

// PreflightWrite 计算写入计划并做全部前置检查(不写任何字节)。
func (a *App) PreflightWrite(path string, force bool) (*WritePreflight, error) {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	dump, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	pf := &WritePreflight{
		Path: path, Addr: dev.Addr(), Generation: dev.Generation(),
		DeviceSize: dev.Size(), FileSize: len(dump), DryRun: dev.DryRun(),
	}
	pf.SizeOK = len(dump) == dev.Size()
	if !pf.SizeOK {
		pf.Blocked, pf.BlockReason = true, fmt.Sprintf(
			"文件 %d 字节与 %s SPD 大小 %d 字节不一致(不支持截断/补齐写入)", len(dump), pf.Generation, dev.Size())
		return pf, nil
	}

	// 目标 dump 必须自身 CRC 有效: 写入一份 CRC 不符的 SPD 等于让主板按错时序启动
	if ok, cerr := spd.CRCOK(dump); cerr != nil {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf("目标文件无法校验: %v", cerr))
	} else {
		pf.TargetCRCValid = ok
	}
	if !pf.TargetCRCValid {
		pf.Blocked = true
		pf.BlockReason = "目标文件 CRC 校验不通过(先用编辑器修复 CRC 或重新生成 dump)"
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
		pf.Blocked = true
		pf.BlockReason = fmt.Sprintf("变更涉及受写保护的块 %v(可先执行 RSWP 清除, 若可逆)", hit)
	}
	if pf.PSWP && !pf.Blocked {
		pf.Blocked = true
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

// SetDryRun 开关干跑模式: 开启时写入只落在内存影子, 一个字节都不上总线。
func (a *App) SetDryRun(on bool) (bool, error) {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return false, fmt.Errorf("请先选择设备")
	}
	if err := dev.SetDryRun(on); err != nil {
		return false, err
	}
	a.logf("干跑模式: %v", map[bool]string{true: "开启(写入不会真正执行)", false: "关闭"}[on])
	return dev.DryRun(), nil
}

// backupCurrent 把设备当前整片内容存到备份目录, 返回路径。
func (a *App) backupCurrent(dev *eeprom.Device) (string, error) {
	img, err := dev.ReadAll()
	if err != nil {
		return "", fmt.Errorf("备份失败(读取当前内容): %w", err)
	}
	dir, err := backupDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录 %s: %w", dir, err)
	}
	name := fmt.Sprintf("spd-%#x-%s.bin", dev.Addr(), time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, img, 0o644); err != nil {
		return "", fmt.Errorf("写备份 %s: %w", path, err)
	}
	a.logf("已备份当前 SPD: %s (%d 字节)", path, len(img))
	return path, nil
}

func backupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("无法确定用户目录: %v", err)
	}
	return filepath.Join(home, ".spdrw", "backups"), nil
}

// WriteConfirmed 执行写入。必须带确认串: 真实写入要求 "WRITE", 干跑要求 "DRYRUN"。
func (a *App) WriteConfirmed(path string, force, dryRun bool, ack string) (*WriteResult, error) {
	want := "WRITE"
	if dryRun {
		want = "DRYRUN"
	}
	if strings.ToUpper(strings.TrimSpace(ack)) != want {
		return nil, fmt.Errorf("确认串不正确(应输入 %s)", want)
	}
	pf, err := a.PreflightWrite(path, force)
	if err != nil {
		return nil, err
	}
	return a.writeWithPreflight(pf, force, dryRun)
}

// writeWithPreflight 在预检通过后执行写入(测试与 WriteConfirmed 共用)。
func (a *App) writeWithPreflight(pf *WritePreflight, force, dryRun bool) (*WriteResult, error) {
	if pf.Blocked {
		return nil, fmt.Errorf("写入被拒绝: %s", pf.BlockReason)
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	dump, err := os.ReadFile(pf.Path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", pf.Path, err)
	}
	res := &WriteResult{DryRun: dryRun}

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
		res.Total, res.Written, res.Verified = len(changes), len(changes), true
		res.Message = fmt.Sprintf("干跑完成: 将写入 %d 字节(总线上零写事务)", len(changes))
		a.logf("%s", res.Message)
		return res, nil
	}

	// 真实写入: 先备份, 再写, 最后整片校验
	if dev.DryRun() {
		return nil, fmt.Errorf("设备处于干跑模式, 本次不会真正写入; 请先关闭干跑模式再执行真实写入")
	}
	backup, err := a.backupCurrent(dev)
	if err != nil {
		return nil, fmt.Errorf("写入前备份失败, 已中止: %w", err)
	}
	res.BackupPath = backup
	changes, err := dev.PlanWrite(dump, force)
	if err != nil {
		return nil, err
	}
	if err := dev.ApplyWrite(dump, changes, func(w, t int) { a.emit("write:progress", w, t) }); err != nil {
		return res, err
	}
	res.Total, res.Written = len(changes), len(changes)
	if err := dev.Verify(dump); err != nil {
		res.Message = "写入后整片校验失败"
		a.logf("写入后校验失败: %v", err)
		return res, fmt.Errorf("写入后整片校验失败: %w", err)
	}
	res.Verified = true
	res.Message = fmt.Sprintf("写入并校验通过: %d 字节(备份 %s)", len(changes), backup)
	a.logf("%s", res.Message)
	return res, nil
}
