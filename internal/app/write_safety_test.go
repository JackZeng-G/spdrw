package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spdrw/internal/eeprom"
	"spdrw/internal/smbus"
	"spdrw/internal/spd"
)

// 本文件的用例全部来自一次针对写入路径的对抗性审计(BLOCKER B1 + HIGH H2/H3 + MEDIUM
// M4/M5/M7/M8 + LOW L10/L11)。每一条都对应真机上一种"会把条写坏 / 让用户误以为没事"
// 的场景, 修完必须有用例钉住, 否则下次重构又会退回去。

// TestBackupHappensBeforeProbeWrite —— B1(阻断项)。
//
// DDR4 及更早世代的"写保护探测"本身就是真实 NVM 写(块首取反写→还原)。旧顺序是
// "先探测、后备份", 于是还原写失败时: byte0 被永久改坏、备份里存的也是坏值、
// 回滚拿坏值当"原始内容"比对自己, 还报告"无需回滚"。
//
// 现在的要求: 备份必须早于任何探测; 探测后整片复核, 一旦不一致立即回滚并中止写入。
func TestBackupHappensBeforeProbeWrite(t *testing.T) {
	f := smbus.NewFake()
	orig := ddr4Fixture()
	copy(f.EEProm, orig)
	rec := smbus.NewRecording(f)
	rec.DenyWriteFrom = -1
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	// 目标: 改一个受校验覆盖的字节(会连带改 CRC)
	target := append([]byte{}, orig...)
	target[20] ^= 0x0F
	if _, err := spd.FixCRC(target); err != nil {
		t.Fatal(err)
	}
	path := writeTempFile(t, target)

	// 只让**探测取反写**之后的还原写失败: 第 1 次写(取反)成功, 第 2 次起全失败 ——
	// 正是"还原失败"的形态。同时禁止回滚写成功, 模拟总线持续不稳。
	rec.Reset()
	rec.FailWriteFrom = 2
	rec.FailWriteCmdFilter = func(cmd byte) bool { return true }

	_, err := a.WriteConfirmed(path, false, false, "WRITE")
	if err == nil {
		t.Fatal("还原写失败时写入必须中止")
	}
	// 关键断言 1: 备份文件必须是**探测之前**的内容(旧实现里它已经被探测写脏)
	a.mu.Lock()
	backup := a.lastBackupPath
	a.mu.Unlock()
	if backup == "" {
		t.Fatal("应先备份(即使随后失败)")
	}
	saved, rerr := os.ReadFile(backup)
	if rerr != nil {
		t.Fatalf("读备份: %v", rerr)
	}
	if saved[0] != orig[0] || saved[0] != 0x11 {
		t.Fatalf("备份里 byte0 = %#x, 应为探测前的 %#x(备份晚于探测就会被污染)", saved[0], orig[0])
	}
	// 关键断言 2: 报错必须说清"探测把内容改动了/还原失败", 而不是"无需回滚"
	if !strings.Contains(err.Error(), "探测") {
		t.Fatalf("错误应指明是探测期间出的问题: %v", err)
	}
	if strings.Contains(err.Error(), "无需回滚") {
		t.Fatalf("设备内容已被探测写坏, 不能说'无需回滚': %v", err)
	}
}

// TestGateDumpRejectsCrossGeneration —— H2。
// 长度相同的世代不止一个(256: DDR2/DDR3; 512: DDR4/LPDDR4; 1024: DDR5/LPDDR5),
// 只比长度会把 DDR3 的镜像写进 DDR2 条里 —— 那基本等于该条不 POST。
func TestGateDumpRejectsCrossGeneration(t *testing.T) {
	// 设备是 256B 的 DDR3
	f := smbus.NewFake()
	d3 := make([]byte, 256)
	d3[2] = 0x0B // DDR3
	if _, err := spd.FixCRC(d3); err != nil {
		t.Fatal(err)
	}
	copy(f.EEProm, d3)
	a := New()
	a.transports = []smbus.Transport{f}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	// 同长度的 DDR2 镜像(FB-DIMM 类型, 与 DDR3 同为 256B)
	d2 := make([]byte, 256)
	d2[2] = 0x0A // DDR2 FB-DIMM probe
	copy(d2[64:72], d3[117:125])
	d2[63] = ddr2Sum(d2)

	pf, err := a.buildPreflight("cross.bin", d2, false, true)
	if err != nil {
		t.Fatalf("buildPreflight: %v", err)
	}
	if !pf.Blocked || pf.BlockKind != "generation" {
		t.Fatalf("跨世代同长度写入必须按 generation 阻断: %+v", pf)
	}
	if !strings.Contains(pf.BlockReason, "世代不一致") {
		t.Fatalf("阻断原因应说清世代: %q", pf.BlockReason)
	}
	// 同一份字节走 writeWithPreflight 也必须被自己那道门拦住(M4)
	if _, err := a.writeWithPreflight(&WritePreflight{}, d2, false, true, nil, ""); err == nil {
		t.Fatal("writeWithPreflight 内部也应拒绝跨世代内容")
	}
	// 正规路径同样拒绝
	if _, err := a.writeWithPreflight(pf, d2, false, false, nil, ""); err == nil {
		t.Fatal("被阻断的预检不应放行")
	}
}

