// Package app 是 GUI 绑定的服务层: 连接管理、扫描、读写、写保护与解析的编排。
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"spdrw/internal/eeprom"
	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// ControllerInfo 是前端展示用的控制器条目。
type ControllerInfo struct {
	// Devices 是枚举时在该控制器上探测到的 SPD 数量(0 = 没有设备, 默认不列出)。
	Devices int    `json:"devices"`
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	NoSpdWp bool   `json:"noSpdWp"` // true = 未检测到 BIOS SPD 写禁止
	WpKnown bool   `json:"wpKnown"`
}

// DimmInfo 是扫描到的 SPD 设备。
type DimmInfo struct {
	Addr    byte   `json:"addr"`
	IsDDR5  bool   `json:"isDdr5"`
	RAMType string `json:"ramType"`
	Size    int    `json:"size"`
}

// LogEntry 日志行。
type LogEntry struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// BuildHash 由构建命令注入(-X), 用于日志自识别版本。
var BuildHash = "dev"

// BuildAuthor 是署名(日志与界面都显示)。
const BuildAuthor = "by jackzeng 2026"

// LogVersion 打印版本行(启动时调用, 便于确认运行的是哪个构建)。
func (a *App) LogVersion() {
	a.logf("SPD Reader Writer (Go) build %s · %s", BuildHash, BuildAuthor)
}

// SetContext 由 main.go 在 OnStartup 注入 Wails 运行时上下文。
func (a *App) SetContext(ctx context.Context) { a.wctx = ctx }

// App 持有全部状态; 方法绑定到前端(Wails)。
type App struct {
	mu sync.Mutex
	// opMu 串行化"会碰总线或编辑器工作副本"的操作。
	//
	// Wails 每个绑定方法各起一个 goroutine: 长 dump 期间点"保护状态"、连点两次"应用",
	// 都会并发进入 eeprom 的分页状态(d.page/pageKnown)或编辑器的 map —— 前者会让分页区
	// 读写到错误页, 后者是 Go 运行时的 concurrent map read/write 直接 fatal。
	// 所有对外入口按"外层加锁、内部不加锁"的约定使用 lockOp。
	opMu sync.Mutex
	// logMu 保护日志切片(Logs 与 logf 可能来自不同 goroutine)。
	logMu sync.Mutex

	// transports 由后端枚举; Windows 上是 PawnIO, 测试中可注入。
	transports []smbus.Transport
	active     smbus.Transport
	ctrl       smbus.Controller
	dev        *eeprom.Device
	dimm       *DimmInfo

	logs []LogEntry

	// lastDump 缓存最近一次成功读取的整片数据(按设备绑定, Select 时失效),
	// 保存文件直接复用, 避免经 JS 传大数组和重读总线。
	lastDumpAddr byte
	// lastBackupPath 是最近一次写入前备份的路径(日志/界面回显, 也是失败时的手动恢复源)
	lastBackupPath string
	lastDump       []byte

	// 编辑器状态(工作副本在 spd.Editor 内)
	editor         *spd.Editor
	editSource     string
	editFromDevice bool

	// wctx 是 Wails 运行时上下文(OnStartup 注入), 对话框等运行时能力用。
	wctx context.Context

	// Emit 由 main.go 注入(wailsjs runtime events); tests 置 nil。
	Emit func(event string, data ...interface{})

	// dialogs 由 main.go 注入(封装 wails runtime 对话框, 需要 ctx);
	// 返回空路径 = 用户取消。tests 置 nil 时对话框方法直接报"不可用"。
	SaveDialog func(title, defaultName string) (string, error)
	OpenDialog func(title string) (string, error)

	// now 便于测试注入。
	now func() time.Time
}

// New 构造未连接的 App。
func New() *App { return &App{now: time.Now} }

// wrapCounting 给传输套上总线计数包装(已经是计数包装则原样返回)。
func wrapCounting(t smbus.Transport) smbus.Transport {
	if _, ok := t.(*smbus.CountingTransport); ok {
		return t
	}
	return smbus.NewCounting(t)
}

