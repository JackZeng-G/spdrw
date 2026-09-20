package app

import (
	"strings"
	"testing"

	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// 写入能力探测: 三种结局(生效 / 被忽略 / 被拒绝)都要能区分, 且探测后设备内容必须
// 与探测前逐字节一致 —— 它是一次真实写, 绝不能留下痕迹。
func TestWriteProbeVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*smbus.FakeTransport)
		verdict string
	}{
		{"可写", func(f *smbus.FakeTransport) {}, "ok"},
		{"写入被忽略", func(f *smbus.FakeTransport) { f.IgnoreFrom = 0 }, "ignored"},
		{"写入被拒绝", func(f *smbus.FakeTransport) { f.ProtectedFrom = 0 }, "rejected"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := smbus.NewFake()
			f.SetDDR5(true)
			img := ddr5WriteFixture()
			// 清出几个空闲字节, 让探测有目标可选
			for i := 555; i < 640; i++ {
				img[i] = 0x00
			}
			copy(f.EEProm, img)
			c.setup(f)
			a := New()
			a.transports = []smbus.Transport{f}
			if err := a.Connect(0); err != nil {
				t.Fatal(err)
			}
			if err := a.Select(0x50); err != nil {
				t.Fatal(err)
			}
			res, err := a.WriteProbe()
			if err != nil {
				t.Fatalf("WriteProbe: %v", err)
			}
			if res.Verdict != c.verdict {
				t.Fatalf("verdict = %q, want %q(note: %s)", res.Verdict, c.verdict, res.Note)
			}
			if res.Backup == "" {
				t.Fatal("探测前必须先备份")
			}
			// 目标字节必须落在"不参与校验"的区域(断电也不会破坏 CRC)
			if spd.AffectsChecksum(spd.CRCRanges(img), res.Offset) {
				t.Fatalf("探测字节 %#x 落在 CRC 覆盖范围内(应选空闲的非覆盖字节)", res.Offset)
			}
			if c.verdict == "ok" {
				if !res.Restored || !res.Verified {
					t.Fatalf("可写场景必须还原并复核: %+v", res)
				}
			}
			// 无论哪种结局: 设备内容与探测前逐字节一致
			for i := range img {
				if f.EEProm[i] != img[i] {
					t.Fatalf("探测改动了设备内容 @%#x: %02X != %02X", i, f.EEProm[i], img[i])
				}
			}
			if res.Note == "" {
				t.Fatal("应给出人类可读的结论")
			}
		})
	}
}

// 探测在干跑模式下必须拒绝(否则会给出误导性结论)。
func TestWriteProbeRefusesDryRun(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.SetDryRun(true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteProbe(); err == nil || !strings.Contains(err.Error(), "干跑") {
		t.Fatalf("干跑模式下应拒绝探测: %v", err)
	}
}

// 失败现场: 写入中止的错误里必须带物理位置与 hub 寄存器现场(真机排障靠这一行)。
func TestWriteFailureIncludesScene(t *testing.T) {
	a, rec, ft := newDDR5WriteApp(t)
	orig := append([]byte{}, ft.EEProm...)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetByte(0x208, int(orig[0x208]^0x01)); err != nil {
		t.Fatal(err)
	}
	rec.Reset()
	// 只拒绝 NVM 窗口写(cmd bit7=1); 切页用的 MR11 寄存器写(cmd<0x80)照常, 否则连
	// 预检里的整片读取都会失败, 测不到"写入失败"那条路径。
	rec.DenyWriteFrom = 0x80
	res, err := a.EditApplyToDevice(false, false, "WRITE")
	if err == nil {
		t.Fatal("注入 NACK 后应失败")
	}
	if res == nil || !res.RolledBack {
		t.Fatalf("应自动回滚: %+v", res)
	}
	msg := err.Error()
	for _, want := range []string{"现场", "页 ", "cmd ", "MR12=", "MR52="} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误里应含 %q: %s", want, msg)
		}
	}
}

// 写入档位自适应: 有些 SPD5 hub 对 NVM 的写只认块写(协议 5), 逐字节写会被忽略。
// 这时程序必须自己切到块写并成功, 而不是报"写被忽略"。
func TestWriteModeFallsBackToBlockWrite(t *testing.T) {
	f := smbus.NewFake()
	f.SetDDR5(true)
	copy(f.EEProm, ddr5WriteFixture())
	rec := smbus.NewRecording(f)
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	orig := append([]byte{}, f.EEProm...)
	if _, err := a.EditSetByte(0x208, int(orig[0x208]^0x01)); err != nil {
		t.Fatal(err)
	}
	// 逐字节写全部"写了不生效", 块写照常
	f.IgnoreByteDataFrom = 0
	rec.Reset()

	res, err := a.EditApplyToDevice(false, false, "WRITE")
	if err != nil {
		t.Fatalf("应自动切到块写并成功: %v", err)
	}
	if !res.Verified {
		t.Fatalf("应校验通过: %+v", res)
	}
	if !strings.Contains(res.Message, "块写") {
		t.Fatalf("结果里应写明已切到块写: %q", res.Message)
	}
	if f.EEProm[0x208] != orig[0x208]^0x01 {
		t.Fatalf("目标字节应已写入: %02X", f.EEProm[0x208])
	}
	// 最后复核: 写入档位记录必须留下说明(日志体现)
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev.WriteModeNote() == "" {
		t.Fatal("档位切换必须留下说明(日志里要看得见)")
	}
}

// 探测必须能区分"只有块写能生效"这种情况 —— 这正是 DDR5 hub 可能的行为差异。
func TestWriteProbeDetectsBlockWriteOnly(t *testing.T) {
	f := smbus.NewFake()
	f.SetDDR5(true)
	img := ddr5WriteFixture()
	for i := 555; i < 640; i++ {
		img[i] = 0x00
	}
	copy(f.EEProm, img)
	f.IgnoreByteDataFrom = 0 // 逐字节写全部不生效, 块写照常
	a := New()
	a.transports = []smbus.Transport{f}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	res, err := a.WriteProbe()
	if err != nil {
		t.Fatalf("WriteProbe: %v", err)
	}
	if res.Verdict != "ok" || res.Mode != "块写(协议 5)" {
		t.Fatalf("应识别出「只有块写生效」: %+v", res)
	}
	for i := range img {
		if f.EEProm[i] != img[i] {
			t.Fatalf("探测改动了设备内容 @%#x", i)
		}
	}
}

// DDR5 的保护状态查询只读 MR12/MR13, 不应该做"写测试备份"、也不该说"会做写测试"。
func TestWPStatusDDR5DoesNotRequireBackup(t *testing.T) {
	f := smbus.NewFake()
	f.SetDDR5(true)
	copy(f.EEProm, ddr5WriteFixture())
	rec := smbus.NewRecording(f)
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	rec.Reset()
	if _, err := a.WPStatus(); err != nil {
		t.Fatalf("WPStatus: %v", err)
	}
	for _, op := range rec.Ops() {
		if op.Write {
			t.Fatalf("DDR5 保护状态查询不得产生任何写事务: %s@%#x", op.Kind, op.Cmd)
		}
	}
	logs := a.Logs()
	for _, l := range logs {
		if strings.Contains(l.Text, "会做写测试") {
			t.Fatalf("DDR5 不该出现「会做写测试」: %s", l.Text)
		}
	}
}
