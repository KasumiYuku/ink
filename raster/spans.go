package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// Mode 是合成方式: Over 是常规 alpha 混合, Add 用于发光, Multiply 用于压暗。
type Mode = rgba.Mode

const (
	Over     = rgba.Over
	Add      = rgba.Add
	Multiply = rgba.Multiply
	Screen   = rgba.Screen
	Copy     = rgba.Copy
)

// Row 是"这块地方用什么颜色", 三档: 纯色(字段)、每行一次(竖向渐变/衰减)、每像素一次(水平/径向)。
// 零值 = 全透明(不落笔)。前两档零闭包零算术, 第三档才逐像素。
type Row struct {
	Solid color.RGBA
	RowFn func(y, y0, y1 int) color.RGBA
	PixFn func(x, y, y0, y1 int) color.RGBA
}

// At 取第 y 行的颜色; y0/y1 是这次填充的竖直范围。
func (r Row) At(y, y0, y1 int) color.RGBA {
	if r.RowFn != nil {
		return r.RowFn(y, y0, y1)
	}
	return r.Solid
}

// PixelAt 取 (x,y) 这个像素的颜色(水平/径向渐变用); y0/y1 同上。
func (r Row) PixelAt(x, y, y0, y1 int) color.RGBA {
	if r.PixFn != nil {
		return r.PixFn(x, y, y0, y1)
	}
	return r.At(y, y0, y1)
}

// PerPixel 表示这档填充要逐像素求值(慢, 但水平/径向渐变只能这样)。
func (r Row) PerPixel() bool { return r.PixFn != nil }

// Solid 是纯色。
func Solid(c color.RGBA) Row { return Row{Solid: c} }

// VGradient 在 [y0,y1) 上从 top 插到 bottom。
func VGradient(top, bottom color.RGBA) Row {
	return Row{RowFn: func(y, y0, y1 int) color.RGBA {
		if y1-y0 <= 1 {
			return top
		}
		return rgba.Lerp(top, bottom, float64(y-y0)/float64(y1-y0-1))
	}}
}

// HGRadient 在每行上从 left 插到 right(横向渐变; 竖直方向不插值)。
func HGRadient(left, right color.RGBA, x0, x1 float64) Row {
	w := x1 - x0
	return Row{PixFn: func(x, _, _, _ int) color.RGBA {
		if w <= 0 {
			return left
		}
		return rgba.Lerp(left, right, (float64(x)+0.5-x0)/w)
	}}
}

// Radial 是以 center 为心、radius 为半径的径向渐变(内圈 inner, 到半径处 outer)。
func Radial(center Pt, radius float64, inner, outer color.RGBA) Row {
	if radius <= 0 {
		return Row{Solid: inner}
	}
	return Row{PixFn: func(x, y, _, _ int) color.RGBA {
		dx, dy := float64(x)+0.5-center.X, float64(y)+0.5-center.Y
		return rgba.Lerp(inner, outer, math.Hypot(dx, dy)/radius)
	}}
}

// Falloff 前 hold 行满值, 之后按 smootherstep 化开到 0(底部渐隐带与衰减遮罩都是它)。
func Falloff(c color.RGBA, hold float64) Row {
	return Row{RowFn: func(y, y0, y1 int) color.RGBA {
		span := float64(y1-y0) - hold
		t := 1.0
		if span > 0 {
			t = (float64(y-y0) - hold) / span
		}
		out := c
		out.A = rgba.Clamp8(int(float64(c.A)*(1-smootherstep(clamp01f(t))) + 0.5))
		return out
	}}
}

// Ramp 是等距多档色阶(两档就等于 VGradient)。
func Ramp(stops ...color.RGBA) Row {
	snapshot := append([]color.RGBA(nil), stops...)
	return Row{RowFn: func(y, y0, y1 int) color.RGBA {
		n := len(snapshot)
		switch {
		case n == 0:
			return color.RGBA{}
		case n == 1 || y1-y0 <= 1:
			return snapshot[0]
		}
		t := float64(y-y0) / float64(y1-y0-1) * float64(n-1)
		i := int(t)
		if i >= n-1 {
			return snapshot[n-1]
		}
		return rgba.Lerp(snapshot[i], snapshot[i+1], t-float64(i))
	}}
}

