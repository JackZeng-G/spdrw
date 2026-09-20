package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// newTestApp 构造注入了 Fake 控制器的 App(DDR4 + DDR5 两片)。
func newTestApp(t *testing.T) (*App, *smbus.FakeTransport) {
	t.Helper()
	f := smbus.NewFake()
	// DDR4: 8Gb×8? 用与 spd 测试一致的 8GiB 夹具再简化: 密度码4(4096Mb)×8芯片×2rank
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	d4[3] = 2
	d4[4] = 1<<6 | 2<<4 | 4
	d4[5] = (17-12)<<3 | (10 - 9)
	d4[12] = 1<<3 | 1
	d4[13] = 1<<3 | 3
	crc := spd.Crc16(d4[:126])
	d4[126], d4[127] = byte(crc), byte(crc>>8)
	copy(f.EEProm, d4)

	a := New()
	a.transports = []smbus.Transport{f}
	return a, f
}

func TestListConnectScanSelect(t *testing.T) {
	f := smbus.NewFake()
	f.Present = map[byte]bool{0x50: true, 0x51: true}
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	d4[3] = 2
	copy(f.EEProm, d4)
	a := New()
	a.transports = []smbus.Transport{f}

	ctl, err := a.ListControllers()
	if err != nil || len(ctl) != 1 {
		t.Fatalf("ListControllers: %v %+v", err, ctl)
	}
	if err := a.Connect(0); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	dimms, err := a.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(dimms) != 2 || dimms[0].Addr != 0x50 {
		t.Fatalf("Scan = %+v", dimms)
	}
	if dimms[0].Size != 512 {
		t.Fatalf("Size = %d", dimms[0].Size)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatalf("Select: %v", err)
	}
	dump, err := a.Dump()
	if err != nil || len(dump) != 512 {
		t.Fatalf("Dump: %v %d", err, len(dump))
	}
}

func TestDecodeDumpDDR4(t *testing.T) {
	a, _ := newTestApp(t)
	_ = a
	// 直接用 spd 测试同款夹具
	d := make([]byte, 512)
	d[2] = 0x0C
	d[3] = 2 // UDIMM
	d[4] = 1<<6 | 2<<4 | 4
	d[5] = (17-12)<<3 | (10 - 9)
	d[12] = 1<<3 | 1
	d[13] = 1<<3 | 3
	d[18] = 6
	d[20] = 0x00
	d[22] = 0xF0 // CL17-20 掩码低位在 byte20... LSB first: 0x0F<<10 → byte20=0x00,byte21=0x3C
	d[21] = 0x3C
	d[24] = 12
	d[25] = 12
	d[26] = 12
	d[320] = 0
	d[321] = 0x2C
	d[323] = 0x24
	d[324] = 0x15
	copy(d[329:349], []byte("TEST16GB-DDR4-3200  "))
	crc := spd.Crc16(d[:126])
	d[126], d[127] = byte(crc), byte(crc>>8)

	r, err := DecodeDump(d)
	if err != nil {
		t.Fatalf("DecodeDump: %v", err)
	}
	if r.RAMType != "DDR4" || r.ModuleType != "UDIMM" {
		t.Fatalf("type = %s/%s", r.RAMType, r.ModuleType)
	}
	if r.Manufacturer != "Micron Technology" {
		t.Fatalf("mfg = %q", r.Manufacturer)
	}
	if r.DateYear != 2024 || r.DateWeek != 15 {
		t.Fatalf("date = %d/%d", r.DateYear, r.DateWeek)
	}
	if r.PartNumber != "TEST16GB-DDR4-3200" {
		t.Fatalf("pn = %q", r.PartNumber)
	}
	if !r.CRCOK || !r.HasTimings {
		t.Fatalf("crc=%v timings=%v", r.CRCOK, r.HasTimings)
	}
	if r.TCK == nil || r.TCK.NS != 0.75 {
		t.Fatalf("tck = %+v", r.TCK)
	}
	if r.TotalMib != 8192 || r.TotalHuman != "8 GiB" {
		t.Fatalf("total = %d %s", r.TotalMib, r.TotalHuman)
	}
	if r.CasLat == "" || r.CasLat[0] != '1' {
		t.Fatalf("cas = %q", r.CasLat)
	}
}

