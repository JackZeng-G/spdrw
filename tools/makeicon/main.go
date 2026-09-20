// makeicon 生成这个工具的图标(纯标准库, 不依赖 ImageMagick/PIL/rsvg)。
//
// 用法:
//
//	go run ./tools/makeicon            # 写 build/icon/appicon.png 与 appicon.ico
//	go run ./tools/makeicon -out DIR   # 换输出目录
//
// 设计意图(按用途):
//   - 主体是一根**内存条**(绿色 PCB + 金色金手指 + 深色颗粒): 一眼看出"在操作 SPD/内存条"。
//   - 一支**斜放的铅笔**: 表示这个工具不只是读, 还能改(写 SPD)。
//   - 底色是与界面一致的深蓝圆角方块 —— 不透明背景, 浅色/深色任务栏都立得住。
//
// 两个细节:
//   - 渲染用 4 倍超采样再做盒式降采样(标准库没有抗锯齿), 小尺寸边缘才干净。
//   - 16/24 像素用**简化版**(去掉铅笔、加粗金手指与颗粒): 那个尺寸下细节只会糊成一团。
//
// 生成物: appicon.png(512, 给 `wails build` 与文档用) + appicon.ico(16/24/32/48/64/128/256,
// ≤64 用 32 位 BMP 条目、≥128 用 PNG 条目)。
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	baseSize = 512 // 逻辑尺寸(图标坐标系)
	ss       = 4   // 超采样倍数
)

// 尺寸表: ≤24 用简化版
var icoSizes = []struct {
	px     int
	simple bool
}{
	{16, true}, {24, true}, {32, false}, {48, false}, {64, false}, {128, false}, {256, false},
}

func main() {
	out := flag.String("out", filepath.Join("build", "icon"), "输出目录")
	flag.Parse()

	full := draw(baseSize*ss, false)
	simpleBig := draw(baseSize*ss, true)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	pngPath := filepath.Join(*out, "appicon.png")
	if err := writePNG(pngPath, downsample(full, baseSize)); err != nil {
		fatal(err)
	}

	// 逐尺寸挑"完整版/简化版"再降采样: 既进 .ico, 也单独落一份 PNG ——
	// go-winres 只吃 PNG(golang 标准库没有 ICO 解码器), 生成 Windows 资源要用它们。
	var entries []icoEntry
	for _, s := range icoSizes {
		src := full
		if s.simple {
			src = simpleBig
		}
		img := downsample(src, s.px)
		if err := writePNG(filepath.Join(*out, fmt.Sprintf("appicon-%d.png", s.px)), img); err != nil {
			fatal(err)
		}
		var data []byte
		if s.px >= 128 {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				fatal(err)
			}
			data = buf.Bytes()
		} else {
			data = bmpEntry(img)
		}
		entries = append(entries, icoEntry{s.px, data})
	}
	icoPath := filepath.Join(*out, "appicon.ico")
	if err := writeICO(icoPath, entries); err != nil {
		fatal(err)
	}
	fmt.Printf("已生成 %s(512x512)与 %s(%d 种尺寸, 其中 16/24 为简化版)\n", pngPath, icoPath, len(entries))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "makeicon:", err)
	os.Exit(1)
}

// ---------- 绘制 ----------

