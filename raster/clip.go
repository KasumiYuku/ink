package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// ClipMask 是按像素的裁剪覆盖率(0..255): 造型裁剪与按图遮罩都编译成它。
type ClipMask struct {
	W, H int
	Pix  []uint8
}

// NewClipMask 造一张全 0 的掩码。
func NewClipMask(w, h int) *ClipMask {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &ClipMask{W: w, H: h, Pix: make([]uint8, w*h)}
}

// FromShape 把外形栅格成覆盖率(行优先, 尺寸 = r.Dx()*r.Dy()); 端点口径与 FillSpan 同源。
func FromShape(s Shape, r image.Rectangle) *ClipMask {
	m := NewClipMask(r.Dx(), r.Dy())
	if s == nil || m.W == 0 || m.H == 0 {
		return m
	}
	buf := make([]Span, 0, 16)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		buf = s.Spans(y, buf[:0])
		mj := y - r.Min.Y
		for _, sp := range buf {
			rasterSpan(m, mj, clampSpan(sp, r), r.Min.X)
		}
	}
	return m
}

// rasterSpan 把一段按 FillSpan 的端点覆盖率写进第 mj 行; 重叠段取大。
func rasterSpan(m *ClipMask, mj int, sp Span, ox int) {
	lo, hi := int(math.Floor(sp.X0)), int(math.Ceil(sp.X1))
	if hi <= lo {
		return
	}
	row := m.Pix[mj*m.W : mj*m.W+m.W]
	set := func(x int, cov float64) {
		i := x - ox
		if i < 0 || i >= m.W {
			return
		}
		if v := cov8(cov); v > row[i] {
			row[i] = v
		}
	}
	if hi-lo == 1 {
		set(lo, sp.X1-sp.X0)
		return
	}
	left, right := lo, hi
	if cov := float64(lo+1) - sp.X0; cov < 1 {
		set(lo, cov)
		left = lo + 1
	}
	if cov := sp.X1 - float64(hi-1); cov < 1 {
		set(hi-1, cov)
		right = hi - 1
	}
	for x := left; x < right; x++ {
		set(x, 1)
	}
}

// cov8 把覆盖率折成 0..255; 直接套 WithCoverage, 与 FillSpan 给不透明墨色算出的 alpha 逐位相同。
func cov8(cov float64) uint8 {
	return rgba.WithCoverage(color.RGBA{A: 255}, cov).A
}

// FromImage 把一张图的 alpha 当覆盖率; threshold>0 时先按阈值二值化, 否则用 alpha 原值。
func FromImage(img *image.RGBA, threshold uint8) *ClipMask {
	if img == nil {
		return NewClipMask(0, 0)
	}
	b := img.Rect
	m := NewClipMask(b.Dx(), b.Dy())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			a := img.Pix[img.PixOffset(b.Min.X+x, b.Min.Y+y)+3]
			if threshold > 0 {
				if a >= threshold {
					m.Pix[y*m.W+x] = 255
				}
				continue
			}
			m.Pix[y*m.W+x] = a
		}
	}
	return m
}

// Intersect 就地与本张求交(逐像素取小); 越出本张尺寸的像素视为 0。
func (m *ClipMask) Intersect(o *ClipMask) {
	if m == nil {
		return
	}
	for y := 0; y < m.H; y++ {
		row := m.Pix[y*m.W : y*m.W+m.W]
		if o == nil || y >= o.H {
			for x := range row {
				row[x] = 0
			}
			continue
		}
		orow := o.Pix[y*o.W : y*o.W+o.W]
		for x := range row {
			if x >= o.W {
				row[x] = 0
			} else if orow[x] < row[x] {
				row[x] = orow[x]
			}
		}
	}
}

// At 取某点的覆盖率(越界 = 0)。
func (m *ClipMask) At(x, y int) uint8 {
	if m == nil || x < 0 || y < 0 || x >= m.W || y >= m.H {
		return 0
	}
	return m.Pix[y*m.W+x]
}

// Empty 是否整张为 0。
func (m *ClipMask) Empty() bool {
	if m == nil {
		return true
	}
	for _, v := range m.Pix {
		if v != 0 {
			return false
		}
	}
	return true
}

// full 是否整张为 255(全 255 时遮罩等于没遮)。
func (m *ClipMask) full() bool {
	for _, v := range m.Pix {
		if v != 255 {
			return false
		}
	}
	return true
}

// MaskedFill 把外形按 b.Mode 填进 b.Dst, 并用 mask 乘覆盖率; mask 的 (0,0) 对齐 b.Bounds().Min。
func MaskedFill(b Brush, mask *ClipMask, s Shape, row Row) {
	if b.Dst == nil || s == nil || mask == nil || mask.Empty() {
		return
	}
	r := b.Bounds()
	if r.Empty() {
		return
	}
	sb := s.Bounds()
	y0, y1 := max(sb.Min.Y, r.Min.Y), min(sb.Max.Y, r.Max.Y)
	if y1 <= y0 {
		return
	}
	holder := b.Scratch.get()
	defer b.Scratch.put(holder)
	scratch := holder.spans
	if mask.full() {
		maskedFullRows(b, r, s, row, y0, y1, scratch)
		return
	}
	yEnd := min(y1, r.Min.Y+mask.H)
	for y := y0; y < yEnd; y++ {
		scratch = s.Spans(y, scratch[:0])
		if len(scratch) == 0 {
			continue
		}
		mrow := mask.Pix[(y-r.Min.Y)*mask.W:]
		for _, sp := range scratch {
			sp = clampSpan(sp, r)
			if sp.X1 <= sp.X0 {
				continue
			}
			maskedSpan(b.Dst, mrow, r.Min.X, y, y0, y1, sp, row, b.Mode, b.Hard)
		}
	}
}

