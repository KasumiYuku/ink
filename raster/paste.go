package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// SpriteOpts 是贴图的形状选项。
type SpriteOpts struct {
	// Lean 非零表示斜切采样: 窗口斜、图不斜(图只是被窗口裁掉), 15° 就是 Lean15。
	Lean float64
	// Mirror 让越界采样镜像折回图内 —— 斜切窗口与图一样大时, 夹边会在角上拉出一排重复像素。
	Mirror bool
	// Nearest 按最近邻重采样(像素级锐利), 默认是按面积/双三次缩放。
	Nearest bool
}

// Sprite 把 img 的 src 那块放进 at; win 是可见窗口(一般等于 at), 落在它外面的不写。
// Lean 非零或 Nearest 时按最近邻逐行采样; 否则目标与源一样大就直拷、不一样就重采样。
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
	if opts.Lean == 0 && !opts.Nearest {
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

// ShapeSprite 同 Sprite, 但每行只写 shape 覆盖到的那一段: 只给窗口一个形状, 不必先建一张同尺寸遮罩。
// 圆角、斜切、按行描述的窗口都走这条; 两端按覆盖率混色, 与 Fill 的边界口径一致。
func (b Brush) ShapeSprite(at, win, src image.Rectangle, img *image.RGBA, s Shape, opts SpriteOpts) {
	if b.Dst == nil || img == nil || s == nil {
		return
	}
	src = src.Intersect(img.Rect)
	if at.Empty() || win.Empty() || src.Empty() || at.Dx() <= 0 {
		return
	}
	win = win.Intersect(b.Bounds())
	if win.Empty() {
		return
	}
	dst := b.Dst
	scale := float64(src.Dx()) / float64(at.Dx())
	var buf [8]Span
	for y := win.Min.Y; y < win.Max.Y; y++ {
		sy := src.Min.Y + (y-at.Min.Y)*src.Dy()/at.Dy()
		if sy < src.Min.Y || sy >= src.Max.Y {
			continue
		}
		var spans []Span
		if sc, ok := s.(SingleShape); ok {
			sp, ok2 := sc.Single(y)
			if !ok2 {
				continue
			}
			spans = append(buf[:0], sp)
		} else {
			spans = s.Spans(y, buf[:0])
		}
		for _, sp := range spans {
			sp = clampSpan(sp, dst.Rect)
			lo, hi := int(math.Floor(sp.X0)), int(math.Ceil(sp.X1))
			for x := lo; x < hi; x++ {
				cov := min(sp.X1, float64(x+1)) - max(sp.X0, float64(x))
				if cov <= 0 {
					continue
				}
				sx := src.Min.X + int((float64(x-at.Min.X)+0.5)*scale)
				if opts.Mirror {
					sx = foldIndex(sx, src.Min.X, src.Max.X)
				} else {
					sx = clampInt(sx, src.Min.X, src.Max.X-1)
				}
				si := img.PixOffset(sx, sy)
				a := img.Pix[si+3]
				if cov < 1 {
					a = uint8(math.Round(float64(a) * cov))
				}
				if a == 255 {
					di := dst.PixOffset(x, y)
					copy(dst.Pix[di:di+4], img.Pix[si:si+4])
				} else if a != 0 {
					rgba.BlendAt(dst.Pix, dst.PixOffset(x, y),
						color.RGBA{R: img.Pix[si], G: img.Pix[si+1], B: img.Pix[si+2], A: a})
				}
			}
		}
	}
}
