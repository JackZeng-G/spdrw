# Windows 资源(图标 + 版本信息)

`winres.json` 是给 [go-winres](https://github.com/tc-hib/go-winres) 的配置: 它把**图标**与
**版本信息**(产品名、版本号、版权 `by jackzeng 2026`)编成一个 Go 目标文件
`rsrc_windows_amd64.syso`, 放在**仓库根目录**。

为什么需要它: 本项目不跑 `wails build`(那是 Windows 上的重活), 而是直接
`go build -tags desktop,production` 交叉编译。Go 会自动把包目录里名字带
`_windows_amd64` 的 `.syso` 链进 windows/amd64 的产物, 图标与文件属性就是这么进去的。
**在 Linux 上构建时它被忽略**(后缀不带当前 GOOS/GOARCH), 所以跨平台编译不受影响。

## 改了图标 / 改了版权信息之后

```bash
# 1) 重画图标(纯标准库, 不依赖 ImageMagick/PIL):
go run ./tools/makeicon                      # → build/icon/appicon.png + appicon.ico + 各尺寸 PNG

# 2) 重新生成资源目标文件(需要能访问 Go module 代理):
go run github.com/tc-hib/go-winres@latest make \
    --in build/winres/winres.json --arch amd64 --out rsrc    # → ./rsrc_windows_amd64.syso

# 3) 重新构建并自检(会校验 .rsrc 节、版本信息与图标字节确实在 exe 里):
./build.sh
go test ./internal/app/ -run TestBuiltExeHasWindowsResources -v
```

## 清单(RT_MANIFEST): 提权与 DPI

清单是**故意加上的**, 两个要点:

- **`requestedExecutionLevel = requireAdministrator`**: 这个工具没有管理员权限就碰不到
  SMBus(读也一样), 与其让用户自己"右键 → 以管理员身份运行", 不如让 Windows 在双击时
  弹一次 UAC。**拒绝提权就启动不了**, 这是有意的取舍。
  想改回去: 把 `winres.json` 里 `execution-level` 改成 `as invoker` 重新生成 syso 即可。
- **DPI 感知与 `wails build` 的模板一致**(`dpiAware=true` + `dpiAwareness=permonitorv2,system`,
  外加 Common-Controls v6): 这样界面在高 DPI 下由 WebView2 按显示器缩放渲染, 不会糊。
  本项目不用 wails CLI, 所以这份清单得自己带, 否则 exe 是 DPI 不感知的(整窗被位图拉伸)。
- 程序内部仍保留"权限不足"的检测与可读提示作为兜底(例如有人手工改过清单, 或用任务计划
  程序以低权限启动)。

## 图标本身

`tools/makeicon/main.go` 用纯标准库画: 深蓝圆角方块 + 绿色内存条(金手指 + 颗粒)+ 斜放的铅笔
(表示可读可写)。16/24 像素用**简化版**(去掉铅笔、加粗金手指), 因为那个尺寸下细节只会糊成一团;
渲染走 4 倍超采样再盒式降采样, 边缘才干净。