// TestWriteWithPreflightGatesDataItself —— M4。
// writeWithPreflight 不能只信调用方传来的 pf: 预检与实际写入之间文件可能被替换。
func TestWriteWithPreflightGatesDataItself(t *testing.T) {
	a, f := newWriteTestApp(t)
	orig := append([]byte{}, f.EEProm...)

	bad := append([]byte{}, orig...)
	bad[20] ^= 0x01 // 破坏内容但**不**重算 CRC
	if _, err := a.writeWithPreflight(&WritePreflight{}, bad, false, true, nil, ""); err == nil {
		t.Fatal("干跑也必须拒绝 CRC 不通过的字节")
	}
	if _, err := a.writeWithPreflight(&WritePreflight{}, bad, false, false, nil, ""); err == nil {
		t.Fatal("真实写入必须拒绝 CRC 不通过的字节")
	}
	// 内容没变: 拒绝发生在任何写之前
	for i := range orig {
		if f.EEProm[i] != orig[i] {
			t.Fatalf("被拒绝的写入不应改动设备 @%#x", i)
		}
	}
	// 长度不符
	if _, err := a.writeWithPreflight(&WritePreflight{}, orig[:256], false, false, nil, ""); err == nil {
		t.Fatal("长度不符必须拒绝")
	}
}

// TestPreviewNeverWritesEvenWhenShadowFails —— M5。
// 预览承诺"绝不写设备"。干跑影子建立失败时必须中止, 而不是照样跑 DDR4 写测试。
func TestPreviewNeverWritesEvenWhenShadowFails(t *testing.T) {
	f := smbus.NewFake()
	copy(f.EEProm, ddr4Fixture())
	rec := smbus.NewRecording(f)
	rec.DenyWriteFrom = -1
	a := New()
	a.transports = []smbus.Transport{rec}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}
	path := writeTempFile(t, ddr4Fixture())

	// 到这里连接/选择都做完了, 再让"整片读取建立影子"失败
	f.FailReads = true
	rec.Reset() // 统计窗口只覆盖这次预览(连接/选择阶段的写不算)
	_, err := a.PreflightWrite(path, false)
	if err == nil {
		t.Fatal("无法建立影子时预览应中止")
	}
	// 预览期间不得有任何数据写
	if n := len(rec.NVMWrites()); n != 0 {
		t.Fatalf("预览不得写设备, 但下发了 %d 次数据写: %v", n, rec.NVMWrites())
	}
	// 允许的只有"读操作需要的页选择"(quick 写 SPA), 绝不允许任何字节数据写 ——
	// 旧实现在这里下发了 8 次数据写(DDR4 写测试)。
	var sb strings.Builder
	for _, op := range rec.Ops() {
		if !op.Write || op.Kind == smbus.OpQuick {
			continue
		}
		fmt.Fprintf(&sb, " %s@%#x/cmd=%#x=%#x(%s)", op.Kind, op.Addr, op.Cmd, op.Val, op.Err)
	}
	if sb.Len() != 0 {
		t.Fatalf("预览不得下发数据写(只有页选择可以):%s", sb.String())
	}
}

