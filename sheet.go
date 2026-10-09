package ink

import (
	"image"
	"image/color"
	"math"
	"strconv"

	"github.com/KasumiYuku/ink/raster"

	"github.com/KasumiYuku/ink/text"
)

// Rect 是浮点矩形: 版式常量带 0.5px 精度, 落到像素前不该被整数化。
type Rect = raster.Box

// R 是 Rect 的构造。
func R(x, y, w, h float64) Rect { return Rect{X: x, Y: y, W: w, H: h} }

// Fit 决定一张不匹配的图怎么进一个矩形。
type Fit uint8

const (
	FitExact Fit = iota
	FitCover
	FitContain
)

// Full 是整张图; Crop 按源坐标裁一块(尽量零拷贝)。
func Full(img *image.RGBA) Rect {
	if img == nil {
		return Rect{}
	}
	b := img.Bounds()
	return Rect{X: float64(b.Min.X), Y: float64(b.Min.Y), W: float64(b.Dx()), H: float64(b.Dy())}
}

func Crop(img *image.RGBA, r Rect) *image.RGBA {
	if img == nil {
		return nil
	}
	b := r.Bounds().Intersect(img.Bounds())
	if b.Empty() {
		return nil
	}
	if sub, ok := img.SubImage(b).(*image.RGBA); ok {
		return sub
	}
	return img
}

// fitSrc 给出 Cover 语义下要取的源矩形, 以及 Contain 语义下落笔的目标矩形。
func fitSrc(img *image.RGBA, dst Rect, fit Fit) (src Rect, win Rect) {
	src, win = Full(img), dst
	if fit == FitExact || img == nil || dst.W <= 0 || dst.H <= 0 {
		return src, win
	}
	sw, sh := src.W, src.H
	if sw <= 0 || sh <= 0 {
		return src, win
	}
	scale := math.Max(dst.W/sw, dst.H/sh)
	if fit == FitContain {
		scale = math.Min(dst.W/sw, dst.H/sh)
	}
	if fit == FitCover {
		src.W, src.H = dst.W/scale, dst.H/scale
		src.X += (sw - src.W) / 2
		src.Y += (sh - src.H) / 2
		return src, win
	}
	win.W, win.H = sw*scale, sh*scale
	win.X += (dst.W - win.W) / 2
	win.Y += (dst.H - win.H) / 2
	return src, win
}

// Kind 是一道绘制指令的种类; 指令是**数据**, 所以整张图能被记录、快照、回放。
type Kind uint8

const (
	KindFill Kind = iota
	KindRound
	KindRoundBinary
	KindStroke
	KindSkew
	KindSprite
	KindTint
	KindBlur
	KindDim
	KindText
	KindTextBox
	KindClip
	KindGroup
	KindCustom
	KindShape
	KindStrokeShape
	KindShadow
	KindGlow
	KindHighlight
	KindFrost
	KindParagraph
	KindClipShape
	KindMask
	KindTransform
	KindParallel
	kindCount
)

