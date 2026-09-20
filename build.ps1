# 构建脚本 (Windows PowerShell): 必须带 desktop,production 标签, 否则启动时报
# "Wails applications will not build without the correct build tags"
$tags = "desktop,production"
$hash = git rev-parse --short HEAD
$ldflags = "-H windowsgui -s -w -X spdrw/internal/app.BuildHash=$hash"
go build -tags $tags -ldflags $ldflags -trimpath -o bin\SPDReaderWriter.exe .
if ($LASTEXITCODE -eq 0) { Write-Host "OK -> bin\SPDReaderWriter.exe" }
