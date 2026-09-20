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

## 两个刻意的决定

- **不带 `RT_MANIFEST`**: 加清单会改变 DPI/虚拟化行为(现有 exe 没有清单), 要动就得
  单独验证一遍界面在高 DPI 下的表现, 不在当前范围内。
- **不加 `requireAdministrator`**: 程序**需要**管理员权限才能碰 SMBus, 但没有把提权写进
  清单 —— 启动时由程序自己检测 PawnIO 与权限并给出可读提示, 这样"先看界面再决定提权"
  是可能的, 也避免每次启动都弹 UAC。

## 图标本身

`tools/makeicon/main.go` 用纯标准库画: 深蓝圆角方块 + 绿色内存条(金手指 + 颗粒)+ 斜放的铅笔
(表示可读可写)。16/24 像素用**简化版**(去掉铅笔、加粗金手指), 因为那个尺寸下细节只会糊成一团;
渲染走 4 倍超采样再盒式降采样, 边缘才干净。