// useEnumBackends 在 Windows 上枚举 PawnIO 控制器; 非 Windows 报可读错误。
var useEnumBackends = func() ([]smbus.Transport, error) {
	return smbus.DiscoverBackends()
}

// AutoConnectResult 自动连接结果(前端展示)。
// 注意: Wails v2 绑定方法只支持 1-2 个返回值, 多返回值会静默返回 null,
// 因此必须打包成单个结构体。
type AutoConnectResult struct {
	CtlIndex int        `json:"ctlIndex"`
	Dimms    []DimmInfo `json:"dimms"`
}

// AutoConnectAll 依次尝试各控制器, 停在第一个扫到设备的上(前端启动自动连接)。
// 都没有设备时返回最后尝试的控制器与空表。
func (a *App) AutoConnectAll() (*AutoConnectResult, error) {
	res := &AutoConnectResult{CtlIndex: -1, Dimms: []DimmInfo{}}
	ctls, err := a.ListControllers()
	if err != nil {
		return nil, err
	}
	// 注意: ctls 是**过滤后**的列表(只保留探测到设备的控制器), 它的下标 != transports 下标。
	// 必须用 ControllerInfo.Index 去 Connect, 否则"SPD 不在 transports[0]"的机器上会连错控制器,
	// 界面里一台设备都看不到(审计发现的阻断项)。
	for i := range ctls {
		if err := a.Connect(ctls[i].Index); err != nil {
			continue
		}
		dimms, err := a.Scan()
		if err != nil {
			continue
		}
		if len(dimms) > 0 {
			res.CtlIndex, res.Dimms = ctls[i].Index, dimms
			return res, nil
		}
		if res.CtlIndex < 0 {
			res.CtlIndex, res.Dimms = ctls[i].Index, dimms
		}
	}
	return res, nil
}

// ListControllers 枚举本机 SMBus 控制器(并缓存)。
func (a *App) ListControllers() ([]ControllerInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transports == nil {
		ts, err := useEnumBackends()
		if err != nil {
			return nil, err
		}
		// 套一层事务计数: 真机验证"干跑零写入"时需要从程序里读到总线事务数
		for i, t := range ts {
			ts[i] = wrapCounting(t)
		}
		a.transports = ts
	}
	all := make([]ControllerInfo, 0, len(a.transports))
	for i, t := range a.transports {
		c, err := t.Identity()
		if err != nil {
			continue
		}
		info := ControllerInfo{
			Index: i, Name: c.Name, Kind: string(c.Kind),
			NoSpdWp: c.NoSpdWp, WpKnown: c.WpKnown,
		}
		info.Devices = probeSPDCount(t)
		all = append(all, info)
	}

	// AMD FCH 的端口 0/2/3/4 共用同一个 IO 基址(0x0B00), 端口 1 是独立辅助控制器,
	// 而只有其中一个端口真的接着内存条。把没有设备的条目也列出来只会让用户困惑
	// (用户反馈: 5 个控制器里 1/3/4/5 的编号没有意义), 所以默认只保留有设备的。
	listed := make([]ControllerInfo, 0, len(all))
	for _, c := range all {
		if c.Devices > 0 {
			listed = append(listed, c)
		}
	}
	if len(listed) == 0 {
		a.logf("未在任何控制器上探测到 SPD, 暂时列出全部 %d 个控制器", len(all))
		return all, nil
	}
	if hidden := len(all) - len(listed); hidden > 0 {
		var names []string
		for _, c := range all {
			if c.Devices == 0 {
				names = append(names, c.Name)
			}
		}
		a.logf("已隐藏 %d 个无设备的控制器(%s)", hidden, strings.Join(names, "; "))
	}
	a.logf("发现 %d 个 SMBus 控制器(其中有设备的 %d 个)", len(all), len(listed))
	return listed, nil
}