// String 是算子的名字(诊断与错误信息里用)。
func (k Kind) String() string {
	switch k {
	case KindFill:
		return "fill"
	case KindRound:
		return "round"
	case KindRoundBinary:
		return "round-binary"
	case KindStroke:
		return "stroke"
	case KindSkew:
		return "skew"
	case KindSprite:
		return "sprite"
	case KindTint:
		return "tint"
	case KindBlur:
		return "blur"
	case KindDim:
		return "dim"
	case KindText:
		return "text"
	case KindParagraph:
		return "paragraph"
	case KindClipShape:
		return "clip-shape"
	case KindMask:
		return "mask"
	case KindTransform:
		return "transform"
	case KindParallel:
		return "parallel"
	case KindTextBox:
		return "text-box"
	case KindClip:
		return "clip"
	case KindGroup:
		return "group"
	case KindCustom:
		return "custom"
	case KindShape:
		return "shape"
	case KindStrokeShape:
		return "stroke-shape"
	case KindShadow:
		return "shadow"
	case KindGlow:
		return "glow"
	case KindHighlight:
		return "highlight"
	case KindFrost:
		return "frost"
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

type Op struct {
	Kind       Kind
	Rect       Rect
	Src        Rect
	Img        *image.RGBA
	Mask       *image.RGBA
	Key        string
	Text       string
	Face       text.Face
	Style      text.Style
	Radius     float64
	Width      float64
	Lean       float64
	Fade       float64
	Color      color.RGBA
	Inner      []Op
	Fn         func(*Canvas)
	Shape      Shape
	Row        Row
	Mode       Mode
	Shadow     ShadowSpec
	Glow       GlowSpec
	Rim        color.RGBA
	LineHeight float64
	Xform      raster.Matrix
}

func (o Op) Draw(c *Canvas) {
	switch o.Kind {
	case KindFill:
		c.Fill(o.Rect, o.Color)
	case KindRound:
		c.Round(o.Rect, o.Radius, o.Color)
	case KindRoundBinary:
		c.RoundBinary(o.Rect, o.Radius, o.Color)
	case KindStroke:
		c.Stroke(o.Rect, o.Width, o.Color)
	case KindSkew:
		c.Skew(o.Rect, o.Lean, o.Color)
	case KindShape:
		c.Shape(o.Shape, o.Row, o.Mode)
	case KindStrokeShape:
		c.StrokeShape(o.Shape, o.Width, o.Color, o.Mode)
	case KindShadow:
		c.Shadow(o.Shape, o.Shadow)
	case KindGlow:
		c.Glow(o.Shape, o.Glow)
	case KindFrost:
		c.Frost(o.Shape, int(o.Radius+0.5), o.Color)
	case KindHighlight:
		c.Highlight(o.Rect, o.Radius, int(o.Width+0.5), o.Color, o.Rim)
	case KindSprite:
		c.Sprite(o.Rect, o.Src, o.Img, o.Lean)
	case KindTint:
		c.Tinted(o.Rect, o.Src, o.Mask, o.Color, o.Lean)
	case KindBlur:
		c.Blur(o.Rect, int(o.Radius+0.5))
	case KindDim:
		c.Dim(o.Rect, o.Fade)
	case KindText:
		c.Text(o.Rect.X, o.Rect.Y, o.Text, o.Face, o.Style)
	case KindParagraph:
		c.Paragraph(o.Rect, o.Text, o.Face, o.Style, o.LineHeight)
	case KindClipShape:
		done := c.ClipShape(o.Shape)
		for _, in := range o.Inner {
			in.Draw(c)
		}
		done()
	case KindMask:
		done := c.ClipImage(o.Img)
		for _, in := range o.Inner {
			in.Draw(c)
		}
		done()
	case KindTransform:
		done := c.Transform(o.Xform)
		for _, in := range o.Inner {
			in.Draw(c)
		}
		done()
	case KindParallel:
		c.Parallel(o.Inner)
	case KindTextBox:
		c.TextBox(o.Rect, o.Text, o.Face, o.Style)
	case KindClip:
		done := c.Clip(o.Rect)
		for _, in := range o.Inner {
			in.Draw(c)
		}
		done()
	case KindGroup:
		done := c.Group(o.Fade)
		for _, in := range o.Inner {
			in.Draw(c)
		}
		done()
	case KindCustom:
		if o.Fn != nil {
			o.Fn(c)
		}
	}
}

// Sheet 是一张图的**声明**: 只记指令不落笔, 想画几次画几次, 也能整份交给别处回放。
type Sheet struct {
	W, H int
	ops  []Op
}

func NewSheet(w, h int) *Sheet { return &Sheet{W: w, H: h} }

func (s *Sheet) Ops() []Op { return s.ops }

func (s *Sheet) Draw(c *Canvas) {
	for _, op := range s.ops {
		op.Draw(c)
	}
}

func (s *Sheet) add(op Op) *Sheet {
	s.ops = append(s.ops, op)
	return s
}

// Bg 是清屏底色。
func (s *Sheet) Bg(col color.RGBA) *Sheet {
	return s.add(Op{Kind: KindFill, Rect: R(0, 0, float64(s.W), float64(s.H)), Color: col})
}

// Image 把一张图按 fit 铺进 r, blur > 0 时对这块做模糊(整屏底图那条路)。
func (s *Sheet) Image(r Rect, img *image.RGBA, fit Fit, blur int) *Sheet {
	src, win := fitSrc(img, r, fit)
	s.add(Op{Kind: KindSprite, Rect: win, Src: src, Img: img})
	if blur > 0 {
		s.Blur(win, blur)
	}
	return s
}

func (s *Sheet) Dim(r Rect, alpha float64) *Sheet {
	return s.add(Op{Kind: KindDim, Rect: r, Fade: alpha})
}

func (s *Sheet) Blur(r Rect, radius int) *Sheet {
	return s.add(Op{Kind: KindBlur, Rect: r, Radius: float64(radius)})
}

func (s *Sheet) Card(r Rect, radius float64, col color.RGBA) *Sheet {
	return s.add(Op{Kind: KindRound, Rect: r, Radius: radius, Color: col})
}

// CardHard 是硬边圆角: 更快, 覆盖率二值化, 边缘不打灰。
func (s *Sheet) CardHard(r Rect, radius float64, col color.RGBA) *Sheet {
	return s.add(Op{Kind: KindRoundBinary, Rect: r, Radius: radius, Color: col})
}

func (s *Sheet) Rim(r Rect, w float64, col color.RGBA) *Sheet {
	return s.add(Op{Kind: KindStroke, Rect: r, Width: w, Color: col})
}

func (s *Sheet) Skew(r Rect, lean float64, col color.RGBA) *Sheet {
	return s.add(Op{Kind: KindSkew, Rect: r, Lean: lean, Color: col})
}

func (s *Sheet) Sprite(r, src Rect, img *image.RGBA, lean float64) *Sheet {
	return s.add(Op{Kind: KindSprite, Rect: r, Src: src, Img: img, Lean: lean})
}

// Tinted 用 col 现染一张白蒙版再贴(图标/角标那一类)。
func (s *Sheet) Tinted(r, src Rect, mask *image.RGBA, col color.RGBA, lean float64) *Sheet {
	return s.add(Op{Kind: KindTint, Rect: r, Src: src, Mask: mask, Color: col, Lean: lean})
}

func (s *Sheet) Text(x, baseline float64, str string, f text.Face, st text.Style) *Sheet {
	return s.add(Op{Kind: KindText, Rect: R(x, baseline, 0, 0), Text: str, Face: f, Style: st})
}

func (s *Sheet) TextBox(r Rect, str string, f text.Face, st text.Style) *Sheet {
	return s.add(Op{Kind: KindTextBox, Rect: r, Text: str, Face: f, Style: st})
}

// Clip / Group 是嵌套: fn 里写的指令挂在这一次裁剪或图层下面。
func (s *Sheet) Clip(r Rect, fn func(*Sheet)) *Sheet {
	inner := &Sheet{W: s.W, H: s.H}
	fn(inner)
	return s.add(Op{Kind: KindClip, Rect: r, Inner: inner.ops})
}

func (s *Sheet) Group(alpha float64, fn func(*Sheet)) *Sheet {
	inner := &Sheet{W: s.W, H: s.H}
	fn(inner)
	return s.add(Op{Kind: KindGroup, Fade: alpha, Inner: inner.ops})
}

// Custom 是逃生舱: 需要库里没有的画法时, 直接拿 Canvas 落笔。
func (s *Sheet) Custom(fn func(*Canvas)) *Sheet {
	return s.add(Op{Kind: KindCustom, Fn: fn})
}

// Shape 是"外形 + 每行墨色"的声明; 底板、面板、条形、进度这些全走它。
func (s *Sheet) Shape(shape Shape, row Row, m Mode) *Sheet {
	return s.add(Op{Kind: KindShape, Shape: shape, Row: row, Mode: m})
}

// StrokeShape 沿外形内缘描一圈。
func (s *Sheet) StrokeShape(shape Shape, w float64, col color.RGBA, m Mode) *Sheet {
	return s.add(Op{Kind: KindStrokeShape, Shape: shape, Width: w, Color: col, Mode: m})
}

// Shadow 是外形之外的落地投影。
func (s *Sheet) Shadow(shape Shape, sp ShadowSpec) *Sheet {
	return s.add(Op{Kind: KindShadow, Shape: shape, Shadow: sp})
}

// Glow 是外形之外的辉光。
func (s *Sheet) Glow(shape Shape, sp GlowSpec) *Sheet {
	return s.add(Op{Kind: KindGlow, Shape: shape, Glow: sp})
}

// Highlight 是玻璃卡那条边: 上缘内高光 + 四周极淡的反光边。
func (s *Sheet) Highlight(b Rect, radius float64, thick int, topInk, rimInk color.RGBA) *Sheet {
	return s.add(Op{Kind: KindHighlight, Rect: R(b.X, b.Y, b.W, b.H), Radius: radius,
		Width: float64(thick), Color: topInk, Rim: rimInk})
}

// Frost 是形状内的磨砂(斜切面板、玻璃卡那一类)。
func (s *Sheet) Frost(shape Shape, radius int, tint color.RGBA) *Sheet {
	return s.add(Op{Kind: KindFrost, Shape: shape, Radius: float64(radius), Color: tint})
}

// Paragraph 是按宽度折行的段落(多行); 单行时与 TextBox 一样是"一行字", 不是两套落笔逻辑。
func (s *Sheet) Paragraph(r Rect, str string, f text.Face, st text.Style, lineHeight float64) *Sheet {
	return s.add(Op{Kind: KindParagraph, Rect: r, Text: str, Face: f, Style: st, LineHeight: lineHeight})
}

// ClipShape 把 fn 里写的指令裁进一个外形(圆角/斜切/路径/环都行)。
func (s *Sheet) ClipShape(shape Shape, fn func(*Sheet)) *Sheet {
	return s.nest(KindClipShape, Op{Shape: shape}, fn)
}

// Mask 用一张图的 alpha 当遮罩, 把 fn 里写的指令裁进去。
func (s *Sheet) Mask(img *image.RGBA, fn func(*Sheet)) *Sheet {
	return s.nest(KindMask, Op{Img: img}, fn)
}

// Transform 给 fn 里写的指令叠加一个仿射变换(作用在矢量外形上)。
func (s *Sheet) Transform(m raster.Matrix, fn func(*Sheet)) *Sheet {
	return s.nest(KindTransform, Op{Xform: m}, fn)
}

func (s *Sheet) nest(k Kind, op Op, fn func(*Sheet)) *Sheet {
	inner := &Sheet{W: s.W, H: s.H}
	fn(inner)
	op.Kind, op.Inner = k, inner.ops
	return s.add(op)
}

// Parallel 声明"这里面的绘制互不重叠", 于是引擎可以并行画它们(实测小算子密集的页 3.6×)。
// 引擎自己核对: 有一对算子的包围盒相交、或里面有会改栈的算子(裁剪/遮罩/变换/图层/自定义嵌套),
// 就退回顺序绘制并 Warn —— 正确性不靠调用方自觉。
func (s *Sheet) Parallel(fn func(*Sheet)) *Sheet {
	inner := &Sheet{W: s.W, H: s.H}
	fn(inner)
	return s.add(Op{Kind: KindParallel, Inner: inner.ops})
}
