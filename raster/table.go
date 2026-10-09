package raster

import (
	"image"
	"image/color"
)

// RowTable 是与 Fill 内联同一条 alpha over 算式的 4 通道查表。
type RowTable [4][256]byte

// NewRowTable 按一份墨色建表; 每一项与 Fill 的算式逐字节相同。
func NewRowTable(c color.RGBA) RowTable {
	var t RowTable
	a := uint32(c.A)
	ia := 255 - a
	base := [4]uint32{uint32(c.R) * a, uint32(c.G) * a, uint32(c.B) * a, a * 255}
	for ch := 0; ch < 4; ch++ {
		for v := 0; v < 256; v++ {
			t[ch][v] = uint8((base[ch] + uint32(v)*ia) / 255)
		}
	}
	return t
}

// Compose 先做 b 再做 a; 结果与依次 over 两遍逐字节相同。
func Compose(a, b RowTable) RowTable {
	var out RowTable
	for ch := 0; ch < 4; ch++ {
		for v := 0; v < 256; v++ {
			out[ch][v] = a[ch][b[ch][v]]
		}
	}
	return out
}

// ApplyTable 用表把那块矩形就地变换, 越界自动裁剪。
func ApplyTable(dst *image.RGBA, r image.Rectangle, tab *RowTable) {
	if dst == nil || tab == nil {
		return
	}
	r = r.Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	n := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(r.Min.X, y):]
		for i := 0; i < n; i += 4 {
			row[i] = tab[0][row[i]]
			row[i+1] = tab[1][row[i+1]]
			row[i+2] = tab[2][row[i+2]]
			row[i+3] = tab[3][row[i+3]]
		}
	}
}
