package spd

import (
	"encoding/binary"
	"testing"
)

// makeDDR4 构造一份合成的 DDR4 SPD dump(带正确 CRC)。
// 描述: DDR4 UDIMM, 16GB(8Gb×16), 2Rx8? 简化: 2rank×8bit×8Gb→16GB? 见字段。
func makeDDR4(t *testing.T) []byte {
	t.Helper()
	d := make([]byte, 512)
	d[0] = 0x08<<4 | 0x04<<0 // total 512, used 512
	d[2] = 0x0C              // DDR4
	d[3] = 2                 // UDIMM(bits0-3)
	// byte 4: bankGroup=2(01→2组), bankAddr=1<<(2+2)=16, cap/die: bit3=0, cap4=4 → 2<<(4+7)=4096Mb=4Gb? 2<<11 = 4096
	d[4] = 1<<6 | 2<<4 | 4       // bg码1(2组) banks码2(16) density码4(4096Mb)
	d[5] = (17-12)<<3 | (10 - 9) // rows码5@bits3-5, cols码1@bits0-2
	d[6] = 0                     // 单片
	d[12] = 1<<3 | 1             // 2 ranks码1@bits3-5, x8码1@bits0-2
	d[13] = 1<<3 | 3<<0          // ECC ext + 64bit
	// timebase byte15: 默认 125ps/1ps → 0
	d[18] = 6  // tCKavgmin medium: 6×125 = 750ps = 1.333GHz? 0.75ns→1333MHz
	d[19] = 12 // tCKavgmax
	// CAS 掩码: CL 9..? bit i → CL i+7. 设 CL17(bit10), CL18, CL19, CL20 → 0xF<<10
	binary.LittleEndian.PutUint32(d[20:24], 0x0F<<10)
	d[24] = 12                                  // tAA = 1.5ns? 12×125ps=1.5ns
	d[25] = 12                                  // tRCD
	d[26] = 12                                  // tRP
	binary.LittleEndian.PutUint16(d[28:30], 32) // tRAS = 4ns? 32×125=4ns
	binary.LittleEndian.PutUint16(d[30:32], 256)
	binary.LittleEndian.PutUint16(d[32:34], 128)
	// 厂商区: 320 = continuation(计数 0 + 奇校验位 → 0x80), 321 = code|parity
	// (实测 Micron/Samsung/Hynix 的 dump 都是这个形态)
	d[320] = 0x80
	d[321] = 0x2C
	d[323] = 0x24 // 2024
	d[324] = 0x15 // 15 周
	copy(d[325:329], []byte{0xDE, 0xAD, 0xBE, 0xEF})
	copy(d[329:329+20], []byte("TEST16GB-DDR4-3200  "))
	// XMP 2.0 @384
	copy(d[384:386], []byte{0x0C, 0x4A})
	d[386] = 0x01 // profile0 enabled
	d[387] = 0x20 // v2.0
	// CRC
	crc1 := Crc16(d[:126])
	d[126], d[127] = byte(crc1), byte(crc1>>8)
	crc2 := Crc16(d[128:254])
	d[254], d[255] = byte(crc2), byte(crc2>>8)
	return d
}

