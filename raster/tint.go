package raster

import (
	"image"
	"image/color"
)

// Tint 用白蒙版现染: 直存 RGB 各乘一次色, alpha 一起乘。
func Tint(mask *image.RGBA, c color.RGBA) *image.RGBA {
	if mask == nil {
		return nil
	}
	if c == (color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}) {
		return mask
	}
	out := image.NewRGBA(mask.Rect)
	b := mask.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := mask.PixOffset(x, y)
			if mask.Pix[i+3] == 0 {
				continue
			}
			o := out.PixOffset(x, y)
			out.Pix[o] = uint8(int(mask.Pix[i]) * int(c.R) / 0xFF)
			out.Pix[o+1] = uint8(int(mask.Pix[i+1]) * int(c.G) / 0xFF)
			out.Pix[o+2] = uint8(int(mask.Pix[i+2]) * int(c.B) / 0xFF)
			out.Pix[o+3] = uint8(int(mask.Pix[i+3]) * int(c.A) / 0xFF)
		}
	}
	return out
}
