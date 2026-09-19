package app

import (
	"os"
	"path/filepath"
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
	if r.RamType != "DDR4" || r.ModuleType != "UDIMM" {
		t.Fatalf("type = %s/%s", r.RamType, r.ModuleType)
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
	if r.RamType != "DDR3" || r.TotalMib != 2048 {
		t.Fatalf("res = %+v", r)
	}
	if r.Basic == nil || r.Basic.TCKminNS != 1.5 {
		t.Fatalf("basic = %+v", r.Basic)
	}
}

func TestWriteFromFileFlow(t *testing.T) {
	f := smbus.NewFake()
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	copy(f.EEProm, d4)
	a := New()
	a.transports = []smbus.Transport{f}
	if err := a.Connect(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Select(0x50); err != nil {
		t.Fatal(err)
	}

	// 目标 dump: 与内容一致 + 一个不同字节(芯片内容同为 0x11 基线)
	want := make([]byte, 512)
	for i := range want {
		want[i] = 0x11
		f.EEProm[i] = 0x11
	}
	want[0x10] = 0x99
	path := filepath.Join(t.TempDir(), "dump.bin")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}

	// 更新模式: 只写差异字节(写前保护检查会产生 4 次 WriteTest 探测写)
	if err := a.WriteFromFile(path, false); err != nil {
		t.Fatalf("WriteFromFile: %v", err)
	}
	n := 0
	for _, w := range f.WriteLog {
		if w.Cmd == 0x10 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("update 模式应只写 cmd=0x10 一次, got %d (总写 %d)", n, len(f.WriteLog))
	}
	// 校验
	if err := a.VerifyFile(path); err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}

	// 保护状态可查询
	blocks, _, _, err := a.WPStatus()
	if err != nil || len(blocks) != 4 {
		t.Fatalf("WPStatus: %v %v", blocks, err)
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

	if err := a.WPSet([]byte{2}); err != nil {
		t.Fatalf("WPSet: %v", err)
	}
	// DDR4 RSWPSet(2) 应发出 SWP2 quick 命令(写 0x35)与 CWP(0x33)
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
	blocks, _, _, err := a.WPStatus()
	if err != nil {
		t.Fatalf("WPStatus: %v", err)
	}
	want := []bool{false, true, false, true}
	for i, w := range want {
		if blocks[i] != w {
			t.Fatalf("blocks = %v, want %v", blocks, want)
		}
	}
	if err := a.WPClear(); err != nil {
		t.Fatalf("WPClear: %v", err)
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
	a.CloseDevice()
	if _, err := a.Dump(); err == nil {
		t.Fatal("CloseDevice 后 Dump 应报错")
	}
	a.Close()
}
