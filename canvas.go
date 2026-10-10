package ink

import (
	"errors"
	"image"
	"image/color"

	"github.com/KasumiYuku/ink/internal/rgba"
	"github.com/KasumiYuku/ink/raster"
	"github.com/KasumiYuku/ink/text"
	"github.com/KasumiYuku/ink/theme"
)

// Canvas 是一次绘制的作用域: 落笔、裁剪、图层都挂在它上面, 额度与素材来自引擎。
type Canvas struct {
	eng    *Engine
	dst    *image.RGBA
	clip   []image.Rectangle
	mask   *raster.ClipMask
	xform  raster.Matrix
	groups []group
	warned bool
	serial bool
}

type group struct {
	base  *image.RGBA
	buf   *image.RGBA
	alpha float64
}

func (c *Canvas) W() int { return c.dst.Rect.Dx() }
func (c *Canvas) H() int { return c.dst.Rect.Dy() }

// RGBA 是当前落笔的画布; 在 Group 里面拿到的是那一层的缓冲, 不是最终输出。
func (c *Canvas) RGBA() *image.RGBA { return c.dst }

func (c *Canvas) Par() raster.Par {
	if c.serial {
		return raster.Par{}
	}
	return c.eng.par
}

func (c *Canvas) Assets() *theme.Assets { return c.eng.assets }

// Warn 是唯一的诊断出口: 渲染继续, 但调用方能拿到"这里出了问题"。
func (c *Canvas) Warn(err error) {
	if err != nil && c.eng.onWarn != nil {
		c.eng.onWarn(err)
	}
}

func (c *Canvas) rect(r Rect) (image.Rectangle, bool) {
	b := r.Bounds()
	for _, cl := range c.clip {
		b = b.Intersect(cl)
	}
	if b.Empty() {
		return b, false
	}
	return b, true
}

func (c *Canvas) Fill(r Rect, col color.RGBA) {
	if b, ok := c.rect(r); ok {
		raster.Fill(c.dst, b, col)
	}
}

func (c *Canvas) Round(r Rect, radius float64, col color.RGBA) {
	if c.mask != nil {
		c.fill(Over, raster.Round(raster.B(r.X, r.Y, r.W, r.H), radius), raster.Solid(col))
		return
	}
	if b, ok := c.rect(r); ok {
		c.brush(Over).Round(b, radius, raster.Solid(col))
	}
}

// RoundBinary 是硬边圆角: 更快, 覆盖率二值化, 边缘不打灰。
func (c *Canvas) RoundBinary(r Rect, radius float64, col color.RGBA) {
	if c.mask != nil {
		b := c.brush(Over)
		b.Hard = true
		raster.MaskedFill(b, c.mask, c.shape(raster.Round(raster.B(r.X, r.Y, r.W, r.H), radius)), raster.Solid(col))
		return
	}
	if b, ok := c.rect(r); ok {
		br := c.brush(Over)
		br.Hard = true
		br.Round(b, radius, raster.Solid(col))
	}
}

func (c *Canvas) Stroke(r Rect, w float64, col color.RGBA) {
	c.brush(Over).Stroke(raster.Rect(r), w, raster.Solid(col))
}

func (c *Canvas) Skew(r Rect, lean float64, col color.RGBA) {
	if b, ok := c.rect(r); ok {
		raster.FillSkewed(c.dst, b, lean, col)
	}
}

// Shape 落一笔"外形 + 每行墨色"; 裁剪层、遮罩、变换与图层都生效。
func (c *Canvas) Shape(s Shape, row Row, m Mode) {
	c.fill(m, s, row)
}

// StrokeShape 沿外形内缘描一圈(圆角与斜切都跟着外形走)。
func (c *Canvas) StrokeShape(s Shape, w float64, col color.RGBA, m Mode) {
	c.brush(m).Stroke(s, w, raster.Solid(col))
}

// Shadow 画外形之外的落地投影。
func (c *Canvas) Shadow(s Shape, sp ShadowSpec) {
	c.brush(sp.Mode).Shadow(s, sp)
}

// Glow 画外形之外的辉光。
func (c *Canvas) Glow(s Shape, sp GlowSpec) {
	c.brush(sp.Mode).Glow(s, sp)
}

// Frost 是磨砂: 形状内先糊一遍再叠上 tint(斜切面板那一类)。
func (c *Canvas) Frost(s Shape, radius int, tint color.RGBA) {
	if s == nil {
		return
	}
	c.decorWarn()
	c.brush(Over).Frost(c.shape(s), radius, tint)
}

// Highlight 是玻璃卡那条边: 上缘内高光 + 四周极淡的反光边。
func (c *Canvas) Highlight(b Rect, radius float64, thick int, topInk, rimInk color.RGBA) {
	c.brush(Over).Highlight(b, radius, thick, topInk, rimInk)
}

// decorWarn 提示"装饰不吃按形状遮罩"这条已知边界(裁剪矩形与变换照常生效)。
func (c *Canvas) decorWarn() {
	if c.mask != nil {
		c.Warn(errors.New("ink: 投影/辉光/内高光/磨砂不参与按形状遮罩, 只吃裁剪矩形; 需要遮罩请先把它们画进图层"))
	}
}

// brush 是这次落笔的上下文: 画布、裁剪层、合成方式、并发策略一并交给 raster。
func (c *Canvas) brush(m Mode) raster.Brush {
	b := raster.Brush{Dst: c.dst, Mode: m, Par: c.Par(), Scratch: c.eng.scratch, Masks: c.eng.masks}
	if n := len(c.clip); n > 0 {
		b.Clip = c.clip[n-1]
	}
	return b
}

