package raster

import (
	"image"
	"image/color"
	"math"
)

// Pt 是浮点落点。
type Pt struct{ X, Y float64 }

// Polyline 按列算纵向覆盖率, 描出带宽度的折线。
//
// 落笔一律走 brush 自己的那段路径(见 span), 于是 Clip / Mode / Hard 与其它原语一个口径 ——
// 以前这里直接调包级的 Fill/FillSpan, 设了 Clip 的 brush 描折线会画到框外面去。
func (b Brush) Polyline(pts []Pt, width float64, ink color.RGBA) {
	if b.Dst == nil || len(pts) == 0 || ink.A == 0 {
		return
	}
	w := width
	if w < 1 {
		w = 1
	}
	r := b.Bounds()
	// 单点: 给一个小方块(一个像素的点在缩图之后基本看不见)。
	if len(pts) == 1 {
		x := int(math.Round(pts[0].X - w/2))
		y := int(math.Round(pts[0].Y - w/2))
		box := image.Rect(x, y, x+int(math.Ceil(w)), y+int(math.Ceil(w))).Intersect(r)
		for yy := box.Min.Y; yy < box.Max.Y; yy++ {
			b.span(yy, Span{X0: float64(box.Min.X), X1: float64(box.Max.X)}, ink)
		}
		return
	}
	half := w / 2
	for i := 0; i+1 < len(pts); i++ {
		from, to := pts[i], pts[i+1]
		if to.X < from.X {
			from, to = to, from
		}
		x0 := int(math.Floor(from.X))
		x1 := int(math.Ceil(to.X))
		if x1 <= x0 {
			x1 = x0 + 1 // 同一列上的两个点(竖直段): 也要落一笔
		}
		for x := max(x0, r.Min.X); x < min(x1, r.Max.X); x++ {
			ya := segY(from, to, float64(x)-0.5)
			yb := segY(from, to, float64(x)+0.5)
			if ya > yb {
				ya, yb = yb, ya
			}
			top, bottom := ya-half, yb+half
			if bottom <= top {
				bottom = top + 1
			}
			for y := max(int(math.Floor(top)), r.Min.Y); y < min(int(math.Ceil(bottom)), r.Max.Y); y++ {
				cov := math.Min(bottom, float64(y)+1) - math.Max(top, float64(y))
				if cov <= 0 {
					continue
				}
				col := ink
				if cov < 1 {
					col.A = uint8(math.Round(float64(ink.A) * cov))
				}
				b.span(y, Span{X0: float64(x), X1: float64(x + 1)}, col)
			}
		}
	}
}

// segY 是线段 a→b 在横坐标 x 处的纵坐标; 两点同列时取两端的较高者。
func segY(a, b Pt, x float64) float64 {
	if b.X <= a.X {
		return math.Min(a.Y, b.Y)
	}
	t := (x - a.X) / (b.X - a.X)
	if t <= 0 {
		return a.Y
	}
	if t >= 1 {
		return b.Y
	}
	return a.Y + (b.Y-a.Y)*t
}

// Area 是折线以下的面积(曲线与基线 baseY 之间): 每行给出若干段。
func Area(pts []Pt, baseY float64) Shape { return area{pts: pts, baseY: baseY} }

// AreaWithin 同 Area, 但整体夹进 clip 外形。
func AreaWithin(pts []Pt, baseY float64, clip Shape) Shape {
	return areaClip{inner: Area(pts, baseY), clip: clip}
}

type area struct {
	pts   []Pt
	baseY float64
}

func (a area) Spans(y int, dst []Span) []Span {
	cy := float64(y) + 0.5
	if len(a.pts) == 0 || cy > a.baseY {
		return dst
	}
	n := len(dst)
	for i := 0; i+1 < len(a.pts); i++ {
		p, q := a.pts[i], a.pts[i+1]
		if p.X > q.X {
			p, q = q, p
		}
		tLo, tHi := 0.0, 1.0
		if dy := q.Y - p.Y; dy == 0 {
			if p.Y > cy {
				continue
			}
		} else {
			t := (cy - p.Y) / dy
			if dy > 0 {
				tHi = math.Min(tHi, t)
			} else {
				tLo = math.Max(tLo, t)
			}
			if tLo >= tHi {
				continue
			}
		}
		ax := p.X + (q.X-p.X)*tLo
		bx := p.X + (q.X-p.X)*tHi
		if ax > bx {
			ax, bx = bx, ax
		}
		if bx > ax {
			dst = append(dst, Span{X0: ax, X1: bx})
		}
	}
	return mergeSpans(dst, n)
}

func (a area) Bounds() image.Rectangle {
	if len(a.pts) == 0 {
		return image.Rectangle{}
	}
	minX, maxX, minY := a.pts[0].X, a.pts[0].X, a.pts[0].Y
	for _, p := range a.pts[1:] {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY = math.Min(minY, p.Y)
	}
	if a.baseY <= minY {
		return image.Rectangle{}
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX)), int(math.Ceil(a.baseY)))
}

type areaClip struct {
	inner Shape
	clip  Shape
}

func (a areaClip) Spans(y int, dst []Span) []Span {
	n := len(dst)
	dst = a.inner.Spans(y, dst)
	area := dst[n:]
	if len(area) == 0 {
		return dst
	}
	dst = a.clip.Spans(y, dst)
	clip := dst[n+len(area):]
	if len(clip) == 0 {
		return dst[:n]
	}
	out := n
	for _, sp := range area {
		for _, c := range clip {
			lo, hi := math.Max(sp.X0, c.X0), math.Min(sp.X1, c.X1)
			if hi <= lo {
				continue
			}
			if out > n && lo <= dst[out-1].X1 {
				dst[out-1].X1 = math.Max(dst[out-1].X1, hi)
				continue
			}
			dst[out] = Span{X0: lo, X1: hi}
			out++
		}
	}
	return dst[:out]
}

func (a areaClip) Bounds() image.Rectangle {
	return a.inner.Bounds().Intersect(a.clip.Bounds())
}

// mergeSpans 把 dst[n:] 里的区间按 X0 排好并合并重叠/相接的那些。
func mergeSpans(dst []Span, n int) []Span {
	sp := dst[n:]
	for i := 1; i < len(sp); i++ {
		for j := i; j > 0 && sp[j].X0 < sp[j-1].X0; j-- {
			sp[j], sp[j-1] = sp[j-1], sp[j]
		}
	}
	out := n
	for _, s := range sp {
		if out > n && s.X0 <= dst[out-1].X1 {
			dst[out-1].X1 = math.Max(dst[out-1].X1, s.X1)
			continue
		}
		dst[out] = s
		out++
	}
	return dst[:out]
}
