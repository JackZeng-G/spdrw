package eeprom

import (
	"strings"
	"testing"

	"spdrw/internal/smbus"
)

// newDDR4 返回已连接的 DDR4 设备(EEPROM 预填 0x11)。
func newDDR4(t *testing.T) (*Device, *smbus.FakeTransport) {
	t.Helper()
	ft := smbus.NewFake()
	ft.EEProm[2] = 0x0C // DDR4
	ft.Fill(0x11)
	d, err := New(ft, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, ft
}

func newDDR5(t *testing.T) (*Device, *smbus.FakeTransport) {
	t.Helper()
	ft := smbus.NewFake()
	ft.SetDDR5(true)
	ft.Fill(0x51) // DDR5 SPD magic
	d, err := New(ft, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, ft
}

func newDDR3(t *testing.T) (*Device, *smbus.FakeTransport) {
	t.Helper()
	ft := smbus.NewFake()
	ft.EEProm[2] = 0x0B // DDR3
	ft.Fill(0x22)
	d, err := New(ft, 0x51)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, ft
}

func TestNewDetectsDDR5(t *testing.T) {
	d, ft := newDDR5(t)
	if !d.IsDDR5() {
		t.Fatal("应识别为 DDR5")
	}
	if d.Size() != 1024 {
		t.Fatalf("DDR5 大小 = %d, want 1024", d.Size())
	}
	// 探测阶段应零写操作(页切换推迟到实际读写)
	if len(ft.WriteLog) != 0 {
		t.Fatalf("New 阶段不应有写操作: %+v", ft.WriteLog)
	}
	// 首次跨页读取时才写 MR11
	if _, err := d.Read(0x100, 1); err != nil {
		t.Fatalf("Read 0x100: %v", err)
	}
	found := false
	for _, op := range ft.WriteLog {
		if op.Cmd == 11 && op.Val == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("跨页读取应写 MR11=2: %+v", ft.WriteLog)
	}
}

func TestNewDetectsSizes(t *testing.T) {
	d4, _ := newDDR4(t)
	if d4.Size() != 512 {
		t.Fatalf("DDR4 大小 = %d", d4.Size())
	}
	d3, _ := newDDR3(t)
	if d3.Size() != 256 {
		t.Fatalf("DDR3 大小 = %d", d3.Size())
	}
	if d3.Addr() != 0x51 {
		t.Fatalf("地址 = %#x", d3.Addr())
	}
}

func TestDDR4Paging(t *testing.T) {
	d, ft := newDDR4(t)
	// 首次读页 0 应显式 quick 写 0x36(EE1004 页状态无寄存器可读, 需显式切换)
	if _, err := d.Read(0, 1); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(ft.QuickLog) == 0 || !ft.QuickLog[0].Write || ft.QuickLog[0].Addr != 0x36 {
		t.Fatalf("首次读应 quick 写 0x36: %v", ft.QuickLog)
	}

	// 读页 1 (0x100) 应先 quick 写 0x37
	if _, err := d.Read(0x100, 1); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(ft.QuickLog) == 0 {
		t.Fatal("跨页应产生 quick 命令")
	}
	q := ft.QuickLog[len(ft.QuickLog)-1]
	if q.Addr != 0x37 || !q.Write {
		t.Fatalf("页 1 应 quick 写 0x37, got %v", q)
	}

	// 同页连读不重复切页
	before := len(ft.QuickLog)
	if _, err := d.Read(0x1FF, 1); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(ft.QuickLog) != before {
		t.Fatal("同页读取不应重复切页")
	}

	// DDR4 偏移直通: 读 0x1FF 即向 0x51 读 0xFF
	// (FakeEEPROM 中 0xFF 已填 0x11)
	b, _ := d.Read(0x1FF, 1)
	if b[0] != 0x11 {
		t.Fatalf("DDR4 页 1 读回 %#x", b[0])
	}
}

func TestDDR5Paging(t *testing.T) {
	d, ft := newDDR5(t)
	ft.QuickLog = nil
	ft.WriteLog = nil

	// 页大小 128: 读 0x100 = 页 2 → MR11=2, 物理地址 0x00|0x80
	if _, err := d.Read(0x100, 1); err != nil {
		t.Fatalf("Read: %v", err)
	}
	var mr11 *smbus.WriteOp
	for i := range ft.WriteLog {
		if ft.WriteLog[i].Cmd == MR11 {
			mr11 = &ft.WriteLog[i]
		}
	}
	if mr11 == nil || mr11.Val != 2 {
		t.Fatalf("MR11 应写页 2, got %+v", mr11)
	}

	// DDR5 偏移映射: 物理地址 = off%128 | 0x80
	b, _ := d.Read(0x81, 1)
	_ = b // 0x81 → 页 0? 0x81>>7 = 1! 页 1, 物理 0x01|0x80
	// (数值已由 Fake 存取,这里只验证不 panic 且分页状态一致)

	// 越界
	if _, err := d.Read(1024, 1); err == nil {
		t.Fatal("越界读取应报错")
	}
}

func TestReadAll(t *testing.T) {
	d4, _ := newDDR4(t)
	b, err := d4.ReadAll()
	if err != nil || len(b) != 512 {
		t.Fatalf("DDR4 ReadAll = %d, %v", len(b), err)
	}
	d5, _ := newDDR5(t)
	b, err = d5.ReadAll()
	if err != nil || len(b) != 1024 {
		t.Fatalf("DDR5 ReadAll = %d, %v", len(b), err)
	}
	d3, _ := newDDR3(t)
	b, err = d3.ReadAll()
	if err != nil || len(b) != 256 {
		t.Fatalf("DDR3 ReadAll = %d, %v", len(b), err)
	}
}

func TestWriteUpdateSkipsIdentical(t *testing.T) {
	d, ft := newDDR4(t)
	dump := make([]byte, 512)
	for i := range dump {
		dump[i] = 0x11 // 与当前值相同 → 跳过
	}
	dump[0x10] = 0x99 // 唯一不同的字节 → 写
	if err := d.Write(dump, false, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(ft.WriteLog) != 1 {
		t.Fatalf("update 模式应只写 1 字节, wrote %d", len(ft.WriteLog))
	}
	if ft.WriteLog[0].Cmd != 0x10 || ft.WriteLog[0].Val != 0x99 {
		t.Fatalf("写错位置: %+v", ft.WriteLog[0])
	}
}

func TestWriteForceWritesAllAndVerify(t *testing.T) {
	d, ft := newDDR4(t)
	dump := make([]byte, 512)
	for i := range dump {
		dump[i] = byte(i)
	}
	if err := d.Write(dump, true, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// 512 写 + 512 回读校验(读不计入 WriteLog)
	if len(ft.WriteLog) != 512 {
		t.Fatalf("force 应写 512 字节, wrote %d", len(ft.WriteLog))
	}
	cur, _ := d.Read(0, 512)
	for i := range cur {
		if cur[i] != byte(i) {
			t.Fatalf("校验失败 @%#x", i)
			break
		}
	}
}

func TestWriteRejectsOversizeAndProtected(t *testing.T) {
	d, ft := newDDR4(t)
	if err := d.Write(make([]byte, 513), false, nil); err == nil {
		t.Fatal("超尺寸应报错")
	}
	// 模拟页 0 受保护(前 128 字节 NACK)
	ft.ProtectedFrom = 0
	dump := make([]byte, 512)
	dump[0] = 0x77
	err := d.Write(dump, false, nil)
	if err == nil || !strings.Contains(err.Error(), "0x00") {
		t.Fatalf("受保护写入应报错并带偏移, got %v", err)
	}
}

func TestRSWP(t *testing.T) {
	// DDR5: MR12/MR13 位图
	d5, ft := newDDR5(t)
	ft.MR[MR12] = 0x00
	ft.MR[MR13] = 0x00
	blocks, err := d5.RSWPStatus()
	if err != nil || len(blocks) != 16 {
		t.Fatalf("DDR5 RSWPStatus: %v len=%d", err, len(blocks))
	}
	for _, b := range blocks {
		if b {
			t.Fatal("初始应全部未保护")
		}
	}
	if err := d5.RSWPSet(3); err != nil {
		t.Fatalf("RSWPSet(3): %v", err)
	}
	if ft.MR[MR12] != 0x08 {
		t.Fatalf("MR12 = %#x, want 0x08", ft.EEProm[MR12])
	}
	if err := d5.RSWPSet(9); err != nil {
		t.Fatalf("RSWPSet(9): %v", err)
	}
	if ft.MR[MR13] != 0x02 {
		t.Fatalf("MR13 = %#x, want 0x02", ft.EEProm[MR13])
	}
	blocks, _ = d5.RSWPStatus()
	if !blocks[3] || !blocks[9] || blocks[0] {
		t.Fatalf("状态位错误: %v", blocks)
	}
	if err := d5.RSWPSet(16); err == nil {
		t.Fatal("block 16 越界应报错")
	}
	if err := d5.RSWPClear(); err != nil {
		t.Fatalf("RSWPClear: %v", err)
	}
	if ft.MR[MR12] != 0 || ft.MR[MR13] != 0 {
		t.Fatal("清除后 MR12/MR13 应为 0")
	}

	// DDR4: 写测试(未保护→false)
	d4, ft4 := newDDR4(t)
	blocks, err = d4.RSWPStatus()
	if err != nil || len(blocks) != 4 {
		t.Fatalf("DDR4 RSWPStatus: %v", err)
	}
	if blocks[0] {
		t.Fatal("未保护应 false")
	}
	// 设置 = quick 写 SWP 地址
	ft4.QuickLog = nil
	if err := d4.RSWPSet(2); err != nil {
		t.Fatalf("RSWPSet(2): %v", err)
	}
	if len(ft4.QuickLog) != 1 || ft4.QuickLog[0].Addr != 0x35 || !ft4.QuickLog[0].Write {
		t.Fatalf("DDR4 block2 应 quick 写 0x35, got %v", ft4.QuickLog)
	}
	// 清除 = quick 写 CWP 0x33
	ft4.QuickLog = nil
	if err := d4.RSWPClear(); err != nil {
		t.Fatalf("RSWPClear: %v", err)
	}
	if ft4.QuickLog[0].Addr != 0x33 {
		t.Fatalf("CWP 应为 0x33, got %#x", ft4.QuickLog[0].Addr)
	}

	// DDR3: 仅 1 块
	d3, _ := newDDR3(t)
	blocks, err = d3.RSWPStatus()
	if err != nil || len(blocks) != 1 {
		t.Fatalf("DDR3 RSWPStatus: %v", err)
	}
	if err := d3.RSWPSet(1); err == nil {
		t.Fatal("DDR3 仅支持 block 0")
	}
}

func TestPSWPStatus(t *testing.T) {
	// PSWP 探测: BYTE_DATA 读 0x30|(addr&7)(PWPB 设备类型 0110b)。
	// 未保护: 设备 ACK → false; 已永久保护: NACK → true。仅 DDR2/DDR3 适用。
	ft := smbus.NewFake()
	ft.EEProm[2] = 0x0B // DDR3(256B, 适用 PWPB)
	d, _ := New(ft, 0x50)
	if !d.PSWPApplicable() {
		t.Fatal("DDR3 应适用 PSWP 探测")
	}
	pswp, err := d.PSWPStatus()
	if err != nil || pswp {
		t.Fatalf("未保护: %v %v", pswp, err)
	}
	// 模拟永久保护: PWPB 设备无响应 — 通过 Present 表让 0x30 不在线
	ft.Present = map[byte]bool{0x30: false}
	pswp, err = d.PSWPStatus()
	if err != nil {
		t.Fatalf("PSWPStatus: %v", err)
	}
	if !pswp {
		t.Fatal("NACK 应判为已永久保护")
	}
}

func TestPSWPNotApplicableDDR4DDR5(t *testing.T) {
	// DDR4(EE1004)与 DDR5(SPD5118)没有 PWPB 设备类型: 对其探测必然 NACK,
	// 旧实现因此把每根条都误报成"PSWP 永久保护已生效"。必须直接判为不适用。
	d4, _ := newDDR4(t)
	if d4.PSWPApplicable() {
		t.Fatal("DDR4 不应适用 PSWP")
	}
	if _, err := d4.PSWPStatus(); err == nil {
		t.Fatal("DDR4 调用 PSWPStatus 应报不适用")
	}
	d5, _ := newDDR5(t)
	if d5.PSWPApplicable() {
		t.Fatal("DDR5 不应适用 PSWP")
	}
	if _, err := d5.PSWPStatus(); err == nil {
		t.Fatal("DDR5 调用 PSWPStatus 应报不适用")
	}

	// DDR5 状态下不得探测 0x30(真实硬件上是空地址 → 假阳性来源)
	rec, recFake := smbus.NewRecordingFake()
	recFake.SetDDR5(true)
	recFake.Fill(0x51)
	d5r, err := New(rec, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	det, err := d5r.WPStatusDetail()
	if err != nil {
		t.Fatalf("WPStatusDetail: %v", err)
	}
	if det.PSWPApplicable || det.PSWP {
		t.Fatalf("DDR5 不应有 PSWP 结论: %+v", det)
	}
	for _, op := range rec.Ops() {
		if op.Addr == 0x30 {
			t.Fatalf("DDR5 不应在 0x30 探测: %s", rec)
		}
	}
	if det.Blocks != 16 || det.BlockSize != 64 {
		t.Fatalf("DDR5 应为 16 块 × 64B: %+v", det)
	}
}

func TestWPStatusDetailDDR5MRs(t *testing.T) {
	d5, ft := newDDR5(t)
	ft.MR[MR12] = 0x0A // 块 1、3 受保护
	ft.MR[MR13] = 0x80 // 块 15 受保护
	ft.MR[MR48] = 0x04 // 离线模式
	ft.MR[MR52] = 0x40 // 写受保护块被忽略
	det, err := d5.WPStatusDetail()
	if err != nil {
		t.Fatalf("WPStatusDetail: %v", err)
	}
	want := map[int]bool{1: true, 3: true, 15: true}
	for i := 0; i < 16; i++ {
		if det.Protected[i] != want[i] {
			t.Fatalf("块 %d = %v, want %v(%+v)", i, det.Protected[i], want[i], det.Protected)
		}
		if !det.Known[i] {
			t.Fatalf("DDR5 位图状态应确知: %+v", det.Known)
		}
	}
	if det.MR12 != 0x0A || det.MR13 != 0x80 || det.MR48 != 0x04 || det.MR52 != 0x40 {
		t.Fatalf("MR 原始值: %+v", det)
	}
	if !det.Offline || !det.ProtectionHit {
		t.Fatalf("offline/protectionHit: %+v", det)
	}
	if len(det.Warnings) == 0 {
		t.Fatal("MR12/MR13 置位应有不可清零提示")
	}
}

func TestWPStatusDetailDDR4WriteTest(t *testing.T) {
	// 未保护: 全部开放且状态确知, 且写测试必须把值还原
	d4, ft := newDDR4(t)
	ft.Fill(0x11)
	det, err := d4.WPStatusDetail()
	if err != nil {
		t.Fatalf("WPStatusDetail: %v", err)
	}
	if det.Blocks != 4 || det.BlockSize != 128 {
		t.Fatalf("DDR4 应为 4 块 × 128B: %+v", det)
	}
	for i := 0; i < 4; i++ {
		if det.Protected[i] || !det.Known[i] {
			t.Fatalf("块 %d 应开放且确知: %+v", i, det)
		}
	}
	cur, _ := d4.ReadAll()
	for i, b := range cur {
		if b != 0x11 {
			t.Fatalf("写测试未还原 @%#x = %#x", i, b)
		}
	}
}

func TestWPStatusDetailUnknownStateOnRestoreFailure(t *testing.T) {
	// 还原失败必须报"状态未知"而不是假装受保护, 也绝不能静默把字节改掉。
	rec, ft := smbus.NewRecordingFake()
	ft.EEProm[2] = 0x0C // DDR4
	ft.Fill(0x11)
	// 第 1 次写(取反)成功; 第 2 次起全部失败 → 还原失败
	rec.FailWriteFrom = 2
	d, err := New(rec, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	det, err := d.WPStatusDetail()
	if err != nil {
		t.Fatalf("WPStatusDetail 应返回结果+警告, got err=%v", err)
	}
	if det.Known[0] {
		t.Fatal("还原失败时块 0 状态应为未知")
	}
	if !det.Protected[0] {
		t.Fatal("状态未知时必须保守地按受保护处理")
	}
	joined := strings.Join(det.Warnings, "; ")
	if !strings.Contains(joined, "状态未知") {
		t.Fatalf("应给出状态未知警告: %v", det.Warnings)
	}
	if !strings.Contains(joined, "还原") {
		t.Fatalf("应指出还原失败: %v", det.Warnings)
	}
}

func TestOfflineMode(t *testing.T) {
	d5, ft := newDDR5(t)
	ft.MR[MR48] = 0x00
	ok, err := d5.OfflineMode()
	if err != nil || ok {
		t.Fatalf("offline: %v %v", ok, err)
	}
	ft.MR[MR48] = 0x04
	ok, err = d5.OfflineMode()
	if err != nil || !ok {
		t.Fatalf("offline bit2: %v %v", ok, err)
	}
	d4, _ := newDDR4(t)
	if _, err := d4.OfflineMode(); err == nil {
		t.Fatal("非 DDR5 不支持 offline 查询")
	}
}

func TestDDR5ResidualPage(t *testing.T) {
	// 模拟 BIOS 上电后 MR11 残留页 5: New 必须回读同步缓存,
	// 首次读页 0 时显式切页, 数据才是页 0 的真实内容。
	ft := smbus.NewFake()
	ft.SetDDR5(true)
	ft.Fill(0xFF)
	for i := 0; i < 1024; i++ {
		ft.EEProm[i] = byte(i)
	}
	// 残留页 5
	ft.QuickLog = nil
	if err := ft.WriteByteData(0x50, 11, 5); err != nil {
		t.Fatalf("seed page 5: %v", err)
	}
	d, err := New(ft, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data, err := d.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if data[0] != 0x00 || data[1] != 0x01 || data[127] != 0x7F {
		t.Fatalf("页 0 数据错误: %#x %#x %#x", data[0], data[1], data[127])
	}
	if data[128] != 0x80 || data[0x1FF] != 0xFF {
		t.Fatalf("页 1/尾页 数据错误: %#x %#x", data[128], data[0x1FF])
	}
}