// Paste 把整张图缩放着铺进 r。
func (c *Canvas) Paste(r Rect, img *image.RGBA) {
	c.Sprite(r, Full(img), img, 0)
}

// Sprite 裁 src(源图坐标)再放进 r; lean 非零按斜切采样(窗口斜、图不斜), 越界采样镜像折回不夹边。
func (c *Canvas) Sprite(r, src Rect, img *image.RGBA, lean float64) {
	if img == nil {
		return
	}
	rb, ok := c.spriteRect(r)
	if !ok {
		c.warnOnce("位图只吃平移与轴向缩放: 当前变换含旋转/斜切, 这次贴图被跳过")
		return
	}
	sb := src.Bounds()
	if rb.Empty() || sb.Empty() {
		return
	}
	opts := raster.SpriteOpts{Lean: lean, Mirror: lean != 0}
	if c.mask != nil {
		raster.MaskedSprite(c.brush(Over), c.mask, rb, rb, sb, img, opts)
		return
	}
	c.brush(Over).Sprite(rb, rb, sb, img, opts)
}

// Tinted 用 col 现染一张白蒙版再贴(图标/角标那一类)。
func (c *Canvas) Tinted(r, src Rect, mask *image.RGBA, col color.RGBA, lean float64) {
	crop := Crop(mask, src)
	if crop == nil {
		return
	}
	tinted := raster.Tint(crop, col)
	defer c.eng.giveBack(tinted)
	c.Sprite(r, Full(tinted), tinted, lean)
}

func (c *Canvas) Blur(r Rect, radius int) {
	if b, ok := c.rect(r); ok {
		c.brush(Over).Blur(b, radius)
	}
}

// Dim 是整片压暗: 比整张模糊便宜得多, 用来把背景退到第二层。
func (c *Canvas) Dim(r Rect, alpha float64) {
	c.Fill(r, color.RGBA{A: byte(255*clamp01(alpha) + 0.5)})
}

// Text 在 (x, baseline) 落一行字, 返回推进宽度; 落在裁剪层里时只在框内可见。
func (c *Canvas) Text(x, baseline float64, s string, f text.Face, st text.Style) float64 {
	if f == nil || s == "" {
		return 0
	}
	x, baseline = c.textPos(x, baseline)
	if n := len(c.clip); n > 0 {
		st.Clip = c.clip[n-1]
	}
	return text.Draw(c.dst, f, x, baseline, s, st)
}

// Paragraph 在矩形里按宽度折行落笔(段落在框里按 st.Align 对齐), 返回画了几行与段落总高。
func (c *Canvas) Paragraph(r Rect, s string, f text.Face, st text.Style, lineHeight float64) (int, float64) {
	b, ok := c.rect(r)
	if !ok || f == nil || s == "" {
		return 0, 0
	}
	if n := len(c.clip); n > 0 {
		st.Clip = c.clip[n-1]
	}
	return text.DrawParagraph(c.dst, f, b, s, st, lineHeight)
}

// textPos 把文字基线按变换挪过去; 文字只吃平移(缩放要换字号, 旋转要转字形, 都会 Warn)。
func (c *Canvas) textPos(x, baseline float64) (float64, float64) {
	if c.xform.IsIdentity() {
		return x, baseline
	}
	if c.xform.A != 1 || c.xform.D != 1 || c.xform.B != 0 || c.xform.C != 0 {
		c.warnOnce("文字只吃平移: 当前变换含缩放/旋转/斜切, 这次文字按平移画")
	}
	return c.point(x, baseline)
}

// warnOnce 是同一次绘制里只报一次的诊断出口。
func (c *Canvas) warnOnce(msg string) {
	if c.warned {
		return
	}
	c.warned = true
	c.Warn(errors.New("ink: " + msg))
}

// TextBox 在矩形里按 st.Align 落一行字, 垂直居中于 r。
func (c *Canvas) TextBox(r Rect, s string, f text.Face, st text.Style) {
	if f == nil || s == "" {
		return
	}
	m := f.Metrics()
	w, _ := text.Measure(f, s, st)
	x := r.X
	switch st.Align {
	case text.Center:
		x = r.X + (r.W-w)/2
	case text.Right:
		x = r.X + r.W - w
	}
	c.Text(x, r.Y+(r.H+m.Ascent-m.Descent)/2, s, f, st)
}

// Clip 压一层裁剪, 返回的还原函数只能调用一次。
func (c *Canvas) Clip(r Rect) func() {
	b, ok := c.spriteRect(r)
	if !ok {
		b = r.Bounds()
		c.warnOnce("裁剪框在旋转/斜切下按外接矩形近似")
	}
	if len(c.clip) > 0 {
		b = b.Intersect(c.clip[len(c.clip)-1])
	}
	c.clip = append(c.clip, b)
	return func() {
		if len(c.clip) > 0 {
			c.clip = c.clip[:len(c.clip)-1]
		}
	}
}

// Group 开一个新图层(buffer 从池里借), 还原时按 alpha 整体叠回 —— 组内互相重叠的边不会露两层。
func (c *Canvas) Group(alpha float64) func() {
	buf := c.eng.borrow(c.W(), c.H())
	clear(buf.Pix)
	c.groups = append(c.groups, group{base: c.dst, buf: buf, alpha: alpha})
	c.dst = buf
	return func() {
		if len(c.groups) == 0 {
			return
		}
		g := c.groups[len(c.groups)-1]
		c.groups = c.groups[:len(c.groups)-1]
		c.dst = g.base
		rgba.Composite(g.base, g.buf, g.alpha)
		c.eng.giveBack(g.buf)
	}
}

// DrawRuns 批量落笔: 同样式同字号的连排走一次遍历。
func (c *Canvas) DrawRuns(runs []text.Run) { text.DrawRuns(c.dst, runs) }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
