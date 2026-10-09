package raster

import (
	"image"
	"image/color"
	"math"
)

// ShadowSpec 是投影参数; Dy 为零时整体下移取 (Steps-1)×StepW(投影不从形状上缘冒出来)。
type ShadowSpec struct {
	Steps int
	StepW float64
	Dy    float64
	Ink   color.RGBA
	Mode  Mode
}

// GlowSpec 是外形外的辉光: 若干圈空框, 越外越淡(等价于按距离衰减)。
type GlowSpec struct {
	Steps int
	StepW float64
	Ink   color.RGBA
	Mode  Mode
}

func shadowGeom(sp ShadowSpec) (steps int, w, dy float64) {
	steps = max(sp.Steps, 1)
	w = sp.StepW
	if w < 1 {
		w = 1
	}
	dy = sp.Dy
	if dy == 0 {
		dy = float64(steps-1) * w
	}
	return steps, w, dy
}

func shadowInk(base color.RGBA, steps, k int) color.RGBA {
	base.A = uint8(int(base.A) * (steps - k) / steps)
	return base
}

// Shadow 是斜切/圆角形状那套投影: 层盒跟着外形(浮点)外扩、由外到内逐层加深。
func (b Brush) Shadow(s Shape, sp ShadowSpec) {
	if b.Dst == nil || s == nil {
		return
	}
	steps, w, dy := shadowGeom(sp)
	if b.shadowFold(s, sp, steps, w, dy) {
		return
	}
	dyi := int(dy)
	r := s.Bounds()
	for k := steps - 1; k >= 0; k-- {
		d := float64(k) * w
		ink := shadowInk(sp.Ink, steps, k)
		if ink.A == 0 {
			continue
		}
		layer := ShiftBy(Outset(s, d), 0, dyi)
		b.FillRows(r.Min.Y+int(dy-d)-1, r.Max.Y+int(dy+d)+2, layer, Solid(ink))
	}
}

// shadowFoldMaxLayers 是栈上排得下的层数上限; 超了就退回逐层那条路。
const shadowFoldMaxLayers = 16

// shadowFold 把投影各层折叠起来画: 各层互相嵌套, 最内层那一段被**所有**层完整盖住, 于是那一段
// 用一张预先穿好的复合表一次填完, 只有外扩出来的环带才逐层落笔 —— 写入量降到约 1/steps。
// 环带在**整数**边界上切开, 每个像素的覆盖率不变; 复合表就是按同样的顺序把同样的整数算式预先
// 穿好, 所以逐字节相同(由 TestShadowMatchesReference 钉住)。返回 false 表示这单子不折叠。
func (b Brush) shadowFold(s Shape, sp ShadowSpec, steps int, w, dy float64) bool {
	if b.Mode != Over || b.Hard || steps < 2 || steps > shadowFoldMaxLayers {
		return false
	}
	dyi := int(dy)
	inner, ok := ShiftBy(s, 0, dyi).(SingleShape)
	if !ok {
		return false
	}
	// 一层层包出来的 SingleShape 壳(shifted/inset/clipped)对多段外形只会一路返回 false —— 那会让
	// 整层被静默跳过。用外形自己那一行的中点探一次, 探不到就说明底层不是单段, 退回逐层那条路。
	rb := s.Bounds()
	if _, probe := inner.Single((rb.Min.Y+rb.Max.Y)/2 + dyi); !probe {
		return false
	}
	var layers [shadowFoldMaxLayers]SingleShape
	var inks [shadowFoldMaxLayers]color.RGBA
	var tab RowTable
	n, have, maxD := 0, false, 0.0
	for k := steps - 1; k >= 0; k-- {
		d := float64(k) * w
		ink := shadowInk(sp.Ink, steps, k)
		if ink.A == 0 {
			continue
		}
		l, ok := ShiftBy(Outset(s, d), 0, dyi).(SingleShape)
		if !ok {
			return false
		}
		layers[n], inks[n] = l, ink
		if d > maxD {
			maxD = d
		}
		n++
		one := NewRowTable(ink)
		if have {
			tab = Compose(one, tab)
		} else {
			tab, have = one, true
		}
	}
	if n == 0 {
		return true
	}
	clip := b.Bounds()
	base := s.Bounds()
	y0 := max(base.Min.Y+int(dy-maxD)-1, clip.Min.Y)
	y1 := min(base.Max.Y+int(dy+maxD)+2, clip.Max.Y)
	if y1 <= y0 {
		return true
	}
	b.Par.Run(y1-y0, func(lo, hi int) {
		for y := y0 + lo; y < y0+hi; y++ {
			midLo, midHi, hasMid := shadowInnerSeg(inner, y, clip)
			if hasMid {
				ApplyTable(b.Dst, image.Rect(midLo, y, midHi, y+1), &tab)
			}
			for i := 0; i < n; i++ {
				sp1, ok := layers[i].Single(y)
				if !ok {
					continue
				}
				x0 := max(sp1.X0, float64(clip.Min.X))
				x1 := min(sp1.X1, float64(clip.Max.X))
				if x1 <= x0 {
					continue
				}
				if !hasMid {
					FillSpan(b.Dst, y, x0, x1, inks[i], b.Mode)
					continue
				}
				if m := float64(midLo); x0 < m {
					FillSpan(b.Dst, y, x0, m, inks[i], b.Mode)
				}
				if m := float64(midHi); m < x1 {
					FillSpan(b.Dst, y, m, x1, inks[i], b.Mode)
				}
			}
		}
	})
	return true
}

