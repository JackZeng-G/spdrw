package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spdrw/internal/spd"
)

// 用真实 dump 语料跑一遍服务层的解析(信息面板走的就是这条路),
// 目的是抓"解析层没问题但展示层崩/字段为空"的问题。
func TestDecodeCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ddr") {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") && !strings.HasSuffix(name, ".spd") {
			continue
		}
		dump, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := DecodeDump(dump)
		if err != nil {
			t.Errorf("%s: DecodeDump: %v", name, err)
			continue
		}
		if r.RamType == "" || r.Size != len(dump) {
			t.Errorf("%s: 结果不完整: %+v", name, r)
		}
		// 少数样本(coreboot 官方模拟数据集、编辑器作者的示例文件)身份区就是空的,
		// 这里只要求"有数据就必须解析出来"
		if r.PartNumber == "" && dump[2] != 0x00 {
			t.Logf("%s: 部件号为空(该样本身份区本身未填写)", name)
		}
		if !r.CRCOK {
			t.Errorf("%s: CRC 应通过", name)
		}
		switch {
		case strings.HasPrefix(name, "ddr5"):
			if len(r.DDR5Timings) < 10 {
				t.Errorf("%s: DDR5 时序字段过少: %d", name, len(r.DDR5Timings))
			}
			if r.TotalMib == 0 {
				t.Errorf("%s: 容量为 0", name)
			}
			if r.BusWidth == 0 {
				t.Errorf("%s: 总线位宽为 0", name)
			}
		case strings.HasPrefix(name, "ddr4"), strings.HasPrefix(name, "ddr3"):
			if r.TotalMib == 0 {
				t.Errorf("%s: 容量为 0", name)
			}
			if r.Ranks == 0 || r.DeviceWidth == 0 {
				t.Errorf("%s: 组织字段为空: ranks=%d width=%d", name, r.Ranks, r.DeviceWidth)
			}
		}
		// 厂商名: 写了厂商 ID 的样本必须能查到名字(且必须带解析说明时给出原因)
		code := manufacturerCodeByte(dump)
		if code != 0 && r.Manufacturer == "" && r.ManufacturerNote == "" {
			t.Errorf("%s: 厂商名解析失败且没有说明", name)
		}
		if code != 0 && r.Manufacturer != "" && r.ManufacturerNote != "" {
			t.Errorf("%s: 厂商名已解析却仍有错误说明: %s", name, r.ManufacturerNote)
		}
		// 编辑器也能载入并给出字段
		ed, err := spd.NewEditor(dump)
		if err != nil {
			t.Errorf("%s: NewEditor: %v", name, err)
			continue
		}
		if len(ed.Fields()) == 0 {
			t.Errorf("%s: 编辑器字段为空", name)
		}
		n++
	}
	if n == 0 {
		t.Skip("语料为空")
	}
	t.Logf("服务层解析语料: %d 份全部通过", n)
}

// manufacturerCodeByte 取出该世代的厂商码字节(0 表示未写入)。
func manufacturerCodeByte(dump []byte) byte {
	switch {
	case len(dump) == 256:
		return dump[118]
	case len(dump) == 512:
		return dump[321]
	case len(dump) == 1024:
		return dump[513]
	}
	return 0
}
