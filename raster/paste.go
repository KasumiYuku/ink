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

// Sprite 把 img 的 src 那块放进 at; win 是可见窗口(一般等于 at), 落在它外面的不写。
// Lean 非零按斜切采样; 目标与源一样大时直拷, 不做重采样。
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
		vis := at.Intersect(win)
		if vis.Empty() {
			return
		}
		if src.Dx() == at.Dx() && src.Dy() == at.Dy() {
			pasteCopy(b.Dst, img, src, at, vis)
			return
		}
		scaleInto(b.Dst, img, src, vis, b.Par, b.Scratch)
		return
	}
	if opts.Mirror {
		pasteMirrorWithin(b.Dst, img, src, at, win, opts.Lean)
		return
	}
	pasteSkewedWithin(b.Dst, img, src, at, win, opts.Lean)
}

// pasteCopy 把 src 原样贴进 at(不重采样), 只写 vis 那部分。
func pasteCopy(dst, src *image.RGBA, crop, at, vis image.Rectangle) {
	dx, dy := vis.Min.X-at.Min.X, vis.Min.Y-at.Min.Y
	pix := src.Pix
	row := crop.Min.Y + dy
	for y := vis.Min.Y; y < vis.Max.Y; y++ {
		si := src.PixOffset(crop.Min.X+dx, row)
		di := dst.PixOffset(vis.Min.X, y)
		for x := 0; x < vis.Dx(); x++ {
			if a := pix[si+3]; a == 255 {
				copy(dst.Pix[di:di+4], pix[si:si+4])
			} else if a != 0 {
				rgba.BlendAt(dst.Pix, di, color.RGBA{R: pix[si], G: pix[si+1], B: pix[si+2], A: a})
			}
			si, di = si+4, di+4
		}
		row++
	}
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
