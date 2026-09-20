package smbus

import "fmt"

// 总线调优: PawnIO 模块把每个事务的等待分成"长等待"(整笔事务的预估时间)与
// "短等待"(轮询状态寄存器一次的时间), 两种等待各有两种实现 —— 忙等(µs 级精确)
// 与线程休眠(受 Windows 时钟中断粒度影响, 一次约 15.6ms)。
//
// 这个区别决定了整个工具的读取速度:
//
//	AlwaysSleep(2): 长等待与轮询都走线程休眠 → 一次事务 ≈ 1~2 个时钟中断 ≈ 31ms
//	AlwaysBusy (0): 两者都忙等 → 一次 2 字节读 ≈ 116µs(396kHz 下的真实总线时间)
//
// 1024 字节按 2 字节/事务 = 512 次事务: 前者 16s, 后者 <0.1s。OpenRGB 用 AlwaysSleep
// 是为了省 CPU(它不做大批量读), 我们做整片读必须用忙等 —— 忙等的总开销也只有百毫秒级。
//
// (下面 SleepMode 的取值就是这段文字里的 0/1/2。)

// SleepMode 是单次事务等待模式的取值: 0=长/短等待都忙等, 1=仅短等待忙等, 2=都休眠。
type SleepMode int

const (
	SleepModeAlwaysBusy  SleepMode = 0 // 长/短等待都忙等(最快, 默认)
	SleepModeShortBusy   SleepMode = 1 // 只有短等待忙等
	SleepModeAlwaysSleep SleepMode = 2 // 都休眠(最省 CPU, 最慢)
)

// SleepModeName 返回人类可读名称(界面/日志用)。
func SleepModeName(m SleepMode) string {
	switch m {
	case SleepModeAlwaysBusy:
		return "忙等(最快)"
	case SleepModeShortBusy:
		return "轮询忙等+长等待休眠(折中)"
	case SleepModeAlwaysSleep:
		return "休眠(最省 CPU, 最慢)"
	default:
		return fmt.Sprintf("未知(%d)", int(m))
	}
}

// ValidSleepMode 报告模式值是否在模块接受的范围内。
func ValidSleepMode(m int) bool {
	return m >= int(SleepModeAlwaysBusy) && m <= int(SleepModeAlwaysSleep)
}

// DefaultSleepMode 是工具的默认等待模式。
//
// 默认忙等: 真机实测(396kHz AMD FCH)休眠模式下每次事务固定多花约 31ms,
// 整片 1024 字节要 16~32 秒; 忙等把这一项降回真实总线时间。
var DefaultSleepMode = SleepModeAlwaysBusy

// Tuner 是可选的"总线调优"能力: 只有真正的硬件会话(PawnIO 模块)才有,
// 测试用的 Fake 等实现不提供 —— 调用方用 TunerOf 探测后再用。
type Tuner interface {
	// SleepMode 返回当前等待模式。
	SleepMode() SleepMode
	// SetSleepMode 设置等待模式, 返回设置后的模式。
	SetSleepMode(mode SleepMode) error
	// ClockHz 返回 SMBus 时钟频率(Hz); 模块不支持时返回错误。
	ClockHz() (int, error)
}

// tunerHost 由包装传输(计数/记录)实现, 把调优能力从内层透出来。
// 注意: 包装器**不**实现 Tuner 本身 —— 否则对内层的 Fake(没有调优能力)也会误报"支持"。
type tunerHost interface{ InnerTuner() (Tuner, bool) }

// TunerOf 从传输(含计数/记录包装)里取出调优能力。
func TunerOf(t Transport) (Tuner, bool) {
	if t == nil {
		return nil, false
	}
	if tuner, ok := t.(Tuner); ok {
		return tuner, true
	}
	if h, ok := t.(tunerHost); ok {
		return h.InnerTuner()
	}
	return nil, false
}
