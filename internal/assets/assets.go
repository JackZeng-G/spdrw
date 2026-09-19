// Package assets 内嵌 PawnIO 官方二进制(签名模块与用户态库)。
//
// 来源与许可见仓库 third_party/pawnio/README.md:
// 模块 (C) namazso/Steve-Tech, LGPL-2.1-or-later; PawnIOLib.dll 为 PawnIO 官方用户态库, LGPL-2.1。
package assets

import "embed"

//go:embed all:files
var FS embed.FS

// MustBytes 返回指定资产文件内容; 缺文件属编译期装配错误,直接 panic。
func MustBytes(name string) []byte {
	b, err := FS.ReadFile("files/" + name)
	if err != nil {
		panic("assets: 缺少内嵌文件 " + name + ": " + err.Error())
	}
	return b
}
