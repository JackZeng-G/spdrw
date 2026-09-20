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
	exe := filepath.Join("..", "..", "bin", "SPDReaderWriter.exe")
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
	}
	for name, m := range markers {
		if !bytes.Contains(data, m) {
			t.Errorf("exe 内缺少 %s(标记 %q) —— 嵌入资源可能在构建时丢了", name, m)
		}
	}
}
