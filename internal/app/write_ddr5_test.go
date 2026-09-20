package app

import (
	"errors"
	"strings"
	"testing"

	"spdrw/internal/eeprom"
	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// DDR5 写入路径的端到端测试。DDR5 与 DDR4 的关键差别是**分页靠 MR11 写**:
// 每一页(128B)都要先写 MR11 再访问 NVM(cmd bit7=1), 写保护则是 16×64B 的 MR12/MR13 位图。
// 真机写入前必须先把这条路径在 Fake 上跑通。

// ddr5WriteFixture 造一份 CRC 有效的 DDR5 镜像(含 XMP3 header + Profile 1)。
func ddr5WriteFixture() []byte {
	d := make([]byte, 1024)
	d[0], d[1], d[2], d[3] = 0x30, 0x10, 0x12, 0x03 // 512B 用/1024B, rev1.2, DDR5, SO-DIMM
	d[4] = 2 << 4                                   // die 码 2, 密度码 2
	d[6] = 1 << 5                                   // x8
	d[7] = 1<<7 | 2<<2
	d[234] = 0
	d[235] = 1<<5 | 0<<0 // 2 通道
	// 身份区
	d[512], d[513] = 0x80, 0xCE // continuation(含校验位) + 厂商码
	d[514] = 0x01
	d[515], d[516] = 0x25, 0x30
	copy(d[517:521], []byte{1, 2, 3, 4})
	copy(d[521:551], []byte("DDR5-WRITE-TEST"))
	// XMP 3.0 header + Profile 1(槽首字节 = VPP 编码, 必须 ≥ 0x20)
	d[0x280], d[0x281], d[0x282], d[0x283] = 0x0C, 0x4A, 0x30, 0x01
	copy(d[0x28E:0x29E], []byte("Write Test P1   "))
	// 槽内布局: +0 VPP, +1 VDD, +2 VDDQ, +4 VMEMCTRL, +5/+6 minCycleTime,
	// +7..+11 CL 掩码, +13/+14 tAA, +0x3C commandRate
	d[0x2C0] = 0x30                 // VPP = 1.80V
	d[0x2C1] = 0x22                 // VDD = 1.10V
	d[0x2C2] = 0x22                 // VDDQ
	d[0x2C5], d[0x2C6] = 0x38, 0x01 // minCycleTime = 0x0138 = 312ps
	d[0x2C8] = 0x02                 // CL 掩码: CL38(bit9 → 字节1 bit1)
	d[0x2CD], d[0x2CE] = 0x6E, 0x31 // tAA = 0x316E
	d[0x2FC] = 0x02                 // Command Rate 2N
	if _, err := spd.FixCRC(d); err != nil {
		panic(err)
	}
	return d
}

func newDDR5WriteApp(t *testing.T) (*App, *smbus.RecordingTransport, *smbus.FakeTransport) {
	t.Helper()
	ft := smbus.NewFake()
	ft.SetDDR5(true)
	copy(ft.EEProm, ddr5WriteFixture())
	rec := smbus.NewRecording(ft)
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	return a, rec, ft
}

func TestDDR5EditorWriteEndToEnd(t *testing.T) {
	a, rec, ft := newDDR5WriteApp(t)

	st, err := a.EditLoadFromDevice()
	if err != nil {
		t.Fatalf("EditLoadFromDevice: %v", err)
	}
	if st.Generation != "DDR5" || st.Size != 1024 || !st.CRCOK {
		t.Fatalf("载入状态: %+v", st)
	}

	// 改 XMP3 Profile 1 的时序与电压(槽内 CRC 需要重算)
	if _, err := a.EditSetField("xmp3.p1.tCK", "0.400"); err != nil {
		t.Fatalf("改 tCK: %v", err)
	}
	if _, err := a.EditSetField("xmp3.p1.vdd", "1.25"); err != nil {
		t.Fatalf("改 VDD: %v", err)
	}
	if _, err := a.EditSetField("serial", "0A0B0C0D"); err != nil {
		t.Fatalf("改序列号: %v", err)
	}
	if _, err := a.EditFixCRC(); err != nil {
		t.Fatalf("FixCRC: %v", err)
	}
	st, _ = a.EditState()
	if !st.CRCOK || !st.Dirty {
		t.Fatalf("重算后状态: %+v", st)
	}

	// 干跑: 零数据写
	rec.Reset()
	res, err := a.EditApplyToDevice(false, true, "DRYRUN")
	if err != nil {
		t.Fatalf("干跑: %v", err)
	}
	if res.Written == 0 {
		t.Fatal("干跑应报告将要写入的字节数")
	}
	// 干跑不得有 NVM 写入; MR 切页写(cmd bit7=0)是读流程的一部分, 不算数据写
	if n := len(rec.NVMWrites()); n != 0 {
		t.Fatalf("干跑不得产生 NVM 写事务, got %d: %s", n, rec)
	}

	// 真实写入
	rec.Reset()
	res, err = a.EditApplyToDevice(false, false, "WRITE")
	if err != nil {
		t.Fatalf("写入: %v", err)
	}
	if !res.Verified || res.BackupPath == "" {
		t.Fatalf("写入结果: %+v", res)
	}
	// 关键: DDR5 必须通过写 MR11 切页, 且 NVM 访问走 cmd bit7=1
	mr11 := rec.WritesAtCmd(11)
	if len(mr11) == 0 {
		t.Fatalf("DDR5 写入必须写 MR11 切页: %s", rec)
	}
	sawNVMWrite := false
	for _, w := range rec.DataWrites() {
		if w.Kind == smbus.OpWriteByteData && w.Cmd&0x80 != 0 {
			sawNVMWrite = true
		}
	}
	if !sawNVMWrite {
		t.Fatalf("NVM 写必须带 bit7=1(NVM 位): %s", rec)
	}
	// 设备内容 = 编辑器内容, 且整片 CRC 通过
	cur, err := a.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	want, _ := a.EditBytes()
	for i := range cur {
		if cur[i] != want[i] {
			t.Fatalf("设备与编辑器不一致 @%#x: %02X != %02X", i, cur[i], want[i])
		}
	}
	if ok, cerr := spd.CRCOK(cur); cerr != nil || !ok {
		t.Fatalf("写入后整片 CRC 应通过: %v %v", ok, cerr)
	}
	// 校验设备侧槽内容确实变了(400ps = 0x0190)
	if ft.EEProm[0x2C5] != 0x90 || ft.EEProm[0x2C6] != 0x01 {
		t.Fatalf("tCK 未写入: %02X %02X", ft.EEProm[0x2C5], ft.EEProm[0x2C6])
	}
	if ft.EEProm[0x2C1] != 0x25 { // 1.25V = 1<<5|5
		t.Fatalf("VDD 未写入: %02X", ft.EEProm[0x2C1])
	}
}

// TestWriteAbortKeepsCRCStale 锁死一条安全性质: 中止时 CRC 字节不会被写脏。
// 计划把 CRC 排在最后, 所以"数据已写、CRC 未写"的中断状态会让 BIOS 拒绝该条,
// 而不是接受一份"校验通过但内容错"的 SPD。
func TestWriteAbortKeepsCRCStale(t *testing.T) {
	a, rec, ft := newDDR5WriteApp(t)
	orig := append([]byte{}, ft.EEProm...)

	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetField("xmp3.p1.tCK", "0.500"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditFixCRC(); err != nil {
		t.Fatal(err)
	}
	target, _ := a.EditBytes()

	// 预检(内含写保护探测)先做掉, 再单独测写入阶段
	pf, err := a.buildPreflight("编辑器内容", target, false)
	if err != nil {
		t.Fatalf("buildPreflight: %v", err)
	}
	if pf.Blocked {
		t.Skipf("预检已阻断(%s), 本用例需要未被写保护的镜像", pf.BlockReason)
	}
	nonCRC := 0
	for _, c := range pf.Changes {
		if !c.IsCRC {
			nonCRC++
		}
	}
	if nonCRC == 0 {
		t.Fatal("应有数据字节变更")
	}

	rec.Reset()
	rec.FailWriteFrom = nonCRC + 1 // 数据写完, 第一个 CRC 字节开始失败
	// 只对 NVM 写计数/注入: 切页 MR11 写不算数据写
	rec.FailWriteCmdFilter = func(cmd byte) bool { return cmd&0x80 != 0 }
	res, werr := a.writeWithPreflight(pf, target, false, false)
	if werr == nil {
		t.Fatal("注入故障后写入应失败")
	}
	var we *eeprom.WriteError
	if !errors.As(werr, &we) {
		t.Fatalf("应返回 WriteError, got %T: %v", werr, werr)
	}
	if we.Written != nonCRC {
		t.Fatalf("应已写 %d 个数据字节, got %d", nonCRC, we.Written)
	}
	if !strings.Contains(werr.Error(), "已写") || !strings.Contains(werr.Error(), "未写") {
		t.Fatalf("错误应说明已写/未写: %v", werr)
	}
	if res == nil || res.BackupPath == "" {
		t.Fatal("真实写入前必须先备份(即使随后失败)")
	}

	// 数据字节已改, 但 CRC 区域仍是旧值 → 整片 CRC 必须不通过
	for _, off := range spd.CRCOffsets(target) {
		if ft.EEProm[off] != orig[off] {
			t.Fatalf("CRC 字节 %#x 被写脏了(%02X → %02X), 中断后应保持旧值",
				off, orig[off], ft.EEProm[off])
		}
	}
	if ok, _ := spd.CRCOK(ft.EEProm[:1024]); ok {
		t.Fatal("半写状态不应通过 CRC")
	}
}

// TestDDR5ProtectedBlockBlocksWrite DDR5 的 MR12/MR13 位图必须能拦住写入。
func TestDDR5ProtectedBlockBlocksWrite(t *testing.T) {
	a, _, ft := newDDR5WriteApp(t)
	// 保护 XMP3 Profile 1 所在的块(0x2C0 = 704 → 64B 块 11)
	// 块 0-7 = MR12 位, 块 8-15 = MR13 位 → 块 11 = MR13 bit3
	ft.MR[eeprom.MR13] = 1 << 3
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetField("xmp3.p1.tCK", "0.416"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditFixCRC(); err != nil {
		t.Fatal(err)
	}
	// 干跑允许在"受写保护"时照样演算(不碰硬件), 但要带上阻断原因
	res, err := a.EditApplyToDevice(false, true, "DRYRUN")
	if err != nil {
		t.Fatalf("干跑应能给出计划: %v", err)
	}
	if !res.DryRun || res.Written == 0 {
		t.Fatalf("干跑结果: %+v", res)
	}
	if !strings.Contains(res.Message, "写保护") {
		t.Fatalf("干跑消息应提示写保护: %q", res.Message)
	}
	// 真实写入必须被预检拦住(变更块落在受保护块里)
	if _, err := a.EditApplyToDevice(false, false, "WRITE"); err == nil {
		t.Fatal("变更落在受写保护的块上, 必须拒绝")
	} else if !strings.Contains(err.Error(), "写保护") {
		t.Fatalf("拒绝原因应提到写保护: %v", err)
	}
	// 受保护块内容不得被改动
	if ft.EEProm[0x2C5] != 0x38 || ft.EEProm[0x2C6] != 0x01 {
		t.Fatalf("受保护块被改写: %02X %02X", ft.EEProm[0x2C5], ft.EEProm[0x2C6])
	}
}
