package app

import (
	"testing"

	"spdrw/internal/spd"
)

// 校验状态查询: 前端把左侧 hex 视图的字节传进来, 后端必须回答
// "是否通过 + 哪些区域参与校验 + 哪些身份字段改了不用重算"。
func TestCRCStatusDDR4(t *testing.T) {
	a, _ := newWriteTestApp(t)
	dump := ddr4Fixture()

	st, err := a.CRCStatus(intsOf(dump))
	if err != nil {
		t.Fatalf("CRCStatus: %v", err)
	}
	if !st.Known || !st.OK {
		t.Fatalf("DDR4 语料应识别且校验通过: %+v", st)
	}
	if st.Generation != "DDR4" || st.Size != 512 {
		t.Fatalf("世代/长度: %+v", st)
	}
	if len(st.Ranges) != 2 || st.Ranges[0].End != 126 || st.Ranges[1].Start != 128 {
		t.Fatalf("应报 DDR4 两段: %+v", st.Ranges)
	}
	if st.Covered != 252 {
		t.Fatalf("覆盖字节数 = %d(want 252)", st.Covered)
	}
	// 序列号/日期/部件号都在校验范围之外 —— 这是用户实际遇到的问题:
	// 改这些字段不该提示"记得重算 CRC"。
	if !hasArea(st.FreeAreas, "序列号") || !hasArea(st.FreeAreas, "生产日期") || !hasArea(st.FreeAreas, "部件号") {
		t.Fatalf("身份区应报告为不参与校验: %+v", st.FreeAreas)
	}
	if hasArea(st.FreeAreas, "块 1") {
		t.Fatalf("FreeAreas 只该含身份字段: %+v", st.FreeAreas)
	}
}

// 改不影响校验的字节 → 仍然通过; 改校验区内的字节 → 不通过。判定必须与范围表一致。
func TestCRCStatusAfterEdits(t *testing.T) {
	a, _ := newWriteTestApp(t)
	dump := ddr4Fixture()

	// 1) 改序列号(0x145 = 325)与生产日期(0x143/0x144 = 323/324): 不影响校验
	for _, off := range []int{323, 324, 325} {
		mod := append([]byte{}, dump...)
		mod[off] ^= 0xFF
		st, err := a.CRCStatus(intsOf(mod))
		if err != nil {
			t.Fatalf("CRCStatus: %v", err)
		}
		if !st.OK {
			t.Fatalf("改身份区 %#x 后不应校验失败: %+v", off, st)
		}
	}

	// 2) 改主时序区(0x14 = 20)与 CRC 值本身: 校验必须失败
	for _, off := range []int{20, 126, 254} {
		mod := append([]byte{}, dump...)
		mod[off] ^= 0xFF
		st, err := a.CRCStatus(intsOf(mod))
		if err != nil {
			t.Fatalf("CRCStatus: %v", err)
		}
		if st.OK {
			t.Fatalf("改校验区内 %#x 后必须校验失败", off)
		}
		if st.Note == "" {
			t.Fatalf("校验失败时应有说明: %+v", st)
		}
	}
}

// 未知/损坏内容不能假装"校验通过"。
func TestCRCStatusUnknown(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.CRCStatus(nil); err == nil {
		t.Fatal("空内容应报错")
	}
	if _, err := a.CRCStatus([]int{0, 1, 999}); err == nil {
		t.Fatal("越界字节值应报错")
	}
	st, err := a.CRCStatus(intsOf(make([]byte, 300))) // 长度不是任何一代
	if err != nil {
		t.Fatalf("CRCStatus: %v", err)
	}
	if st.Known || st.OK {
		t.Fatalf("无法识别时应 Known=false OK=false: %+v", st)
	}
}

