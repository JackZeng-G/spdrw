package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"spdrw/internal/eeprom"
	"spdrw/internal/spd"
)

// SPD 编辑器的服务层。
//
// 编辑只发生在内存工作副本上; 写设备必须走 EditApplyToDevice(预检 + 备份 +
// 确认串 + 可选干跑), 与文件写入共用同一套护栏。

// EditState 是编辑器的当前状态(前端每次操作后刷新)。
type EditState struct {
	Source      string `json:"source"` // "设备 0x50" 或文件路径
	Generation  string `json:"generation"`
	Size        int    `json:"size"`
	Dirty       bool   `json:"dirty"`
	ChangeCount int    `json:"changeCount"`
	CRCOK       bool   `json:"crcOk"`
	CanWrite    bool   `json:"canWrite"` // 编辑器有内容即可写回设备(内容来自文件也行, 走同一套预检门禁)
	// CRCStale 表示"有改动落在校验覆盖范围内" —— 这类改动必须重算 CRC 才能写入;
	// 改序列号/生产日期/部件号(不在覆盖范围)不置此位, 这才是对的提示。
	CRCStale bool `json:"crcStale"`
}

// EditDiff 是编辑结果的差异视图。
type EditDiff struct {
	Changes       []spd.EditChange `json:"changes"`
	Fields        []FieldChange    `json:"fields"`
	HighRisk      int              `json:"highRisk"`
	CRCFields     int              `json:"crcFields"`
	ChangeCount   int              `json:"changeCount"`
	CRCOK         bool             `json:"crcOk"`
	Truncated     bool             `json:"truncated"`
	PreviewBase64 string           `json:"previewBase64,omitempty"`
	// 改动的"是否影响校验"分类: 落在校验覆盖区(或校验值字节本身)里的改动会
	// 让 CRC 失效, 必须点"重算 CRC"; 落在校验区之外的(如 DDR4/DDR5 的序列号、
	// 生产日期、部件号区)改多少都不影响 —— 界面要把这两类分开显示。
	CRCDirty     int   `json:"crcDirty"`
	CRCFreeDirty int   `json:"crcFreeDirty"`
	DirtyInCRC   []int `json:"dirtyInCrc"` // 影响校验的改动偏移(hex 视图标红用)
	DirtyFree    []int `json:"dirtyFree"`  // 不影响校验的改动偏移(hex 视图标蓝用)
}

// EditLoadFromDevice 把当前选中设备的整片内容读进编辑器。
func (a *App) EditLoadFromDevice() (*EditState, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	// 复用刚才读过的缓存: 编辑器要的是"设备当前内容", 而 Select/Dump 已经读过一次,
	// 真机上整片读取要 2~3 秒(逐字节事务), 没必要再读一遍。
	a.mu.Lock()
	cached := a.lastDump != nil && a.lastDumpAddr == dev.Addr()
	var dump []byte
	if cached {
		dump = append([]byte{}, a.lastDump...)
	}
	a.mu.Unlock()
	if !cached {
		var err error
		if dump, err = dev.ReadAll(); err != nil {
			return nil, err
		}
	}
	ed, err := spd.NewEditor(dump)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.editor = ed
	a.editSource = fmt.Sprintf("设备 %#x", dev.Addr())
	a.editFromDevice = true
	a.mu.Unlock()
	a.logf("编辑器载入设备 %#x(%d 字节, %s)", dev.Addr(), len(dump), dev.Generation())
	return a.editStateLocked()
}

// EditLoadPath 从文件载入编辑器(用于离线修改 dump)。
func (a *App) EditLoadPath(path string) (*EditState, error) {
	defer a.lockOp()()
	dump, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	ed, err := spd.NewEditor(dump)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.editor = ed
	a.editSource = path
	a.editFromDevice = false
	a.mu.Unlock()
	a.logf("编辑器载入 %s(%d 字节)", path, len(dump))
	return a.editStateLocked()
}

// EditLoadFileDialog 通过对话框选择文件载入编辑器。
func (a *App) EditLoadFileDialog() (*EditState, error) {
	if err := a.dialogGuard(); err != nil {
		return nil, err
	}
	path, err := a.OpenDialog("选择要编辑的 SPD dump 文件")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("已取消")
	}
	return a.EditLoadPath(path)
}

// EditState 返回编辑器状态(对外入口, 持操作锁)。
func (a *App) EditState() (*EditState, error) {
	defer a.lockOp()()
	return a.editStateLocked()
}