func TestDDR4Decode(t *testing.T) {
	d := makeDDR4(t)
	p, err := NewDDR4(d)
	if err != nil {
		t.Fatalf("NewDDR4: %v", err)
	}
	if p.ModuleType() != "UDIMM" {
		t.Fatalf("ModuleType = %s", p.ModuleType())
	}
	bg, ba, capDie := p.DensityBanks()
	if bg != 2 || ba != 16 || capDie != 4096 {
		t.Fatalf("DensityBanks = %d %d %d", bg, ba, capDie)
	}
	rows, cols := p.Addressing()
	if rows != 17 || cols != 10 {
		t.Fatalf("Addressing = %d/%d", rows, cols)
	}
	asym, ranks, width := p.Organization()
	if asym || ranks != 2 || width != 8 {
		t.Fatalf("Organization = %v %d %d", asym, ranks, width)
	}
	ext, bus := p.BusWidth()
	if !ext || bus != 64 {
		t.Fatalf("BusWidth = %v %d", ext, bus)
	}
	// 容量 = 4096Mb/8(512MB/die) × 8 芯片 × 2 rank = 8GiB
	if got := p.TotalCapacityBytes(); got != 8*1024*1024*1024 {
		t.Fatalf("TotalCapacity = %d (%.2f GiB)", got, float64(got)/(1024*1024*1024))
	}
	tb := p.Timebase()
	if tb.Medium != 125 || tb.Fine != 1 {
		t.Fatalf("Timebase = %+v", tb)
	}
	if ns := p.TCKAVGmin().NanoSeconds(tb); ns != 0.75 {
		t.Fatalf("tCKmin = %f", ns)
	}
	if cl := p.CasLatencies(); cl.HighRange || len(cl.ToArray()) != 4 || cl.ToArray()[0] != 17 {
		t.Fatalf("CAS = %v %v", cl.Bitmask, cl.ToArray())
	}
	if name, _, _ := p.Manufacturer(); name != "Micron Technology" {
		t.Fatalf("Manufacturer = %q", name)
	}
	y, w := p.DateCode()
	if y != 2024 || w != 15 {
		t.Fatalf("Date = %d/%d", y, w)
	}
	if sn := p.SerialNumber(); sn[0] != 0xDE || sn[3] != 0xEF {
		t.Fatalf("Serial = %x", sn)
	}
	if pn := p.PartNumber(); pn != "TEST16GB-DDR4-3200" {
		t.Fatalf("PartNumber = %q", pn)
	}
	if !p.CRCOK() {
		t.Fatal("合成 dump 的 CRC 应通过")
	}
	// 破坏 CRC → FixCRC 修复
	d[5] ^= 0xFF
	if p.CRCOK() {
		t.Fatal("破坏后 CRC 应失败")
	}
	if !p.FixCRC() {
		t.Fatal("FixCRC 应修复")
	}

	// XMP: 设置 profile0 的几个关键字段验证换算
	// tCKmin @ base+0x0C = 384+12 = 396, fine @ base+0x2F = 384+47 = 431
	d[396] = 5 // 5×125ps = 0.625ns → 1600MHz
	d[431] = 0
	// tAA medium @ 0x191=401: 10×125ps = 1.25ns → CL = ceil(1.25/0.625) = 2
	d[401] = 10
	// 电压 @ base+0x09 = 393: bit7=1(1V基准) + 35/100 → 1.35V
	d[393] = 0x80 | 35
	crc1 := Crc16(d[:126])
	d[126], d[127] = byte(crc1), byte(crc1>>8)

	xmp := p.XMPProfiles()
	if !p.XMPPresence() || !xmp[0].Enabled {
		t.Fatalf("XMP 应存在且启用: %v %v", p.XMPPresence(), xmp[0].Enabled)
	}
	if v := xmp[0].TCKmin.NanoSeconds(tb); v != 0.625 {
		t.Fatalf("XMP tCK = %v", v)
	}
	if xmp[0].Volts != 1.35 {
		t.Fatalf("Volts = %v", xmp[0].Volts)
	}
	if s := XMPString(xmp[0], tb); s != "1600 MHz 2-0-0-0 1.35V" {
		t.Fatalf("XMPString = %q", s)
	}
}

// makeDDR5 构造 DDR5 SPD: DDR5 UDIMM, 16GB(2R? 简化 1R×8 通道? 见字段)。
func makeDDR5(t *testing.T) []byte {
	t.Helper()
	d := make([]byte, 1024)
	d[2] = 0x12 // DDR5
	d[3] = 2    // UDIMM
	// byte4: die=1(0000→1), density=4Gb? 密度索引 2 = 8Gb
	d[4] = 0<<4 | 2    // die码0(1片), density码2(8Gb)
	d[7] = 1<<7 | 2<<2 // groups code 1→2组, banks code 2→4
	// addressing byte5: 字段是"相对基址的码", rows = 16+码, cols = 10+码
	d[5] = 1<<0 | 0<<5 // rows码1(→17), cols码0(→10)
	// byte 6: IO width code 1 → 8bit
	d[6] = 1 << 5 // io width 码1(x8)@bits5-7
	// byte 234: 1 rank
	d[234] = 0<<6 | 0<<5
	// byte 235: 2 channels, ext 8? primary per channel 16bit: code 0 → 1<<3=8? (1<<(0+3))&0xF8=8
	d[235] = 1<<5 | 0<<3 | 0<<0 // 通道码1(2通道)@bits5-6, ext码0, primary码0(8bit)
	d[512] = 0x80               // continuation(计数 0 + 奇校验位, 与真实 dump 一致)
	d[513] = 0xCE               // 厂商码(含奇校验位)
	d[515] = 0x25               // 2025
	d[516] = 0x30               // 30 周
	copy(d[517:521], []byte{0x11, 0x22, 0x33, 0x44})
	copy(d[521:521+30], []byte("DDR5-TEST-16GB           "))
	// EXPO @832
	copy(d[832:836], []byte("EXPO"))
	crc := Crc16(d[:510])
	d[510], d[511] = byte(crc), byte(crc>>8)
	ecrc := Crc16(d[832:958])
	d[958], d[959] = byte(ecrc), byte(ecrc>>8)
	return d
}

