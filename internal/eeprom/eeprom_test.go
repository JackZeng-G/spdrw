package eeprom

import (
	"errors"
	"strings"
	"testing"

	"spdrw/internal/smbus"
	"spdrw/internal/spd"
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
	// 长度不足也必须报错(旧实现 dump[:size] 会切片越界 panic)
	if err := d.Write(make([]byte, 256), false, nil); err == nil {
		t.Fatal("长度不足应报错(不能截断/补齐)")
	}
	if err := d.Write(nil, false, nil); err == nil {
		t.Fatal("空数据应报错")
	}
	// 模拟页 0 受保护(前 128 字节 NACK)
	ft.ProtectedFrom = 0
	dump := make([]byte, 512)
	dump[0] = 0x77
	err := d.Write(dump, false, nil)
	if err == nil || !strings.Contains(err.Error(), "0x000") {
		t.Fatalf("受保护写入应报错并带偏移, got %v", err)
	}
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("应返回 WriteError(带已写/未写计数), got %T", err)
	}
	if we.Written != 0 || we.Total == 0 {
		t.Fatalf("WriteError 计数不对: %+v", we)
	}
	if !strings.Contains(we.Error(), "未写") {
		t.Fatalf("错误应说明未写字节数: %v", we)
	}
}

func TestPlanWriteCRCLastAndDiffOnly(t *testing.T) {
	// CRC 字节必须排在计划最后: 写中断时留下的是"CRC 与数据不符"的 SPD
	d, ft := newDDR4(t)
	cur := make([]byte, 512)
	for i := range cur {
		cur[i] = 0x11
	}
	copy(ft.EEProm, cur)
	target := make([]byte, 512)
	copy(target, cur)
	target[325] = 0xAB // 序列号
	crc := spd.Crc16(target[:126])
	target[126], target[127] = byte(crc), byte(crc>>8)

	changes, err := d.PlanWrite(target, false)
	if err != nil {
		t.Fatalf("PlanWrite: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("应只有 3 个变更(数据 1 + CRC 2), got %d: %+v", len(changes), changes)
	}
	last := changes[len(changes)-1]
	if !last.IsCRC {
		t.Fatalf("最后一个变更必须是 CRC 字节: %+v", changes)
	}
	// 所有 CRC 变更必须排在所有非 CRC 变更之后
	seenCRC := false
	for i, c := range changes {
		if c.IsCRC {
			seenCRC = true
			continue
		}
		if seenCRC {
			t.Fatalf("第 %d 个非 CRC 变更出现在 CRC 之后: %+v", i, changes)
		}
	}
	// 计划本身不写任何字节
	if len(ft.WriteLog) != 0 {
		t.Fatalf("PlanWrite 不应写字节: %+v", ft.WriteLog)
	}
	// 执行后内容与目标一致
	if err := d.ApplyWrite(target, changes, nil); err != nil {
		t.Fatalf("ApplyWrite: %v", err)
	}
	got, _ := d.ReadAll()
	for i := range got {
		if got[i] != target[i] {
			t.Fatalf("写入后不一致 @%#x: %#x != %#x", i, got[i], target[i])
		}
	}
}

func TestApplyWriteAbortsWithCounts(t *testing.T) {
	// 第 3 次写起持续失败(模拟总线故障/写保护): 必须报"已写/未写"并中止
	rec, ft := smbus.NewRecordingFake()
	ft.EEProm[2] = 0x0C
	ft.Fill(0x11)
	rec.FailWriteFrom = 3
	d, err := New(rec, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	target := make([]byte, 512)
	for i := range target {
		target[i] = 0x11
	}
	target[0x10], target[0x20], target[0x30], target[0x40] = 0xAA, 0xBB, 0xCC, 0xDD
	changes, err := d.PlanWrite(target, false)
	if err != nil {
		t.Fatalf("PlanWrite: %v", err)
	}
	if len(changes) < 4 {
		t.Fatalf("计划过短: %d", len(changes))
	}
	err = d.ApplyWrite(target, changes, nil)
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("应返回 WriteError, got %v", err)
	}
	if we.Written != 2 {
		t.Fatalf("应已写 2 字节, got %d", we.Written)
	}
	if we.Offset != changes[2].Offset {
		t.Fatalf("中止偏移应为 %#x, got %#x", changes[2].Offset, we.Offset)
	}
	if !strings.Contains(err.Error(), "已写 2") {
		t.Fatalf("错误应带已写计数: %v", err)
	}
}

func TestDryRunWritesNothingToBus(t *testing.T) {
	rec, ft := smbus.NewRecordingFake()
	ft.EEProm[2] = 0x0C
	ft.Fill(0x11)
	d, err := New(rec, 0x50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.SetDryRun(true); err != nil {
		t.Fatalf("SetDryRun: %v", err)
	}
	target := make([]byte, 512)
	for i := range target {
		target[i] = 0x22
	}
	rec.Reset() // 只统计干跑写入阶段
	changes, err := d.PlanWrite(target, true)
	if err != nil {
		t.Fatalf("PlanWrite: %v", err)
	}
	if err := d.ApplyWrite(target, changes, nil); err != nil {
		t.Fatalf("ApplyWrite(干跑): %v", err)
	}
	if n := len(rec.DataWrites()); n != 0 {
		t.Fatalf("干跑模式不得产生数据写事务, got %d: %s", n, rec)
	}
	// 影子反映目标内容
	img := d.ShadowImage()
	if len(img) != 512 || img[0] != 0x22 {
		t.Fatalf("影子镜像不对: len=%d", len(img))
	}
	// 设备真实内容未变
	real, _ := ft.ReadByteData(0x50, 0x00)
	if real != 0x11 {
		t.Fatalf("干跑不得改动真实内容, got %#x", real)
	}
	// 关闭干跑后真实写入生效
	if err := d.SetDryRun(false); err != nil {
		t.Fatalf("SetDryRun(false): %v", err)
	}
	if err := d.Write(target, true, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !ft.Closed && ft.EEProm[0] != 0x22 {
		t.Fatalf("真实写入未生效: %#x", ft.EEProm[0])
	}
}

func TestCRCOffsetsTargets(t *testing.T) {
	d4, _ := newDDR4(t)
	dump := make([]byte, 512)
	for i := range dump {
		dump[i] = 0x11
	}
	got := d4.CRCOffsets(dump)
	if len(got) != 4 {
		t.Fatalf("DDR4 应有 4 个 CRC 字节, got %v", got)
	}
	// 空白(全 0xFF)扩展区不产生 CRC 目标
	d5 := &Device{size: 1024, ddr5: true}
	blank := make([]byte, 1024)
	for i := range blank {
		blank[i] = 0xFF
	}
	if got := d5.CRCOffsets(blank); len(got) != 2 {
		t.Fatalf("空白 DDR5 只应有基础段 2 个 CRC, got %v", got)
	}
	// 有 XMP header 时补 header CRC
	blank[0x280], blank[0x281] = 0x0C, 0x4A
	got = d5.CRCOffsets(blank)
	if len(got) != 4 {
		t.Fatalf("DDR5 + XMP header 应有 4 个 CRC, got %v", got)
	}
	found := false
	for _, o := range got {
		if o == 0x2BE {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含 XMP header CRC 0x2BE: %v", got)
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
	// 未保护: 设备 ACK → false; 已永久保护: NACK → true。仅 DDR3 适用。
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

// newPatternDDR4/DDR5 造一份每字节都不同的镜像(能暴露分页/块边界错位)。
func newPatternDDR4(t *testing.T) (*Device, *smbus.FakeTransport, []byte) {
	t.Helper()
	ft := smbus.NewFake()
	img := make([]byte, 512)
	for i := range img {
		img[i] = byte(i)
	}
	img[2] = 0x0C
	copy(ft.EEProm, img)
	d, err := New(ft, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	return d, ft, img
}

func newPatternDDR5(t *testing.T) (*Device, *smbus.FakeTransport, []byte) {
	t.Helper()
	ft := smbus.NewFake()
	ft.SetDDR5(true)
	img := make([]byte, 1024)
	for i := range img {
		img[i] = byte(i * 7)
	}
	img[0], img[1], img[2] = 0x30, 0x10, 0x12
	copy(ft.EEProm, img)
	d, err := New(ft, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	return d, ft, img
}

// TestBlockReadEqualsByteRead 块读路径必须与逐字节读给出完全一样的字节
// (含跨页边界: DDR4 每 256B 一页, DDR5 每 128B 一页, 块读不得跨页)。
func TestBlockReadEqualsByteRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(*testing.T) (*Device, *smbus.FakeTransport, []byte)
	}{
		{"DDR4", newPatternDDR4},
		{"DDR5", newPatternDDR5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, img := tc.mk(t)
			d.SetFastRead(true)
			fast, err := d.ReadAll()
			if err != nil {
				t.Fatalf("块读 ReadAll: %v", err)
			}
			st := d.ReadStats()
			if !st.BlockReadKnown || !st.BlockReadOK {
				t.Fatalf("块读应被探测为可用: %+v", st)
			}
			if st.Transactions > len(img)/32+4 {
				t.Fatalf("块读事务数应约等于 %d, got %d", len(img)/32, st.Transactions)
			}
			// 逐字节路径作为参照
			d2, _, _ := tc.mk(t)
			d2.SetFastRead(false)
			slow, err := d2.ReadAll()
			if err != nil {
				t.Fatalf("逐字节 ReadAll: %v", err)
			}
			if len(fast) != len(slow) {
				t.Fatalf("长度不同: %d vs %d", len(fast), len(slow))
			}
			for i := range fast {
				if fast[i] != slow[i] || fast[i] != img[i] {
					t.Fatalf("块读与逐字节不一致 @%#x: %02X vs %02X(镜像 %02X)", i, fast[i], slow[i], img[i])
				}
			}
			// 边界附近单独读一遍(页尾/页首)
			for _, off := range []uint16{0, 31, 32, 96, 127, 128, 255, 256, 257, uint16(len(img) - 1)} {
				got, err := d.Read(off, 1)
				if err != nil {
					t.Fatalf("Read(%d): %v", off, err)
				}
				if got[0] != img[off] {
					t.Fatalf("偏移 %d: %02X != %02X", off, got[0], img[off])
				}
			}
		})
	}
}

// TestReadFallbackChain 读路径是"块读(32B) → 字读(2B) → 逐字节(1B)"三级回退:
// 设备支持哪一级就用哪一级, 数据必须始终正确。
func TestReadFallbackChain(t *testing.T) {
	// 1) 只不支持块读 → 应落到字读
	d, ft, img := newPatternDDR5(t)
	ft.BlockReadUnsupported = true
	d.SetFastRead(true)
	got, err := d.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	for i := range got {
		if got[i] != img[i] {
			t.Fatalf("数据错 @%#x", i)
		}
	}
	st := d.ReadStats()
	if st.BlockReadOK || !st.BlockReadKnown {
		t.Fatalf("应记住块读不可用: %+v", st)
	}
	if !st.WordReadOK || st.WordBytes != len(img) {
		t.Fatalf("应回退到字读并读满整片: %+v", st)
	}
	if st.Transactions > len(img)/2+8 {
		t.Fatalf("字读事务数应约等于 %d, got %d", len(img)/2, st.Transactions)
	}

	// 2) 块读与字读都不支持 → 逐字节
	d2, ft2, img2 := newPatternDDR5(t)
	ft2.BlockReadUnsupported = true
	ft2.WordReadUnsupported = true
	d2.SetFastRead(true)
	got2, err := d2.ReadAll()
	if err != nil {
		t.Fatalf("逐字节 ReadAll: %v", err)
	}
	for i := range got2 {
		if got2[i] != img2[i] {
			t.Fatalf("逐字节数据错 @%#x", i)
		}
	}
	st2 := d2.ReadStats()
	if st2.WordReadOK {
		t.Fatalf("应记住字读不可用: %+v", st2)
	}
	if st2.FallbackBytes != len(img2) || st2.Transactions != len(img2) {
		t.Fatalf("逐字节回退应读满 %d 字节且事务数相同: %+v", len(img2), st2)
	}
	if st2.Mode == "" || st2.ElapsedMS < 0 {
		t.Fatalf("应给出读取方式与耗时: %+v", st2)
	}
}

// 写测试的判定依据必须用最原始的逐字节读: 若走块读/字读, 读路径一旦有缓存/偏差,
// "还原失败"会被误判成"还原成功" —— 那是静默数据损坏(块首字节被永久取反)。
func TestWriteTestUsesPrimitiveReadsForVerdict(t *testing.T) {
	f := smbus.NewFake()
	copy(f.EEProm, ddr4FixtureForTest())
	d, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	// 块读路径全部返回"取反前的原值"(模拟缓存/偏差), 逐字节读是真值
	orig := append([]byte{}, f.EEProm...)
	f.BlockReadOverride = orig
	ok, err := d.WriteTest(0)
	if err != nil {
		t.Fatalf("WriteTest: %v", err)
	}
	_ = ok
	// 关键: 设备内容必须原样(取反写已被还原)
	for i := range orig {
		if f.EEProm[i] != orig[i] {
			t.Fatalf("写测试后设备内容被改动 @%#x: %02X != %02X", i, f.EEProm[i], orig[i])
		}
	}
}

func ddr4FixtureForTest() []byte {
	d := make([]byte, 512)
	for i := range d {
		d[i] = byte(i)
	}
	d[2] = 0x0C
	return d
}

// 256 字节(DDR3/SDRAM)是单页器件: 首次访问绝不能写 SPA 地址 0x36 ——
// 0x36 属于 SWP/PSWP 的设备类型地址空间, 真实主板上通常无人应答(读整片直接失败),
// 应答的那一根(SA=6 → 0x56)还可能被误写 PSWP。审计发现的阻断项。
func TestSinglePageDeviceNeverPageSwitches(t *testing.T) {
	f := smbus.NewFake()
	d := make([]byte, 256)
	for i := range d {
		d[i] = byte(i)
	}
	d[2] = 0x0B // DDR3
	copy(f.EEProm, d)
	dev, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dev.ReadAll(); err != nil {
		t.Fatalf("单页器件应能直接读取: %v", err)
	}
	for _, q := range f.QuickLog {
		if q.Write {
			t.Fatalf("单页器件不应发任何 Quick 写(页选择会打到 0x%02X)", q.Addr)
		}
	}
	for _, w := range f.WriteLog {
		t.Fatalf("单页器件不应发任何写事务: cmd=%#x val=%#x", w.Cmd, w.Val)
	}
}

// 真实主板常见情况: 0x36/0x37 无人应答 → 单页器件读取仍必须成功。
func TestSinglePageReadWorksWhenSPAUnpopulated(t *testing.T) {
	f := smbus.NewFake()
	d := make([]byte, 256)
	for i := range d {
		d[i] = byte(i * 3)
	}
	d[2] = 0x0B
	copy(f.EEProm, d)
	f.Present = map[byte]bool{0x50: true} // 只有 0x50 存在, 0x36/0x37 一律 NACK
	dev, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dev.ReadAll()
	if err != nil {
		t.Fatalf("0x36/0x37 无人应答时单页器件仍应可读: %v", err)
	}
	for i := range d {
		if got[i] != d[i] {
			t.Fatalf("@%#x = %#x, want %#x", i, got[i], d[i])
		}
	}
}

// 干跑模式下 RSWP 加保护/清除必须被拒绝(写保护是设备状态, 部分颗粒置位后很难清)。
func TestRSWPBlockedInDryRun(t *testing.T) {
	for _, ddr5 := range []bool{false, true} {
		f := smbus.NewFake()
		if ddr5 {
			f.SetDDR5(true)
			copy(f.EEProm, make([]byte, 1024))
		} else {
			d4 := make([]byte, 512)
			d4[2] = 0x0C
			copy(f.EEProm, d4)
		}
		dev, err := New(f, 0x50)
		if err != nil {
			t.Fatal(err)
		}
		if err := dev.SetDryRun(true); err != nil {
			t.Fatal(err)
		}
		before := len(f.WriteLog)
		if err := dev.RSWPSet(0); err == nil {
			t.Fatalf("ddr5=%v: 干跑下 RSWPSet 必须被拒绝", ddr5)
		}
		if err := dev.RSWPClear(); err == nil {
			t.Fatalf("ddr5=%v: 干跑下 RSWPClear 必须被拒绝", ddr5)
		}
		if len(f.WriteLog) != before {
			t.Fatalf("ddr5=%v: 干跑下不应下发任何写事务", ddr5)
		}
	}
}

// 写入计划里的偏移必须先校验再转 uint16: 0x10010 截成 0x10 会写到错误位置还报成功
// (审计复现的"最坏失败形态"), 越界必须直接报错且一个字节都不写。
func TestApplyWriteRejectsOutOfRangeOffset(t *testing.T) {
	f := smbus.NewFake()
	copy(f.EEProm, ddr4FixtureForTest())
	d, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	target := append([]byte{}, f.EEProm...)
	target[20] ^= 0x0F
	before := append([]byte{}, f.EEProm...)
	changes := []ByteChange{{Offset: 0x10010, New: 0x99}}
	err = d.ApplyWrite(target, changes, nil)
	if err == nil {
		t.Fatal("越界偏移必须报错")
	}
	for i := range before {
		if f.EEProm[i] != before[i] {
			t.Fatalf("越界计划不应写任何字节(改了 @%#x)", i)
		}
	}
}

// 逐字节复核同样要先校验偏移(导出的 VerifyChangedByteWise 传错 plan 不能 panic)。
func TestVerifyChangedByteWiseRejectsOutOfRange(t *testing.T) {
	f := smbus.NewFake()
	copy(f.EEProm, ddr4FixtureForTest())
	d, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	dump := append([]byte{}, f.EEProm...)
	if err := d.VerifyChangedByteWise(dump, []ByteChange{{Offset: 0x10005}}); err == nil {
		t.Fatal("越界偏移应报错而不是 panic")
	}
	if err := d.VerifyChangedByteWise(dump, []ByteChange{{Offset: -1}}); err == nil {
		t.Fatal("负偏移应报错")
	}
}

// MR11 读不出来时不许盲写(盲写会先清掉 bit7:3 再报错)。
func TestSetPageDoesNotBlindWriteMR11(t *testing.T) {
	f := smbus.NewFake()
	f.SetDDR5(true)
	f.FailReads = false
	copy(f.EEProm, make([]byte, 1024))
	d, err := New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	// 让所有 MR11 读失败(仅 cmd=11 的读), 写照常: 这时切页必须放弃而不是盲写
	f.MRFailRead = map[byte]bool{11: true}
	if err := d.setPage(2); err == nil {
		t.Fatal("MR11 读失败时应放弃切页并报错")
	}
	for _, w := range f.WriteLog {
		if w.Cmd == 11 {
			t.Fatalf("不应盲写 MR11(实际写入 %#x)", w.Val)
		}
	}
}