// TestBIOSWriteDisableBlocksRealWrite —— M8。
// BIOS 的 SPD Write Disable 打开时, 写入一定失败 —— 必须在预检里提前拦住。
func TestBIOSWriteDisableBlocksRealWrite(t *testing.T) {
	a, _ := newWriteTestApp(t)
	// 模拟 i801 控制器报告"SPD 写禁止位已打开"
	a.mu.Lock()
	a.ctrl = smbus.Controller{Kind: smbus.KindI801, Name: "Intel PCH", WpKnown: true, NoSpdWp: false}
	a.mu.Unlock()

	target := ddr4Fixture()
	target[20] ^= 0x0F
	if _, err := spd.FixCRC(target); err != nil {
		t.Fatal(err)
	}
	pf, err := a.buildPreflight("bios.bin", target, false, true)
	if err != nil {
		t.Fatalf("buildPreflight: %v", err)
	}
	if !pf.Blocked || pf.BlockKind != "bios" {
		t.Fatalf("BIOS 写禁止打开时应阻断: %+v", pf)
	}
	// 干跑允许(只是内存演算), 但结果里必须带这条警示
	res, err := a.writeWithPreflight(pf, target, false, true, nil, "")
	if err != nil {
		t.Fatalf("干跑应放行: %v", err)
	}
	if !strings.Contains(res.Message, "SPD Write Disable") {
		t.Fatalf("干跑结果应提示 BIOS 写禁止: %q", res.Message)
	}
	// 真实写入被拒
	if _, err := a.writeWithPreflight(pf, target, false, false, nil, ""); err == nil {
		t.Fatal("BIOS 写禁止时真实写入必须被拒绝")
	}
}

// TestNVMWriteCountNotTruncated —— M7。
// force 模式写 1024 字节时, "NVM 写"这个证据不能被写日志上限(512)截断。
func TestNVMWriteCountNotTruncated(t *testing.T) {
	a, rec, ft := newDDR5WriteApp(t)
	orig := append([]byte{}, ft.EEProm...)
	counter, ok := a.activeTransport().(*smbus.CountingTransport)
	if !ok {
		t.Fatal("测试需要计数包装")
	}
	rec.Reset()
	counter.Reset()
	res, err := a.writeWithPreflight(&WritePreflight{}, orig, true /*force: 全部写*/, true, nil, "")
	if err != nil {
		t.Fatalf("干跑(force): %v", err)
	}
	if res.Total != 1024 {
		t.Fatalf("force 干跑应计划 1024 字节, got %d", res.Total)
	}
	// 干跑零写仍然成立, 且计数不能再被日志上限影响
	if res.NVMWrites != 0 {
		t.Fatalf("干跑不得有 NVM 写: %d", res.NVMWrites)
	}
	if counter.NVMWriteCount() != 0 {
		t.Fatalf("干跑计数器应保持 0, got %d", counter.NVMWriteCount())
	}
	// 真实 force 写入: 1024 个字节必须是 1024 次 NVM 写(而不是 512)
	counter.Reset()
	res, err = a.writeWithPreflight(&WritePreflight{}, orig, true, false, nil, "")
	if err != nil {
		t.Fatalf("force 写入: %v", err)
	}
	if res.NVMWrites != 1024 {
		t.Fatalf("NVM 写计数 = %d, 应为 1024(日志截断会少报成 512)", res.NVMWrites)
	}
	if counter.NVMWriteCount() != 1024 {
		t.Fatalf("计数器 = %d, 应为 1024", counter.NVMWriteCount())
	}
}

// TestBackupNamesAreUnique —— L10。
// 同一秒内两次真实写入不能互相覆盖(第一次的备份才是"原始内容")。
func TestBackupNamesAreUnique(t *testing.T) {
	a, _ := newWriteTestApp(t)
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	_, p1, err := a.backupCurrent(dev)
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := a.backupCurrent(dev)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("两次备份落到同一个文件: %s", p1)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("备份文件不存在 %s: %v", p, err)
		}
	}
	// 备份目录在用户目录下, 测试结束清掉自己造的文件
	dir := filepath.Dir(p1)
	_ = os.Remove(p1)
	_ = os.Remove(p2)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

// TestVerifyChangedByteWiseCatchesReadBias —— L11。
// 逐字节回读与整片 Verify 共用同一条(块读/字读)读路径; 用逐字节读法再核一遍,
// 才能发现"读路径有系统性偏差、两边一起同意"的情况。
func TestVerifyChangedByteWiseCatchesReadBias(t *testing.T) {
	f := smbus.NewFake()
	want := ddr4Fixture()
	copy(f.EEProm, want)
	d, err := eeprom.New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	changes := []eeprom.ByteChange{{Offset: 20, New: want[20]}, {Offset: 21, New: want[21]}}
	if err := d.VerifyChangedByteWise(want, changes); err != nil {
		t.Fatalf("一致时应通过: %v", err)
	}
	// 构造"读路径骗人"的场景: 设备上 0x15 其实没写成功(与目标不一致),
	// 但块读返回的是目标值 → 走块读的 Verify 认为一切正常。
	f.EEProm[21] ^= 0xFF
	f.BlockReadOverride = want
	if err := d.Verify(want); err != nil {
		t.Fatalf("这条用例的前提是块读路径会被蒙混过关: %v", err)
	}
	if err := d.VerifyChangedByteWise(want, changes); err == nil {
		t.Fatal("块读路径骗人时, 逐字节复核必须发现设备与目标不一致")
	}
	// 复核不会把读路径设置改坏
	f.BlockReadOverride = nil
	f.EEProm[21] ^= 0xFF
	if err := d.Verify(want); err != nil {
		t.Fatalf("复核后读路径应恢复正常: %v", err)
	}
}