func TestDDR5Decode(t *testing.T) {
	d := makeDDR5(t)
	p, err := NewDDR5(d)
	if err != nil {
		t.Fatalf("NewDDR5: %v", err)
	}
	if p.ModuleType() != "UDIMM" {
		t.Fatalf("ModuleType = %s", p.ModuleType())
	}
	dies, dens := p.DensityPackages()
	if dies[0] != 1 || dens[0] != 8 {
		t.Fatalf("Density = %v %v", dies, dens)
	}
	_, ranks := p.Organization()
	if ranks != 1 {
		t.Fatalf("Ranks = %d", ranks)
	}
	ch, ext, primary := p.ChannelBusWidth()
	if ch != 2 || ext != 0 || primary != 8 {
		t.Fatalf("ChannelBusWidth = %d %d %d", ch, ext, primary)
	}
	// 容量: ch=2 × (8/io8=1) × die1 × 8Gb/8 = 2×1×1×1×1 = 2GiB?:
	// 公式 = channels × primary/io × dies × densityGb/8 × ranks = 2×1×1×1×1 = 2 bytes?? 单位是 GB(×2^30)
	// 2×1×1×(8/8)×1 = 2 → 2GiB
	if got := p.TotalCapacityBytes(); got != 2 {
		t.Fatalf("TotalCapacity(GB units) = %d, want 2", got)
	}
	if name, _, _ := p.Manufacturer(); name != "Samsung" {
		t.Fatalf("Manufacturer = %q", name)
	}
	y, w := p.DateCode()
	if y != 2025 || w != 30 {
		t.Fatalf("Date = %d/%d", y, w)
	}
	if pn := p.PartNumber(); pn != "DDR5-TEST-16GB" {
		t.Fatalf("PartNumber = %q", pn)
	}
	if !p.CRCOK() {
		t.Fatal("CRC 应通过")
	}
	d[10] ^= 0xFF
	if p.CRCOK() {
		t.Fatal("破坏后应失败")
	}
}

func TestDDR5EXPOAndXMP(t *testing.T) {
	d := makeDDR5(t)
	p, _ := NewDDR5(d)
	if !p.EXPOPresence() {
		t.Fatal("EXPO 应存在")
	}
	if p.XMPPresence() {
		t.Fatal("XMP 未写入不应存在")
	}
	copy(d[640:642], []byte{0x0C, 0x4A})
	if !p.XMPPresence() {
		t.Fatal("XMP 应存在")
	}
}

func makeDDR3(t *testing.T) []byte {
	t.Helper()
	d := make([]byte, 256)
	d[0] = 0x01 << 4
	d[2] = 0x0B                  // DDR3
	d[3] = 2                     // UDIMM
	d[4] = 2                     // density码2 → 1<<(2+8)=1024Mb
	d[5] = (16-12)<<3 | (10 - 9) // rows码4, cols码1
	d[7] = 1<<3 | 1              // 2 ranks码1@bits3-5, x8@bits0-2
	d[8] = 3                     // 64bit
	d[9] = 1<<7 | 1<<3           // MTB 1/8ns? dividend=1 divisor=8: byte10=1 byte11=8
	d[10] = 1
	d[11] = 8
	d[12] = 12    // tCK = 12×(1/8) = 1.5ns
	d[117] = 0x80 // continuation(计数 0 + 奇校验位)
	d[118] = 0x2C // 厂商码(含奇校验位)
	copy(d[128:146], []byte("DDR3-TEST-8GB      "))
	crc := Crc16(d[:126])
	d[126], d[127] = byte(crc), byte(crc>>8)
	return d
}

