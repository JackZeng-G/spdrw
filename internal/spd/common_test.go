package spd

import (
	"encoding/json"
	"testing"
)

func TestCrc16(t *testing.T) {
	// CRC-16/CCITT-FALSE 家族向量校验: 初值不同的变体较多;
	// SPD 用初值 0。标准向量: "123456789" (初值0,无反射,多项式0x1021) = 0x31C3。
	got := Crc16([]byte("123456789"))
	if got != 0x31C3 {
		t.Fatalf("Crc16(\"123456789\") = %#04x, want 0x31C3", got)
	}
	if Crc16(nil) != 0 {
		t.Fatal("空输入应为 0")
	}
}

func TestBCD(t *testing.T) {
	cases := map[byte]int{0x21: 21, 0x09: 9, 0x99: 99, 0x00: 0}
	for in, want := range cases {
		if got := BCD(in); got != want {
			t.Fatalf("BCD(%#x) = %d, want %d", in, got, want)
		}
	}
}

func TestIdentify(t *testing.T) {
	d5 := make([]byte, 1024)
	d5[2] = 0x12
	rt, size, err := Identify(d5)
	if err != nil || rt != DDR5 || size != 1024 {
		t.Fatalf("DDR5: %v %v %d", rt, err, size)
	}
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	rt, size, _ = Identify(d4)
	if rt != DDR4 || size != 512 {
		t.Fatalf("DDR4: %v %d", rt, size)
	}
	d3 := make([]byte, 256)
	d3[2] = 0x0B
	rt, size, _ = Identify(d3)
	if rt != DDR3 || size != 256 {
		t.Fatalf("DDR3: %v %d", rt, size)
	}
	if _, _, err := Identify([]byte{1, 2}); err == nil {
		t.Fatal("短数据应报错")
	}
	bad := make([]byte, 256)
	bad[2] = 0x77
	rt, _, _ = Identify(bad)
	if rt != Unknown {
		t.Fatalf("未知类型应为 Unknown, got %v", rt)
	}
}

func TestValidateSpd(t *testing.T) {
	d4 := make([]byte, 512)
	d4[2] = 0x0C
	if !ValidateSpd(d4) {
		t.Fatal("合法 DDR4 dump 应通过")
	}
	if ValidateSpd(d4[:256]) {
		t.Fatal("长度不符应拒绝")
	}
	bad := make([]byte, 256)
	bad[2] = 0x99
	if ValidateSpd(bad) {
		t.Fatal("未知类型应拒绝")
	}
}

func TestManufacturerName(t *testing.T) {
	// bank0 code 0x01(奇校验 0x81 亦可) = AMD
	if got := ManufacturerName(0, 0x01); got != "AMD" {
		t.Fatalf("bank0/0x01 = %q, want AMD", got)
	}
	if got := ManufacturerName(0, 0x81); got != "AMD" {
		t.Fatalf("奇偶位应忽略: %q", got)
	}
	// bank0 code 0x2C = Micron Technology
	if got := ManufacturerName(0, 0x2C); got != "Micron Technology" {
		t.Fatalf("bank0/0x2C = %q", got)
	}
	if got := ManufacturerName(0, 0); got != "" {
		t.Fatalf("码 0 应为空: %q", got)
	}
	if got := ManufacturerName(200, 1); got != "" {
		t.Fatalf("越界 continuation 应为空: %q", got)
	}
}

func TestFindManufacturer(t *testing.T) {
	cont, code, ok := FindManufacturer("Samsung")
	if !ok {
		t.Fatal("应能找到 Samsung")
	}
	name := ManufacturerName(cont, code)
	if name != "Samsung" {
		t.Fatalf("回查 = %q, want Samsung", name)
	}
	if _, _, ok := FindManufacturer("不存在的厂商xyz"); ok {
		t.Fatal("未知厂商不应命中")
	}
}

// TestIdcodesIntegrity 保证嵌入表与生成脚本输出一致(防手改破坏)。
func TestIdcodesIntegrity(t *testing.T) {
	raw, err := idcodesFS.ReadFile("data/idcodes.json")
	if err != nil {
		t.Fatal(err)
	}
	var tables [][]string
	if err := json.Unmarshal(raw, &tables); err != nil {
		t.Fatal(err)
	}
	// 15 个银行: 原资源的块 1 把 JEP106 银行 1(125 条)与银行 2(126 条)放在一起,
	// 提取脚本按实测对齐点拆开 → 共 15 行(见 tools/extract_idcodes)
	if len(tables) != 15 {
		t.Fatalf("表数 = %d, want 15", len(tables))
	}
	if tables[0][0] != "AMD" {
		t.Fatalf("bank0[0] = %q", tables[0][0])
	}
	// 关键锚点(实测真实 dump 校验过): 银行 2 第 30 条 = Corsair, 银行 5 第 27 条 = Crucial
	if got := ManufacturerName(0x02, 0x9E); got != "Corsair" {
		t.Fatalf("bank2[30] = %q, want Corsair", got)
	}
	if got := ManufacturerName(0x85, 0x9B); got != "Crucial Technology" {
		t.Fatalf("bank5[27] = %q, want Crucial Technology", got)
	}
}