// maskedFullRows 是掩码全 255 的快路径: 与 FillRows 同一套落笔, 只是省掉掩码读取, 因而零额外分配。
func maskedFullRows(b Brush, r image.Rectangle, s Shape, row Row, y0, y1 int, scratch []Span) {
	for y := y0; y < y1; y++ {
		scratch = s.Spans(y, scratch[:0])
		if len(scratch) == 0 {
			continue
		}
		c := row.At(y, y0, y1)
		for _, sp := range scratch {
			sp = clampSpan(sp, r)
			if sp.X1 <= sp.X0 {
				continue
			}
			if row.PerPixel() {
				b.spanPixels(y, sp, row, y0, y1)
				continue
			}
			b.span(y, sp, c)
		}
	}
}

// maskedSpan 与 FillSpan 同口径落一段, 每像素颜色取 row.PixelAt, 再乘掩码覆盖率。
func maskedSpan(dst *image.RGBA, mrow []uint8, ox, y, y0, y1 int, sp Span, row Row, mode Mode, hard bool) {
	lo, hi := int(math.Floor(sp.X0)), int(math.Ceil(sp.X1))
	if hi <= lo {
		return
	}
	if hard {
		if sp.X1-sp.X0 < 0.5 {
			return
		}
		if hi-lo == 1 {
			maskedPixel(dst, mrow, ox, lo, y, y0, y1, 1, row, mode, true)
			return
		}
		for x := lo; x < hi; x++ {
			if math.Min(sp.X1, float64(x+1))-math.Max(sp.X0, float64(x)) >= 0.5 {
				maskedPixel(dst, mrow, ox, x, y, y0, y1, 1, row, mode, true)
			}
		}
		return
	}
	if hi-lo == 1 {
		maskedPixel(dst, mrow, ox, lo, y, y0, y1, sp.X1-sp.X0, row, mode, false)
		return
	}
	left, right := lo, hi
	if cov := float64(lo+1) - sp.X0; cov < 1 {
		maskedPixel(dst, mrow, ox, lo, y, y0, y1, cov, row, mode, false)
		left = lo + 1
	}
	if cov := sp.X1 - float64(hi-1); cov < 1 {
		maskedPixel(dst, mrow, ox, hi-1, y, y0, y1, cov, row, mode, false)
		right = hi - 1
	}
	for x := left; x < right; x++ {
		maskedPixel(dst, mrow, ox, x, y, y0, y1, 1, row, mode, false)
	}
}

func maskedPixel(dst *image.RGBA, mrow []uint8, ox, x, y, y0, y1 int, cov float64, row Row, mode Mode, hard bool) {
	i := x - ox
	if i < 0 || i >= len(mrow) || x < dst.Rect.Min.X || x >= dst.Rect.Max.X || y < dst.Rect.Min.Y || y >= dst.Rect.Max.Y {
		return
	}
	if mrow[i] == 0 {
		return
	}
	if hard {
		cov = 1
	}
	c := maskedInk(row.PixelAt(x, y, y0, y1), cov, mrow[i])
	if c.A == 0 {
		return
	}
	rgba.BlendModeAt(dst.Pix, dst.PixOffset(x, y), c, mode)
}

// maskedInk 把覆盖率与掩码一起压进 alpha; m=255 时退化成 WithCoverage, 保证与无遮罩逐位相同。
func maskedInk(c color.RGBA, cov float64, m uint8) color.RGBA {
	if m == 255 {
		return rgba.WithCoverage(c, cov)
	}
	c.A = rgba.Clamp8(int(float64(c.A)*cov*float64(m)/255 + 0.5))
	return c
}

// MaskedSprite 同 Sprite, 但用 mask 乘覆盖率; mask 的 (0,0) 对齐 b.Bounds().Min。
func MaskedSprite(b Brush, mask *ClipMask, at, win, src image.Rectangle, img *image.RGBA, opts SpriteOpts) {
	if b.Dst == nil || img == nil || mask == nil || mask.Empty() {
		return
	}
	if mask.full() {
		b.Sprite(at, win, src, img, opts)
		return
	}
	r := b.Bounds()
	if r.Empty() {
		return
	}
	tmp := image.NewRGBA(r)
	inner := b
	inner.Dst = tmp
	inner.Sprite(at, win, src, img, opts)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		mj := y - r.Min.Y
		if mj >= mask.H {
			break
		}
		mrow := mask.Pix[mj*mask.W:]
		for x := r.Min.X; x < r.Max.X; x++ {
			mi := x - r.Min.X
			if mi >= mask.W || mrow[mi] == 0 {
				continue
			}
			i := tmp.PixOffset(x, y)
			a := tmp.Pix[i+3]
			if a == 0 {
				continue
			}
			if m := mrow[mi]; m != 255 {
				a = uint8((uint32(a)*uint32(m) + 127) / 255)
				if a == 0 {
					continue
				}
			}
			rgba.BlendAt(b.Dst.Pix, b.Dst.PixOffset(x, y),
				color.RGBA{R: tmp.Pix[i], G: tmp.Pix[i+1], B: tmp.Pix[i+2], A: a})
		}
	}
}
