package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// Fill 实心矩形, 自带裁剪; 半透明那条路是 alpha over。
// 行长够时走向量那条路(SSE2 / NEON), 常数只穿一次; 短跨段仍走标量, 免得为几字节付调用开销。
func Fill(dst *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(dst.Rect)
	if r.Empty() || c.A == 0 {
		return
	}
	if c.A == 255 {
		rgba.FillRect(dst, r, c)
		return
	}
	a := uint32(c.A)
	ia := 255 - a
	cr, cg, cb := uint32(c.R)*a, uint32(c.G)*a, uint32(c.B)*a
	n := r.Dx() * 4
	if n < blendMinBytes {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			blendScalar(dst.Pix[dst.PixOffset(r.Min.X, y):][:n], cr, cg, cb, a, ia)
		}
		return
	}
	bv := blendPrep(cr, cg, cb, a, ia)
	vec := n &^ 15
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(r.Min.X, y):]
		blendRow(&bv, row[:vec])
		blendScalar(row[vec:n], cr, cg, cb, a, ia)
	}
}

// coverRound 返回像素被圆角矩形覆盖的比例(0..1)。
func coverRound(x, y int, r image.Rectangle, radius float64) float64 {
	cx := float64(x) + 0.5
	cy := float64(y) + 0.5
	dx, dy := 0.0, 0.0
	switch {
	case cx < float64(r.Min.X)+radius:
		dx = float64(r.Min.X) + radius - cx
	case cx > float64(r.Max.X)-radius:
		dx = cx - (float64(r.Max.X) - radius)
	}
	switch {
	case cy < float64(r.Min.Y)+radius:
		dy = float64(r.Min.Y) + radius - cy
	case cy > float64(r.Max.Y)-radius:
		dy = cy - (float64(r.Max.Y) - radius)
	}
	if dx <= 0 || dy <= 0 {
		return 1
	}
	switch dist := math.Hypot(dx, dy); {
	case dist <= radius-0.5:
		return 1
	case dist >= radius+0.5:
		return 0
	default:
		return radius + 0.5 - dist
	}
}