// Brush 是一次落笔的上下文: 画布、裁剪框、合成方式、并发策略都挂在它身上。
type Brush struct {
	Dst     *image.RGBA
	Clip    image.Rectangle
	Mode    Mode
	Par     Par
	Scratch *Scratch
	Masks   *MaskCache
	// Hard 关掉抗锯齿的中间覆盖率: 边界要么满要么空。
	Hard bool
}

// NewBrush 是推荐入口: 落笔上下文用链式构造, 免得手写字段时漏掉并发或缓存。
func NewBrush(dst *image.RGBA) Brush {
	return Brush{Dst: dst, Par: Par{Workers: 1, MinRows: 32}}
}

func (b Brush) WithPar(p Par) Brush              { b.Par = p; return b }
func (b Brush) WithMode(m Mode) Brush            { b.Mode = m; return b }
func (b Brush) WithClip(r image.Rectangle) Brush { b.Clip = r; return b }
func (b Brush) WithScratch(s *Scratch) Brush     { b.Scratch = s; return b }
func (b Brush) WithMasks(m *MaskCache) Brush     { b.Masks = m; return b }
func (b Brush) WithHard(on bool) Brush           { b.Hard = on; return b }

// Bounds 是这次落笔真正能写到的范围(画布 ∩ 裁剪框; 没设裁剪就只按画布裁)。
func (b Brush) Bounds() image.Rectangle {
	if b.Dst == nil {
		return image.Rectangle{}
	}
	if b.Clip.Empty() {
		return b.Dst.Rect
	}
	return b.Clip.Intersect(b.Dst.Rect)
}

// Fill 把整个外形按行填满: 第 y 行取 s.Span(y), 墨色取 row.At(y, y0, y1)。
func (b Brush) Fill(s Shape, row Row) {
	if s == nil {
		return
	}
	r := s.Bounds()
	b.FillRows(r.Min.Y, r.Max.Y, s, row)
}