// TestWriteErrorCountsIncludeReadStageByte —— L12。
// 回读阶段失败时, 当前字节已经写下去了, "已写"应算上它。
func TestWriteErrorCountsIncludeReadStageByte(t *testing.T) {
	f := smbus.NewFake()
	copy(f.EEProm, ddr4Fixture())
	d, err := eeprom.New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	target := append([]byte{}, ddr4Fixture()...)
	target[20] ^= 0x0F
	changes, err := d.PlanWrite(target, false) // 计划阶段要读当前内容, 所以先计划
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Skip("需要至少一个变更")
	}
	f.FailReads = true // 之后所有读(含回读)都失败
	err = d.ApplyWrite(target, changes, nil)
	var we *eeprom.WriteError
	if !errors.As(err, &we) {
		t.Fatalf("应是 WriteError: %v", err)
	}
	if we.Written != 1 {
		t.Fatalf("第 1 个字节已写下去、随后回读失败 → 已写应为 1, got %d", we.Written)
	}
}

func ddr2Sum(d []byte) byte {
	sum := byte(0)
	for _, v := range d[:63] {
		sum += v
	}
	return sum
}

// TestIncrementalWriteOnlyChangedBytes —— "写入能只写很小字段吗?"
//
// 能。默认(增量)写入只把**与设备当前内容不同**的字节下到总线, 其余字节一个写事务都不发;
// 这正是"拿不重要的位置做写入测试"的依据: 改 1 个字节就只写 1 个字节。
// 只有强制模式(force)才会写全部字节(用于修复), 编辑器路径固定 force=false。
func TestIncrementalWriteOnlyChangedBytes(t *testing.T) {
	a, rec, ft := newDDR5WriteApp(t)
	orig := append([]byte{}, ft.EEProm...)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	// 只改序列号的最后一个字节(0x208, 不在任何 CRC 覆盖范围内 → 计划里连 CRC 字节都没有)
	if _, err := a.EditSetByte(0x208, int(orig[0x208]^0x01)); err != nil {
		t.Fatal(err)
	}
	target, err := a.EditBytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.EditDiff()
	if err != nil {
		t.Fatal(err)
	}
	if d.ChangeCount != 1 {
		t.Fatalf("只改 1 个字节时计划应恰好 1 个变更, got %d", d.ChangeCount)
	}
	if d.DirtyInCRC != nil && len(d.DirtyInCRC) != 0 {
		t.Fatalf("序列号不在校验范围, 不应产生 CRC 变更: %v", d.DirtyInCRC)
	}
	rec.Reset()
	if counter, ok := a.activeTransport().(*smbus.CountingTransport); ok {
		counter.Reset()
	}
	res, err := a.EditApplyToDevice(false, false, "WRITE")
	if err != nil {
		t.Fatalf("写入: %v", err)
	}
	if res.Written != 1 || res.Total != 1 {
		t.Fatalf("应只写 1 个字节: %+v", res)
	}
	// 总线上恰好 1 次 NVM 数据写 —— 就是"只写一个字节"的硬证据
	if res.NVMWrites != 1 {
		t.Fatalf("NVM 写 = %d, 应为 1", res.NVMWrites)
	}
	if n := len(rec.NVMWrites()); n != 1 {
		t.Fatalf("记录到的 NVM 写 = %d, 应为 1", n)
	}
	if !res.Verified {
		t.Fatal("写入后必须校验通过")
	}
	// 除目标字节外全部原样
	for i := range orig {
		want := target[i]
		if ft.EEProm[i] != want {
			t.Fatalf("@%#x = %02X, 目标 %02X", i, ft.EEProm[i], want)
		}
	}
	// 反向确认: 强制模式才会写全部
	if counter, ok := a.activeTransport().(*smbus.CountingTransport); ok {
		counter.Reset()
	}
	rec.Reset()
	resForce, err := a.writeWithPreflight(&WritePreflight{}, target, true, true, nil, "") // force 干跑: 只算计划
	if err != nil {
		t.Fatalf("force 干跑: %v", err)
	}
	if resForce.Total != 1024 {
		t.Fatalf("force 模式应计划全部 1024 字节, got %d", resForce.Total)
	}
}

