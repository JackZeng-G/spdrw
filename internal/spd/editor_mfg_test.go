package spd

import "testing"

// 常用厂商预设: 名字必须都能在 JEP106 表里查到(否则下拉里缺项), ID 必须与表一致。
func TestMfgPresets(t *testing.T) {
	presets := MfgPresets()
	if len(presets) < 20 {
		t.Fatalf("预设太少: %d 条(名字与 idcodes.json 对不上的会被跳过, 检查拼写)", len(presets))
	}
	byName := map[string]MfgEntry{}
	for _, m := range presets {
		if m.Name == "" || m.Code == 0 {
			t.Fatalf("预设条目异常: %+v", m)
		}
		byName[m.Name] = m
	}
	// 抽几个知名厂商对照(表中原始条目), 防止名字改成别名后查不到
	for _, want := range []string{"Samsung", "SK Hynix", "Micron Technology", "Kingston", "CXMT"} {
		m, ok := byName[want]
		if !ok {
			t.Fatalf("预设缺少 %q(现表里应能精确查到)", want)
		}
		all := SearchManufacturers(want, 500)
		var exact []MfgEntry
		for _, e := range all {
			if e.Name == want {
				exact = append(exact, e)
			}
		}
		if len(exact) == 0 {
			t.Fatalf("%q 在搜索结果里反而没有(表可能变了)", want)
		}
		found := false
		for _, e := range exact {
			if e == m {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q 预设 ID(%+v)与表中条目不一致", want, m)
		}
	}
}