func TestParseBasicDDR3(t *testing.T) {
	d := makeDDR3(t)
	bi, err := ParseBasic(d)
	if err != nil {
		t.Fatalf("ParseBasic: %v", err)
	}
	if bi.Type != DDR3 || bi.ModuleType != "UDIMM" {
		t.Fatalf("type/mod = %v %s", bi.Type, bi.ModuleType)
	}
	if bi.Ranks != 2 || bi.DeviceWidth != 8 || bi.BusWidthBits != 64 {
		t.Fatalf("org = %d %d %d", bi.Ranks, bi.DeviceWidth, bi.BusWidthBits)
	}
	// 1024Mb/8 × 64/8 × 2 = 2048MiB
	if bi.BytesMib != 2048 {
		t.Fatalf("cap = %d MiB", bi.BytesMib)
	}
	if bi.PartNumber != "DDR3-TEST-8GB" {
		t.Fatalf("pn = %q", bi.PartNumber)
	}
	if bi.Manufacturer != "Micron Technology" {
		t.Fatalf("mfg = %q", bi.Manufacturer)
	}
	if !bi.CRCOK {
		t.Fatal("CRC 应通过")
	}
	if bi.TCKminNS != 1.5 {
		t.Fatalf("tCK = %f", bi.TCKminNS)
	}
}

func makeDDR2(t *testing.T) []byte {
	t.Helper()
	d := make([]byte, 256)
	d[2] = 0x08 // DDR2
	d[3] = 13   // rows
	d[4] = 10   // cols
	d[5] = 1    // 2 ranks码1@bits0-2
	// JEDEC DDR2: byte6 = 模块数据宽度(总线), byte13 = 芯片位宽, byte8 = 接口电压
	d[6] = 64   // 64bit 总线
	d[8] = 3    // 接口电压(不参与容量)
	d[9] = 0x25 // 2.5ns
	d[13] = 8   // x8 芯片
	d[17] = 4   // banks
	// 身份区
	// DDR2 厂商 ID 按小端存放: 续延码(0x7F×bank)在前, 随后是含奇校验位的厂商码, 其余补 0
	d[64], d[65] = 0x2C, 0x00
	d[72] = 0x03 // 生产地点
	copy(d[73:91], []byte("DDR2-TEST-1GB     "))
	d[91], d[92] = 0x12, 0x34 // 修订码
	d[93], d[94] = 24, 15     // 2024 年第 15 周(非 BCD)
	d[95], d[96], d[97], d[98] = 0xDE, 0xAD, 0xBE, 0xEF
	d[63] = ddr2Checksum(d) // byte63 = sum(0..62)
	return d
}

func TestParseBasicDDR2(t *testing.T) {
	d := makeDDR2(t)
	bi, err := ParseBasic(d)
	if err != nil {
		t.Fatalf("ParseBasic: %v", err)
	}
	// 2^13×2^10×4 banks = 2^25 bits = 4MiB/芯片 × (64/8) 芯片 × 2 rank = 64MiB
	if bi.BytesMib != 64 {
		t.Fatalf("cap = %d MiB (want 64)", bi.BytesMib)
	}
	if bi.TCKminNS != 2.5 {
		t.Fatalf("tCK = %f", bi.TCKminNS)
	}
	if bi.PartNumber != "DDR2-TEST-1GB" {
		t.Fatalf("pn = %q", bi.PartNumber)
	}
	if bi.Ranks != 2 || bi.DeviceWidth != 8 || bi.BusWidthBits != 64 {
		t.Fatalf("org = %v", bi)
	}
	// 身份区与校验和
	if bi.DateYear != 2024 || bi.DateWeek != 15 {
		t.Fatalf("date = %d/%d", bi.DateYear, bi.DateWeek)
	}
	if bi.SerialHex != "DEADBEEF" {
		t.Fatalf("sn = %q", bi.SerialHex)
	}
	if !bi.ChecksumOK || !bi.CRCOK {
		t.Fatalf("DDR2 校验和应通过: %+v", bi)
	}
	if bi.Revision != 0x1234 {
		t.Fatalf("revision = %#x", bi.Revision)
	}
	// 破坏一个字节后校验和必须失败
	d[10] ^= 0xFF
	bi2, _ := ParseBasic(d)
	if bi2.CRCOK {
		t.Fatal("改动字节后 DDR2 校验和应失败")
	}
}
