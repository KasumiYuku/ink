package raster

import (
	"image"
	"math"
)

// Cap 是线段两端的形状。
type Cap uint8

const (
	CapButt Cap = iota
	CapRound
	CapSquare
)

// Join 是拐角的形状。
type Join uint8

const (
	JoinMiter Join = iota
	JoinRound
	JoinBevel
)

// StrokeOpts 是中心线描边的形状参数。
type StrokeOpts struct {
	Width      float64
	Cap        Cap
	Join       Join
	MiterLimit float64
	Dash       []float64
	Closed     bool
}

// poly 是一块凸多边形, 逐行采样时取水平线与它的交区间。
type poly []Pt

type outlineShape struct {
	polys []poly
	b     image.Rectangle
}

// Outline 把折线按中心线描边成外形; 结果可以直接 Fill/ClipShape/Shadow/Glow。
func Outline(pts []Pt, opts StrokeOpts) Shape {
	h := opts.Width / 2
	if opts.Width <= 0 || len(pts) == 0 {
		return outlineShape{}
	}
	clean, closed := cleanPts(pts, opts.Closed)
	if len(clean) == 0 {
		return outlineShape{}
	}
	if len(clean) == 1 {
		return finish(singlePoint(clean[0], h, opts.Cap))
	}
	var polys []poly
	if len(opts.Dash) > 0 {
		for _, sub := range dashSplit(clean, closed, opts.Dash) {
			if len(sub) >= 2 {
				polys = appendStroke(polys, sub, false, opts, h)
			}
		}
	} else {
		polys = appendStroke(polys, clean, closed, opts, h)
	}
	return finish(polys)
}

func (o outlineShape) Spans(y int, dst []Span) []Span {
	if y < o.b.Min.Y || y >= o.b.Max.Y {
		return dst
	}
	cy := float64(y) + 0.5
	n := len(dst)
	for _, p := range o.polys {
		if lo, hi, ok := polySpan(p, cy); ok {
			dst = append(dst, Span{X0: lo, X1: hi})
		}
	}
	return mergeSpans(dst, n)
}

func (o outlineShape) Bounds() image.Rectangle { return o.b }

func polySpan(p poly, cy float64) (float64, float64, bool) {
	lo, hi := 0.0, 0.0
	found := false
	add := func(x float64) {
		if !found {
			lo, hi, found = x, x, true
		} else {
			lo, hi = math.Min(lo, x), math.Max(hi, x)
		}
	}
	n := len(p)
	for i := 0; i < n; i++ {
		a, b := p[i], p[(i+1)%n]
		if a.Y == b.Y {
			// 采样线正好压在水平边上时把该边算进来, 与逐行中心口径一致。
			if a.Y == cy {
				add(a.X)
				add(b.X)
			}
			continue
		}
		if (a.Y > cy) != (b.Y > cy) {
			add(a.X + (b.X-a.X)*(cy-a.Y)/(b.Y-a.Y))
		}
	}
	if !found || hi <= lo {
		return 0, 0, false
	}
	return lo, hi, true
}

