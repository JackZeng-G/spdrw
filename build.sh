#!/usr/bin/env bash
# 构建(在 Linux/macOS 上交叉编译 Windows exe, 不需要 CGO)。
#
# 图标与版本信息来自仓库根目录的 rsrc_windows_amd64.syso —— Go 会自动把它链进
# windows/amd64 的产物; 改了图标或版本信息后按 build/winres/README.md 重新生成。
# Windows 上用 build.ps1(等价)。
set -euo pipefail
cd "$(dirname "$0")"

export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
hash=$(git rev-parse --short HEAD 2>/dev/null || echo dev)

go build -tags desktop,production -trimpath \
	-ldflags "-H windowsgui -s -w -X spdrw/internal/app.BuildHash=$hash" \
	-o build/SPDReaderWriter.exe .

echo "OK -> build/SPDReaderWriter.exe (build $hash)"
