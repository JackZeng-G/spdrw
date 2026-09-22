package spd

import (
	"fmt"
	"regexp"
	"sort"
	"testing"
)

// infoSpan 把字段的 Offset 文本解析成字节区间(闭区间 [start,end])。
// 需要认识的形态(与前端 parseFieldSpans 一致, 另加括号注记):
//
//	"0x050"          单字节
//	"0x140-0x141"    起-止(含两端)
//	"0x149-20B"      起 + 字节长
//	"0x18/0x7B"      多处(斜杠分隔)
//	"0x1B[3:0]"      nibble 字段(按整个字节算覆盖)
//	"0x46-0x47(ps)"  带单位注记
func infoSpan(s string) [][2]int {
	var out [][2]int
	for _, part := range regexp.MustCompile(`\s*/\s*`).Split(s, -1) {
		part = regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(part, "")  // 去掉 "(ps)" 注记
		part = regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(part, "") // 去掉 "[3:0]" nibble
		m := regexp.MustCompile(`^0x([0-9A-Fa-f]+)(?:\s*-\s*(?:0x([0-9A-Fa-f]+)|(\d+)\s*B))?$`).
			FindStringSubmatch(part)
		if m == nil {
			continue
		}
		start := parseHex(m[1])
		end := start
		if m[2] != "" {
			end = parseHex(m[2])
		} else if m[3] != "" {
			end = start + atoi(m[3]) - 1
		}
		if end >= start {
			out = append(out, [2]int{start, end})
		}
	}
	return out
}

func parseHex(s string) int { v := 0; fmt.Sscanf(s, "%x", &v); return v }
func atoi(s string) int     { v := 0; fmt.Sscanf(s, "%d", &v); return v }

// TestInfoFieldsCoverEveryByte 是"提示补全"的硬性保证:
// 原始数据视图里每个字节(除 CRC 值本身, 它有专属色带与"CRC 值本身"提示)都必须
// 能从某个字段(可编辑字段或只读布局说明)拿到名字 —— 不允许再出现"未映射到字段"。
func TestInfoFieldsCoverEveryByte(t *testing.T) {
	cases := []struct {
		name string
		dump []byte
	}{
		{"DDR3", makeDDR3(t)},
		{"DDR4", makeDDR4(t)},
		{"DDR5", makeDDR5(t)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := editorFor(t, c.dump)
			size := len(c.dump)
			covered := make([]bool, size)
			for _, off := range CRCOffsets(c.dump) {
				if off < size {
					covered[off] = true
				}
			}
			for _, f := range e.Fields() {
				for _, sp := range infoSpan(f.Offset) {
					for off := sp[0]; off <= sp[1] && off < size; off++ {
						if off >= 0 {
							covered[off] = true
						}
					}
				}
			}
			var missing []int
			for off := 0; off < size; off++ {
				if !covered[off] {
					missing = append(missing, off)
				}
			}
			if len(missing) > 0 {
				// 压缩成区间, 报错可读
				ranges := fmtRanged(missing)
				t.Errorf("%s: %d 个字节悬停无说明: %s", c.name, len(missing), ranges)
			}
		})
	}
}

func fmtRanged(offs []int) string {
	sort.Ints(offs)
	var out string
	for i := 0; i < len(offs); {
		j := i
		for j+1 < len(offs) && offs[j+1] == offs[j]+1 {
			j++
		}
		if i == j {
			out += fmt.Sprintf("0x%03X ", offs[i])
		} else {
			out += fmt.Sprintf("0x%03X-0x%03X ", offs[i], offs[j])
		}
		i = j + 1
	}
	return out
}

// SetField 必须拒绝只读布局说明字段(防手滑/防脚本遍历写字段)。
func TestSetFieldRejectsInfoFields(t *testing.T) {
	e := editorFor(t, makeDDR4(t))
	var infoKey string
	for _, f := range e.Fields() {
		if f.Kind == "info" {
			infoKey = f.Key
			break
		}
	}
	if infoKey == "" {
		t.Fatal("DDR4 编辑器里没有任何 info 字段")
	}
	if err := e.SetField(infoKey, "1"); err == nil {
		t.Fatalf("SetField(%q) 应报错(只读)", infoKey)
	}
}
