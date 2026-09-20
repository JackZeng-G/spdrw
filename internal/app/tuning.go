package app

import (
	"fmt"

	"spdrw/internal/smbus"
)

// 总线调优的服务层: 把 PawnIO 模块的"等待模式"和 SMBus 时钟频率暴露给界面。
//
// 为什么这决定了读速: 模块把每次事务的等待分成长等待(整笔事务预估时间)与轮询等待,
// 两者若交给 Windows 线程休眠, 每次等待至少一个时钟中断(约 15.6ms) —— 一次 2 字节读
// 因此要 ~31ms, 整片 1024 字节 512 次事务就是 16 秒。改成忙等后回到真实总线时间
// (396kHz 下一次 2 字节读约 116µs)。

// BusTuningResult 是当前总线的调优状态(界面显示用)。
type BusTuningResult struct {
	Tunable     bool   `json:"tunable"` // 当前控制器是否支持调优(PawnIO 硬件会话才支持)
	ClockHz     int    `json:"clockHz"`
	ClockNote   string `json:"clockNote,omitempty"`
	SleepMode   int    `json:"sleepMode"`
	SleepModeOf string `json:"sleepModeName"`
	FastRead    bool   `json:"fastRead"`
	Note        string `json:"note,omitempty"`
}

// BusTuning 返回当前控制器的 SMBus 时钟与等待模式。
func (a *App) BusTuning() (*BusTuningResult, error) {
	a.mu.Lock()
	active := a.active
	dev := a.dev
	a.mu.Unlock()
	if active == nil {
		return nil, fmt.Errorf("尚未连接控制器")
	}
	res := &BusTuningResult{}
	if dev != nil {
		res.FastRead = dev.FastRead()
	}
	tuner, ok := smbus.TunerOf(active)
	if !ok {
		res.Note = "当前控制器不支持调优(测试/非 PawnIO 会话)"
		return res, nil
	}
	res.Tunable = true
	mode := tuner.SleepMode()
	res.SleepMode, res.SleepModeOf = int(mode), smbus.SleepModeName(mode)
	if hz, err := tuner.ClockHz(); err == nil && hz > 0 {
		res.ClockHz = hz
	} else {
		res.ClockNote = "该控制器不支持读取 SMBus 时钟"
	}
	if mode == smbus.SleepModeAlwaysSleep {
		res.Note = "当前为休眠模式: 每次事务固定多花约 31ms, 整片读取会慢几十倍;" +
			"除非要省 CPU, 建议切成忙等"
	}
	return res, nil
}

// SetSleepMode 切换等待模式(0=忙等 / 1=折中 / 2=休眠), 返回设置后的模式。
func (a *App) SetSleepMode(mode int) (int, error) {
	defer a.lockOp()()
	a.mu.Lock()
	active := a.active
	a.mu.Unlock()
	if active == nil {
		return 0, fmt.Errorf("尚未连接控制器")
	}
	tuner, ok := smbus.TunerOf(active)
	if !ok {
		return 0, fmt.Errorf("当前控制器不支持切换等待模式(测试/非 PawnIO 会话)")
	}
	if !smbus.ValidSleepMode(mode) {
		return 0, fmt.Errorf("等待模式 %d 无效(0=忙等 / 1=折中 / 2=休眠)", mode)
	}
	if err := tuner.SetSleepMode(smbus.SleepMode(mode)); err != nil {
		return 0, fmt.Errorf("设置等待模式失败: %w", err)
	}
	cur := int(tuner.SleepMode())
	a.logf("总线等待模式: %s", smbus.SleepModeName(tuner.SleepMode()))
	return cur, nil
}

// busTuningNote 生成一行用于日志的"总线环境"说明(时钟 + 等待模式)。
// 自行加锁 —— 持锁的调用点(Select/Dump)必须用 busTuningNoteLocked, 否则自锁死。
func (a *App) busTuningNote() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busTuningNoteLocked()
}

// busTuningNoteLocked 假定调用方已持 a.mu。
func (a *App) busTuningNoteLocked() string {
	tuner, ok := smbus.TunerOf(a.active)
	if !ok {
		return ""
	}
	mode := tuner.SleepMode()
	hz, err := tuner.ClockHz()
	if err != nil {
		hz = 0
	}
	return fmt.Sprintf("SMBus %s, %s", formatClockHz(hz), smbus.SleepModeName(mode))
}

// formatClockHz 把 Hz 格式化成 kHz 文本。
func formatClockHz(hz int) string {
	if hz <= 0 {
		return "时钟未知"
	}
	return fmt.Sprintf("%.1fkHz", float64(hz)/1000)
}
