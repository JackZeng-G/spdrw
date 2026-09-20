package smbus

import (
	"strings"
	"testing"
)

// fake 的 Present 表必须对**全部**地址生效: 0x30-0x37 的 SWP/CWP/SPA quick 命令
// 也要能模拟"无人应答"(旧的 addr<0x30 豁免让这类模拟形同虚设)。
func TestFakeQuickRespectsPresentOnCommandAddresses(t *testing.T) {
	f := NewFake()
	f.Present = map[byte]bool{0x50: true}
	if err := f.Quick(0x50, true); err != nil {
		t.Fatalf("在表内地址应 ACK: %v", err)
	}
	for _, addr := range []byte{0x30, 0x33, 0x35, 0x36, 0x37} {
		if err := f.Quick(addr, true); err == nil || !strings.Contains(err.Error(), "NACK") {
			t.Fatalf("Quick(%#x) 应 NACK, got %v", addr, err)
		}
	}
}

// recording 的 Quick 必须**先转发后记录**: 转发产生的真实错误(NACK/总线)
// 也要落进 op.Err, 录制的序列才能与真实总线一致。
func TestRecordingQuickRecordsForwardedErrors(t *testing.T) {
	f := NewFake()
	f.Present = map[byte]bool{} // 全部 NACK
	rec := NewRecording(f)
	if err := rec.Quick(0x50, false); err == nil {
		t.Fatal("内层 NACK 应向上传播")
	}
	ops := rec.Ops()
	if len(ops) != 1 || ops[0].Kind != OpQuick {
		t.Fatalf("应恰好记录一条 Quick: %+v", ops)
	}
	if ops[0].Err == "" || !strings.Contains(ops[0].Err, "NACK") {
		t.Fatalf("内部错误应记录在 op.Err: %+v", ops[0])
	}
}
