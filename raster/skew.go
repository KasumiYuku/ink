package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// SkewDX 是斜切矩形在第 y 行的水平位移。
func SkewDX(y0, y1, y, lean float64) int {
	center := (y0 + y1 - 1) / 2
	return int(math.Round(lean * (center - y)))
}

// FillSkewed 填充一个斜切矩形(平行四边形), 自带裁剪, 基准取自身的竖直中线。
func FillSkewed(dst *image.RGBA, r image.Rectangle, lean float64, c color.RGBA) {
	if r.Empty() || c.A == 0 {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dx := SkewDX(float64(r.Min.Y), float64(r.Max.Y), float64(y), lean)
		Fill(dst, image.Rect(r.Min.X+dx, y, r.Max.X+dx, y+1), c)
	}
}

// pasteSkewedWithin 把 src 贴进斜切窗口: draw 是图该占的位置, win 是窗口, 共用窗口竖直中线当基准。
func pasteSkewedWithin(dst, src *image.RGBA, crop, draw, win image.Rectangle, lean float64) {
	if dst == nil || src == nil || draw.Empty() || win.Empty() || crop.Empty() {
		return
	}
	for y := win.Min.Y; y < win.Max.Y; y++ {
		if y < dst.Rect.Min.Y || y >= dst.Rect.Max.Y {
			continue
		}
		dx := SkewDX(float64(win.Min.Y), float64(win.Max.Y), float64(y), lean)
		x0 := max(win.Min.X+dx, draw.Min.X+dx, dst.Rect.Min.X)
		x1 := min(win.Max.X+dx, draw.Max.X+dx, dst.Rect.Max.X)
		if x0 >= x1 {
			continue
		}
		sy := crop.Min.Y + (y-draw.Min.Y)*crop.Dy()/draw.Dy()
		if sy < crop.Min.Y || sy >= crop.Max.Y {
			continue
		}
		scale := float64(crop.Dx()) / float64(draw.Dx())
		di := dst.PixOffset(x0, y)
		for x := x0; x < x1; x++ {
			sx := clampInt(crop.Min.X+int((float64(x-draw.Min.X)+0.5)*scale), crop.Min.X, crop.Max.X-1)
			si := src.PixOffset(sx, sy)
			a := src.Pix[si+3]
			if a == 255 {
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			} else if a != 0 {
				rgba.BlendAt(dst.Pix, di, color.RGBA{src.Pix[si], src.Pix[si+1], src.Pix[si+2], a})
			}
			di += 4
		}
	}
}
