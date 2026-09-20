package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestBuiltExeEmbedsAssets 校验发布产物真的把前端与 PawnIO 模块打进去了。
//
// 为什么值得测: 前端是 go:embed 的直接产物, 构建标签/路径一改就可能悄悄丢掉,
// 而丢掉的症状是"程序能跑但界面空白"或"驱动模块释放失败" —— 只有到真机上才发现。
// 产物不存在时跳过(未构建的环境不该因此失败)。
func TestBuiltExeEmbedsAssets(t *testing.T) {
	exe := filepath.Join("..", "..", "build", "SPDReaderWriter.exe")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Skipf("未找到构建产物(%v)", err)
	}
	if len(data) < 4<<20 {
		t.Fatalf("exe 只有 %d 字节, 明显不完整(前端 + 三个 PawnIO 模块约 12MB)", len(data))
	}
	markers := map[string][]byte{
		"前端页面(index.html)": []byte(`id="hexgrid"`),
		"前端脚本(app.js)":     []byte("WPStatus"),
		"前端样式(style.css)":  []byte("hexgrid"),
		"PawnIO 模块":        []byte("Smbus"),
		"PawnIOLib.dll":    []byte("PawnIOLib"),
		"中文界面文案":           []byte("写入设备…"),
		"顶栏文件操作(新)":        []byte("打开 dump 文件…"),
		"校验状态面板(新)":        []byte(`id="crc-status"`),
		"校验状态绑定(新)":        []byte("CRCStatus"),
		"版本日志格式":           []byte("SPD Reader Writer (Go) build"),
		"版权署名(界面)":         []byte("by jackzeng 2026"),
		"写入区标题(新)":         []byte("写入SPD芯片"),
	}
	for name, m := range markers {
		if !bytes.Contains(data, m) {
			t.Errorf("exe 内缺少 %s(标记 %q) —— 嵌入资源可能在构建时丢了", name, m)
		}
	}
}

// TestBuiltExeHasWindowsResources 校验 Windows 资源(图标 + 版本信息)真的链进了 exe。
//
// 资源来自仓库根目录的 rsrc_windows_amd64.syso(由 build/winres/winres.json + 图标生成,
// 见 README「构建」一节)。它很容易在"重新编译 / 换构建命令"时被漏掉 —— 症状是 exe 在
// 资源管理器里是个白板图标、属性里没有版本信息, 而程序本身完全正常, 所以必须机械守住。
func TestBuiltExeHasWindowsResources(t *testing.T) {
	exe := filepath.Join("..", "..", "build", "SPDReaderWriter.exe")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Skipf("未找到构建产物(%v)", err)
	}
	if !bytes.Contains(data, []byte(".rsrc")) {
		t.Fatal("exe 里没有 .rsrc 节 —— 图标与版本信息没链进去(rsrc_windows_amd64.syso 缺失?)")
	}
	// 版本信息按 UTF-16LE 存, 所以用 UTF-16 形式找
	for _, s := range []string{"by jackzeng 2026", "SPD Reader Writer (Go)", "jackzeng"} {
		u16 := utf16le(s)
		if !bytes.Contains(data, u16) {
			t.Errorf("版本信息里缺少 %q", s)
		}
	}
	// 清单: 双击自动提权(requireAdministrator)与 DPI 感知都在这里面
	for _, want := range []string{"requireAdministrator", "permonitorv2", "Microsoft.Windows.Common-Controls"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("应用清单里缺少 %q —— 提权/DPI 感知会在构建时丢掉", want)
		}
	}
	// 图标: RT_ICON 直接存 PNG 原文件, 所以 256px 的图标字节应原样出现
	icon, err := os.ReadFile(filepath.Join("..", "..", "build", "icon", "appicon-256.png"))
	if err != nil {
		t.Skipf("未找到图标源(%v)", err)
	}
	if !bytes.Contains(data, icon) {
		t.Error("exe 内找不到 256px 图标数据 —— 图标资源没嵌进去")
	}
}

func utf16le(s string) []byte {
	var out []byte
	for _, r := range s {
		if r < 0x10000 {
			out = append(out, byte(r), byte(r>>8))
			continue
		}
		r -= 0x10000
		hi, lo := 0xD800+uint16(r>>10), 0xDC00+uint16(r&0x3FF)
		out = append(out, byte(lo), byte(lo>>8), byte(hi), byte(hi>>8))
	}
	return out
}
