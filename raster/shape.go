package raster

import (
	"image"
	"math"
)

// Lean15 是 15° 斜切的正切。
const Lean15 = 0.2679492

// Box 是浮点矩形: 版式常量带 0.5px 精度, 落到像素前不该被整数化。
type Box struct{ X, Y, W, H float64 }

func B(x, y, w, h float64) Box { return Box{X: x, Y: y, W: w, H: h} }

func (b Box) X1() float64        { return b.X + b.W }
func (b Box) Y1() float64        { return b.Y + b.H }
func (b Box) CenterY() float64   { return b.Y + b.H/2 }
func (b Box) Empty() bool        { return b.W <= 0 || b.H <= 0 }
func (b Box) Grow(d float64) Box { return Box{X: b.X - d, Y: b.Y - d, W: b.W + 2*d, H: b.H + 2*d} }

// Bounds 折成像素矩形(两端四舍五入), 与既有工程的版式口径一致。
func (b Box) Bounds() image.Rectangle {
	return image.Rect(int(math.Round(b.X)), int(math.Round(b.Y)),
		int(math.Round(b.X1())), int(math.Round(b.Y1())))
}

// Span 是一行的水平区间(左闭右开, 浮点); 端点的覆盖率由小数部分给。
type Span struct{ X0, X1 float64 }

// Shape 是外形的唯一抽象: 第 y 行可以有若干段 —— 凹形、洞、描边那一圈环、任意路径都是多段。
// Spans 把这一行的区间追加进调用方的缓冲并返回它(自备缓冲 ⇒ 热路径零分配), 没覆盖的行追加 0 段。
type Shape interface {
	Spans(y int, dst []Span) []Span
	Bounds() image.Rectangle
}

// AppendSpan 追加一段; 空段直接丢掉。
func AppendSpan(dst []Span, x0, x1 float64) []Span {
	if x1 <= x0 {
		return dst
	}
	return append(dst, Span{X0: x0, X1: x1})
}

// SpanAt 取一行的第一段(单段外形的便捷读法); buf 是调用方自备的缓冲, 避免热路径分配。
func SpanAt(s Shape, y int, buf []Span) (Span, bool) {
	spans := s.Spans(y, buf[:0])
	if len(spans) == 0 {
		return Span{}, false
	}
	return spans[0], true
}

// SingleShape 是"这一行最多一段"的外形: 提供它就能免掉逐行缓冲(否则每行都要借一块)。
// 多段外形(路径/并集/环)不实现它, 走 Spans 那条通用路。
type SingleShape interface {
	Single(y int) (Span, bool)
}

// spanOf 取一行的第一段: 优先走 SingleShape(零缓冲), 否则用调用方的缓冲走 Spans。
func spanOf(s Shape, y int, buf []Span) (Span, bool) {
	if one, ok := s.(SingleShape); ok {
		return one.Single(y)
	}
	spans := s.Spans(y, buf[:0])
	if len(spans) == 0 {
		return Span{}, false
	}
	return spans[0], true
}

// Outsetter 是"能按几何外扩"的形状: 圆角矩形靠它让弧心落在同一条法线上。
type Outsetter interface{ Outset(d float64) Shape }

// Outset 外扩 d(负值内缩): 认得出几何的形状走自己的算法, 其余按行内缩。
func Outset(s Shape, d float64) Shape {
	if o, ok := s.(Outsetter); ok {
		return o.Outset(d)
	}
	return inset{s: s, d: d}
}

// Ring 是"外形状减内形状"的环; 描边就是它 —— 角上天然不会叠两次。
func Ring(outer, inner Shape) Shape { return ring{outer: outer, inner: inner} }

// LeftBar 是"外形每行最左边 w 宽"的那一条, RightBar 同理取最右边 —— 竖条跟着斜边/圆角收时用它。
//
// 某一行比 w 还窄(圆角收口那几行)时整行都算: 色条不该画到外形外面去。
func LeftBar(s Shape, w float64) Shape  { return edgeBar{s: s, w: w} }
func RightBar(s Shape, w float64) Shape { return edgeBar{s: s, w: -w} }

type edgeBar struct {
	s Shape
	// w 为正是左缘那一条, 为负是右缘那一条。
	w float64
}

func (e edgeBar) Single(y int) (Span, bool) { return e.bar(y) }

func (e edgeBar) Spans(y int, dst []Span) []Span {
	sp, ok := e.bar(y)
	if !ok {
		return dst
	}
	return AppendSpan(dst, sp.X0, sp.X1)
}

