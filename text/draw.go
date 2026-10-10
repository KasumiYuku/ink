package text

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/math/fixed"
)

// Style 是一段文字的落笔样式; 零值 = 左对齐/灰度/无字距/不裁剪/不透明白。
type Style struct {
	Align Align
	Mode  Mode
	Track float64
	Ink   color.RGBA
	Clip  image.Rectangle
	Kern  bool
}

func (s Style) WithInk(c color.RGBA) Style       { s.Ink = c; return s }
func (s Style) WithTrack(px float64) Style       { s.Track = px; return s }
func (s Style) WithAlign(a Align) Style          { s.Align = a; return s }
func (s Style) WithMode(m Mode) Style            { s.Mode = m; return s }
func (s Style) WithKern(on bool) Style           { s.Kern = on; return s }
func (s Style) WithClip(r image.Rectangle) Style { s.Clip = r; return s }

func inkOf(st Style) color.RGBA {
	if st.Ink == (color.RGBA{}) {
		return color.RGBA{A: 255}
	}
	return st.Ink
}

// Draw 在 (x, baseline) 落一行字, 返回实际推进宽度。
func Draw(dst *image.RGBA, f Face, x, baseline float64, s string, st Style) float64 {
	gf, ok := f.(*glyphFace)
	if !ok || gf == nil || len(gf.faces) == 0 || s == "" || dst == nil {
		return 0
	}
	ink := inkOf(st)
	if ink.A == 0 {
		return 0
	}
	bin := st.Mode == ModeBinary

	if st.Align != Left {
		adv := gf.advance(s, st)
		switch st.Align {
		case Center:
			x -= adv / 2
		case Right:
			x -= adv
		}
	}
	start := fixed.Int26_6(math.Round(x * 64))
	pen := start
	track := fixed.Int26_6(math.Round(st.Track * 64))
	by := int(math.Round(baseline))
	var prev rune
	first := true
	for _, r := range s {
		if fs := gf.owner(r, bin); fs != nil {
			if st.Kern && !first {
				pen += gf.set.kern(gf.faces[0], prev, r)
			}
			m := gf.set.glyph(fs, r, bin, uint8(pen&63))
			if m.W > 0 && m.H > 0 {
				blit(dst, m, int(pen>>6), by, ink, st.Clip)
			}
			pen += m.Adv
			prev, first = r, false
		}
		pen += track
	}
	return float64(pen-start) / 64
}

// Measure 返回一行字的宽度与行高。
func Measure(f Face, s string, st Style) (w, h float64) {
	gf, ok := f.(*glyphFace)
	if !ok || gf == nil || len(gf.faces) == 0 {
		return 0, 0
	}
	return gf.advance(s, st), gf.Metrics().LineHeight
}

// Width 是一行字的推进宽度(与 Measure 同一个数, 只是省掉一次解构)。
func Width(f Face, s string, st Style) float64 {
	w, _ := Measure(f, s, st)
	return w
}

type Run struct {
	X, Baseline float64
	Text        string
	Face        Face
	Style       Style
}

func DrawRuns(dst *image.RGBA, runs []Run) {
	for i := range runs {
		Draw(dst, runs[i].Face, runs[i].X, runs[i].Baseline, runs[i].Text, runs[i].Style)
	}
}

// blit 把覆盖率掩码合成到直线 alpha 画布; 灰度与二值共用这条路径。
func blit(dst *image.RGBA, m *mask, penX, baseY int, ink color.RGBA, clip image.Rectangle) {
	cx0, cy0 := dst.Rect.Min.X, dst.Rect.Min.Y
	cx1, cy1 := dst.Rect.Max.X, dst.Rect.Max.Y
	if !clip.Empty() {
		cx0, cy0 = max(cx0, clip.Min.X), max(cy0, clip.Min.Y)
		cx1, cy1 = min(cx1, clip.Max.X), min(cy1, clip.Max.Y)
		if cx0 >= cx1 || cy0 >= cy1 {
			return
		}
	}
	x0 := penX + m.Left
	y0 := baseY + m.Top
	pix := dst.Pix
	sr, sg, sb, sa := ink.RGBA()
	for row := 0; row < m.H; row++ {
		dy := y0 + row
		if dy < cy0 || dy >= cy1 {
			continue
		}
		cStart := 0
		if x0 < cx0 {
			cStart = cx0 - x0
		}
		cEnd := m.W
		if x0+m.W > cx1 {
			cEnd = cx1 - x0
		}
		if cStart >= cEnd {
			continue
		}
		srcRow := row * m.W
		base := (dy-dst.Rect.Min.Y)*dst.Stride + (x0+cStart-dst.Rect.Min.X)*4
		for col := cStart; col < cEnd; col++ {
			if cov := m.Pix[srcRow+col]; cov != 0 {
				shadeGlyph(pix[base:base+4], cov, sr, sg, sb, sa)
			}
			base += 4
		}
	}
}

// shadeGlyph 是与 image/draw 的字形混合**逐字节相同**的那条算式(见 drawRGBAMaskOver)。
func shadeGlyph(d []byte, cov uint8, sr, sg, sb, sa uint32) {
	const m = 1<<16 - 1
	ma := uint32(cov)
	ma |= ma << 8
	a := (m - sa*ma/m) * 0x101
	d[0] = uint8((uint32(d[0])*a + sr*ma) / m >> 8)
	d[1] = uint8((uint32(d[1])*a + sg*ma) / m >> 8)
	d[2] = uint8((uint32(d[2])*a + sb*ma) / m >> 8)
	d[3] = uint8((uint32(d[3])*a + sa*ma) / m >> 8)
}

const ellipsis = "…"

// Ellipsize 把过长的文本截到 maxWidth 内并补省略号; 一个字都放不下时返回空串。
func Ellipsize(f Face, s string, maxWidth float64, st Style) string {
	if f == nil || math.Round(f.Advance(s, st)) <= maxWidth {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if math.Round(f.Advance(string(runes)+ellipsis, st)) <= maxWidth {
			return string(runes) + ellipsis
		}
	}
	return ""
}
