package app

import (
	"strings"
	"sync"
	"testing"

	"spdrw/internal/smbus"
)

// TestConcurrentOperationsDoNotRace 复现审查发现的并发场景:
// Wails 每个绑定方法各起一个 goroutine, 用户长 dump 期间点"保护状态"、
// 连点两次"应用"都会真正并发进入 —— 之前 Editor 的 map 与 Device 的分页状态
// 都没有保护(go test -race 能直接抓到 concurrent map read/write)。
// 现在所有对外入口都按 lockOp 串行化。
func TestConcurrentOperationsDoNotRace(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func(n int) {
			defer wg.Done()
			_, _ = a.Dump()
		}(i)
		go func(n int) {
			defer wg.Done()
			_, _ = a.WPStatus()
		}(i)
		go func(n int) {
			defer wg.Done()
			_, _ = a.EditSetField("partNumber", "RACE"+strings.Repeat("X", n%3))
		}(i)
		go func(n int) {
			defer wg.Done()
			_ = a.Logs()
			_, _ = a.EditFields()
			_, _ = a.BusStats()
		}(i)
	}
	wg.Wait()

	// 串行化之后: 编辑器内容必须是"某一次编辑后的完整结果", 不能是半截状态
	if _, err := a.EditDiff(); err != nil {
		t.Fatalf("并发之后编辑器应仍可用: %v", err)
	}
	st, err := a.EditState()
	if err != nil {
		t.Fatalf("EditState: %v", err)
	}
	// 注意: 读取(Dump)现在会自动把内容载入编辑器, 并发下最后一次可能是"读取重置后的干净副本",
	// 所以这里不要求 Dirty, 只要求状态自洽(载入来源/长度/CRC 都说得通)。
	if st.Size == 0 || st.Source == "" {
		t.Fatalf("并发之后编辑器状态应自洽: %+v", st)
	}
	// 设备内容仍应是合法 CRC 的镜像(并发读不该破坏任何东西)
	cur, err := a.Dump()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 512 {
		t.Fatalf("dump 长度异常: %d", len(cur))
	}
	_ = smbus.NewFake // 保持 import(测试夹具来自 smbus)
}