func (e edgeBar) Bounds() image.Rectangle { return e.s.Bounds() }

// Outset 让色条跟着外形一起外扩/内缩(见 Outsetter): 斜切板的左缘条外扩后贴着同一条斜边。
func (e edgeBar) Outset(d float64) Shape { return edgeBar{s: Outset(e.s, d), w: e.w} }

func (e edgeBar) bar(y int) (Span, bool) {
	sp, ok := spanOf(e.s, y, nil)
	if !ok {
		return Span{}, false
	}
	w := math.Abs(e.w)
	if w <= 0 || w >= sp.X1-sp.X0 {
		return sp, true
	}
	if e.w > 0 {
		return Span{X0: sp.X0, X1: sp.X0 + w}, true
	}
	return Span{X0: sp.X1 - w, X1: sp.X1}, true
}

type ring struct {
	outer, inner Shape
}

func (r ring) Spans(y int, dst []Span) []Span {
	// 两边都"每行一段"(卡片/描边的绝大多数情况)时完全不需要缓冲 —— 这是热路径。
	if o, ok := r.outer.(SingleShape); ok {
		if i, iok := r.inner.(SingleShape); iok {
			osp, ok1 := o.Single(y)
			if !ok1 {
				return dst
			}
			isp, ok2 := i.Single(y)
			if !ok2 {
				return AppendSpan(dst, osp.X0, osp.X1)
			}
			var holes [1]Span
			return subtract(dst, osp, append(holes[:0], isp))
		}
	}
	var obuf, ibuf [4]Span
	os := r.outer.Spans(y, obuf[:0])
	if len(os) == 0 {
		return dst
	}
	is := r.inner.Spans(y, ibuf[:0])
	if len(is) == 0 {
		return append(dst, os...)
	}
	for _, o := range os {
		dst = subtract(dst, o, is)
	}
	return dst
}

func (r ring) Bounds() image.Rectangle { return r.outer.Bounds() }

func (r ring) Outset(d float64) Shape {
	return ring{outer: Outset(r.outer, d), inner: Outset(r.inner, d)}
}

// subtract 把 holes(已排序、互不相交)从 spa 里挖掉。
func subtract(dst []Span, spa Span, holes []Span) []Span {
	cur := spa.X0
	for _, h := range holes {
		if h.X1 <= cur {
			continue
		}
		if h.X0 >= spa.X1 {
			break
		}
		if h.X0 > cur {
			dst = AppendSpan(dst, cur, math.Min(h.X0, spa.X1))
		}
		cur = math.Max(cur, h.X1)
		if cur >= spa.X1 {
			return dst
		}
	}
	return AppendSpan(dst, cur, spa.X1)
}

// BoxShape 是"矩形 + 圆角 + 斜切"这一族外形, 出图里的形状绝大多数都是它。
// 斜切以自身竖直中线为基准, 越靠上的行越往右; 不取整正是为了没有台阶。
type BoxShape struct {
	Box    Box
	Radius float64
	Lean   float64
}

func Rect(b Box) Shape                       { return BoxShape{Box: b} }
func Round(b Box, radius float64) Shape      { return BoxShape{Box: b, Radius: radius} }
func Skew(b Box, radius, lean float64) Shape { return BoxShape{Box: b, Radius: radius, Lean: lean} }

// SkewFrom 是"斜切基准不在自己身上"的斜切矩形: 一整页共用一条斜切基线时用它(见 SkewDX)。
//
// 与 Skew 的两点区别: 位移按 anchor 的竖直中线算, 且取整到**整像素** —— 一整页的斜边落在同一条
// 像素网格上, 边与边之间才不会一段一段地错开(逐行浮点位移会在窄条上露出来)。
func SkewFrom(b Box, lean float64, anchor Box) Shape {
	return skewFrom{b: b, lean: lean, anchor: anchor}
}

type skewFrom struct {
	b      Box
	lean   float64
	anchor Box
}

func (s skewFrom) Range(y int) (float64, float64) {
	dx := float64(SkewDX(s.anchor.Y, s.anchor.Y1(), float64(y), s.lean))
	return s.b.X + dx, s.b.X1() + dx
}

func (s skewFrom) Single(y int) (Span, bool) {
	x0, x1 := s.Range(y)
	return Span{X0: x0, X1: x1}, x1 > x0
}

func (s skewFrom) Spans(y int, dst []Span) []Span {
	x0, x1 := s.Range(y)
	return AppendSpan(dst, x0, x1)
}

func (s skewFrom) Bounds() image.Rectangle { return s.b.Bounds() }

