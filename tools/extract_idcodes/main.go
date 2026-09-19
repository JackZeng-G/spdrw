// extract_idcodes 从原 C# 仓库 Resources.cs 提取 JEP106 厂商表,
// 解 gzip 后按 0x0A 切分为名字数组,输出 internal/spd/data/idcodes.json。
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "用法: extract_idcodes <Resources.cs> <out.json>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	// 截取 IdCodes = { ... } 段
	i := strings.Index(string(src), "IdCodes = {")
	if i < 0 {
		panic("IdCodes 段未找到")
	}
	seg := string(src[i:])

	// 按 "new byte[] {" 切分 continuation blocks
	reBlock := regexp.MustCompile(`new byte\[\] \{`)
	loc := reBlock.FindAllStringIndex(seg, -1)
	fmt.Fprintf(os.Stderr, "blocks: %d\n", len(loc))

	var tables [][]string
	reByte := regexp.MustCompile(`0x([0-9A-Fa-f]{2})`)
	reComment := regexp.MustCompile(`//[^\n]*`)
	for b := 0; b < len(loc); b++ {
		start := loc[b][1]
		end := len(seg)
		if b+1 < len(loc) {
			end = loc[b+1][0]
		}
		body := reComment.ReplaceAllString(seg[start:end], " ")
		// 在块结束的 "};" 处截断
		if j := strings.Index(body, "};"); j >= 0 {
			body = body[:j]
		}
		var data []byte
		for _, m := range reByte.FindAllStringSubmatch(body, -1) {
			v, _ := strconv.ParseUint(m[1], 16, 8)
			data = append(data, byte(v))
		}
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			panic(fmt.Sprintf("block %d: %v", b, err))
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			panic(fmt.Sprintf("block %d: %v", b, err))
		}
		names := strings.Split(string(raw), "\x0A")
		var clean []string
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n != "" {
				clean = append(clean, n)
			}
		}
		tables = append(tables, clean)
		fmt.Fprintf(os.Stderr, "block %d: %d 厂商\n", b, len(clean))
	}
	out, _ := json.MarshalIndent(tables, "", "  ")
	if err := os.WriteFile(os.Args[2], out, 0o644); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "写入 %s\n", os.Args[2])
}
