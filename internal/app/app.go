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
	a.mu.Lock()
	defer a.mu.Unlock()
	if index < 0 || index >= len(a.transports) {
		return fmt.Errorf("控制器索引 %d 越界", index)
	}
	a.disconnectLocked()
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
	a.dimm = &DimmInfo{Addr: addr, IsDDR5: dev.IsDDR5(), Size: dev.Size()}
	a.logf("已选择 %#x (%s, %d 字节)", addr, map[bool]string{true: "DDR5", false: "非DDR5"}[dev.IsDDR5()], dev.Size())
	return nil
}

// Dump 读取整片 SPD; progress(0..100) 经事件推送。
func (a *App) Dump() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dev == nil {
		return nil, fmt.Errorf("请先选择设备")
	}
	data, err := a.dev.ReadAll()
	if err != nil {
		return nil, err
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

// WriteFromFile 从文件写入(读取文件 → eeprom.Write)。
// force=false 跳过相同字节; verify 写后回读已内置。
func (a *App) WriteFromFile(path string, force bool) error {
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
	// 写前保护检查
	status, err := dev.RSWPStatus()
	if err == nil {
		for i, protected := range status {
			if protected {
				return fmt.Errorf("块 %d 处于写保护, 写入被拒绝; 可先执行 RSWP 清除(若可逆)", i)
			}
		}
	} else {
		a.mu.Lock()
		a.logf("保护状态查询失败(继续): %v", err)
		a.mu.Unlock()
	}
	a.mu.Lock()
	size := dev.Size()
	a.mu.Unlock()
	if len(dump) > size {
		return fmt.Errorf("文件 %d 字节超过 SPD 大小 %d", len(dump), size)
	}
	err = dev.Write(dump[:size], force, func(written int) {
		a.emit("write:progress", written)
	})
	if err != nil {
		a.mu.Lock()
		a.logf("写入失败: %v", err)
		a.mu.Unlock()
		return err
	}
	a.logf("写入完成: %s (%d 字节)", path, size)
	return nil
}

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

// WriteFileDialog 弹出打开对话框并把所选文件写入当前设备, 返回文件路径。
func (a *App) WriteFileDialog(force bool) (string, error) {
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
	return path, a.WriteFromFile(path, force)
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

// WPStatus 返回各块 RSWP 状态与 PSWP 永久保护状态。
func (a *App) WPStatus() (blocks []bool, pswp bool, offline bool, err error) {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return nil, false, false, fmt.Errorf("请先选择设备")
	}
	blocks, err = dev.RSWPStatus()
	if err != nil {
		return nil, false, false, err
	}
	pswp, _ = dev.PSWPStatus()
	if dev.IsDDR5() {
		offline, _ = dev.OfflineMode()
	}
	return blocks, pswp, offline, nil
}

// WPSet 设置指定块 RSWP; blocks 为块号列表。
func (a *App) WPSet(blocks []byte) error {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("请先选择设备")
	}
	for _, b := range blocks {
		if err := dev.RSWPSet(b); err != nil {
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
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disconnectLocked()
	for _, t := range a.transports {
		_ = t.Close()
	}
	a.transports = nil
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
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

func (a *App) logf(format string, args ...interface{}) {
	entry := LogEntry{Time: a.now().Format("15:04:05"), Text: fmt.Sprintf(format, args...)}
	a.logs = append(a.logs, entry)
	if a.Emit != nil {
		a.Emit("log", entry)
	}
}

func (a *App) emit(event string, data ...interface{}) {
	if a.Emit != nil {
		a.Emit(event, data...)
	}
}
