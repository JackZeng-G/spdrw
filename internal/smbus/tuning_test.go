package smbus

import "testing"

func TestSleepModeNamesAndValidation(t *testing.T) {
	if SleepModeName(SleepModeAlwaysBusy) == "" || SleepModeName(SleepModeAlwaysSleep) == "" {
		t.Fatal("模式名不能为空")
	}
	for _, m := range []int{0, 1, 2} {
		if !ValidSleepMode(m) {
			t.Errorf("模式 %d 应有效", m)
		}
	}
	for _, m := range []int{-1, 3, 99} {
		if ValidSleepMode(m) {
			t.Errorf("模式 %d 应无效", m)
		}
	}
	if DefaultSleepMode != SleepModeAlwaysBusy {
		t.Fatalf("默认必须是忙等(休眠模式每次事务固定多花约 31ms): %v", DefaultSleepMode)
	}
}

// fakeTuner 模拟一个硬件会话的调优能力。
type fakeTuner struct {
	FakeTransport
	mode SleepMode
	hz   int
}

func (f *fakeTuner) SleepMode() SleepMode           { return f.mode }
func (f *fakeTuner) SetSleepMode(m SleepMode) error { f.mode = m; return nil }
func (f *fakeTuner) ClockHz() (int, error)          { return f.hz, nil }

func TestTunerOfUnwrapsWrappers(t *testing.T) {
	ft := &fakeTuner{mode: SleepModeAlwaysBusy, hz: 396000}
	inner := NewFake()
	// 直接持有
	if tuner, ok := TunerOf(ft); !ok || tuner == nil {
		t.Fatal("硬件会话应被识别为可调优")
	}
	// 计数包装
	counted := NewCounting(ft)
	tuner, ok := TunerOf(counted)
	if !ok {
		t.Fatal("计数包装应把调优能力透出来")
	}
	if hz, err := tuner.ClockHz(); err != nil || hz != 396000 {
		t.Fatalf("时钟读取失败: %d %v", hz, err)
	}
	// 记录包装
	rec := NewRecording(ft)
	if _, ok := TunerOf(rec); !ok {
		t.Fatal("记录包装应把调优能力透出来")
	}
	// 双层包装
	double := NewCounting(NewRecording(ft))
	if tuner, ok := TunerOf(double); !ok {
		t.Fatal("双层包装也应透出调优能力")
	} else if tuner.SleepMode() != SleepModeAlwaysBusy {
		t.Fatal("模式应透传")
	}
	// Fake 没有调优能力 —— 不能误报"支持"
	if _, ok := TunerOf(inner); ok {
		t.Fatal("Fake 不应被识别为可调优")
	}
	if _, ok := TunerOf(NewCounting(inner)); ok {
		t.Fatal("包装 Fake 后仍不应被识别为可调优")
	}
	if _, ok := TunerOf(nil); ok {
		t.Fatal("nil 不应被识别为可调优")
	}
}