func finish(polys []poly) Shape {
	if len(polys) == 0 {
		return outlineShape{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range polys {
		for _, q := range p {
			minX, maxX = math.Min(minX, q.X), math.Max(maxX, q.X)
			minY, maxY = math.Min(minY, q.Y), math.Max(maxY, q.Y)
		}
	}
	return outlineShape{polys: polys, b: image.Rect(
		int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))}
}

// cleanPts 去掉相邻重合点; Closed 时末点等于首点则合并。
func cleanPts(pts []Pt, closed bool) ([]Pt, bool) {
	out := make([]Pt, 0, len(pts))
	for _, p := range pts {
		if len(out) > 0 && out[len(out)-1] == p {
			continue
		}
		out = append(out, p)
	}
	if closed && len(out) > 1 && out[0] == out[len(out)-1] {
		out = out[:len(out)-1]
	}
	if closed && len(out) < 2 {
		closed = false
	}
	return out, closed
}

func singlePoint(p Pt, h float64, c Cap) []poly {
	switch c {
	case CapRound:
		return []poly{disc(p, h)}
	case CapSquare:
		return []poly{{
			{p.X - h, p.Y - h}, {p.X + h, p.Y - h},
			{p.X + h, p.Y + h}, {p.X - h, p.Y + h},
		}}
	}
	return nil
}

func appendStroke(polys []poly, pts []Pt, closed bool, opts StrokeOpts, h float64) []poly {
	n := len(pts)
	m := n - 1
	if closed {
		m = n
	}
	for i := 0; i < m; i++ {
		polys = append(polys, segQuad(pts[i], pts[(i+1)%n], h))
	}
	if closed {
		for i := 0; i < n; i++ {
			polys = appendJoin(polys, pts[(i+n-1)%n], pts[i], pts[(i+1)%n], h, opts.Join, miterLimit(opts))
		}
	} else {
		for i := 1; i+1 < n; i++ {
			polys = appendJoin(polys, pts[i-1], pts[i], pts[i+1], h, opts.Join, miterLimit(opts))
		}
		if d, ok := dirOf(pts[0], pts[1]); ok {
			polys = appendCap(polys, pts[0], Pt{-d.X, -d.Y}, h, opts.Cap)
		}
		if d, ok := dirOf(pts[n-2], pts[n-1]); ok {
			polys = appendCap(polys, pts[n-1], d, h, opts.Cap)
		}
	}
	return polys
}

func miterLimit(opts StrokeOpts) float64 {
	if opts.MiterLimit <= 0 {
		return 4
	}
	return opts.MiterLimit
}

func dirOf(a, b Pt) (Pt, bool) {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return Pt{}, false
	}
	return Pt{dx / l, dy / l}, true
}

func segQuad(a, b Pt, h float64) poly {
	d, _ := dirOf(a, b)
	nx, ny := -d.Y*h, d.X*h
	return poly{
		{a.X + nx, a.Y + ny}, {b.X + nx, b.Y + ny},
		{b.X - nx, b.Y - ny}, {a.X - nx, a.Y - ny},
	}
}

// appendJoin 在拐角外侧补上 Join 指定的那块; 外侧 = 转向的反侧。
func appendJoin(polys []poly, a, v, b Pt, h float64, j Join, limit float64) []poly {
	d1, ok1 := dirOf(a, v)
	d2, ok2 := dirOf(v, b)
	if !ok1 || !ok2 {
		return polys
	}
	cr := d1.X*d2.Y - d1.Y*d2.X
	if cr == 0 && d1.X*d2.X+d1.Y*d2.Y > 0 {
		return polys
	}
	s := 1.0
	if cr > 0 {
		s = -1
	}
	m1 := Pt{-s * d1.Y, s * d1.X}
	m2 := Pt{-s * d2.Y, s * d2.X}
	if d1.X*d2.X+d1.Y*d2.Y < 0 {
		return append(polys, disc(v, h))
	}
	A := Pt{v.X + m1.X*h, v.Y + m1.Y*h}
	B := Pt{v.X + m2.X*h, v.Y + m2.Y*h}
	switch j {
	case JoinBevel:
		polys = append(polys, poly{v, A, B})
	case JoinRound:
		polys = append(polys, arcPoly(v, h, m1, math.Atan2(m1.X*m2.Y-m1.Y*m2.X, m1.X*m2.X+m1.Y*m2.Y)))
	default:
		c := m1.X*m2.X + m1.Y*m2.Y
		if c <= -1 || math.Sqrt(2/(1+c)) > limit {
			polys = append(polys, poly{v, A, B})
			return polys
		}
		k := h / (1 + c)
		M := Pt{v.X + (m1.X+m2.X)*k, v.Y + (m1.Y+m2.Y)*k}
		polys = append(polys, poly{v, A, M, B})
	}
	return polys
}

