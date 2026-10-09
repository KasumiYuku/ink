package raster

import (
	"image"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// pathPt 是路径里的浮点落点(与其它文件的 Pt 区分, 避免耦合)。
type pathPt struct{ X, Y float64 }

// pathSeg 是一个绘制段: 'L' 直线(仅 0), 'Q' 二次(0 控制点, 1 终点), 'C' 三次(0/1 控制点, 2 终点)。
type pathSeg struct {
	op byte
	p  [3]pathPt
}

type pathSub struct {
	start pathPt
	segs  []pathSeg
}

// FillRule 决定重合区域的填充方式。
type FillRule uint8

const (
	NonZero FillRule = iota // 非零环绕: 环绕数非零即内部(默认)。
	EvenOdd                 // 奇偶: 交叉数为奇数即内部。
)

// Path 是任意路径: 多个子路径, 直线与贝塞尔混用。
// Spans/Bounds 可被多个 goroutine 同时调用; 改路径/改容差属于装配行为, 与并发绘制同时进行不受支持。
type Path struct {
	subs   []pathSub
	cur    pathSub
	hasCur bool
	tol    float64
	fill   FillRule

	gen   atomic.Uint64
	mu    sync.Mutex
	cache atomic.Pointer[pathCache]
}

// pathCache 是一次装配的只读产物; gen 与 Path.gen 相等时可直接复用。
type pathCache struct {
	gen    uint64
	bnds   image.Rectangle
	spans  []Span
	rowOff []int32
	yMin   int
	rows   int
}

// DefaultTolerance 是曲线细分默认容差(像素)。
const DefaultTolerance = 0.25

func NewPath() *Path { return &Path{tol: DefaultTolerance} }

// SetTolerance 设曲线细分容差; 缓存失效, 下次 Spans/Bounds 自动重建。
func (p *Path) SetTolerance(t float64) {
	p.tol = t
	p.invalidate()
}

// FillRule 返回当前填充规则。
func (p *Path) FillRule() FillRule { return p.fill }

// SetFillRule 设填充规则; 缓存失效, 下次使用自动重建。
func (p *Path) SetFillRule(r FillRule) {
	p.fill = r
	p.invalidate()
}

func (p *Path) invalidate() { p.gen.Add(1) }

func (p *Path) tolOr() float64 {
	if p.tol <= 0 {
		return DefaultTolerance
	}
	if p.tol < 1e-4 {
		return 1e-4
	}
	return p.tol
}

func (p *Path) flush() {
	if p.hasCur && len(p.cur.segs) > 0 {
		p.subs = append(p.subs, p.cur)
	}
	p.cur = pathSub{}
	p.hasCur = false
}

// MoveTo 起一个新的子路径。
func (p *Path) MoveTo(x, y float64) {
	p.flush()
	p.cur = pathSub{start: pathPt{x, y}}
	p.hasCur = true
	p.invalidate()
}

// LineTo 追加一条直线段; 没有当前点时等同于 MoveTo。
func (p *Path) LineTo(x, y float64) {
	if !p.hasCur {
		p.MoveTo(x, y)
		return
	}
	p.cur.segs = append(p.cur.segs, pathSeg{op: 'L', p: [3]pathPt{{X: x, Y: y}}})
	p.invalidate()
}

// QuadTo 追加一条二次贝塞尔, (cx,cy) 为控制点, (x,y) 为终点。
func (p *Path) QuadTo(cx, cy, x, y float64) {
	if !p.hasCur {
		p.MoveTo(x, y)
		return
	}
	p.cur.segs = append(p.cur.segs, pathSeg{op: 'Q', p: [3]pathPt{{X: cx, Y: cy}, {X: x, Y: y}}})
	p.invalidate()
}

// CubicTo 追加一条三次贝塞尔。
func (p *Path) CubicTo(c1x, c1y, c2x, c2y, x, y float64) {
	if !p.hasCur {
		p.MoveTo(x, y)
		return
	}
	p.cur.segs = append(p.cur.segs, pathSeg{op: 'C', p: [3]pathPt{{X: c1x, Y: c1y}, {X: c2x, Y: c2y}, {X: x, Y: y}}})
	p.invalidate()
}

// Close 闭合当前子路径(填充时子路径总被隐式闭合, 这里只是结束它)。
func (p *Path) Close() {
	p.flush()
	p.invalidate()
}

// Transform 把仿射变换作用到整条路径(控制点逐点变换, 曲线随之变换); 可叠加。
func (p *Path) Transform(m Matrix) {
	for i := range p.subs {
		transformSub(&p.subs[i], m)
	}
	if p.hasCur {
		transformSub(&p.cur, m)
	}
	p.invalidate()
}

func transformSub(s *pathSub, m Matrix) {
	s.start = xformPt(m, s.start)
	for i := range s.segs {
		sg := &s.segs[i]
		n := 1
		switch sg.op {
		case 'Q':
			n = 2
		case 'C':
			n = 3
		}
		for k := 0; k < n; k++ {
			sg.p[k] = xformPt(m, sg.p[k])
		}
	}
}

func xformPt(m Matrix, q pathPt) pathPt {
	return pathPt{m.A*q.X + m.C*q.Y + m.E, m.B*q.X + m.D*q.Y + m.F}
}

// flattenLoops 把所有子路径细分成闭合折线, 含尚未提交的当前子路径(不改动路径状态)。
func (p *Path) flattenLoops() [][]pathPt {
	tol := p.tolOr()
	out := make([][]pathPt, 0, len(p.subs)+1)
	add := func(s pathSub) {
		if pts := flattenSub(s, tol); len(pts) >= 3 {
			out = append(out, pts)
		}
	}
	for _, s := range p.subs {
		add(s)
	}
	if p.hasCur && len(p.cur.segs) > 0 {
		add(p.cur)
	}
	return out
}

func flattenSub(s pathSub, tol float64) []pathPt {
	pts := []pathPt{s.start}
	cur := s.start
	for _, sg := range s.segs {
		switch sg.op {
		case 'L':
			cur = sg.p[0]
			pts = append(pts, cur)
		case 'Q':
			pts = flatQuad(pts, cur, sg.p[0], sg.p[1], tol, 0)
			cur = sg.p[1]
		case 'C':
			pts = flatCubic(pts, cur, sg.p[0], sg.p[1], sg.p[2], tol, 0)
			cur = sg.p[2]
		}
	}
	return pts
}

const pathMaxDepth = 18

func flatQuad(dst []pathPt, p0, p1, p2 pathPt, tol float64, depth int) []pathPt {
	if depth >= pathMaxDepth || distToLine(p1, p0, p2) <= tol {
		return append(dst, p2)
	}
	a := midPt(p0, p1)
	b := midPt(p1, p2)
	m := midPt(a, b)
	dst = flatQuad(dst, p0, a, m, tol, depth+1)
	return flatQuad(dst, m, b, p2, tol, depth+1)
}

func flatCubic(dst []pathPt, p0, p1, p2, p3 pathPt, tol float64, depth int) []pathPt {
	if depth >= pathMaxDepth || maxf(distToLine(p1, p0, p3), distToLine(p2, p0, p3)) <= tol {
		return append(dst, p3)
	}
	a := midPt(p0, p1)
	b := midPt(p1, p2)
	c := midPt(p2, p3)
	d := midPt(a, b)
	e := midPt(b, c)
	m := midPt(d, e)
	dst = flatCubic(dst, p0, a, d, m, tol, depth+1)
	return flatCubic(dst, m, e, c, p3, tol, depth+1)
}

func midPt(a, b pathPt) pathPt { return pathPt{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }

// distToLine 是点到直线 ab 的距离; a==b 时退化成点距。
func distToLine(p, a, b pathPt) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l < 1e-12 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	return math.Abs(dx*(a.Y-p.Y)-dy*(a.X-p.X)) / l
}

// build 按当前装配构造只读缓存: 细分曲线 + 扫描线求每行的填充区间。
func (p *Path) build(gen uint64) *pathCache {
	loops := p.flattenLoops()
	c := &pathCache{gen: gen}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	any := false
	for _, l := range loops {
		for _, q := range l {
			minX, maxX = math.Min(minX, q.X), math.Max(maxX, q.X)
			minY, maxY = math.Min(minY, q.Y), math.Max(maxY, q.Y)
			any = true
		}
	}
	if !any {
		return c
	}
	c.bnds = image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
	c.yMin = c.bnds.Min.Y
	c.rows = c.bnds.Max.Y - c.bnds.Min.Y
	if c.rows <= 0 {
		return c
	}

	type pedge struct{ x0, y0, x1, y1, ymin, ymax float64 }
	edges := make([]pedge, 0, 16)
	for _, l := range loops {
		n := len(l)
		for i := 0; i < n; i++ {
			a, b := l[i], l[(i+1)%n]
			if a.Y == b.Y {
				continue
			}
			e := pedge{x0: a.X, y0: a.Y, x1: b.X, y1: b.Y}
			e.ymin, e.ymax = math.Min(a.Y, b.Y), math.Max(a.Y, b.Y)
			edges = append(edges, e)
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ymin < edges[j].ymin })

	evenOdd := p.fill == EvenOdd
	spans := make([]Span, 0, 16)
	rowOff := make([]int32, 0, c.rows+1)
	act := make([]int, 0, 16)
	type cross struct {
		x   float64
		dir int
	}
	ct := make([]cross, 0, 16)
	ei := 0
	for y := c.yMin; y < c.yMin+c.rows; y++ {
		cy := float64(y) + 0.5
		k := 0
		for _, idx := range act {
			if edges[idx].ymax > cy {
				act[k] = idx
				k++
			}
		}
		act = act[:k]
		for ei < len(edges) && edges[ei].ymin <= cy {
			act = append(act, ei)
			ei++
		}
		ct = ct[:0]
		for _, idx := range act {
			e := edges[idx]
			x := e.x0 + (cy-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
			dir := 1
			if e.y1 < e.y0 {
				dir = -1
			}
			ct = append(ct, cross{x, dir})
		}
		sort.Slice(ct, func(i, j int) bool { return ct[i].x < ct[j].x })
		rowOff = append(rowOff, int32(len(spans)))
		if evenOdd {
			for k := 0; k+1 < len(ct); k += 2 {
				spans = AppendSpan(spans, ct[k].x, ct[k+1].x)
			}
			continue
		}
		w := 0
		var xs float64
		for _, cr := range ct {
			prev := w
			w += cr.dir
			if prev == 0 && w != 0 {
				xs = cr.x
			} else if prev != 0 && w == 0 {
				spans = AppendSpan(spans, xs, cr.x)
			}
		}
	}
	rowOff = append(rowOff, int32(len(spans)))
	c.spans = spans
	c.rowOff = rowOff
	return c
}

// Spans 追加第 y 行的填充区间; 缓存建好后只读, 可并发。
func (p *Path) Spans(y int, dst []Span) []Span {
	c := p.snapshot()
	i := y - c.yMin
	if i < 0 || i >= c.rows {
		return dst
	}
	return append(dst, c.spans[c.rowOff[i]:c.rowOff[i+1]]...)
}

func (p *Path) Bounds() image.Rectangle { return p.snapshot().bnds }

// snapshot 取当前装配对应的只读缓存; gen 不匹配时在锁内重建再发布。
func (p *Path) snapshot() *pathCache {
	gen := p.gen.Load()
	if c := p.cache.Load(); c != nil && c.gen == gen {
		return c
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	gen = p.gen.Load()
	if c := p.cache.Load(); c != nil && c.gen == gen {
		return c
	}
	c := p.build(gen)
	p.cache.Store(c)
	return c
}

// Outset 沿法线整体外扩 d(负值内缩): 每条边平移 d, 凸角补圆角、凹角取两偏移线交点。
// 所有子路径保持各自的环绕方向(洞跟着缩), 接缝无洞无裂缝。
func (p *Path) Outset(d float64) Shape {
	if d == 0 {
		return p
	}
	tol := p.tolOr()
	loops := p.flattenLoops()
	gSign := 1.0
	big := 0.0
	for _, l := range loops {
		a := pathLoopArea(l)
		if math.Abs(a) > big {
			big = math.Abs(a)
			if a < 0 {
				gSign = -1
			} else {
				gSign = 1
			}
		}
	}
	var out Path
	out.tol = tol
	for _, l := range loops {
		if off := offsetLoop(l, d, tol, gSign); len(off) >= 3 {
			out.addPolyline(off)
		}
	}
	return &out
}

func (p *Path) addPolyline(pts []pathPt) {
	if len(pts) < 2 {
		return
	}
	p.cur = pathSub{start: pts[0]}
	p.hasCur = true
	for _, q := range pts[1:] {
		p.cur.segs = append(p.cur.segs, pathSeg{op: 'L', p: [3]pathPt{q}})
	}
	p.subs = append(p.subs, p.cur)
	p.cur = pathSub{}
	p.hasCur = false
}

// offsetLoop 把一个闭合折线整体偏移 d, 逐顶点给出圆角(向外张开)或斜接点(向内收拢)。
func offsetLoop(loop []pathPt, d, tol, gSign float64) []pathPt {
	n := len(loop)
	if n < 3 {
		return nil
	}
	area := pathLoopArea(loop)
	if math.Abs(area) < 1e-12 {
		return nil
	}
	lsgn := 1.0
	if area < 0 {
		lsgn = -1
	}
	dir := make([]pathPt, n)
	out := make([]pathPt, n)
	for i := 0; i < n; i++ {
		a, b := loop[i], loop[(i+1)%n]
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l < 1e-12 {
			if i > 0 {
				dir[i] = dir[i-1]
			} else {
				dir[i] = pathPt{1, 0}
			}
		} else {
			dir[i] = pathPt{dx / l, dy / l}
		}
		// 外侧法线由全局区域朝向决定: 洞(反向子路径)也跟着区域一起收缩。
		out[i] = pathPt{gSign * dir[i].Y, -gSign * dir[i].X}
	}
	res := make([]pathPt, 0, n*2)
	for j := 0; j < n; j++ {
		i := (j + n - 1) % n
		cross := dir[i].X*dir[j].Y - dir[i].Y*dir[j].X
		dot := dir[i].X*dir[j].X + dir[i].Y*dir[j].Y
		v := loop[j]
		if math.Abs(cross) < 1e-12 && dot > 0 {
			res = append(res, pathPt{v.X + d*out[i].X, v.Y + d*out[i].Y})
			continue
		}
		if cross*lsgn*d > 0 {
			res = appendArc(res, v, out[i], out[j], math.Abs(d), tol)
			continue
		}
		l1a := pathPt{loop[i].X + d*out[i].X, loop[i].Y + d*out[i].Y}
		l1b := pathPt{v.X + d*out[i].X, v.Y + d*out[i].Y}
		w := loop[(j+1)%n]
		l2a := pathPt{v.X + d*out[j].X, v.Y + d*out[j].Y}
		l2b := pathPt{w.X + d*out[j].X, w.Y + d*out[j].Y}
		if m, ok := lineIsect(l1a, l1b, l2a, l2b); ok {
			res = append(res, m)
		} else {
			res = append(res, l1b)
		}
	}
	return res
}

// appendArc 从 center+from*r 沿原多边形转向扫到 center+to*r(含两端)。
func appendArc(res []pathPt, center, from, to pathPt, r, tol float64) []pathPt {
	a0 := math.Atan2(from.Y, from.X)
	a1 := math.Atan2(to.Y, to.X)
	sweep := a1 - a0
	for sweep > math.Pi {
		sweep -= 2 * math.Pi
	}
	for sweep < -math.Pi {
		sweep += 2 * math.Pi
	}
	steps := 1
	if r > tol {
		maxA := 2 * math.Acos(clampRatio(1-tol/r))
		if maxA > 1e-9 {
			if k := int(math.Ceil(math.Abs(sweep) / maxA)); k > steps {
				steps = k
			}
		}
	}
	for k := 0; k <= steps; k++ {
		a := a0 + sweep*float64(k)/float64(steps)
		res = append(res, pathPt{center.X + r*math.Cos(a), center.Y + r*math.Sin(a)})
	}
	return res
}

func clampRatio(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// lineIsect 求两条直线的交点; 平行时返回 false。
func lineIsect(a, b, c, e pathPt) (pathPt, bool) {
	d1x, d1y := b.X-a.X, b.Y-a.Y
	d2x, d2y := e.X-c.X, e.Y-c.Y
	den := d1x*d2y - d1y*d2x
	if math.Abs(den) < 1e-12 {
		return pathPt{}, false
	}
	t := ((c.X-a.X)*d2y - (c.Y-a.Y)*d2x) / den
	return pathPt{a.X + t*d1x, a.Y + t*d1y}, true
}

// pathLoopArea 是闭合折线的带符号面积(正=逆时针)。
func pathLoopArea(l []pathPt) float64 {
	s := 0.0
	n := len(l)
	for i := 0; i < n; i++ {
		a, b := l[i], l[(i+1)%n]
		s += a.X*b.Y - b.X*a.Y
	}
	return s / 2
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var (
	_ Shape     = (*Path)(nil)
	_ Outsetter = (*Path)(nil)
)

// Polylines 把路径展平成折线(每个子路径一条, 已按容差细分), 供中心线描边与外部几何用。
func (p *Path) Polylines() [][]Pt {
	loops := p.flattenLoops()
	out := make([][]Pt, 0, len(loops))
	for _, loop := range loops {
		pts := make([]Pt, len(loop))
		for i, q := range loop {
			pts[i] = Pt{X: q.X, Y: q.Y}
		}
		out = append(out, pts)
	}
	return out
}
