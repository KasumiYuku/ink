package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// Frost 是磨砂: 形状覆盖那块先糊一遍, 再按合成方式叠上 tint —— 形状外一点痕迹都没有。
func (b Brush) Frost(s Shape, radius int, tint color.RGBA) {
	if b.Dst == nil || s == nil || tint.A == 0 {
		return
	}
	dst := b.Dst
	gb := s.Bounds()
	w, h := gb.Dx(), gb.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	buf := image.NewRGBA(image.Rect(0, 0, w, h))
	holder := b.Scratch.get()
	defer b.Scratch.put(holder)
	scratch := holder.spans

	for y := 0; y < h; y++ {
		sy := gb.Min.Y + y
		dx := 0
		if sy >= dst.Rect.Min.Y && sy < dst.Rect.Max.Y {
			if sp, ok := SpanAt(s, sy, scratch); ok {
				dx = int(math.Round(sp.X0)) - gb.Min.X
			}
		}
		cy := clampInt(sy, dst.Rect.Min.Y, dst.Rect.Max.Y-1)
		brow := buf.Pix[y*buf.Stride:]
		drow := dst.Pix[dst.PixOffset(dst.Rect.Min.X, cy):]
		for x := 0; x < w; x++ {
			sx := clampInt(gb.Min.X+x+dx, dst.Rect.Min.X, dst.Rect.Max.X-1)
			si := (sx - dst.Rect.Min.X) * 4
			copy(brow[x*4:x*4+4], drow[si:si+4])
		}
	}
	if radius > 0 {
		Blur(buf, buf.Rect, radius, b.Par)
	}
	for y := 0; y < h; y++ {
		sy := gb.Min.Y + y
		if sy < dst.Rect.Min.Y || sy >= dst.Rect.Max.Y {
			continue
		}
		sp, ok := SpanAt(s, sy, scratch)
		if !ok {
			continue
		}
		dx := int(math.Round(sp.X0)) - gb.Min.X
		span := int(math.Round(sp.X1)) - int(math.Round(sp.X0))
		if span > w {
			span = w
		}
		brow := buf.Pix[y*buf.Stride:]
		for x := 0; x < span; x++ {
			sx := gb.Min.X + x + dx
			if sx < dst.Rect.Min.X || sx >= dst.Rect.Max.X {
				continue
			}
			i := dst.PixOffset(sx, sy)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = brow[x*4], brow[x*4+1], brow[x*4+2]
			dst.Pix[i+3] = 255
			rgba.BlendModeAt(dst.Pix, i, tint, b.Mode)
		}
	}
}
