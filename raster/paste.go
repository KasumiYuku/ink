package raster

import (
	"image"
	"image/color"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// SpriteOpts 是贴图的形状选项。
type SpriteOpts struct {
	// Lean 非零表示斜切采样: 窗口斜、图不斜(图只是被窗口裁掉), 15° 就是 Lean15。
	Lean float64
	// Mirror 让越界采样镜像折回图内 —— 斜切窗口与图一样大时, 夹边会在角上拉出一排重复像素。
	Mirror bool
}

// Sprite 把 img 的 src 那块放进 at: 只画 win 里那部分(图比窗口大时多出来的被裁掉),
// Lean 非零按斜切采样。win 一般等于 at; 图比窗口大时给窗口。
func (b Brush) Sprite(at, win, src image.Rectangle, img *image.RGBA, opts SpriteOpts) {
	if b.Dst == nil || img == nil {
		return
	}
	if at.Empty() || win.Empty() {
		return
	}
	src = src.Intersect(img.Rect)
	if src.Empty() {
		return
	}
	win = win.Intersect(b.Bounds())
	if win.Empty() {
		return
	}
	if opts.Lean == 0 {
		if this := at.Intersect(win); !this.Empty() {
			scaleInto(b.Dst, img, src, this, b.Par, b.Scratch)
		}
		return
	}
	if opts.Mirror {
		pasteMirrorWithin(b.Dst, img, src, at, win, opts.Lean)
		return
	}
	pasteSkewedWithin(b.Dst, img, src, at, win, opts.Lean)
}

// pasteMirrorWithin 把 src 贴进斜切窗口, 越界采样按镜像折回图内(不夹边)。
func pasteMirrorWithin(dst, src *image.RGBA, crop, draw, win image.Rectangle, lean float64) {
	if dst == nil || src == nil || draw.Empty() || win.Empty() || crop.Empty() || draw.Dy() <= 0 {
		return
	}
	scale := float64(crop.Dx()) / float64(draw.Dx())
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
		di := dst.PixOffset(x0, y)
		for x := x0; x < x1; x++ {
			sx := foldIndex(crop.Min.X+int((float64(x-draw.Min.X)+0.5)*scale), crop.Min.X, crop.Max.X)
			si := src.PixOffset(sx, sy)
			a := src.Pix[si+3]
			if a == 255 {
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			} else if a != 0 {
				rgba.BlendAt(dst.Pix, di, color.RGBA{R: src.Pix[si], G: src.Pix[si+1], B: src.Pix[si+2], A: a})
			}
			di += 4
		}
	}
}

// foldIndex 把越界的采样下标镜像折回图内(周期 2n)。
func foldIndex(i, lo, hi int) int {
	if i >= lo && i < hi {
		return i
	}
	n := hi - lo
	if n <= 1 {
		return lo
	}
	period := 2 * n
	k := (i - lo) % period
	if k < 0 {
		k += period
	}
	if k >= n {
		k = period - 1 - k
	}
	return lo + k
}
