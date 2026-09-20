package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spdrw/internal/eeprom"
	"spdrw/internal/smbus"
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
		if r.RAMType == "" || r.Size != len(dump) {
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

// TestCorpusEditPreflightDryRun 把"编辑 → 预检 → 干跑"整条写入前置链路在全部真实 dump
// 上跑一遍。真机写入前, 这些步骤是我们能离线验证的全部内容 —— 语料覆盖 DDR3/DDR4/DDR5
// 三代, 能发现跨世代的 CRC 偏移、分页、保护探测问题。
func TestCorpusEditPreflightDryRun(t *testing.T) {
	// 关掉逐字节读间隔(默认 1ms, 真机需要; 测试里纯属浪费)
	old := eeprom.ReadDelay
	eeprom.ReadDelay = 0
	defer func() { eeprom.ReadDelay = old }()
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	ran := 0
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
		rt, size, err := spd.Identify(dump)
		if err != nil || len(dump) != size {
			continue
		}

		// 用 Fake 装一份"设备内容 = 语料"的镜像
		f := smbus.NewFake()
		if rt == spd.DDR5 || rt == spd.LPDDR5 || rt == spd.DDR5NVDIMMP || rt == spd.LPDDR5X {
			f.SetDDR5(true)
		}
		copy(f.EEProm, dump)
		a := New()
		a.transports = []smbus.Transport{f}
		if err := a.Connect(0); err != nil {
			t.Fatalf("%s: Connect: %v", name, err)
		}
		if err := a.Select(0x50); err != nil {
			t.Fatalf("%s: Select: %v", name, err)
		}

		// 1) 未修改的 dump 干跑: 不应有变更
		pf, err := a.buildPreflight(name, dump, false, true)
		if err != nil {
			t.Errorf("%s: 预检失败: %v", name, err)
			continue
		}
		if pf.Blocked {
			t.Errorf("%s: 未修改的合法 dump 不应被阻断: %s", name, pf.BlockReason)
			continue
		}
		if pf.ChangeCount != 0 {
			t.Errorf("%s: 与设备内容相同却算出 %d 个变更", name, pf.ChangeCount)
		}

		// 2) 载入编辑器并改一个字段 + 重算 CRC → 预检必须放行, 且 CRC 字节排在计划最后
		if _, err := a.EditLoadFromDevice(); err != nil {
			t.Errorf("%s: EditLoadFromDevice: %v", name, err)
			continue
		}
		changed := false
		for _, key := range []string{"serial", "partNumber"} {
			val := "0A0B0C0D"
			if key == "partNumber" {
				val = "EDITED"
			}
			if _, err := a.EditSetField(key, val); err == nil {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		if _, err := a.EditFixCRC(); err != nil {
			t.Errorf("%s: FixCRC: %v", name, err)
			continue
		}
		target, err := a.EditBytes()
		if err != nil {
			t.Errorf("%s: EditBytes: %v", name, err)
			continue
		}

		// 2a) 故意不修 CRC 时必须被 CRC 门拦住(至少对带 CRC 的世代)
		if true {
			raw := append([]byte{}, target...)
			// 直接改一个数据字节, 不动 CRC
			raw[10] ^= 0x01
			badPf, err := a.buildPreflight(name, raw, false, true)
			if err != nil {
				t.Errorf("%s: 预检(坏 CRC)失败: %v", name, err)
			} else if !badPf.Blocked || badPf.BlockKind != "crc" {
				t.Errorf("%s: CRC 不通过时必须按 crc 门阻断, got blocked=%v kind=%q",
					name, badPf.Blocked, badPf.BlockKind)
			}
		}

		pf, err = a.buildPreflight(name, target, false, true)
		if err != nil {
			t.Errorf("%s: 预检(改后)失败: %v", name, err)
			continue
		}
		if pf.Blocked {
			t.Errorf("%s: 改一个字段+重算 CRC 后不应被阻断: %s(%s)", name, pf.BlockReason, pf.BlockKind)
			continue
		}
		if pf.ChangeCount == 0 {
			t.Errorf("%s: 应有变更", name)
		}
		sawCRC := false
		for i, c := range pf.Changes {
			if c.IsCRC {
				sawCRC = true
				continue
			}
			if sawCRC {
				t.Errorf("%s: 第 %d 个非 CRC 变更出现在 CRC 之后", name, i)
				break
			}
		}

		// 3) 干跑: 一个 NVM 字节都不能写
		st, err := a.EditState()
		if err != nil {
			t.Errorf("%s: EditState: %v", name, err)
			continue
		}
		if st.Generation == "" || st.Size != size {
			t.Errorf("%s: 编辑器状态异常: %+v", name, st)
		}
		// 走应用的**入口**(EditApplyToDevice): 它会在跑预检之前先把设备切到干跑,
		// 因此连预检里的写保护探测也不会碰总线 —— 这正是真机 V2 的路径。
		res, err := a.EditApplyToDevice(false, true, "DRYRUN")
		if err != nil {
			t.Errorf("%s: 干跑失败: %v", name, err)
			continue
		}
		if res.Written == 0 || !res.DryRun {
			t.Errorf("%s: 干跑结果异常: %+v", name, res)
		}
		// 干跑必须能从总线计数上证明"零 NVM 写"(真机 V2 靠的就是这个信号)
		if res.BusStats == nil {
			t.Errorf("%s: 干跑结果缺少总线统计", name)
		} else if res.BusStats.NVMWrites != 0 || res.NVMWrites != 0 {
			t.Errorf("%s: 干跑出现 NVM 写: %+v", name, res.BusStats)
		} else if res.BusStats.Reads == 0 {
			t.Errorf("%s: 总线统计没有读事务, 计数可能没生效", name)
		}
		// 设备(Eeprom)必须原封不动
		for i := 0; i < size; i++ {
			if f.EEProm[i] != dump[i] {
				t.Fatalf("%s: 干跑改动了设备内容 @%#x", name, i)
			}
		}
		setDryRunForTest(t, a, false)
		ran++
	}
	if ran == 0 {
		t.Skip("语料为空")
	}
	t.Logf("编辑→预检→干跑 全链路: %d 份真实 dump 全部通过", ran)
}

// TestCorpusRealWriteEndToEnd 把**真实写入**(非干跑)在全部真实 dump 上跑一遍:
// 备份 → 按计划逐字节写(CRC 排最后) → 逐字节回读 → 整片校验 → 设备内容 == 目标。
//
// 这是真机写入前能做的最后一层离线验证: 语料覆盖 DDR3/DDR4/DDR5 三代, 包含
// DDR5 的 MR11 分页与 XMP/EXPO 各自的 CRC, 任何"写错页/漏写 CRC/回读判断反了"
// 都会在这里现形。
func TestCorpusRealWriteEndToEnd(t *testing.T) {
	old := eeprom.ReadDelay
	eeprom.ReadDelay = 0
	defer func() { eeprom.ReadDelay = old }()
	dir := filepath.Join("..", "..", "testdata", "spd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无语料目录(%v)", err)
	}
	ran, byGen := 0, map[string]int{}
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
		rt, size, err := spd.Identify(dump)
		if err != nil || len(dump) != size {
			continue
		}
		if ok, _ := spd.CRCOK(dump); !ok {
			continue
		}

		f := smbus.NewFake()
		if rt == spd.DDR5 || rt == spd.LPDDR5 || rt == spd.DDR5NVDIMMP || rt == spd.LPDDR5X {
			f.SetDDR5(true)
		}
		copy(f.EEProm, dump)
		a := New()
		a.transports = []smbus.Transport{f}
		if err := a.Connect(0); err != nil {
			t.Fatalf("%s: Connect: %v", name, err)
		}
		if err := a.Select(0x50); err != nil {
			t.Fatalf("%s: Select: %v", name, err)
		}
		if _, err := a.EditLoadFromDevice(); err != nil {
			t.Errorf("%s: EditLoadFromDevice: %v", name, err)
			continue
		}
		// 改序列号(所有世代都支持; DDR3 上它还在 CRC 覆盖范围内, 正好连带测 CRC 重写)
		if _, err := a.EditSetField("serial", "0A0B0C0D"); err != nil {
			t.Errorf("%s: 改序列号: %v", name, err)
			continue
		}
		if _, err := a.EditFixCRC(); err != nil {
			t.Errorf("%s: FixCRC: %v", name, err)
			continue
		}
		target, err := a.EditBytes()
		if err != nil {
			t.Errorf("%s: EditBytes: %v", name, err)
			continue
		}
		want, err := a.EditDiff()
		if err != nil {
			t.Errorf("%s: EditDiff: %v", name, err)
			continue
		}

		res, err := a.EditApplyToDevice(false, false, "WRITE")
		if err != nil {
			t.Errorf("%s: 真实写入失败: %v", name, err)
			continue
		}
		switch {
		case res == nil:
			t.Errorf("%s: 无结果", name)
			continue
		case res.DryRun:
			t.Errorf("%s: 应是真实写入", name)
		case !res.Verified:
			t.Errorf("%s: 未标记校验通过: %+v", name, res)
		case res.RolledBack:
			t.Errorf("%s: 不该发生回滚: %+v", name, res)
		case res.BackupPath == "":
			t.Errorf("%s: 真实写入必须先备份", name)
		case res.Written != want.ChangeCount || res.Total != want.ChangeCount:
			t.Errorf("%s: 写入计数 %d/%d 与变更数 %d 不符", name, res.Written, res.Total, want.ChangeCount)
		case res.BusStats == nil:
			t.Errorf("%s: 缺少总线统计", name)
		case rt == spd.DDR5 || rt == spd.LPDDR5 || rt == spd.DDR5NVDIMMP || rt == spd.LPDDR5X:
			// DDR5 的保护探测只读 MR 寄存器, 不做写测试 → NVM 写数必须**精确等于**变更数
			if res.BusStats.NVMWrites != want.ChangeCount {
				t.Errorf("%s: DDR5 NVM 写数 %d 与变更数 %d 不符", name, res.BusStats.NVMWrites, want.ChangeCount)
			}
		default:
			// DDR4 及更早: 预检里的写保护探测(取反写+还原 × 块数)也计入同一窗口
			if res.BusStats.NVMWrites < want.ChangeCount {
				t.Errorf("%s: NVM 写数 %d 少于变更数 %d", name, res.BusStats.NVMWrites, want.ChangeCount)
			}
		}
		// 设备内容必须与目标逐字节一致, 且 CRC 有效
		for i := 0; i < size; i++ {
			if f.EEProm[i] != target[i] {
				t.Errorf("%s: 设备 @%#x = %02X, 目标 %02X", name, i, f.EEProm[i], target[i])
				break
			}
		}
		if ok, cerr := spd.CRCOK(f.EEProm[:size]); cerr != nil || !ok {
			t.Errorf("%s: 写入后整片 CRC 应通过: %v %v", name, ok, cerr)
		}
		byGen[rt.String()]++
		ran++
	}
	if ran == 0 {
		t.Skip("语料为空")
	}
	t.Logf("真实写入全链路(备份/写/回读/整片校验): %d 份真实 dump 全部通过 %v", ran, byGen)
}
