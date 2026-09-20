# 构建脚本 (Windows PowerShell): 必须带 desktop,production 标签, 否则启动时报
# "Wails applications will not build without the correct build tags"
$tags = "desktop,production"
$ldflags = "-H windowsgui -s -w"
go build -tags $tags -ldflags $ldflags -trimpath -o bin\SPDReaderWriter.exe .
if ($LASTEXITCODE -eq 0) { Write-Host "OK -> bin\SPDReaderWriter.exe" }