// probeSPDCount 逐个 SPD 地址探测(只读 byte0, 与扫描同一套判据)并返回在线设备数。
// 用只读判据而不是快速命令: 在桥接/AMD 平台上更可靠, 而且探测阶段不产生写事务。
func probeSPDCount(t smbus.Transport) int {
	n := 0
	for addr := byte(0x50); addr <= 0x57; addr++ {
		if _, err := t.ReadByteData(addr, 0); err == nil {
			n++
		}
	}
	return n
}

// Connect 选择控制器并复位状态。
func (a *App) Connect(index int) error {
	defer a.lockOp()()
	a.mu.Lock()
	defer a.mu.Unlock()
	if index < 0 || index >= len(a.transports) {
		return fmt.Errorf("控制器索引 %d 越界", index)
	}
	a.disconnectLocked()
	// 统一在这里套总线计数(幂等): 无论传输是枚举来的还是测试注入的,
	// 干跑/写入都能报告"总线下发了哪些事务"。
	a.transports[index] = wrapCounting(a.transports[index])
	a.active = a.transports[index]
	c, err := a.active.Identity()
	if err != nil {
		return err
	}
	a.ctrl = c
	a.logf("已连接 %s", c.Name)
	return nil
}

// Scan 扫描当前控制器的 0x50-0x57。
func (a *App) Scan() ([]DimmInfo, error) {
	defer a.lockOp()()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return nil, fmt.Errorf("请先连接控制器")
	}
	out := []DimmInfo{}
	lastErr := map[byte]error{}
	scanOnce := func() {
		for addr := byte(0x50); addr <= 0x57; addr++ {
			// 探测: 读 SPD 字节0(比快速命令在桥接/AMD 平台上更可靠)
			b, err := a.active.ReadByteData(addr, 0)
			if err != nil {
				lastErr[addr] = err
				continue
			}
			a.logf("探测 %#x 在线(byte0=%#x)", addr, b)
			dev, err := eeprom.New(a.active, addr)
			if err != nil {
				a.logf("地址 %#x: %v", addr, err)
				continue
			}
			info := DimmInfo{Addr: addr, IsDDR5: dev.IsDDR5(), Size: dev.Size()}
			if data, err := dev.Read(0, 3); err == nil {
				if rt, _, _ := spd.Identify(append([]byte{}, data...)); rt != spd.Unknown {
					info.RAMType = rt.String()
				} else {
					info.RAMType = "未知"
				}
			}
			out = append(out, info)
			dev.Close()
		}
	}
	scanOnce()
	if len(out) == 0 {
		// 第二遍: 总线/HUB 空闲后重试(对齐上游实现的宽容时序)
		time.Sleep(300 * time.Millisecond)
		scanOnce()
	}
	if len(out) == 0 && len(lastErr) > 0 {
		// 全部地址有错误响应: 分类汇报首个地址的错误, 便于定位
		classify := func(err error) string {
			s := err.Error()
			switch {
			case strings.Contains(s, "NACK"), strings.Contains(s, "无响应"):
				return "设备无应答(NACK)"
			case strings.Contains(s, "超时"):
				return "事务超时(HUB 响应过慢或设备离线)"
			case strings.Contains(s, "占用"):
				return "总线被占用"
			default:
				return s
			}
		}
		// lastErr 可能缺 0x50(例如 0x50 读到了 byte0 但 eeprom.New 失败), 直接索引会拿到
		// nil error → classify 里 err.Error() 空指针 panic(审计发现的崩溃点)。
		var first error
		for addr := byte(0x50); addr <= 0x57; addr++ {
			if e, ok := lastErr[addr]; ok && e != nil {
				first = e
				break
			}
		}
		if first == nil {
			for _, e := range lastErr {
				if e != nil {
					first = e
					break
				}
			}
		}
		if first == nil {
			a.logf("扫描完成: 0 个设备(地址 0x50-0x57 均未应答或无法识别, 无错误详情)")
		} else {
			a.logf("扫描完成: 0 个设备(首个错误: %s)", classify(first))
		}
		return out, nil
	}
	a.logf("扫描完成: %d 个设备", len(out))
	return out, nil
}