// editStateLocked 假定调用方已持操作锁。
func (a *App) editStateLocked() (*EditState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.editor == nil {
		return nil, fmt.Errorf("编辑器尚未载入数据")
	}
	ranges := spd.CRCRanges(a.editor.Bytes())
	stale := false
	for _, c := range a.editor.Changes() {
		if spd.AffectsChecksum(ranges, c.Offset) {
			stale = true
			break
		}
	}
	return &EditState{
		Source:      a.editSource,
		Generation:  a.editor.RamType().String(),
		Size:        a.editor.Size(),
		Dirty:       a.editor.IsDirty(),
		ChangeCount: len(a.editor.Changes()),
		CRCOK:       a.editor.CRCOK(),
		CanWrite:    true,
		CRCStale:    stale,
	}, nil
}

// editLocked 返回编辑器(调用方需自行保证并发安全)。
func (a *App) editLocked() (*spd.Editor, error) {
	a.mu.Lock()
	ed := a.editor
	a.mu.Unlock()
	if ed == nil {
		return nil, fmt.Errorf("编辑器尚未载入数据")
	}
	return ed, nil
}

// EditFields 返回全部可编辑字段(当前值/范围/风险)。
func (a *App) EditFields() ([]spd.Field, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	return ed.Fields(), nil
}

// EditSetField 修改一个字段(值非法时返回错误, 编辑器保持原样)。
func (a *App) EditSetField(key, value string) (*EditState, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	if err := ed.SetField(key, value); err != nil {
		return nil, err
	}
	return a.editStateLocked()
}

// EditSetByte 原始 hex 编辑: 直接改一个字节(高风险)。
func (a *App) EditSetByte(offset, value int) (*EditState, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	if value < 0 || value > 255 {
		return nil, fmt.Errorf("字节值必须在 0-255 之间")
	}
	if err := ed.SetByte(offset, byte(value)); err != nil {
		return nil, err
	}
	return a.editStateLocked()
}

// EditFixCRC 重算全部 CRC/校验和。
func (a *App) EditFixCRC() (*EditState, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	// 内容**已经**通过校验时直接返回(一个字节都不改): 底层 FixCRC 会顺手初始化
	// "半填充的扩展槽"(判据与硬校验不同), 在正常 dump 上会改动厂商残留字节并让文件
	// 变脏(审计 M1: 67 份语料里有 6 份被这样改过)。用户按"重算 CRC"的意图是修校验。
	if ed.CRCOK() {
		a.logf("重算 CRC: 当前内容已通过校验, 未改动任何字节")
		return a.editStateLocked()
	}
	n, err := ed.FixCRC()
	if err != nil {
		return nil, err
	}
	a.logf("编辑器: 已重算 CRC(改动 %d 字节)", n)
	return a.editStateLocked()
}

// EditReset 放弃全部编辑。
func (a *App) EditReset() (*EditState, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	ed.Reset()
	a.logf("编辑器: 已放弃全部修改")
	return a.editStateLocked()
}

// EditDiff 返回变更列表(按区域聚合 + 高风险计数)。
func (a *App) EditDiff() (*EditDiff, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	changes := ed.Changes()
	d := &EditDiff{ChangeCount: len(changes), CRCOK: ed.CRCOK()}
	const maxShow = 500
	shown := changes
	if len(shown) > maxShow {
		shown, d.Truncated = shown[:maxShow], true
	}
	d.Changes = shown
	if d.Changes == nil {
		d.Changes = []spd.EditChange{}
	}
	crcRanges := spd.CRCRanges(ed.Bytes())
	byteChanges := make([]eeprom.ByteChange, 0, len(changes))
	crcSet := map[int]bool{}
	for _, off := range spd.CRCBytes(crcRanges) {
		crcSet[off] = true
	}
	for _, c := range changes {
		bc := eeprom.ByteChange{Offset: c.Offset, Old: c.Old, New: c.New, IsCRC: crcSet[c.Offset]}
		byteChanges = append(byteChanges, bc)
		if bc.IsCRC {
			d.CRCFields++
		}
		// 影响校验 vs 不影响: 前者必须重算 CRC, 后者(序列号/日期等)不用管
		if spd.AffectsChecksum(crcRanges, c.Offset) {
			d.CRCDirty++
			d.DirtyInCRC = append(d.DirtyInCRC, c.Offset)
		} else {
			d.CRCFreeDirty++
			d.DirtyFree = append(d.DirtyFree, c.Offset)
		}
		if c.Risk == "high" {
			d.HighRisk++
		}
	}
	d.Fields = summarizeFields(byteChanges, ed.Bytes())
	return d, nil
}

// EditBytes 返回编辑器当前内容的 base64(前端刷新 hex 视图用)。
func (a *App) EditBytes() ([]byte, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ed.Bytes()))
	copy(out, ed.Bytes())
	return out, nil
}