// Outset 整块外扩 d, 圆角半径同步涨 d(弧心落在同一条法线上); d 为负就是内缩。
func (s BoxShape) Outset(d float64) Shape {
	radius := s.Radius + d
	if radius < 0 {
		radius = 0
	}
	return BoxShape{Box: s.Box.Grow(d), Radius: radius, Lean: s.Lean}
}

// Range 是这一行的水平范围: 斜切与圆角算在同一个式子里。
func (s BoxShape) Range(y int) (float64, float64) {
	return SpanOf(s.Box, s.Radius, s.Lean, y)
}

// SpanOf 是"斜切圆角矩形在第 y 行的水平范围"的纯函数读法: 不建形状、不分配 —— 热路径(逐行读几百上千次)
// 用它, 其余地方用形状。算术只有这一份, 两种读法不会各算一套。
func SpanOf(b Box, radius, lean float64, y int) (float64, float64) {
	cy := float64(y) + 0.5
	top, bottom := b.Y, b.Y1()
	if cy < top || cy > bottom {
		return 0, 0
	}
	dx := 0.0
	if lean != 0 {
		dx = lean * (b.CenterY() - cy)
	}
	x0, x1 := b.X+dx, b.X1()+dx
	if radius > 0 {
		if d := cy - top; d < radius {
			k := radius - math.Sqrt(radius*radius-(radius-d)*(radius-d))
			x0, x1 = x0+k, x1-k
		}
		if d := bottom - cy; d < radius {
			k := radius - math.Sqrt(radius*radius-(radius-d)*(radius-d))
			x0, x1 = x0+k, x1-k
		}
	}
	return x0, x1
}

// Single 是 BoxShape 的免缓冲读法(它每行就是一段)。
func (s BoxShape) Single(y int) (Span, bool) {
	x0, x1 := s.Range(y)
	return Span{X0: x0, X1: x1}, x1 > x0
}

func (s BoxShape) Spans(y int, dst []Span) []Span {
	x0, x1 := s.Range(y)
	return AppendSpan(dst, x0, x1)
}

func (s BoxShape) Bounds() image.Rectangle {
	pad := math.Abs(s.Lean) * s.Box.H / 2
	return image.Rect(
		int(math.Floor(s.Box.X-pad)), int(math.Floor(s.Box.Y)),
		int(math.Ceil(s.Box.X1()+pad)), int(math.Ceil(s.Box.Y1())))
}

type shifted struct {
	s  Shape
	dx float64
	dy int
}

// ShiftBy 平移外形: 横纵向都按整块挪, 斜边保持原样、不做重采样。
func ShiftBy(s Shape, dx float64, dy int) Shape { return shifted{s: s, dx: dx, dy: dy} }

func (s shifted) Single(y int) (Span, bool) {
	sp, ok := s.s.(SingleShape)
	if !ok {
		return Span{}, false
	}
	out, ok2 := sp.Single(y - s.dy)
	if !ok2 {
		return Span{}, false
	}
	return Span{X0: out.X0 + s.dx, X1: out.X1 + s.dx}, true
}

func (s shifted) Spans(y int, dst []Span) []Span {
	n := len(dst)
	dst = s.s.Spans(y-s.dy, dst)
	for i := n; i < len(dst); i++ {
		dst[i].X0 += s.dx
		dst[i].X1 += s.dx
	}
	return dst
}

func (s shifted) Bounds() image.Rectangle {
	return s.s.Bounds().Add(image.Pt(int(math.Round(s.dx)), s.dy))
}

func (s shifted) Outset(d float64) Shape { return shifted{s: Outset(s.s, d), dx: s.dx, dy: s.dy} }

type clipped struct {
	s Shape
	r image.Rectangle
}

// ClipShape 把外形夹进一个矩形(画布或裁剪框)。
func ClipShape(s Shape, r image.Rectangle) Shape { return clipped{s: s, r: r} }

func (s clipped) Single(y int) (Span, bool) {
	inner, ok := s.s.(SingleShape)
	if !ok || y < s.r.Min.Y || y >= s.r.Max.Y {
		return Span{}, false
	}
	sp, ok2 := inner.Single(y)
	if !ok2 {
		return Span{}, false
	}
	sp.X0 = math.Max(sp.X0, float64(s.r.Min.X))
	sp.X1 = math.Min(sp.X1, float64(s.r.Max.X))
	return sp, sp.X1 > sp.X0
}

