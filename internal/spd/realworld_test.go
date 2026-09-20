package spd

import "testing"

func TestRealWorldDump(t *testing.T) {
	// 取自用户实机 dump (build 6fa39d3 读取, CRC 校验通过):
	// SemsoTai 16GiB DDR5 SO-DIMM, 部件号 888046950, 2023wk50, SN 0000011F
	raw := make([]byte, 1024)
	raw[0], raw[1], raw[2], raw[3] = 0x30, 0x10, 0x12, 0x03 // 头: 512/1024B, rev1.2, SO-DIMM
	raw[4] = 0x04                                           // density
	raw[6] = 0x20                                           // IO width bits[7:5]=001 → x8
	raw[7] = 0x62                                           // banks
	raw[0x200], raw[0x201] = 0x08, 0xC8                     // manufacturer ID
	raw[0x203], raw[0x204] = 0x23, 0x50                     // 2023 wk50
	copy(raw[0x205:0x209], []byte{0x00, 0x00, 0x01, 0x1F})  // SN
	copy(raw[0x209:], "888046950")                          // part number
	raw[0xEA] = 0x00                                        // byte234: 1 rank, 对称
	raw[0xEB] = 0x22                                        // byte235: 2 通道 × 每通道 32bit

	d, err := NewDDR5(raw)
	if err != nil {
		t.Fatalf("NewDDR5: %v", err)
	}
	_, ranks := d.Organization()
	cap := d.TotalCapacityBytes() // 返回值实为 GiB
	mfg, _, _ := d.Manufacturer()
	if ranks != 1 || d.DeviceWidth() != 8 || cap != 16 {
		t.Fatalf("解码错误: ranks=%d deviceWidth=%d cap=%d MiB", ranks, d.DeviceWidth(), cap)
	}
	t.Logf("moduleType=%s ranks=%d deviceWidth=%d cap=%dGiB mfg=%q part=%q",
		d.ModuleType(), ranks, d.DeviceWidth(), cap, mfg, d.PartNumber())
}