// Select 选定要操作的 DIMM。
func (a *App) Select(addr byte) error {
	defer a.lockOp()()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return fmt.Errorf("请先连接控制器")
	}
	dev, err := eeprom.New(a.active, addr)
	if err != nil {
		return err
	}
	if a.dev != nil {
		a.dev.Close()
	}
	a.dev = dev
	if c, ok := a.active.(*smbus.CountingTransport); ok {
		c.SetDDR5(dev.IsDDR5()) // NVM 写的判据随世代不同(cmd bit7)
	}
	// 把总线环境打进日志: 读速异常时第一眼要看的就是这两项(时钟频率 + 等待模式)
	if tune := a.busTuningNoteLocked(); tune != "" {
		a.logf("总线环境: %s", tune)
	}
	a.lastDump = nil // 换设备后缓存失效
	a.editor = nil   // 换设备后编辑器内容失效(避免把 A 条的编辑写进 B 条)
	a.editSource = ""
	a.editFromDevice = false
	a.dimm = &DimmInfo{Addr: addr, IsDDR5: dev.IsDDR5(), Size: dev.Size()}
	a.logf("已选择 %#x (%s, %d 字节)", addr, map[bool]string{true: "DDR5", false: "非DDR5"}[dev.IsDDR5()], dev.Size())
	return nil
}

// Dump 读取整片 SPD; progress(0..100) 经事件推送。
func (a *App) Dump() ([]byte, error) {
	defer a.lockOp()()
	return a.dumpLocked()
}

// dumpLocked 是 Dump 的实现, 假定调用方已持操作锁(opMu)。
// SaveDump 这类"已经持锁"的入口必须走这里, 否则会自锁死 —— 死锁守卫测试抓到过一次。
func (a *App) dumpLocked() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	start := time.Now()
	data, err := a.dev.ReadAll()
	if err != nil {
		// 读失败: 清掉缓存, 免得后面"从设备载入/复用缓存"拿到上一次的旧内容
		a.lastDump, a.lastDumpAddr = nil, 0
		// 自动降级: 忙等(默认, 最快)下若出现事务异常, 自动换成"轮询忙等 + 长等待休眠"
		// 再试一次 —— 有些控制器/HUB 需要更宽松的等待, 用户不必关心这些细节, 但日志要写清楚。
		if tuner, ok := smbus.TunerOf(a.active); ok && tuner.SleepMode() == smbus.SleepModeAlwaysBusy {
			a.logf("读取失败(%v): 自动把总线等待模式降级为「%s」后重试",
				err, smbus.SleepModeName(smbus.SleepModeShortBusy))
			if serr := tuner.SetSleepMode(smbus.SleepModeShortBusy); serr == nil {
				start = time.Now()
				data, err = a.dev.ReadAll()
			}
		}
		if err != nil {
			return nil, err
		}
	}
	st := a.dev.ReadStats()
	mode := st.Mode
	if mode == "" {
		mode = "未知读取方式"
	}
	line := fmt.Sprintf("读取 %d 字节: %s; 事务 %d 次, 耗时 %s(等待 %d ms)",
		len(data), mode, st.Transactions, time.Since(start).Round(time.Millisecond), st.SleepMS)
	if tune := a.busTuningNoteLocked(); tune != "" {
		line += "; " + tune
	}
	if st.Note != "" && st.FallbackBytes > 0 {
		line += "; 回退原因: " + st.Note
	}
	a.logf("%s", line)
	a.lastDumpAddr = a.dev.Addr()
	a.lastDump = data
	// 读一次就自动把内容放进编辑器(用户反馈: 编辑器再点一次"从设备载入"是多余的)。
	// 但如果编辑器里有**未保存的改动**, 不能悄悄丢弃(审计 M4): 保留工作副本并提示。
	if a.editor != nil && a.editor.IsDirty() {
		a.logf("设备内容已刷新; 编辑器里有 %d 处未保存改动, 未覆盖(需要时点“放弃修改”)",
			len(a.editor.Changes()))
	} else if ed, eerr := spd.NewEditor(data); eerr == nil {
		a.editor = ed
		a.editSource = fmt.Sprintf("设备 %#x", a.dev.Addr())
		a.editFromDevice = true
	} else {
		a.editor, a.editSource, a.editFromDevice = nil, "", false
		a.logf("编辑器未载入(该内容暂不支持编辑): %v", eerr)
	}
	a.emit("dump:done", len(data))
	a.logf("读取 %d 字节", len(data))
	// DDR5 诊断: 关键 MR 寄存器 + NVM 前 4 字节直读, 用于定位页/NVM 访问问题
	if a.dev.IsDDR5() {
		a.mu.Unlock()
		a.dumpDiagnostics()
		a.mu.Lock()
	}
	return data, nil
}