// draw 在 size×size 的画布上画图标(逻辑坐标系固定 512, 靠 s 缩放到像素)。
func draw(size int, simple bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size) / baseSize // 逻辑单位 → 像素

	// 1) 圆角方块底: 纵向渐变 + 内侧亮边
	tileR := 112.0
	for y := 0; y < size; y++ {
		fy := float64(y) / s
		bg := lerp(color.RGBA{0x1b, 0x2b, 0x46, 0xff}, color.RGBA{0x0a, 0x11, 0x20, 0xff}, clamp01(fy/512))
		for x := 0; x < size; x++ {
			if inRoundRect(float64(x)/s, fy, 8, 8, 496, 496, tileR) {
				img.Set(x, y, bg)
			}
		}
	}
	rim := color.RGBA{0x4f, 0x9c, 0xf9, 0x66}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)/s, float64(y)/s
			if inRoundRect(fx, fy, 8, 8, 496, 496, tileR) && !inRoundRect(fx, fy, 13, 13, 486, 486, tileR-5) {
				img.Set(x, y, blend(img.RGBAAt(x, y), rim))
			}
		}
	}

	// 2) 内存条: 绿色 PCB + 投影
	modX, modY, modW, modH := 44.0, 190.0, 424.0, 162.0
	if !simple {
		for i := 0; i < 4; i++ { // 用几层递减 alpha 近似软阴影(标准库没有模糊)
			grow := float64(i) * 5
			a := uint8(26 - i*6)
			fillRoundRect(img, s, modX-grow+3, modY+grow*0.6+7, modW+grow*2, modH+grow, 16+grow, func(fx, fy float64) color.RGBA {
				return color.RGBA{0x00, 0x00, 0x00, a}
			})
		}
	}
	fillRoundRect(img, s, modX, modY, modW, modH, 16, func(fx, fy float64) color.RGBA {
		return lerp(color.RGBA{0x1c, 0x6a, 0x46, 0xff}, color.RGBA{0x33, 0xb5, 0x72, 0xff}, clamp01((fy-modY)/modH))
	})
	// PCB 高光边
	edge := color.RGBA{0x7f, 0xe6, 0xb0, 0x99}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)/s, float64(y)/s
			if inRoundRect(fx, fy, modX, modY, modW, modH, 16) &&
				!inRoundRect(fx, fy, modX+3, modY+3, modW-6, modH-6, 14) {
				img.Set(x, y, blend(img.RGBAAt(x, y), edge))
			}
		}
	}

	// 3) 金手指: 底部一排(简化版更粗更少, 小尺寸下才看得出"是一排脚")
	pinY, pinH := modY+modH-32, 32.0
	pinW, pitch := 14.0, 26.0
	if simple {
		pinW, pitch = 26.0, 52.0
	}
	for i := 0; ; i++ {
		px := modX + 14 + float64(i)*pitch
		if px < 214 && px+pinW > 214 { // 卡口处让开
			px += pitch
		}
		if px+pinW > modX+modW-10 {
			break
		}
		fillRoundRect(img, s, px, pinY, pinW, pinH, 2, func(fx, fy float64) color.RGBA {
			return lerp(color.RGBA{0xf0, 0xd0, 0x6a, 0xff}, color.RGBA{0xb0, 0x86, 0x24, 0xff}, clamp01((fy-pinY)/pinH))
		})
	}

	// 4) 颗粒(DRAM): 均分在 PCB 上, 卡口处不断开也要保证左右对称
	n := 4
	if simple {
		n = 3
	}
	gap := 22.0
	chipW := (modW - 48 - gap*float64(n-1)) / float64(n)
	chipY, chipH := modY+24, 86.0
	if simple {
		chipY, chipH = modY+22, 96.0
	}
	for i := 0; i < n; i++ {
		cx := modX + 24 + float64(i)*(chipW+gap)
		fillRoundRect(img, s, cx, chipY, chipW, chipH, 9, func(fx, fy float64) color.RGBA {
			return lerp(color.RGBA{0x24, 0x46, 0x3a, 0xff}, color.RGBA{0x0d, 0x1f, 0x18, 0xff}, clamp01((fy-chipY)/chipH))
		})
		if simple {
			continue // 小尺寸只保留块面
		}
		// 两点冷光: 让颗粒看着是"芯片"而不是空洞
		fillRoundRect(img, s, cx+12, chipY+14, 18, 8, 4, func(fx, fy float64) color.RGBA {
			return color.RGBA{0xa8, 0xe4, 0xff, 0x88}
		})
		fillRoundRect(img, s, cx+chipW-22, chipY+14, 10, 8, 4, func(fx, fy float64) color.RGBA {
			return color.RGBA{0xa8, 0xe4, 0xff, 0x66}
		})
	}

	// 5) 铅笔(只在大尺寸出现): 笔尖落在第 3 颗颗粒上, 表示"可写"
	if !simple {
		drawPencil(img, s, 452, 104, 300, 306, 46)
	}
	return img
}

// drawPencil 从 (x1,y1)(尾端橡皮)到 (x2,y2)(笔尖)画一支宽 w 的铅笔。
func drawPencil(img *image.RGBA, s, x1, y1, x2, y2, w float64) {
	dx, dy := x2-x1, y2-y1
	l := math.Hypot(dx, dy)
	ux, uy := dx/l, dy/l
	px, py := -uy, ux // 垂直方向

	body := color.RGBA{0xf2, 0xf6, 0xfb, 0xff}
	band := color.RGBA{0x4f, 0x9c, 0xf9, 0xff}
	tip := color.RGBA{0x1c, 0x24, 0x33, 0xff}
	wood := color.RGBA{0xe8, 0xc9, 0x8a, 0xff}

	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			fx, fy := float64(x)/s, float64(y)/s
			rx, ry := fx-x1, fy-y1
			t := rx*ux + ry*uy // 沿笔身
			o := rx*px + ry*py // 垂直偏移
			if t < 0 || t > l+30 || math.Abs(o) > w/2 {
				continue
			}
			switch {
			case t > l-8: // 笔尖(三角形收窄)
				k := (t - (l - 8)) / 38
				if math.Abs(o) > (w/2)*(1-k) {
					continue
				}
				if k > .35 {
					img.Set(x, y, tip)
				} else {
					img.Set(x, y, wood)
				}
			case t > l-46: // 笔箍
				img.Set(x, y, band)
			case t < 30: // 尾端橡皮
				img.Set(x, y, color.RGBA{0xf2, 0x8b, 0x8b, 0xff})
			default: // 笔身: 中间亮、两侧略暗, 做出圆柱感
				img.Set(x, y, lerp(color.RGBA{0xb9, 0xc6, 0xd8, 0xff}, body, 1-math.Abs(o)/(w/2)*0.35))
			}
		}
	}
}

