package rgba

import "image/color"

// Mode 是合成方式: Over 是常规 alpha 混合, Add 用于发光, Multiply 用于压暗。
type Mode uint8

const (
	Over Mode = iota
	Add
	Multiply
	Screen
	Copy
)

// BlendModeAt 按模式把一个像素合成到 pix[i:]; 非 Over 的模式先把结果算成新 RGB, 再按源 alpha 走 Over。
func BlendModeAt(pix []uint8, i int, c color.RGBA, mode Mode) {
	switch mode {
	case Copy:
		if c.A != 0 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, c.A
		}
		return
	case Add:
		c = fold(pix, i, c, func(d, s uint8) uint8 { return Clamp8(int(d) + int(s)) })
	case Multiply:
		c = fold(pix, i, c, func(d, s uint8) uint8 { return uint8(uint32(d) * uint32(s) / 255) })
	case Screen:
		c = fold(pix, i, c, func(d, s uint8) uint8 {
			return uint8(255 - uint32(255-d)*uint32(255-s)/255)
		})
	}
	BlendAt(pix, i, c)
}

func fold(pix []uint8, i int, c color.RGBA, f func(d, s uint8) uint8) color.RGBA {
	c.R = f(pix[i], c.R)
	c.G = f(pix[i+1], c.G)
	c.B = f(pix[i+2], c.B)
	return c
}
