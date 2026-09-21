package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"spdrw/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.New()

	err := wails.Run(&options.App{
		Title:     "SPD 读写 Go + PawnIO by jackzeng 2026",
		Width:     1200,
		Height:    866, // 顶栏两行化后给右侧"SPD 信息"框多留约三行文字的高度
		MinWidth:  960,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 24, G: 26, B: 32, A: 1},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
		OnStartup: func(ctx context.Context) {
			// app 服务层的 log/进度事件 → Wails 前端事件
			a.Emit = func(event string, data ...interface{}) {
				runtime.EventsEmit(ctx, event, data...)
			}
			// 对话框必须从 Go 侧调用(v2 的 JS 运行时无对话框 API)
			a.SaveDialog = func(title, defaultName string) (string, error) {
				return runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
					Title:           title,
					DefaultFilename: defaultName,
				})
			}
			a.OpenDialog = func(title string) (string, error) {
				return runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{Title: title})
			}
			a.LogVersion()
		},
		// 关窗时释放设备会话与全局 SMBus 锁(否则句柄与互斥量要等进程退出才回收)
		OnShutdown: func(ctx context.Context) { a.Shutdown() },
		Bind: []interface{}{
			a,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