// ---------- 形状与颜色工具 ----------

func inRoundRect(x, y, rx, ry, w, h, r float64) bool {
	if x < rx || y < ry || x > rx+w || y > ry+h {
		return false
	}
	cx := math.Min(math.Max(x, rx+r), rx+w-r)
	cy := math.Min(math.Max(y, ry+r), ry+h-r)
	return math.Hypot(x-cx, y-cy) <= r
}

// fillRoundRect 用 paint(fx,fy) 填充圆角矩形(参数都是逻辑单位)。
func fillRoundRect(img *image.RGBA, s, x, y, w, h, r float64, paint func(fx, fy float64) color.RGBA) {
	b := img.Bounds()
	for iy := int(y * s); iy < int((y+h)*s); iy++ {
		for ix := int(x * s); ix < int((x+w)*s); ix++ {
			if ix < b.Min.X || iy < b.Min.Y || ix >= b.Max.X || iy >= b.Max.Y {
				continue
			}
			fx, fy := float64(ix)/s, float64(iy)/s
			if !inRoundRect(fx, fy, x, y, w, h, r) {
				continue
			}
			c := paint(fx, fy)
			if c.A < 0xff {
				// 半透明色必须**合成**而不是覆盖: 直接 Set 会把底下挖成透明的
				// (阴影与颗粒冷光都吃过这个亏: 渲染出来像一圈白边)
				c = blend(img.RGBAAt(ix, iy), c)
			}
			img.Set(ix, iy, c)
		}
	}
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	t = clamp01(t)
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), mix(a.A, b.A)}
}

// blend 按 src 的 alpha 做 source-over 合成。
func blend(dst, src color.RGBA) color.RGBA {
	a := float64(src.A) / 255
	mix := func(d, s uint8) uint8 { return uint8(float64(d)*(1-a) + float64(s)*a) }
	return color.RGBA{mix(dst.R, src.R), mix(dst.G, src.G), mix(dst.B, src.B), dst.A}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---------- 缩放与编码 ----------

// downsample 盒式降采样: src 边长必须是 outSize 的整数倍。
func downsample(src *image.RGBA, outSize int) *image.RGBA {
	f := src.Bounds().Dx() / outSize
	dst := image.NewRGBA(image.Rect(0, 0, outSize, outSize))
	for y := 0; y < outSize; y++ {
		for x := 0; x < outSize; x++ {
			var r, g, b, a uint32
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					c := src.RGBAAt(x*f+dx, y*f+dy)
					// 预乘后再平均, 避免透明边缘发黑
					r += uint32(c.R) * uint32(c.A) / 255
					g += uint32(c.G) * uint32(c.A) / 255
					b += uint32(c.B) * uint32(c.A) / 255
					a += uint32(c.A)
				}
			}
			n := uint32(f * f)
			alpha := uint8(a / n)
			var rr, gg, bb uint8
			if a > 0 {
				rr = uint8(r * 255 / a)
				gg = uint8(g * 255 / a)
				bb = uint8(b * 255 / a)
			}
			dst.SetRGBA(x, y, color.RGBA{rr, gg, bb, alpha})
		}
	}
	return dst
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// icoEntry 是一个尺寸的图标数据。
type icoEntry struct {
	size int
	data []byte
}

// writeICO 组装 .ico(条目数据由调用方按尺寸准备好)。
func writeICO(path string, entries []icoEntry) error {
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // type = icon
	binary.Write(&out, binary.LittleEndian, uint16(len(entries)))
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		w := byte(e.size)
		if e.size >= 256 {
			w = 0 // 256 在 ICO 里写 0
		}
		out.Write([]byte{w, w, 0, 0})
		binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
		binary.Write(&out, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// bmpEntry 生成 ICO 里的 32 位 BMP 条目(BITMAPINFOHEADER + BGRA 倒序行 + 全零 AND 掩码)。
func bmpEntry(img *image.RGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	maskRow := (w + 31) / 32 * 4
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2)) // XOR + AND
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // BI_RGB
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4+maskRow*h))
	binary.Write(&buf, binary.LittleEndian, int32(0)) // 分辨率(可省)
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // 调色板
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	for y := h - 1; y >= 0; y-- { // 自下而上
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	buf.Write(make([]byte, maskRow*h)) // AND 掩码全 0: 透明度交给 alpha
	return buf.Bytes()
}