func appendCap(polys []poly, p, out Pt, h float64, c Cap) []poly {
	switch c {
	case CapSquare:
		n := Pt{-out.Y, out.X}
		polys = append(polys, poly{
			{p.X + n.X*h, p.Y + n.Y*h}, {p.X - n.X*h, p.Y - n.Y*h},
			{p.X - n.X*h + out.X*h, p.Y - n.Y*h + out.Y*h},
			{p.X + n.X*h + out.X*h, p.Y + n.Y*h + out.Y*h},
		})
	case CapRound:
		n := Pt{-out.Y, out.X}
		sweep := math.Pi
		if n.X*out.Y-n.Y*out.X < 0 {
			sweep = -math.Pi
		}
		polys = append(polys, arcPoly(p, h, n, sweep))
	}
	return polys
}

// arcPoly 是以 v 为心、半径 h, 从 unit 方向扫过 sweep 弧度的扇形。
func arcPoly(v Pt, h float64, unit Pt, sweep float64) poly {
	a0 := math.Atan2(unit.Y, unit.X)
	steps := int(math.Ceil(math.Abs(sweep) / (math.Pi / 32)))
	if steps < 1 {
		steps = 1
	}
	p := make(poly, 0, steps+2)
	p = append(p, v)
	for i := 0; i <= steps; i++ {
		a := a0 + sweep*float64(i)/float64(steps)
		p = append(p, Pt{v.X + math.Cos(a)*h, v.Y + math.Sin(a)*h})
	}
	return p
}

func disc(v Pt, h float64) poly {
	const steps = 64
	p := make(poly, 0, steps)
	for i := 0; i < steps; i++ {
		a := 2 * math.Pi * float64(i) / steps
		p = append(p, Pt{v.X + math.Cos(a)*h, v.Y + math.Sin(a)*h})
	}
	return p
}

// dashSplit 按弧长把折线切成"实"在的子折线; 奇数长度的 pattern 隐式重复一遍。
func dashSplit(pts []Pt, closed bool, pat []float64) [][]Pt {
	p := normalizeDash(pat)
	if p == nil {
		return [][]Pt{pts}
	}
	n := len(pts)
	m := n - 1
	if closed {
		m = n
	}
	pi, on := 0, true
	remain := p[0]
	var out [][]Pt
	var cur []Pt
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, cur)
		}
		cur = nil
	}
	for i := 0; i < m; i++ {
		a, b := pts[i], pts[(i+1)%n]
		L := math.Hypot(b.X-a.X, b.Y-a.Y)
		if L == 0 {
			continue
		}
		ux, uy := (b.X-a.X)/L, (b.Y-a.Y)/L
		for s := 0.0; s < L; {
			if remain <= 0 {
				pi = (pi + 1) % len(p)
				on = !on
				remain = p[pi]
				continue
			}
			t := math.Min(remain, L-s)
			if on {
				if len(cur) == 0 {
					cur = append(cur, Pt{a.X + ux*s, a.Y + uy*s})
				}
				if t >= L-s {
					cur = append(cur, b)
				} else {
					cur = append(cur, Pt{a.X + ux*(s+t), a.Y + uy*(s+t)})
				}
			}
			s += t
			remain -= t
			if remain <= 0 {
				flush()
			}
		}
	}
	flush()
	return out
}

func normalizeDash(pat []float64) []float64 {
	if len(pat) == 0 {
		return nil
	}
	p := make([]float64, 0, 2*len(pat))
	for _, v := range pat {
		p = append(p, math.Max(v, 0))
	}
	if len(p)%2 == 1 {
		p = append(p, p...)
	}
	sum := 0.0
	for _, v := range p {
		sum += v
	}
	if sum <= 0 {
		return nil
	}
	return p
}

// OutlinePath 沿路径的中心线描边(每个子路径按 opts.Closed 处理), 结果仍是外形。
func OutlinePath(p *Path, opts StrokeOpts) Shape {
	if p == nil {
		return nil
	}
	loops := p.Polylines()
	switch len(loops) {
	case 0:
		return nil
	case 1:
		return Outline(loops[0], opts)
	}
	parts := make([]Shape, 0, len(loops))
	for _, loop := range loops {
		if len(loop) == 0 {
			continue
		}
		parts = append(parts, Outline(loop, opts))
	}
	return Union(parts...)
}