func (s clipped) Spans(y int, dst []Span) []Span {
	if y < s.r.Min.Y || y >= s.r.Max.Y {
		return dst
	}
	n := len(dst)
	dst = s.s.Spans(y, dst)
	for i := n; i < len(dst); i++ {
		dst[i].X0 = math.Max(dst[i].X0, float64(s.r.Min.X))
		dst[i].X1 = math.Min(dst[i].X1, float64(s.r.Max.X))
	}
	return dst
}

func (s clipped) Bounds() image.Rectangle { return s.s.Bounds().Intersect(s.r) }

func (s clipped) Outset(d float64) Shape { return clipped{s: Outset(s.s, d), r: s.r} }

type profile struct {
	fn func(y int) (x0, x1 float64)
	b  image.Rectangle
}

// Profile 把一个逐行函数当形状。
func Profile(fn func(y int) (x0, x1 float64), b image.Rectangle) Shape {
	return profile{fn: fn, b: b}
}

func (s profile) Single(y int) (Span, bool) {
	x0, x1 := s.fn(y)
	return Span{X0: x0, X1: x1}, x1 > x0
}

func (s profile) Spans(y int, dst []Span) []Span {
	x0, x1 := s.fn(y)
	return AppendSpan(dst, x0, x1)
}

func (s profile) Bounds() image.Rectangle { return s.b }

type inset struct {
	s Shape
	d float64
}

func (s inset) Single(y int) (Span, bool) {
	inner, ok := s.s.(SingleShape)
	if !ok {
		return Span{}, false
	}
	sp, ok2 := inner.Single(y)
	if !ok2 {
		return Span{}, false
	}
	sp.X0, sp.X1 = sp.X0+s.d, sp.X1-s.d
	return sp, sp.X1 > sp.X0
}

func (s inset) Spans(y int, dst []Span) []Span {
	n := len(dst)
	dst = s.s.Spans(y, dst)
	for i := n; i < len(dst); i++ {
		dst[i].X0 += s.d
		dst[i].X1 -= s.d
	}
	return dst
}

func (s inset) Bounds() image.Rectangle {
	b := s.s.Bounds().Inset(-int(math.Ceil(s.d)))
	if b.Empty() {
		return image.Rectangle{}
	}
	return b
}

func (s inset) Outset(d float64) Shape { return inset{s: s.s, d: s.d + d} }

// TransformShape 把仿射变换作用到外形上。恒等与纯平移走精确路径; 路径(*Path)整体变换也精确;
// 其余形状先按行采成多边形再变换(近似, 旋转/斜切时边界按行采样精度)。
func TransformShape(s Shape, m Matrix) Shape {
	if s == nil {
		return nil
	}
	if m.IsIdentity() {
		return s
	}
	if p, ok := s.(*Path); ok {
		p.Transform(m)
		return p
	}
	if m.A == 1 && m.D == 1 && m.B == 0 && m.C == 0 {
		return ShiftBy(s, m.E, int(m.F))
	}
	return newTransformed(s, m)
}

// Union 把若干外形并成一个(逐行区间取并集, 重叠处只算一次)。
func Union(parts ...Shape) Shape { return union(parts) }

type union []Shape

func (u union) Spans(y int, dst []Span) []Span {
	n := len(dst)
	var buf [8]Span
	for _, s := range u {
		buf2 := s.Spans(y, buf[:0])
		dst = append(dst, buf2...)
	}
	return mergeSpans(dst, n)
}

func (u union) Bounds() image.Rectangle {
	var out image.Rectangle
	for i, s := range u {
		if i == 0 {
			out = s.Bounds()
			continue
		}
		out = out.Union(s.Bounds())
	}
	return out
}

// newTransformed 把外形按行采成 1px 高的四边形, 逐角变换后并成外形。
// 它只用于非路径形状的旋转/斜切(路径走精确的 Path.Transform), 精度即行采样精度。
func newTransformed(s Shape, m Matrix) Shape {
	b := s.Bounds()
	if b.Empty() {
		return outlineShape{}
	}
	var polys []poly
	var buf [8]Span
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for _, sp := range s.Spans(y, buf[:0]) {
			if sp.X1 <= sp.X0 {
				continue
			}
			x0, x1 := sp.X0, sp.X1
			y0, y1 := float64(y), float64(y+1)
			ax, ay := m.Apply(x0, y0)
			bx, by := m.Apply(x1, y0)
			cx, cy := m.Apply(x1, y1)
			dx, dy := m.Apply(x0, y1)
			polys = append(polys, poly{{X: ax, Y: ay}, {X: bx, Y: by}, {X: cx, Y: cy}, {X: dx, Y: dy}})
		}
	}
	return finish(polys)
}