// shadowInnerSeg 给第 y 行"被所有层完整盖住"的整像素段(已裁到 clip); 不足一个像素就返回 false。
func shadowInnerSeg(inner SingleShape, y int, clip image.Rectangle) (int, int, bool) {
	sp, ok := inner.Single(y)
	if !ok {
		return 0, 0, false
	}
	lo, hi := int(math.Floor(sp.X0)), int(math.Ceil(sp.X1))
	if hi-lo <= 1 {
		return 0, 0, false
	}
	left, right := lo, hi
	if float64(lo+1)-sp.X0 < 1 {
		left = lo + 1
	}
	if sp.X1-float64(hi-1) < 1 {
		right = hi - 1
	}
	left, right = max(left, clip.Min.X), min(right, clip.Max.X)
	if left >= right {
		return 0, 0, false
	}
	return left, right, true
}

// ShadowFlat 是圆角卡那套投影: 层盒按整像素对齐、每层同色, 整体向下偏 Dy。
func (b Brush) ShadowFlat(box Box, radius float64, sp ShadowSpec) {
	if b.Dst == nil || box.Empty() {
		return
	}
	steps, w, dy := shadowGeom(sp)
	for i := steps; i >= 1; i-- {
		d := w * float64(i)
		layer := image.Rect(
			int(math.Round(box.X-d)), int(math.Round(box.Y-d+dy)),
			int(math.Round(box.X1()+d)), int(math.Round(box.Y1()+d+dy)))
		if layer.Empty() {
			continue
		}
		b.FillRows(layer.Min.Y, layer.Max.Y,
			Round(B(float64(layer.Min.X), float64(layer.Min.Y), float64(layer.Dx()), float64(layer.Dy())), radius+d),
			Solid(sp.Ink))
	}
}

// Glow 画外形外的辉光; 每圈是"两个外扩量之间的空框", 角上不会叠两次。
func (b Brush) Glow(s Shape, sp GlowSpec) {
	if b.Dst == nil || s == nil {
		return
	}
	steps := max(sp.Steps, 1)
	step := int(sp.StepW)
	if step < 1 {
		step = 1
	}
	r := s.Bounds()
	for i := 0; i < steps; i++ {
		dOut := float64(i*step + step)
		dIn := float64(i * step)
		ink := sp.Ink
		ink.A = uint8(int(ink.A) * (steps - i) / steps)
		if ink.A == 0 {
			continue
		}
		b.ringRowsWithin(r.Min.Y-int(dOut)-1, r.Max.Y+int(dOut)+1,
			Outset(s, dOut), Outset(s, dIn), Solid(ink))
	}
}

// Highlight 是玻璃卡那条边: 上缘一条内高光 + 四周一圈极淡的反光边。
func (b Brush) Highlight(box Box, radius float64, thick int, topInk, rimInk color.RGBA) {
	if b.Dst == nil || box.Empty() {
		return
	}
	if thick < 1 {
		thick = 1
	}
	clip := b.Bounds()
	r := box.Bounds()
	if x0, x1 := r.Min.X+int(math.Round(radius)), r.Max.X-int(math.Round(radius)); x1 > x0 {
		Fill(b.Dst, image.Rect(x0, r.Min.Y, x1, r.Min.Y+thick).Intersect(clip), topInk)
	}
	shape := Round(box, radius)
	holder := b.Scratch.get()
	defer b.Scratch.put(holder)
	scratch := holder.spans
	y0, y1 := max(r.Min.Y, clip.Min.Y), min(r.Max.Y, clip.Max.Y)
	for y := y0; y < y1; y++ {
		sp, ok := SpanAt(shape, y, scratch)
		if !ok {
			continue
		}
		li, ri := int(math.Round(sp.X0)), int(math.Round(sp.X1))
		if ri-li <= 2*thick {
			continue
		}
		Fill(b.Dst, image.Rect(li, y, li+thick, y+1).Intersect(clip), rimInk)
		Fill(b.Dst, image.Rect(ri-thick, y, ri, y+1).Intersect(clip), rimInk)
	}
}