// 编辑器差异视图必须把改动分成"影响校验"与"不影响校验"两类。
func TestEditDiffCRCSplit(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatalf("EditLoadFromDevice: %v", err)
	}

	// 改序列号: 不影响校验 → 不需要重算 CRC
	if _, err := a.EditSetByte(325, 0xAB); err != nil {
		t.Fatalf("EditSetByte: %v", err)
	}
	d, err := a.EditDiff()
	if err != nil {
		t.Fatalf("EditDiff: %v", err)
	}
	if d.CRCFreeDirty != 1 || d.CRCDirty != 0 {
		t.Fatalf("序列号改动应算「不影响校验」: %+v", d)
	}
	if len(d.DirtyFree) != 1 || d.DirtyFree[0] != 325 || len(d.DirtyInCRC) != 0 {
		t.Fatalf("偏移分类错误: free=%v inCRC=%v", d.DirtyFree, d.DirtyInCRC)
	}
	if !d.CRCOK {
		t.Fatal("只改序列号后 CRC 应仍然通过")
	}

	// 再改一个主时序字节: 影响校验 → 必须重算
	if _, err := a.EditSetByte(20, 0x0F); err != nil {
		t.Fatalf("EditSetByte: %v", err)
	}
	d, _ = a.EditDiff()
	if d.CRCDirty != 1 || d.CRCFreeDirty != 1 {
		t.Fatalf("应各计 1: crcDirty=%d crcFreeDirty=%d", d.CRCDirty, d.CRCFreeDirty)
	}
	if len(d.DirtyInCRC) != 1 || d.DirtyInCRC[0] != 20 {
		t.Fatalf("影响校验的偏移应为 20: %v", d.DirtyInCRC)
	}
	if d.CRCOK {
		t.Fatal("改了校验区内的字节后 CRC 必须不通过(提示重算)")
	}

	// 重算 CRC 后: 偏移分类保持不变(改动还在), 但 CRC 恢复通过
	if _, err := a.EditFixCRC(); err != nil {
		t.Fatalf("EditFixCRC: %v", err)
	}
	d, _ = a.EditDiff()
	if !d.CRCOK {
		t.Fatal("重算后 CRC 必须通过")
	}
	if d.CRCFreeDirty != 1 {
		t.Fatalf("序列号的分类不应变化: %+v", d)
	}
	// 重算会新增 CRC 字节本身的改动(126/127), 它们同样算"影响校验"
	for _, off := range []int{20, 126, 127} {
		if !containsInt(d.DirtyInCRC, off) {
			t.Fatalf("重算后的影响校验偏移应含 %#x: %v", off, d.DirtyInCRC)
		}
	}
}

// CRC 字节本身的改动要单独计数(写入时这些字节排在最后)。
func TestEditDiffCRCBytes(t *testing.T) {
	a, _ := newWriteTestApp(t)
	if _, err := a.EditLoadFromDevice(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EditSetByte(126, 0x00); err != nil {
		t.Fatal(err)
	}
	d, err := a.EditDiff()
	if err != nil {
		t.Fatal(err)
	}
	if d.CRCFields != 1 {
		t.Fatalf("改 CRC 值本身应计入 crcFields: %+v", d)
	}
	if d.CRCDirty != 1 || d.CRCFreeDirty != 0 {
		t.Fatalf("CRC 值本身必须算「影响校验」: %+v", d)
	}
}

func intsOf(b []byte) []int {
	out := make([]int, len(b))
	for i, v := range b {
		out[i] = int(v)
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hasArea(areas []spd.Area, name string) bool {
	for _, a := range areas {
		if a.Name == name {
			return true
		}
	}
	return false
}

// 总线调优绑定: 测试用的 Fake 没有调优能力, 必须报"不支持"而不是假装成功。
func TestBusTuningUnsupportedOnFake(t *testing.T) {
	a, _ := newWriteTestApp(t)
	res, err := a.BusTuning()
	if err != nil {
		t.Fatalf("BusTuning: %v", err)
	}
	if res.Tunable {
		t.Fatalf("Fake 不应报告可调优: %+v", res)
	}
	if res.Note == "" {
		t.Fatal("应说明为什么不可调优")
	}
	if _, err := a.SetSleepMode(0); err == nil {
		t.Fatal("不可调优时 SetSleepMode 应报错")
	}
	// 未连接控制器时也要报可读错误
	empty := New()
	if _, err := empty.BusTuning(); err == nil {
		t.Fatal("未连接时应报错")
	}
	if _, err := empty.SetSleepMode(0); err == nil {
		t.Fatal("未连接时 SetSleepMode 应报错")
	}
}