// dumpDiagnostics 只读诊断(压缩为单行, 完整页扫描已完成使命并移除):
// HUB 配置/页寄存器/写保护状态, 用于远程排障。
func (a *App) dumpDiagnostics() {
	t := a.active
	addr := a.dev.Addr()
	type mr struct {
		name string
		cmd  byte
	}
	mrs := []mr{{"MR0", 0x00}, {"MR3", 0x03}, {"MR11", 0x0B}, {"MR29", 0x1D}, {"MR48", 0x30}}
	var sb strings.Builder
	for i, m := range mrs {
		if i > 0 {
			sb.WriteByte(' ')
		}
		if b, err := t.ReadByteData(addr, m.cmd); err == nil {
			fmt.Fprintf(&sb, "%s=%02x", m.name, b)
		} else {
			fmt.Fprintf(&sb, "%s=ERR", m.name)
		}
	}
	a.logf("HUB 诊断: %s", sb.String())
}

// WriteFileDialog 已由 PickWriteFile + PreflightWrite + WriteConfirmed 取代
// (旧实现弹框后直接写, 没有 diff/风险/备份环节), 保留此名会诱导绕过预检, 故删除。

// dialogGuard 返回对话框函数是否可用(测试环境未注入时给出明确错误)。
func (a *App) dialogGuard() error {
	if a.SaveDialog == nil || a.OpenDialog == nil {
		return fmt.Errorf("文件对话框不可用(运行环境未注入)")
	}
	return nil
}

// VerifyFileDialog 弹出打开对话框并校验所选文件与设备当前内容, 返回文件路径。
func (a *App) VerifyFileDialog() (string, error) {
	if err := a.dialogGuard(); err != nil {
		return "", err
	}
	path, err := a.OpenDialog("选择要校验的 dump 文件")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("已取消")
	}
	return path, a.verifyFile(path)
}

// VerifyFile 比对文件与设备内容。
func (a *App) verifyFile(path string) error {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("请先选择设备")
	}
	dump, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	if err := dev.Verify(dump); err != nil {
		a.mu.Lock()
		a.logf("校验失败: %v", err)
		a.mu.Unlock()
		return err
	}
	a.logf("校验通过: %s", path)
	return nil
}

// WPStatusResult 是写保护状态的完整结果。
//
// 注意: Wails v2 绑定方法**最多 2 个返回值**, 3 个以上会被静默序列化成 null
// (见 internal/binding/boundMethod.go 的 Call 只处理 OutputCount 1/2) ——
// 旧实现 `WPStatus() (blocks, pswp, offline, err)` 因此在 UI 侧永远拿到 null。
// 这里必须打包成单结构体。
type WPStatusResult struct {
	DDR5           bool     `json:"ddr5"`
	Generation     string   `json:"generation"`
	Blocks         int      `json:"blocks"`
	BlockSize      int      `json:"blockSize"`
	Protected      []bool   `json:"protected"`
	Known          []bool   `json:"known"`
	MR11           byte     `json:"mr11"`
	MR12           byte     `json:"mr12"`
	MR13           byte     `json:"mr13"`
	MR29           byte     `json:"mr29"`
	MR48           byte     `json:"mr48"`
	MR52           byte     `json:"mr52"`
	ProtectionHit  bool     `json:"protectionHit"`
	Offline        bool     `json:"offline"`
	PSWPApplicable bool     `json:"pswpApplicable"`
	PSWP           bool     `json:"pswp"`
	Warnings       []string `json:"warnings"`
}