// FillRows 限定行范围地填充外形。
func (b Brush) FillRows(y0, y1 int, s Shape, row Row) {
	if b.Dst == nil || s == nil {
		return
	}
	r := b.Bounds()
	y0, y1 = max(y0, r.Min.Y), min(y1, r.Max.Y)
	if y1 <= y0 {
		return
	}
	b.Par.Run(y1-y0, func(lo, hi int) {
		var holder *spanBuf
		var scratch []Span
		for y := y0 + lo; y < y0+hi; y++ {
			if one, ok := s.(SingleShape); ok {
				if sp, ok2 := one.Single(y); ok2 {
					sp = clampSpan(sp, r)
					if sp.X1 <= sp.X0 {
						continue
					}
					c := row.At(y, y0, y1)
					if row.PerPixel() {
						b.spanPixels(y, sp, row, y0, y1)
					} else {
						b.span(y, sp, c)
					}
					continue
				}
			}
			if holder == nil {
				holder = b.Scratch.get()
				scratch = holder.spans
			}
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
		if holder != nil {
			b.Scratch.put(holder)
		}
	})
}

// span 落一段; Hard 时把端点覆盖率二值化(边界要么满要么空)。
func (b Brush) span(y int, sp Span, c color.RGBA) {
	if !b.Hard {
		FillSpan(b.Dst, y, sp.X0, sp.X1, c, b.Mode)
		return
	}
	lo, hi := int(math.Floor(sp.X0+0.5)), int(math.Ceil(sp.X1-0.5))
	if lo < hi {
		FillRectMode(b.Dst, image.Rect(lo, y, hi, y+1), c, b.Mode)
		return
	}
	FillSpan(b.Dst, y, sp.X0, sp.X1, c, b.Mode)
}

// spanPixels 是逐像素那一档(水平/径向渐变): 端点覆盖率照旧, 颜色逐点求。
func (b Brush) spanPixels(y int, sp Span, row Row, y0, y1 int) {
	if sp.X1-sp.X0 < 1 {
		if b.Hard {
			if sp.X1-sp.X0 >= 0.5 {
				x := int(math.Floor(sp.X0 + 0.5))
				FillPixel(b.Dst, x, y, row.PixelAt(x, y, y0, y1), b.Mode)
			}
			return
		}
		x := int(math.Floor(sp.X0))
		c := row.PixelAt(x, y, y0, y1)
		FillPixel(b.Dst, x, y, rgba.WithCoverage(c, sp.X1-sp.X0), b.Mode)
		return
	}
	lo, hi := int(math.Floor(sp.X0)), int(math.Ceil(sp.X1))
	for x := lo; x < hi; x++ {
		cov := math.Min(sp.X1, float64(x+1)) - math.Max(sp.X0, float64(x))
		if cov <= 0 {
			continue
		}
		c := row.PixelAt(x, y, y0, y1)
		if b.Hard {
			if cov < 0.5 {
				continue
			}
			FillPixel(b.Dst, x, y, c, b.Mode)
			continue
		}
		FillPixel(b.Dst, x, y, rgba.WithCoverage(c, cov), b.Mode)
	}
}

// Blur 糊一块区域(半径是像素, 方框模糊)。
func (b Brush) Blur(r image.Rectangle, radius int) {
	if b.Dst == nil || radius <= 0 {
		return
	}
	Blur(b.Dst, r, radius, b.Par)
}

// Stroke 沿外形的内缘描一圈宽 w: 就是"外形减内缩形"那个环, 角上不会叠两次。
func (b Brush) Stroke(s Shape, w float64, row Row) {
	if s == nil || w <= 0 {
		return
	}
	b.ringRows(s, Outset(s, -w), row)
}

// ringRows 填"外形状减内形状"的环; 两边都是单段外形(卡片/条/它们的描边与辉光)时零缓冲零分配。
func (b Brush) ringRows(outer, inner Shape, row Row) {
	if b.Dst == nil || outer == nil || inner == nil {
		return
	}
	r := b.Bounds()
	b.ringRowsWithin(r.Min.Y, r.Max.Y, outer, inner, row)
}

func (b Brush) ringRowsWithin(y0, y1 int, outer, inner Shape, row Row) {
	r := b.Bounds()
	y0, y1 = max(y0, r.Min.Y), min(y1, r.Max.Y)
	if y1 <= y0 {
		return
	}
	o1, oSingle := outer.(SingleShape)
	i1, iSingle := inner.(SingleShape)
	b.Par.Run(y1-y0, func(lo, hi int) {
		var oh, ih *spanBuf
		var obuf, ibuf []Span
		if !oSingle {
			oh = b.Scratch.get()
			obuf = oh.spans
		}
		if !iSingle {
			ih = b.Scratch.get()
			ibuf = ih.spans
		}
		for y := y0 + lo; y < y0+hi; y++ {
			var osp Span
			var ok bool
			if oSingle {
				osp, ok = o1.Single(y)
			} else {
				obuf = outer.Spans(y, obuf[:0])
				if len(obuf) > 0 {
					osp, ok = obuf[0], true
				}
			}
			osp = clampSpan(osp, r)
			if !ok || osp.X1 <= osp.X0 {
				continue
			}
			c := row.At(y, y0, y1)
			var isp Span
			var iok bool
			if iSingle {
				isp, iok = i1.Single(y)
			} else {
				ibuf = inner.Spans(y, ibuf[:0])
				if len(ibuf) > 0 {
					isp, iok = ibuf[0], true
				}
			}
			isp = clampSpan(isp, r)
			if !iok || isp.X1 <= isp.X0 {
				b.paint(y, osp, c, row, y0, y1)
				continue
			}
			b.paint(y, Span{X0: osp.X0, X1: math.Min(osp.X1, isp.X0)}, c, row, y0, y1)
			b.paint(y, Span{X0: math.Max(osp.X0, isp.X1), X1: osp.X1}, c, row, y0, y1)
		}
		if oh != nil {
			b.Scratch.put(oh)
		}
		if ih != nil {
			b.Scratch.put(ih)
		}
	})
}

// paint 落一段(逐像素那一档走 spanPixels)。
func (b Brush) paint(y int, sp Span, c color.RGBA, row Row, y0, y1 int) {
	if sp.X1 <= sp.X0 {
		return
	}
	if row.PerPixel() {
		b.spanPixels(y, sp, row, y0, y1)
		return
	}
	b.span(y, sp, c)
}

// Round 铺一个圆角矩形(每行墨色可取 row); 有掩码缓存时按表铺, 否则逐像素算覆盖率 —— 两者逐字节相同。
func (b Brush) Round(r image.Rectangle, radius float64, row Row) {
	if b.Dst == nil {
		return
	}
	r = r.Intersect(b.Dst.Rect)
	if r.Empty() {
		return
	}
	radius = min(radius, float64(min(r.Dx(), r.Dy()))/2)
	if b.Masks != nil {
		b.Masks.Round(radius).Fill(b.Dst, r, row, b.Mode, b.Hard)
		return
	}
	b.fillRoundRows(r, radius, row)
}

// fillRoundRows 是逐像素算覆盖率的那条路(掩码缓存未接入时走它, 结果与掩码逐字节相同)。
func (b Brush) fillRoundRows(r image.Rectangle, radius float64, row Row) {
	if radius <= 0 {
		b.FillRows(r.Min.Y, r.Max.Y, BoxShape{Box: B(float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy()))}, row)
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		c := row.At(y, r.Min.Y, r.Max.Y)
		for x := r.Min.X; x < r.Max.X; x++ {
			cov := coverRound(x, y, r, radius)
			if b.Hard {
				if cov >= 0.5 {
					FillPixel(b.Dst, x, y, c, b.Mode)
				}
				continue
			}
			if cov == 0 {
				continue
			}
			FillPixel(b.Dst, x, y, coverageInk(c, cov), b.Mode)
		}
	}
}

