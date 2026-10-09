// Package rgba 是像素底座: 约定画布为**直线 alpha**(非预乘), 合成按整数算式逐位确定。
package rgba

import (
	"image"
	"image/color"
	"image/draw"
)

// Clamp8 夹到 0..255。
func Clamp8(v int) uint8 {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	default:
		return uint8(v)
	}
}

// Lerp8 单通道线性插值(t 已夹在 0..1)。
func Lerp8(a, b uint8, t float64) uint8 {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return Clamp8(int(float64(a) + (float64(b)-float64(a))*t + 0.5))
}

// Lerp 两色线性插值, alpha 一起插。
func Lerp(a, b color.RGBA, t float64) color.RGBA {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return color.RGBA{Lerp8(a.R, b.R, t), Lerp8(a.G, b.G, t), Lerp8(a.B, b.B, t), Lerp8(a.A, b.A, t)}
}

// WithCoverage 把覆盖率乘进 alpha, 供 1px 抗锯齿落笔用。
func WithCoverage(c color.RGBA, cov float64) color.RGBA {
	if cov <= 0 {
		c.A = 0
		return c
	}
	if cov >= 1 {
		return c
	}
	c.A = Clamp8(int(float64(c.A)*cov + 0.5))
	return c
}

// Blend 把一个像素按 alpha over 合成到 dst; 越界直接忽略。取整方式固定, 结果可复现。
func Blend(dst *image.RGBA, x, y int, c color.RGBA) {
	if c.A == 0 || !(image.Point{X: x, Y: y}.In(dst.Rect)) {
		return
	}
	i := dst.PixOffset(x, y)
	if c.A == 255 {
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = c.R, c.G, c.B, 255
		return
	}
	a := uint32(c.A)
	ia := 255 - a
	dst.Pix[i] = Clamp8(int((uint32(c.R)*a + uint32(dst.Pix[i])*ia) / 255))
	dst.Pix[i+1] = Clamp8(int((uint32(c.G)*a + uint32(dst.Pix[i+1])*ia) / 255))
	dst.Pix[i+2] = Clamp8(int((uint32(c.B)*a + uint32(dst.Pix[i+2])*ia) / 255))
	dst.Pix[i+3] = Clamp8(int(uint32(dst.Pix[i+3]) + a*(255-uint32(dst.Pix[i+3]))/255))
}

// BlendAt 同 Blend, 但按字节偏移直接落笔(内循环用, 省掉坐标换算)。
func BlendAt(pix []uint8, i int, c color.RGBA) {
	if c.A == 255 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, 255
		return
	}
	a := uint32(c.A)
	ia := 255 - a
	pix[i] = Clamp8(int((uint32(c.R)*a + uint32(pix[i])*ia) / 255))
	pix[i+1] = Clamp8(int((uint32(c.G)*a + uint32(pix[i+1])*ia) / 255))
	pix[i+2] = Clamp8(int((uint32(c.B)*a + uint32(pix[i+2])*ia) / 255))
	pix[i+3] = Clamp8(int(uint32(pix[i+3]) + a*(255-uint32(pix[i+3]))/255))
}

// FillRect 用不透明色铺满 r(自带裁剪); 半透明请走 Blend。
func FillRect(dst *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	pix := dst.Pix
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := y * dst.Stride
		for x := r.Min.X; x < r.Max.X; x++ {
			i := row + x*4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
}

// Clip 把 r 裁进 dst; 裁空了返回 false。
func Clip(dst *image.RGBA, r image.Rectangle) (image.Rectangle, bool) {
	r = r.Intersect(dst.Rect)
	return r, !r.Empty()
}

// ToStraight 把任意解码出来的图转成**直存 alpha(非预乘)**的 RGBA 缓冲 —— 本库全部像素按这一套算。
//
// 这一条踩过坑: 带透明通道的 PNG 解出来是 *image.NRGBA(直存), 若直接 draw.Draw 进 image.RGBA 会把
// RGB 乘进 alpha(image.RGBA 的语义本来就是预乘), 之后绘制层再按直存贴一遍, 等于预乘两次, RGB 被压掉
// 一个 (1-A) 档 —— 白底上的小角标会莫名其妙发灰。所以 NRGBA 必须**字节对拷**。
func ToStraight(src image.Image) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	if b.Empty() {
		return out
	}
	switch s := src.(type) {
	case *image.NRGBA:
		for y := 0; y < b.Dy(); y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+b.Dx()*4],
				s.Pix[s.PixOffset(b.Min.X, b.Min.Y+y):s.PixOffset(b.Min.X, b.Min.Y+y)+b.Dx()*4])
		}
	case *image.NRGBA64:
		for y := 0; y < b.Dy(); y++ {
			row := y * out.Stride
			for x := 0; x < b.Dx(); x++ {
				c := s.NRGBA64At(b.Min.X+x, b.Min.Y+y)
				i := row + x*4
				out.Pix[i] = uint8(c.R >> 8)
				out.Pix[i+1] = uint8(c.G >> 8)
				out.Pix[i+2] = uint8(c.B >> 8)
				out.Pix[i+3] = uint8(c.A >> 8)
			}
		}
	default:
		draw.Draw(out, out.Rect, src, b.Min, draw.Src)
		unpremultiply(out)
	}
	return out
}

// unpremultiply 把预乘的 RGB 除回直存; 全透明像素直接归零(除不出颜色)。
func unpremultiply(img *image.RGBA) {
	for i := 0; i < len(img.Pix); i += 4 {
		a := uint32(img.Pix[i+3])
		if a == 255 {
			continue
		}
		if a == 0 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 0, 0, 0
			continue
		}
		img.Pix[i] = Clamp8(int((uint32(img.Pix[i])*255 + a/2) / a))
		img.Pix[i+1] = Clamp8(int((uint32(img.Pix[i+1])*255 + a/2) / a))
		img.Pix[i+2] = Clamp8(int((uint32(img.Pix[i+2])*255 + a/2) / a))
	}
}

// Premultiply 就地预乘 RGB(插值前用, 插值后要 ToStraight 那套除回来)。
func Premultiply(img *image.RGBA) {
	for i := 0; i < len(img.Pix); i += 4 {
		a := uint32(img.Pix[i+3])
		if a == 255 {
			continue
		}
		img.Pix[i] = Clamp8(int((uint32(img.Pix[i])*a + 127) / 255))
		img.Pix[i+1] = Clamp8(int((uint32(img.Pix[i+1])*a + 127) / 255))
		img.Pix[i+2] = Clamp8(int((uint32(img.Pix[i+2])*a + 127) / 255))
	}
}

// Composite 把 src 按 alpha 叠到 dst 上(尺寸相同); src 是直存 alpha, 只缩 alpha 不动 RGB。
func Composite(dst, src *image.RGBA, alpha float64) {
	if alpha <= 0 || src == nil {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	for i := 0; i+3 < len(dst.Pix) && i+3 < len(src.Pix); i += 4 {
		a := Clamp8(int(float64(src.Pix[i+3])*alpha + 0.5))
		if a == 0 {
			continue
		}
		BlendAt(dst.Pix, i, color.RGBA{R: src.Pix[i], G: src.Pix[i+1], B: src.Pix[i+2], A: a})
	}
}