// WPStatus 返回各块 RSWP 状态、原始寄存器与永久保护状态(单结构体返回)。
//
// 注意: DDR4 及更早世代**没有**保护状态寄存器, 状态只能靠"对块首取反写一字节再还原"
// 的写测试得出 —— 也就是说"查询保护状态"这个按钮本身会写设备。因此这里在探测前
// 先做整片备份, 探测后再整片复核; 一旦还原写失败(内容与备份不一致)立即回滚并报错,
// 绝不让用户拿着一份被探测写坏的条继续操作。
func (a *App) WPStatus() (*WPStatusResult, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	// 只有"靠写测试探测"的世代(DDR4 及更早)才需要先备份; DDR5 读 MR12/MR13 位图,
	// 一个字节都不写 —— 那就别做无谓的整片备份, 日志也不该说"会做写测试"。
	var img []byte
	var backup string
	if dev.NeedsWriteTest() && !dev.DryRun() {
		var berr error
		img, backup, berr = a.backupCurrent(dev)
		if berr != nil {
			return nil, fmt.Errorf("查询保护状态需要先备份当前内容(该世代用写测试探测): %w", berr)
		}
		a.logf("保护状态查询: 该世代用写测试探测, 已先备份当前内容(%s)", backup)
	}
	det, err := dev.WPStatusDetail()
	if err != nil {
		return nil, err
	}
	if err := a.afterProbeCheck(dev, img); err != nil {
		return nil, err
	}
	res := &WPStatusResult{
		DDR5: det.DDR5, Generation: dev.Generation(),
		Blocks: det.Blocks, BlockSize: det.BlockSize,
		Protected: det.Protected, Known: det.Known,
		MR11: det.MR11, MR12: det.MR12, MR13: det.MR13,
		MR29: det.MR29, MR48: det.MR48, MR52: det.MR52,
		ProtectionHit: det.ProtectionHit, Offline: det.Offline,
		PSWPApplicable: det.PSWPApplicable, PSWP: det.PSWP,
		Warnings: det.Warnings,
	}
	a.logf("保护状态: %s", summarizeWP(res))
	for _, w := range res.Warnings {
		a.logf("保护状态提示: %s", w)
	}
	return res, nil
}

// summarizeWP 生成一行人类可读的保护状态摘要(用于日志)。
func summarizeWP(r *WPStatusResult) string {
	var sb strings.Builder
	for i := 0; i < r.Blocks; i++ {
		if i > 0 {
			sb.WriteByte(' ')
		}
		state := "开放"
		switch {
		case !r.Known[i]:
			state = "未知"
		case r.Protected[i]:
			state = "保护"
		}
		fmt.Fprintf(&sb, "B%d=%s", i, state)
	}
	if r.DDR5 {
		fmt.Fprintf(&sb, " | MR12=%#02x MR13=%#02x", r.MR12, r.MR13)
		if r.Offline {
			sb.WriteString(" 离线模式")
		}
	} else if r.PSWPApplicable {
		if r.PSWP {
			sb.WriteString(" | PSWP 永久保护已生效")
		} else {
			sb.WriteString(" | PSWP 未设置")
		}
	} else {
		sb.WriteString(" | PSWP 不适用")
	}
	return sb.String()
}

