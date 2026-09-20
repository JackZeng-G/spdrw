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
	RamType string `json:"ramType"`
	Size    int    `json:"size"`
}

// LogEntry 日志行。
type LogEntry struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// BuildHash 由构建命令注入(-X), 用于日志自识别版本。
var BuildHash = "dev"

// LogVersion 打印版本行(启动时调用, 便于确认运行的是哪个构建)。
func (a *App) LogVersion() {
	a.logf("SPD Reader Writer (Go) build %s", BuildHash)
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
	lastDump     []byte

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
	for i := range ctls {
		if err := a.Connect(i); err != nil {
			continue
		}
		dimms, err := a.Scan()
		if err != nil {
			continue
		}
		if len(dimms) > 0 {
			res.CtlIndex, res.Dimms = i, dimms
			return res, nil
		}
		if res.CtlIndex < 0 {
			res.CtlIndex, res.Dimms = i, dimms
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
	out := make([]ControllerInfo, 0, len(a.transports))
	for i, t := range a.transports {
		c, err := t.Identity()
		if err != nil {
			continue
		}
		out = append(out, ControllerInfo{
			Index: i, Name: c.Name, Kind: string(c.Kind),
			NoSpdWp: c.NoSpdWp, WpKnown: c.WpKnown,
		})
	}
	a.logf("发现 %d 个 SMBus 控制器", len(out))
	return out, nil
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
					info.RamType = rt.String()
				} else {
					info.RamType = "未知"
				}
			}
			out = append(out, info)
			dev.Close()
		}
	}
	scanOnce()
	if len(out) == 0 {
		// 第二遍: 总线/HUB 空闲后重试(对齐原版的宽容时序)
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
		a.logf("扫描完成: 0 个设备(0x50: %s; 其余地址同类)", classify(lastErr[0x50]))
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
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	start := time.Now()
	data, err := a.dev.ReadAll()
	if err != nil {
		return nil, err
	}
	st := a.dev.ReadStats()
	if st.BlockReadKnown && st.BlockReadOK {
		a.logf("读取 %d 字节: 块读加速(64 字节/事务… 事务 %d 次, 耗时 %s)", len(data), st.Transactions, time.Since(start).Round(time.Millisecond))
	} else if st.BlockReadKnown {
		a.logf("读取 %d 字节: 逐字节(块读不可用, 已回退; 事务 %d 次, 耗时 %s)",
			len(data), st.Transactions, time.Since(start).Round(time.Millisecond))
	}
	a.lastDumpAddr = a.dev.Addr()
	a.lastDump = data
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

// SaveDump 保存到文件: 优先复用最近一次读取的缓存, 无缓存时现读。
func (a *App) SaveDump(path string) error {
	a.mu.Lock()
	if a.dev == nil {
		a.mu.Unlock()
		return fmt.Errorf("请先选择设备")
	}
	var data []byte
	if a.lastDump != nil && a.lastDumpAddr == a.dev.Addr() {
		data = a.lastDump
	} else {
		// 无缓存: 现读(释放锁让 Dump() 正常加锁)
		a.mu.Unlock()
		var err error
		if data, err = a.Dump(); err != nil {
			return err
		}
		a.mu.Lock()
	}
	c := make([]byte, len(data))
	copy(c, data)
	a.mu.Unlock()
	if err := os.WriteFile(path, c, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	a.logf("已保存 %s (%d 字节)", path, len(c))
	return nil
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

// SaveDumpDialog 弹出保存对话框并把缓存/现读的 dump 写入所选路径。
func (a *App) SaveDumpDialog() error {
	if err := a.dialogGuard(); err != nil {
		return err
	}
	a.mu.Lock()
	name := "spd-1024B.bin"
	if a.dimm != nil {
		name = fmt.Sprintf("spd-%#x-%d.bin", a.dimm.Addr, a.dimm.Size)
	}
	a.mu.Unlock()
	path, err := a.SaveDialog("保存 SPD dump", name)
	if err != nil {
		return err
	}
	if path == "" {
		a.logf("已取消保存")
		return nil
	}
	return a.SaveDump(path)
}

// DecodeFileDialog 弹出打开对话框并解析所选 dump 文件。
func (a *App) DecodeFileDialog() (*DecodeResult, error) {
	if err := a.dialogGuard(); err != nil {
		return nil, err
	}
	path, err := a.OpenDialog("选择 SPD dump 文件")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("已取消")
	}
	r, err := a.DecodeFile(path)
	if r != nil {
		r.Path = path
	}
	return r, err
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
	return path, a.VerifyFile(path)
}

// SaveDumpData 把前端传入的 dump 写到文件。
func (a *App) SaveDumpData(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	a.mu.Lock()
	a.logf("已保存 %s (%d 字节)", path, len(data))
	a.mu.Unlock()
	return nil
}

// DecodeFile 离线解析 dump 文件(不依赖设备)。
func (a *App) DecodeFile(path string) (*DecodeResult, error) {
	dump, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	a.mu.Lock()
	a.logf("解析 %s", path)
	a.mu.Unlock()
	return DecodeDump(dump)
}

// ReadFileBytes 读取文件的原始字节(前端展示用)。
func (a *App) ReadFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// VerifyFile 比对文件与设备内容。
func (a *App) VerifyFile(path string) error {
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
func (a *App) WPStatus() (*WPStatusResult, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	det, err := dev.WPStatusDetail()
	if err != nil {
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

// WPSet 设置指定块 RSWP; blocks 为块号列表。
// 参数必须是 []int: Wails v2 用 json.Unmarshal 解参数, JS 数组解不进 []byte
// ([]byte 只能从 base64 字符串解出), 旧签名 []byte 会让"加保护"必然失败。
func (a *App) WPSet(blocks []int) error {
	defer a.lockOp()()
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
		if b < 0 || b > 255 {
			return fmt.Errorf("块号 %d 无效", b)
		}
		if err := dev.RSWPSet(byte(b)); err != nil {
			a.mu.Lock()
			a.logf("RSWP 设置块 %d 失败: %v", b, err)
			a.mu.Unlock()
			return err
		}
		a.logf("RSWP: 已保护块 %d", b)
	}
	return nil
}

// WPClear 清除全部可逆写保护。
func (a *App) WPClear() error {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("请先选择设备")
	}
	if err := dev.RSWPClear(); err != nil {
		a.mu.Lock()
		a.logf("RSWP 清除失败: %v", err)
		a.mu.Unlock()
		return err
	}
	a.logf("RSWP: 已清除全部块保护")
	return nil
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
}

// CloseDevice 仅断开设备选择。
func (a *App) CloseDevice() {
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
func (a *App) Logs() []LogEntry {
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
func (a *App) BusStats() (*BusStatsResult, error) {
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
	ddr5 := dev != nil && dev.IsDDR5()
	nvm := smbus.NVMWrites(c.WriteLog(), ddr5)
	res := &BusStatsResult{
		Reads: st.Reads, QuickWrites: st.QuickWrites,
		ByteDataWrites: st.ByteDataWrites, ByteWrites: st.ByteWrites,
		NVMWrites: len(nvm),
	}
	if dev != nil {
		res.Generation = dev.Generation()
	}
	return res, nil
}

// ResetBusStats 清零总线计数(真机验证前后各调一次即可看到本次操作的净事务数)。
func (a *App) ResetBusStats() error {
	defer a.lockOp()()
	a.mu.Lock()
	active := a.active
	a.mu.Unlock()
	c, ok := active.(*smbus.CountingTransport)
	if !ok {
		return fmt.Errorf("当前控制器没有总线计数")
	}
	c.Reset()
	return nil
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

// SetFastRead 开关块读加速(默认开)。关掉可对照"逐字节"的兼容模式。
func (a *App) SetFastRead(on bool) (bool, error) {
	defer a.lockOp()()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return false, fmt.Errorf("请先选择设备")
	}
	dev.SetFastRead(on)
	a.logf("块读加速: %v", map[bool]string{true: "开启(快)", false: "关闭(逐字节兼容)"}[on])
	return dev.FastRead(), nil
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