// TestVerifyByteWiseCoversWholeImage —— 最终那层"逐字节复核"必须覆盖**整片**,
// 而不只是改动过的字节: 分页/窗口写错位、外部工具同时动总线都会改到计划之外的字节。
// (读速修好之前整片逐字节读要 32 秒, 所以当时只复核改动字节; 忙等模式下约 1 秒。)
func TestVerifyByteWiseCoversWholeImage(t *testing.T) {
	f := smbus.NewFake()
	want := ddr4Fixture()
	copy(f.EEProm, want)
	d, err := eeprom.New(f, 0x50)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.VerifyByteWise(want); err != nil {
		t.Fatalf("一致时应通过: %v", err)
	}
	// 篡改一个**没有被写入计划覆盖**的字节(设备上变了但改动列表里没有它)
	f.EEProm[400] ^= 0xFF
	err = d.VerifyByteWise(want)
	if err == nil {
		t.Fatal("整片逐字节复核必须发现计划之外的字节变化")
	}
	if !strings.Contains(err.Error(), "0x190") { // 400 = 0x190
		t.Fatalf("错误里应指出偏移: %v", err)
	}
	// 只复核改动字节的话, 就会漏掉它 —— 这正是升级成整片复核的理由
	if err := d.VerifyChangedByteWise(want, []eeprom.ByteChange{{Offset: 20, New: want[20]}}); err != nil {
		t.Fatalf("只复核改动字节本来就不该发现 0x190: %v", err)
	}
	// 读路径设置必须恢复(否则后面所有读都退化成逐字节)
	f.EEProm[400] ^= 0xFF
	if err := d.Verify(want); err != nil {
		t.Fatalf("复核后应恢复块读路径: %v", err)
	}
}

// 读加速与等待模式都不再有手动开关(用户要求"做成自动切换, 异常自动降级, 日志体现"):
// 这条用例锁住"后端仍然提供 BusTuning 供界面显示, 而设置入口不再是界面开关"。
// 真正的档位回退(块读→字读→逐字节)已有 eeprom 用例; 这里只确认服务层不会因为
// 缺少可调优能力而报错(测试用 Fake 没有 Tuner)。
func TestBusTuningIsInformational(t *testing.T) {
	a, _ := newWriteTestApp(t)
	res, err := a.BusTuning()
	if err != nil {
		t.Fatalf("BusTuning 不应报错: %v", err)
	}
	if res.Tunable {
		t.Fatal("Fake 不该报告可调优")
	}
	if res.Note == "" {
		t.Fatal("应说明不可调优的原因")
	}
}

// 审计 M1: 数据本身不合格(CRC 不通过/世代不符)时, 必须在**备份与写保护探测之前**就拒绝 ——
// 否则一个注定被拒的写入会先对 DDR4 及更早的条做 4 块 × (取反写+还原) = 8 次真实 NVM 写。
func TestRejectedTargetIsProbedNever(t *testing.T) {
	a, f, rec := func() (*App, *smbus.FakeTransport, *smbus.RecordingTransport) {
		f := smbus.NewFake()
		copy(f.EEProm, ddr4Fixture())
		rec := smbus.NewRecording(f)
		a := New()
		a.transports = []smbus.Transport{rec}
		if err := a.Connect(0); err != nil {
			t.Fatal(err)
		}
		if err := a.Select(0x50); err != nil {
			t.Fatal(err)
		}
		return a, f, rec
	}()
	// CRC 不通过的目标(改一个字节但不重算 CRC)
	bad := append([]byte{}, ddr4Fixture()...)
	bad[20] ^= 0x0F
	path := writeTempFile(t, bad)
	rec.Reset()
	if _, err := a.WriteConfirmed(path, false, false, "WRITE"); err == nil {
		t.Fatal("CRC 不通过的目标必须被拒绝")
	}
	if n := len(rec.NVMWrites()); n != 0 {
		t.Fatalf("被拒绝的目标不应触发任何 NVM 写(实际 %d 次)", n)
	}
	for _, q := range f.QuickLog {
		if q.Write {
			t.Fatalf("被拒绝的目标不应触发写保护探测的 Quick 写(@%#x)", q.Addr)
		}
	}
}