// WPSet 设置指定块 RSWP; blocks 为块号列表。确认串统一为 "CLEAR"
// (旧写法 "RSWP" 仍接受, 免得改了界面就点不动)。
//
// RSWP 在部分平台上是**不可逆**的(清零需要离线模式或断电), 且 EE1004 的 SWP/CWP
// 命令需要 WP 引脚上的 VHV —— 器件可能照样 ACK 但忽略命令, 所以下发后必须回读复核,
// 不能"命令没报错"就当成功。
func (a *App) WPSet(blocks []int, ack string) error {
	defer a.lockOp()()
	switch strings.ToUpper(strings.TrimSpace(ack)) {
	case "CLEAR", "RSWP": // CLEAR 为统一确认串, RSWP 为兼容旧写法
	default:
		return fmt.Errorf("确认串不正确(应输入 CLEAR)")
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("请先选择设备")
	}
	if len(blocks) == 0 {
		return fmt.Errorf("未指定块号")
	}
	for _, b := range blocks {
		if b < 0 || b >= 256 {
			return fmt.Errorf("块号 %d 无效", b)
		}
	}
	if dev.DryRun() {
		return fmt.Errorf("干跑模式下不修改写保护(请先关闭干跑模式)")
	}
	img, backup, err := a.backupIfNeeded(dev, dev.DryRun())
	if err != nil {
		return fmt.Errorf("设置写保护前备份失败, 已中止: %w", err)
	}
	if img != nil {
		a.logf("RSWP 设置: 已先备份当前内容(%s)", backup)
	}
	for _, b := range blocks {
		if err := dev.RSWPSet(byte(b)); err != nil {
			a.logf("RSWP 设置块 %d 失败: %v", b, err)
			return err
		}
		a.logf("RSWP: 已下发保护命令 块 %d", b)
	}
	if err := a.afterProbeCheck(dev, img); err != nil {
		return err
	}
	if ok, note := a.verifyProtection(dev, blocks, true); !ok {
		a.logf("RSWP 复核: %s", note)
		return fmt.Errorf("写保护命令已下发但回读显示未生效: %s", note)
	}
	a.logf("RSWP: 块 %v 已确认受保护(回读复核通过)", blocks)
	return nil
}

// WPClear 清除全部可逆写保护。必须带确认串 "CLEAR"。
func (a *App) WPClear(ack string) error {
	defer a.lockOp()()
	if strings.ToUpper(strings.TrimSpace(ack)) != "CLEAR" {
		return fmt.Errorf("确认串不正确(清除写保护应输入 CLEAR)")
	}
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("请先选择设备")
	}
	if dev.DryRun() {
		return fmt.Errorf("干跑模式下不修改写保护(请先关闭干跑模式)")
	}
	img, backup, err := a.backupIfNeeded(dev, dev.DryRun())
	if err != nil {
		return fmt.Errorf("清除写保护前备份失败, 已中止: %w", err)
	}
	if img != nil {
		a.logf("RSWP 清除: 已先备份当前内容(%s)", backup)
	}
	if err := dev.RSWPClear(); err != nil {
		a.logf("RSWP 清除失败: %v", err)
		return err
	}
	if err := a.afterProbeCheck(dev, img); err != nil {
		return err
	}
	all := make([]int, 0, 16)
	for i := 0; i < dev.WPBlockCount(); i++ {
		all = append(all, i)
	}
	if ok, note := a.verifyProtection(dev, all, false); !ok {
		a.logf("RSWP 清除复核: %s", note)
		return fmt.Errorf("清除命令已下发但回读显示仍有块受保护: %s", note)
	}
	a.logf("RSWP: 已确认全部块可写(回读复核通过)")
	return nil
}

// verifyProtection 回读复核保护状态(want=true 期望受保护)。
func (a *App) verifyProtection(dev *eeprom.Device, blocks []int, want bool) (bool, string) {
	det, err := dev.WPStatusDetail()
	if err != nil {
		return false, fmt.Sprintf("回读保护状态失败: %v", err)
	}
	var bad []string
	for _, b := range blocks {
		if b < 0 || b >= det.Blocks {
			continue
		}
		if !det.Known[b] {
			bad = append(bad, fmt.Sprintf("块 %d 状态未知(写测试失败)", b))
			continue
		}
		if det.Protected[b] != want {
			got := "未受保护"
			if det.Protected[b] {
				got = "受保护"
			}
			bad = append(bad, fmt.Sprintf("块 %d 实际%s", b, got))
		}
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ") +
			"(EE1004 的 SWP/CWP 需要 WP 引脚 VHV, 器件可能 ACK 但忽略命令)"
	}
	return true, ""
}

