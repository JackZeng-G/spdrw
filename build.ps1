# 构建脚本 (Windows PowerShell): 必须带 desktop,production 标签, 否则启动时报
# "Wails applications will not build without the correct build tags"
$tags = "desktop,production"
$hash = git rev-parse --short HEAD 2>$null
if (-not $hash) { $hash = "dev" }   # 与 build.sh 的 `|| echo dev` 对齐: 非 git 环境兜底
$ldflags = "-H windowsgui -s -w -X spdrw/internal/app.BuildHash=$hash"
go build -tags $tags -ldflags $ldflags -trimpath -o build\SPDReaderWriter.exe .
if ($LASTEXITCODE -eq 0) { Write-Host "OK -> build\SPDReaderWriter.exe" }
exit $LASTEXITCODE   # 失败时让 CI/调用方拿到非零退出码(此前失败也返回 0)