// EditExportDialog 把编辑器内容另存为文件(不写设备)。
func (a *App) EditExportDialog() (string, error) {
	defer a.lockOp()()
	if err := a.dialogGuard(); err != nil {
		return "", err
	}
	ed, err := a.editLocked()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	src := a.editSource
	a.mu.Unlock()
	name := fmt.Sprintf("spd-edited-%d.bin", ed.Size())
	if !a.editFromDevice && src != "" {
		// 从文件来的编辑器内容: 默认文件名取原文件名(含路径会变成带目录的怪名字,
		// Windows 上还会被当成子路径)
		base := filepath.Base(src)
		ext := filepath.Ext(base)
		name = strings.TrimSuffix(base, ext) + "-edited" + ext
	}
	path, err := a.SaveDialog("另存编辑后的 SPD dump", name)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("已取消")
	}
	if err := os.WriteFile(path, ed.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s: %w", path, err)
	}
	a.logf("编辑器: 已导出 %s(%d 字节)", path, ed.Size())
	return path, nil
}

// EditApplyToDevice 把编辑器内容写入设备。要求确认串 WRITE(真实写入)或 DRYRUN(干跑)。
//
// 与 WriteConfirmed 同一套顺序: 确认串 → 备份(**早于**任何探测) → 预检 → 探测后复核 →
// 写入 → 校验 → 失败回滚。DDR4 及更早的写保护探测是真实写, 备份必须排在它前面。
func (a *App) EditApplyToDevice(force, dryRun bool, ack string) (*WriteResult, error) {
	defer a.lockOp()()
	want := "WRITE"
	if dryRun {
		want = "DRYRUN"
	}
	if strings.ToUpper(strings.TrimSpace(ack)) != want {
		return nil, fmt.Errorf("确认串不正确(应输入 %s)", want)
	}
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	restore, err := a.beginDryRunIfRequested(dryRun)
	if err != nil {
		return nil, err
	}
	defer restore()
	a.mu.Lock()
	fromDev := a.editFromDevice
	dev := a.dev
	a.mu.Unlock()
	if !fromDev {
		// 内容来自文件也可以写回设备(例如克隆/修复): 预检里的长度/世代/CRC/写保护/BIOS
		// 门禁就是为这种情况准备的 —— 写错世代的文件会在那里被拦住。
		a.mu.Lock()
		a.logf("编辑器内容来自 %s(非当前设备读取), 仍走完整预检门禁后写入", a.editSource)
		a.mu.Unlock()
	}
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	dump := make([]byte, len(ed.Bytes()))
	copy(dump, ed.Bytes())
	img, backup, err := a.backupIfNeeded(dev, dryRun)
	if err != nil {
		return nil, fmt.Errorf("写入前备份失败, 已中止: %w", err)
	}
	a.resetBusCounter() // 统计窗口覆盖预检(与 WriteConfirmed 一致)
	pf, err := a.buildPreflight("编辑器内容", dump, force, true)
	if err != nil {
		return nil, err
	}
	if err := a.afterProbeCheck(dev, img); err != nil {
		return nil, err
	}
	return a.writeWithPreflight(pf, dump, force, dryRun, img, backup)
}

// MfgSearch 在 JEP106 厂商表中搜索(编辑器厂商下拉用)。
func (a *App) MfgSearch(query string, limit int) ([]spd.MfgEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return spd.SearchManufacturers(query, limit), nil
}

// EditVerifyFile 把编辑器内容与设备当前内容比对(不写入)。
func (a *App) EditVerifyFile() (*EditDiff, error) {
	defer a.lockOp()()
	ed, err := a.editLocked()
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	cur, err := dev.ReadAll()
	if err != nil {
		return nil, err
	}
	want := ed.Bytes()
	if len(want) != len(cur) {
		// 编辑器内容来自文件(可能是另一代的 dump): 长度不同直接报错, 不能按索引访问
		return nil, fmt.Errorf("编辑器内容 %d 字节与设备 %d 字节不一致, 无法比对(请先从设备载入)",
			len(want), len(cur))
	}
	d := &EditDiff{CRCOK: ed.CRCOK()}
	for i, b := range cur {
		want := want[i]
		if b != want {
			d.Changes = append(d.Changes, spd.EditChange{Offset: i, Old: b, New: want, Field: "与设备不一致", Risk: "medium"})
			if len(d.Changes) >= 500 {
				d.Truncated = true
				break
			}
		}
	}
	d.ChangeCount = len(d.Changes)
	return d, nil
}
