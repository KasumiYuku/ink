package ink

import (
	"image"

	"github.com/KasumiYuku/ink/raster"
)

// Save 压一层当前状态(裁剪栈 + 遮罩 + 变换), 返回的还原函数只能调用一次。
func (c *Canvas) Save() func() {
	clip := c.clip
	mask, xform := c.mask, c.xform
	return func() {
		c.clip, c.mask, c.xform = clip, mask, xform
	}
}

// Transform 叠加一个仿射变换(先作用新变换, 再作用已有的), 返回的还原函数只能调用一次。
// 变换作用在**矢量外形**上(矩形/圆角/斜切/路径/描边/装饰); 位图能吃平移与轴向缩放, 文字只吃平移,
// 其余情况会 Warn —— 不静默丢掉。
func (c *Canvas) Transform(m raster.Matrix) func() {
	prev := c.xform
	c.xform = prev.Mul(m)
	return func() { c.xform = prev }
}

// ClipShape 按外形裁剪(圆角、斜切、路径、环都行), 返回的还原函数只能调用一次。
func (c *Canvas) ClipShape(s Shape) func() {
	if s == nil {
		return func() {}
	}
	prev := c.mask
	next := raster.FromShape(c.shape(s), c.dst.Rect)
	if prev != nil {
		next.Intersect(prev)
	}
	c.mask = next
	return func() { c.mask = prev }
}

// ClipImage 用一张图的 alpha 当遮罩, 返回的还原函数只能调用一次。
func (c *Canvas) ClipImage(img *image.RGBA) func() {
	if img == nil {
		return func() {}
	}
	prev := c.mask
	next := raster.FromImage(img, 0)
	if prev != nil {
		next.Intersect(prev)
	}
	c.mask = next
	return func() { c.mask = prev }
}

// shape 把当前变换作用到外形上(恒等时不复制)。
func (c *Canvas) shape(s Shape) Shape {
	if s == nil || c.xform.IsIdentity() {
		return s
	}
	return raster.TransformShape(s, c.xform)
}

// point 把当前变换作用到一个点(位图与文字用)。
func (c *Canvas) point(x, y float64) (float64, float64) { return c.xform.Apply(x, y) }

// axisAligned 判断变换是否只含平移与轴向缩放(位图能吃的就这两样)。
func (c *Canvas) axisAligned() bool { return c.xform.B == 0 && c.xform.C == 0 }

// spriteRect 把落笔矩形按变换挪过去; 含旋转/斜切时返回 false 并由调用方 Warn。
func (c *Canvas) spriteRect(r Rect) (image.Rectangle, bool) {
	if c.xform.IsIdentity() {
		return r.Bounds(), true
	}
	if !c.axisAligned() {
		return image.Rectangle{}, false
	}
	x0, y0 := c.point(r.X, r.Y)
	x1, y1 := c.point(r.X1(), r.Y1())
	return image.Rect(int(x0+0.5), int(y0+0.5), int(x1+0.5), int(y1+0.5)), true
}

// fill 是"外形落笔"的唯一出口: 有遮罩走遮罩那条, 否则走 Brush 的快路。
func (c *Canvas) fill(m Mode, s Shape, row Row) {
	if s == nil {
		return
	}
	b := c.brush(m)
	s = c.shape(s)
	if c.mask == nil {
		b.Fill(s, row)
		return
	}
	raster.MaskedFill(b, c.mask, s, row)
}