func TestDecodeDumpDDR3(t *testing.T) {
	d := make([]byte, 256)
	d[2] = 0x0B
	d[3] = 2
	d[4] = 2 // 1Gb
	d[5] = (16-12)<<3 | (10 - 9)
	d[7] = 1<<3 | 1
	d[8] = 3
	d[10] = 1
	d[11] = 8
	d[12] = 12
	copy(d[128:146], []byte("DDR3-TEST-8GB      "))
	crc := spd.Crc16(d[:126])
	d[126], d[127] = byte(crc), byte(crc>>8)
	r, err := DecodeDump(d)
	if err != nil {
		t.Fatalf("DecodeDump: %v", err)
	}
	if r.RAMType != "DDR3" || r.TotalMib != 2048 {
		t.Fatalf("res = %+v", r)
	}
	if r.Basic == nil || r.Basic.TCKminNS != 1.5 {
		t.Fatalf("basic = %+v", r.Basic)
	}
}

// ddr4Fixture 构造一份 CRC 有效的 DDR4(512B) 内容。
func ddr4Fixture() []byte {
	d := make([]byte, 512)
	for i := range d {
		d[i] = 0x11
	}
	d[2] = 0x0C // DDR4
	d[3] = 0x02 // UDIMM
	d[325] = 0x01
	spd.FixCRC(d)
	return d
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dump.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newWriteTestApp(t *testing.T) (*App, *smbus.FakeTransport) {
	t.Helper()
	f := smbus.NewFake()
	copy(f.EEProm, ddr4Fixture())
	a := New()
	a.Emit = nil
	a.transports = []smbus.Transport{f}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	return a, f
}

// setDryRunForTest 直接开"设备级"干跑。
// 绑定层已不再暴露 App.SetDryRun(干跑改由 ack/参数显式传入), 所以测试自己拿设备开关。
func setDryRunForTest(t *testing.T, a *App, on bool) {
	t.Helper()
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev == nil {
		t.Fatal("测试需要先选择设备")
	}
	if err := dev.SetDryRun(on); err != nil {
		t.Fatalf("SetDryRun(%v): %v", on, err)
	}
}

func TestWriteFlowWithPreflight(t *testing.T) {
	a, f := newWriteTestApp(t)
	cur := ddr4Fixture()

	// 目标: 改序列号(第 3 段, 不参与 CRC) + 改 1 个主时序字节(第 1 段, 需重算 CRC)
	want := append([]byte{}, cur...)
	want[325] = 0xAB
	want[20] = 0x0F
	spd.FixCRC(want)
	path := writeTempFile(t, want)

	pf, err := a.preflightWrite(path, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	if pf.Blocked {
		t.Fatalf("不应被阻断: %s", pf.BlockReason)
	}
	if !pf.SizeOK || !pf.TargetCRCValid {
		t.Fatalf("预检基本项失败: %+v", pf)
	}
	// 变更 = 序列号 1 + 主时序 1 + 第 1 段 CRC 2
	if pf.ChangeCount != 4 {
		t.Fatalf("变更数 = %d: %+v", pf.ChangeCount, pf.Changes)
	}
	// 序列号区域被正确归类为低风险
	found := false
	for _, fl := range pf.Fields {
		if fl.Region == "序列号" {
			found = true
			if fl.Risk != "low" {
				t.Fatalf("序列号应为低风险: %+v", fl)
			}
		}
	}
	if !found {
		t.Fatalf("未归类出序列号区域: %+v", pf.Fields)
	}
	// CRC 变更排在最后
	if !pf.Changes[len(pf.Changes)-1].IsCRC {
		t.Fatalf("CRC 应最后写: %+v", pf.Changes)
	}

	// 确认串错误 → 拒绝
	if _, err := a.writeConfirmed(path, false, false, "yes"); err == nil {
		t.Fatal("确认串错误应被拒绝")
	}
	// 正确确认 → 写入
	res, err := a.writeConfirmed(path, false, false, "WRITE")
	if err != nil {
		t.Fatalf("WriteConfirmed: %v", err)
	}
	if !res.Verified || res.Written != 4 {
		t.Fatalf("写入结果: %+v", res)
	}
	if res.BackupPath == "" {
		t.Fatal("真实写入必须先生成备份")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	// 设备内容 = 目标
	got, _ := a.Dump()
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("内容不一致 @%#x: %#x != %#x", i, got[i], want[i])
		}
	}
	// 只写了差异字节(cmd 325 出现 1 次)
	n := 0
	for _, w := range f.WriteLog {
		if w.Cmd == 0x45 { // 偏移 325 = 0x145 → 页 1 的页内偏移 0x45
			n++
		}
	}
	if n != 1 {
		t.Fatalf("应只写序列号 1 次, got %d(总写 %d)", n, len(f.WriteLog))
	}
}

func TestWritePreflightBlocks(t *testing.T) {
	a, f := newWriteTestApp(t)
	cur := ddr4Fixture()

	// 1) 长度不符
	short := writeTempFile(t, make([]byte, 256))
	pf, err := a.preflightWrite(short, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	if !pf.Blocked || pf.SizeOK {
		t.Fatalf("长度不符必须阻断: %+v", pf)
	}
	if _, err := a.writeConfirmed(short, false, false, "WRITE"); err == nil {
		t.Fatal("长度不符必须拒绝写入")
	}

	// 2) 目标 CRC 不通过
	bad := append([]byte{}, cur...)
	bad[200] = 0x77 // 改动数据但不修 CRC
	badPath := writeTempFile(t, bad)
	pf, err = a.preflightWrite(badPath, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	if pf.TargetCRCValid || !pf.Blocked {
		t.Fatalf("CRC 不通过必须阻断: %+v", pf)
	}
	if !strings.Contains(pf.BlockReason, "CRC") {
		t.Fatalf("阻断原因应提到 CRC: %s", pf.BlockReason)
	}

	// 3) 受保护块包含变更 → 真实写入必须被拒绝
	//
	// 注意: 预览用的 PreflightWrite 不做写保护探测(DDR4 的探测要真写一个字节),
	// 所以它把保护状态报成"未知"; 真正的判定发生在写入路径上。
	f.ProtectedFrom = 0 // 全部块 NACK
	pf, err = a.preflightWrite(badPath, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	if len(pf.UnknownBlocks) == 0 {
		t.Fatalf("预览应把保护状态报成未知(不写测试): %+v", pf)
	}
	if _, err := a.writeConfirmed(badPath, false, false, "WRITE"); err == nil {
		t.Fatal("受保护块有变更时真实写入必须被拒绝")
	} else if !strings.Contains(err.Error(), "写保护") && !strings.Contains(err.Error(), "CRC") {
		t.Fatalf("拒绝原因应可解释: %v", err)
	}
	// 受保护块内容不得被改动
	if f.EEProm[200] == 0x77 {
		t.Fatal("受保护块被写入")
	}
}

func TestWritePreflightHighRiskFields(t *testing.T) {
	a, _ := newWriteTestApp(t)
	cur := ddr4Fixture()
	// 改 byte4(密度/封装)—— 高危字段
	want := append([]byte{}, cur...)
	want[4] = 0x21
	spd.FixCRC(want)
	path := writeTempFile(t, want)

	pf, err := a.preflightWrite(path, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	if pf.HighRiskCount == 0 {
		t.Fatalf("应识别出高危字节: %+v", pf.Fields)
	}
	// 高危不阻断(用户可能确实要改), 但必须有警告
	if pf.Blocked {
		t.Fatalf("高危字段不应直接阻断: %s", pf.BlockReason)
	}
	joined := strings.Join(pf.Warnings, "; ")
	if !strings.Contains(joined, "高危") {
		t.Fatalf("应有高危警告: %v", pf.Warnings)
	}
}

func TestWriteDryRunNoBusDataWrites(t *testing.T) {
	rec, f := smbus.NewRecordingFake()
	copy(f.EEProm, ddr4Fixture())
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{}, ddr4Fixture()...)
	want[325] = 0xCD
	spd.FixCRC(want)
	path := writeTempFile(t, want)

	pf, err := a.preflightWrite(path, false)
	if err != nil {
		t.Fatalf("PreflightWrite: %v", err)
	}
	setDryRunForTest(t, a, true)
	// 只统计干跑写入阶段(预检里的 DDR4 块首写测试本身会写字节, 不属干跑范围)
	rec.Reset()
	res, err := a.writeWithPreflight(pf, want, false, true, nil, "")
	if err != nil {
		t.Fatalf("干跑写入: %v", err)
	}
	if !res.DryRun || res.Written == 0 {
		t.Fatalf("干跑结果: %+v", res)
	}
	if res.BackupPath != "" {
		t.Fatal("干跑不应写备份文件")
	}
	if n := len(rec.DataWrites()); n != 0 {
		t.Fatalf("干跑不得产生数据写事务, got %d: %s", n, rec)
	}
	// 完整入口(含预检)同样不得写入目标字节
	rec.Reset()
	if _, err := a.writeConfirmed(path, false, true, "DRYRUN"); err != nil {
		t.Fatalf("WriteConfirmed(干跑): %v", err)
	}
	for _, w := range rec.DataWrites() {
		if w.Cmd == 0x45 || w.Cmd == 0x14 { // 0x145=序列号页内偏移, 0x14=byte20
			t.Fatalf("干跑写到了目标字节: %+v", w)
		}
	}
	// 干跑时真实写入必须被拒绝
	if _, err := a.writeConfirmed(path, false, false, "WRITE"); err == nil {
		t.Fatal("干跑模式下真实写入应被拒绝")
	}
	if err := func() error { setDryRunForTest(t, a, false); return nil }(); err != nil {
		t.Fatal(err)
	}
}

func TestWPSetClear(t *testing.T) {
	f := smbus.NewFake()
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	copy(f.EEProm, d4)
	a := New()
	a.transports = []smbus.Transport{f}
	_ = a.Connect(0)
	_ = a.Select(0x50)

	// 确认串不对必须拒绝(RSWP 在部分平台不可逆, 不能一句话就下发)
	if err := a.WPSet([]int{2}, "WRONG"); err == nil {
		t.Fatal("确认串不正确时应拒绝")
	}
	// 统一确认串 CLEAR 必须可用(旧写法 RSWP 仍兼容)
	if err := a.WPSet([]int{2}, "clear"); err != nil && !strings.Contains(err.Error(), "回读") {
		t.Fatalf("CLEAR 应被接受: %v", err)
	}
	// DDR4 RSWPSet(2) 应发出 SWP2 quick 命令(写 0x35)与 CWP(0x33);
	// 这个 Fake 的保护是"偏移 >=128 之后 NACK", 与 RSWP 命令无关 —— 因此回读复核
	// 必须如实报告"命令下发了但没生效"(审计 H3: 器件 ACK 但忽略命令的情况)
	werr := a.WPSet([]int{2}, "RSWP")
	if werr == nil || !strings.Contains(werr.Error(), "回读") {
		t.Fatalf("应报回读复核未生效: %v", werr)
	}
	found := false
	for _, q := range f.QuickLog {
		if q.Write && q.Addr == 0x35 {
			found = true
		}
	}
	if !found {
		t.Fatalf("QuickLog 中无 0x35: %+v", f.QuickLog)
	}
	// 保护状态由芯片侧 NACK 模拟: 页0 内 cmd>=128 NACK → 块1(0x80)与块3(页1 cmd 0x80)只读;
	// 块2 位于页1 cmd 0,不受影响 —— 验证状态检测确实按块区分。
	f.ProtectedFrom = 128
	st, err := a.WPStatus()
	if err != nil {
		t.Fatalf("WPStatus: %v", err)
	}
	want := []bool{false, true, false, true}
	for i, w := range want {
		if st.Protected[i] != w {
			t.Fatalf("blocks = %v, want %v", st.Protected, want)
		}
		if !st.Known[i] {
			t.Fatalf("块 %d 状态应确知: %v", i, st.Known)
		}
	}
	// 清除: Fake 里块 1/3 仍因 ProtectedFrom 而 NACK → 复核必须报"仍有块受保护"
	cerr := a.WPClear("CLEAR")
	if cerr == nil || !strings.Contains(cerr.Error(), "回读") {
		t.Fatalf("清除后复核应报仍有块受保护: %v", cerr)
	}
}

func TestConnectOOB(t *testing.T) {
	a := New()
	a.transports = []smbus.Transport{smbus.NewFake()}
	if err := a.Connect(5); err == nil {
		t.Fatal("越界应报错")
	}
	if _, err := a.Scan(); err == nil {
		t.Fatal("未连接就扫描应报错")
	}
	if _, err := a.Dump(); err == nil {
		t.Fatal("未选择设备应报错")
	}
}

// CloseDevice 与 App.Close 无害
func TestCloseDevice(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	a.closeDevice()
	if _, err := a.Dump(); err == nil {
		t.Fatal("CloseDevice 后 Dump 应报错")
	}
	a.Shutdown()
}

// ---------------- 编辑器服务层 ----------------

func TestEditorFlow(t *testing.T) {
	a, _ := newWriteTestApp(t)

	st, err := a.EditLoadFromDevice()
	if err != nil {
		t.Fatalf("EditLoadFromDevice: %v", err)
	}
	if st.Generation != "DDR4" || st.Size != 512 || !st.CanWrite || st.Dirty {
		t.Fatalf("初始状态: %+v", st)
	}
	if !st.CRCOK {
		t.Fatal("初始 CRC 应通过")
	}

	fields, err := a.EditFields()
	if err != nil || len(fields) < 10 {
		t.Fatalf("EditFields: %v len=%d", err, len(fields))
	}

	// 改部件号 + 序列号
	if _, err := a.EditSetField("partNumber", "EDITED-PN"); err != nil {
		t.Fatalf("EditSetField: %v", err)
	}
	if _, err := a.EditSetField("serial", "AABBCCDD"); err != nil {
		t.Fatalf("EditSetField(serial): %v", err)
	}
	st, _ = a.EditState()
	if !st.Dirty || st.ChangeCount == 0 {
		t.Fatalf("应有变更: %+v", st)
	}

	d, err := a.EditDiff()
	if err != nil {
		t.Fatalf("EditDiff: %v", err)
	}
	if d.ChangeCount == 0 || len(d.Changes) == 0 {
		t.Fatalf("diff 为空: %+v", d)
	}
	// 序列号在 bytes 325-328, 改 4 字节 → 变更数 >= 4
	if d.ChangeCount < 4 {
		t.Fatalf("变更数偏少: %d", d.ChangeCount)
	}
	for _, fl := range d.Fields {
		if fl.Region == "序列号" && fl.Count != 4 {
			t.Fatalf("序列号区域字节数应为 4: %+v", fl)
		}
	}

	// 原始 hex 编辑
	if _, err := a.EditSetByte(500, 0x5A); err != nil {
		t.Fatalf("EditSetByte: %v", err)
	}
	if _, err := a.EditSetByte(500, 300); err == nil {
		t.Fatal("越界字节值应被拒")
	}

	// 导出到文件
	dir := t.TempDir()
	out := filepath.Join(dir, "edited.bin")
	a.SaveDialog = func(title, def string) (string, error) { return out, nil }
	a.OpenDialog = func(title string) (string, error) { return out, nil }
	path, err := a.EditExportDialog()
	if err != nil {
		t.Fatalf("EditExportDialog: %v", err)
	}
	if path != out {
		t.Fatalf("导出路径: %s", path)
	}
	saved, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取导出文件: %v", err)
	}
	got, _ := a.EditBytes()
	for i := range saved {
		if saved[i] != got[i] {
			t.Fatalf("导出内容不一致 @%#x", i)
		}
	}

	// 放弃修改
	st, err = a.EditReset()
	if err != nil {
		t.Fatalf("EditReset: %v", err)
	}
	if st.Dirty || st.ChangeCount != 0 {
		t.Fatalf("Reset 后应无变更: %+v", st)
	}
}

func TestEditorApplyToDevice(t *testing.T) {
	a, f := newWriteTestApp(t)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	// 改序列号并重算 CRC(第 3 段不参与 CRC, 所以还要改一个 base 区字节)
	if _, err := a.EditSetField("serial", "11223344"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetField("ddr4.tAA", "15.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditFixCRC(); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.EditState(); !st.CRCOK {
		t.Fatalf("重算后 CRC 应通过: %+v", st)
	}

	// 确认串错误 → 拒绝
	if _, err := a.EditApplyToDevice(false, false, "nope"); err == nil {
		t.Fatal("确认串错误应被拒")
	}
	// 干跑: 不写总线数据
	res, err := a.EditApplyToDevice(false, true, "DRYRUN")
	if err != nil {
		t.Fatalf("干跑: %v", err)
	}
	if !res.DryRun || res.Written == 0 {
		t.Fatalf("干跑结果: %+v", res)
	}
	// 真实写入
	res, err = a.EditApplyToDevice(false, false, "WRITE")
	if err != nil {
		t.Fatalf("写入: %v", err)
	}
	if !res.Verified || res.BackupPath == "" {
		t.Fatalf("写入结果: %+v", res)
	}
	// 设备内容 = 编辑器内容
	cur, _ := a.Dump()
	want, _ := a.EditBytes()
	for i := range cur {
		if cur[i] != want[i] {
			t.Fatalf("设备内容未同步 @%#x", i)
		}
	}
	// 设备内容 CRC 通过
	ok, err := spd.CRCOK(cur)
	if err != nil || !ok {
		t.Fatalf("写入后 CRC 应通过: %v %v", ok, err)
	}
	_ = f
}

// 编辑器内容与设备世代不符(DDR4 文件 → DDR3 设备)必须在备份/写保护探测**之前**被拦
// (审计 M2: 旧实现先做真实写探测再在预检里报错, 探测本身有 8 次 NVM 写风险)。
func TestEditorApplyRejectsWrongGenerationBeforeBackup(t *testing.T) {
	a, _ := newWriteTestApp(t)
	// 设备换成 DDR3 256B, 但编辑器从 DDR4 文件载入
	f3 := smbus.NewFake()
	d3 := make([]byte, 256)
	d3[2] = 0x0B
	if _, err := spd.FixCRC(d3); err != nil {
		t.Fatal(err)
	}
	copy(f3.EEProm, d3)
	a.mu.Lock()
	a.transports = []smbus.Transport{f3}
	a.mu.Unlock()
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	path := writeTempFile(t, ddr4Fixture())
	if _, err := a.editLoadPath(path); err != nil {
		t.Fatal(err)
	}
	before := len(f3.WriteLog)
	if _, err := a.EditApplyToDevice(false, false, "WRITE"); err == nil {
		t.Fatal("世代不符必须被拒")
	}
	if len(f3.WriteLog) != before {
		t.Fatalf("被拒时不应有任何写事务(含备份前的写保护探测): %d 次写", len(f3.WriteLog)-before)
	}
}

func TestEditorBlocksWriteWhenFileStale(t *testing.T) {
	a, _ := newWriteTestApp(t)
	// 未载入编辑器就写
	if _, err := a.EditApplyToDevice(false, false, "WRITE"); err == nil {
		t.Fatal("未载入编辑器应拒绝")
	}
	// 从文件载入: 现在允许写回设备(克隆/修复场景), 但必须过预检门禁。
	// 这里文件与设备内容一致 → 无需写入(changes = 0), 走的是"无需写入"分支。
	dump := ddr4Fixture()
	path := writeTempFile(t, dump)
	if _, err := a.editLoadPath(path); err != nil {
		t.Fatal(err)
	}
	res, err := a.EditApplyToDevice(false, false, "WRITE")
	if err != nil {
		t.Fatalf("内容与设备一致时应返回“无需写入”而不是报错: %v", err)
	}
	if res.Written != 0 {
		t.Fatalf("内容一致时不应写任何字节: %+v", res)
	}
	// 换一份**跨世代**的文件: 必须被预检门禁挡住(这是允许文件写入后的安全网)
	other := spd.DDR3
	_ = other
	d3 := make([]byte, 256)
	d3[2] = 0x0B // DDR3
	if _, err := spd.FixCRC(d3); err != nil {
		t.Fatal(err)
	}
	d3path := writeTempFile(t, d3)
	if _, err := a.editLoadPath(d3path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditApplyToDevice(false, false, "WRITE"); err == nil {
		t.Fatal("跨世代(长度也不同)的文件必须被预检挡住")
	}
	// 换设备后编辑器失效
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditState(); err == nil {
		t.Fatal("重新 Select 后编辑器应失效")
	}
}

// TestEditExportDefaultName 从文件载入编辑器时, "另存为"的默认文件名必须是干净的
// 文件名 —— 早先用字符串拼接会把整条路径塞进去(Windows 上会被当成子路径)。
func TestEditExportDefaultName(t *testing.T) {
	a, _ := newWriteTestApp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "my-dump.bin")
	if err := os.WriteFile(src, ddr4Fixture(), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotName string
	a.SaveDialog = func(title, def string) (string, error) {
		gotName = def
		return filepath.Join(dir, "out.bin"), nil
	}
	a.OpenDialog = func(string) (string, error) { return src, nil }
	if _, err := a.editLoadPath(src); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditExportDialog(); err != nil {
		t.Fatalf("EditExportDialog: %v", err)
	}
	if gotName != "my-dump-edited.bin" {
		t.Fatalf("默认文件名 = %q, 期望 my-dump-edited.bin", gotName)
	}
	if strings.ContainsAny(gotName, `/\`) {
		t.Fatalf("默认文件名不应含路径分隔符: %q", gotName)
	}
	// 从设备载入时沿用原来的命名
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditExportDialog(); err != nil {
		t.Fatal(err)
	}
	if gotName != "spd-edited-512.bin" {
		t.Fatalf("设备来源的默认文件名 = %q", gotName)
	}
}