// FillSpan 是低层原语: 填一行的浮点范围(两端按覆盖率混色, 中间整段一次落笔)。调用方请用 Brush。
func FillSpan(dst *image.RGBA, y int, x0, x1 float64, c color.RGBA, mode Mode) {
	if dst == nil || c.A == 0 || x1 <= x0 {
		return
	}
	lo, hi := int(math.Floor(x0)), int(math.Ceil(x1))
	if hi <= lo {
		return
	}
	// 只落在一个像素里: 两端重合, 按整段长度混一次(不能左右各混一次, 那是叠两遍)。
	if hi-lo == 1 {
		FillPixel(dst, lo, y, rgba.WithCoverage(c, x1-x0), mode)
		return
	}
	left, right := lo, hi
	if cov := float64(lo+1) - x0; cov < 1 {
		FillPixel(dst, lo, y, rgba.WithCoverage(c, cov), mode)
		left = lo + 1
	}
	if cov := x1 - float64(hi-1); cov < 1 {
		FillPixel(dst, hi-1, y, rgba.WithCoverage(c, cov), mode)
		right = hi - 1
	}
	if left < right {
		r := image.Rect(left, y, right, y+1)
		if mode == Over {
			Fill(dst, r, c)
			return
		}
		FillRectMode(dst, r, c, mode)
	}
}

// FillPixel 按合成方式落一个像素(自带画布裁剪)。
func FillPixel(dst *image.RGBA, x, y int, c color.RGBA, mode Mode) {
	if c.A == 0 || x < dst.Rect.Min.X || x >= dst.Rect.Max.X || y < dst.Rect.Min.Y || y >= dst.Rect.Max.Y {
		return
	}
	rgba.BlendModeAt(dst.Pix, dst.PixOffset(x, y), c, mode)
}

// FillRectMode 是带合成方式的实心矩形(Over 请直接用 Fill, 那条路是内联展开的)。
func FillRectMode(dst *image.RGBA, r image.Rectangle, c color.RGBA, mode Mode) {
	if mode == Over {
		Fill(dst, r, c)
		return
	}
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			rgba.BlendModeAt(dst.Pix, dst.PixOffset(x, y), c, mode)
		}
	}
}

// clampSpan 把一段夹进裁剪框。
func clampSpan(sp Span, r image.Rectangle) Span {
	return Span{X0: max(sp.X0, float64(r.Min.X)), X1: min(sp.X1, float64(r.Max.X))}
}

func smootherstep(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func clamp01f(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