// Decode 解析当前设备或给定 dump 的 SPD, 供信息面板展示。
func (a *App) Decode(dump []byte) (*DecodeResult, error) {
	defer a.lockOp()()
	if len(dump) == 0 {
		a.mu.Lock()
		dev := a.dev
		a.mu.Unlock()
		if dev == nil {
			return nil, fmt.Errorf("请先选择设备或提供 dump")
		}
		var err error
		dump, err = dev.ReadAll()
		if err != nil {
			return nil, err
		}
	}
	return DecodeDump(dump)
}

// Close 释放全部传输。
func (a *App) Close() {
	defer a.lockOp()()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disconnectLocked()
	for _, t := range a.transports {
		_ = t.Close()
	}
	a.transports = nil
}

// lockOp 取得操作锁, 用法: defer a.lockOp()()。
// 只允许在对外入口调用; 内部函数假定调用方已持锁(否则会自锁死)。
func (a *App) lockOp() func() {
	a.opMu.Lock()
	return a.opMu.Unlock
}

func (a *App) disconnectLocked() {
	if a.dev != nil {
		a.dev.Close()
		a.dev = nil
		a.dimm = nil
	}
	if a.active != nil {
		a.active = nil
	}
	// 断开后编辑器/控制器状态都要清干净: 否则 EditState 还会说"设备 0x50, 可写"
	// (审计 LOW: 只有 a.editor 非空就报 CanWrite=true)。
	a.editor, a.editSource, a.editFromDevice = nil, "", false
	a.ctrl = smbus.Controller{}
	a.lastDump, a.lastDumpAddr = nil, 0
}

// CloseDevice 仅断开设备选择。
func (a *App) closeDevice() {
	defer a.lockOp()()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dev != nil {
		a.dev.Close()
		a.dev = nil
		a.dimm = nil
	}
}

// Logs 返回全部日志。
func (a *App) logEntries() []LogEntry {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

func (a *App) logf(format string, args ...interface{}) {
	entry := LogEntry{Time: a.now().Format("15:04:05"), Text: fmt.Sprintf(format, args...)}
	a.logMu.Lock()
	a.logs = append(a.logs, entry)
	a.logMu.Unlock()
	if a.Emit != nil {
		a.Emit("log", entry)
	}
}

func (a *App) emit(event string, data ...interface{}) {
	if a.Emit != nil {
		a.Emit(event, data...)
	}
}

// BusStats 返回当前控制器自上次 Reset 以来的总线事务计数, 外加"对 SPD NVM 的字节写"
// 数量(按当前设备的世代判定)。真机验证干跑时用这个作为"确实没写"的证据。
func (a *App) busStats() (*BusStatsResult, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	active := a.active
	a.mu.Unlock()
	c, ok := active.(*smbus.CountingTransport)
	if !ok {
		return nil, fmt.Errorf("当前控制器没有总线计数(未连接或非枚举得到的控制器)")
	}
	st := c.Stats()
	// NVM 写用**独立累计**的计数(M7): WriteLog 有上限, force 写 1024 字节时
	// 从日志里数会少报, 而这是真机"干跑零写入"的唯一证据。
	res := &BusStatsResult{
		Reads: st.Reads, QuickWrites: st.QuickWrites,
		ByteDataWrites: st.ByteDataWrites, ByteWrites: st.ByteWrites,
		NVMWrites: c.NVMWriteCount(),
	}
	if dev != nil {
		res.Generation = dev.Generation()
	}
	return res, nil
}

// BusStatsResult 是总线事务统计。
type BusStatsResult struct {
	Generation     string `json:"generation"`
	Reads          int    `json:"reads"`
	QuickWrites    int    `json:"quickWrites"`
	ByteDataWrites int    `json:"byteDataWrites"`
	ByteWrites     int    `json:"byteWrites"`
	NVMWrites      int    `json:"nvmWrites"`
}

// ReadStats 返回当前设备的读取方式统计(界面显示"这次是快读还是慢读")。
func (a *App) ReadStats() (*eeprom.ReadStats, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	st := dev.ReadStats()
	return &st, nil
}
