package ink

import (
	"image"
	"image/color"

	"github.com/KasumiYuku/ink/raster"
)

// 外形与填充的类型别名: 调用方写签名时不必同时 import raster。
type (
	Shape = raster.Shape
	Row   = raster.Row
	Mode  = raster.Mode
	// Brush 是落笔上下文; Path / Pt / SpriteOpts 是图像与路径的形状参数。
	Brush      = raster.Brush
	Par        = raster.Par
	Path       = raster.Path
	Pt         = raster.Pt
	SpriteOpts = raster.SpriteOpts
	// ShadowSpec / GlowSpec 是装饰参数。
	ShadowSpec = raster.ShadowSpec
	GlowSpec   = raster.GlowSpec
)

// 变换与矩阵。
type (
	Matrix     = raster.Matrix
	StrokeOpts = raster.StrokeOpts
	Cap        = raster.Cap
	Join       = raster.Join
	FillRule   = raster.FillRule
)

const (
	CapButt   = raster.CapButt
	CapRound  = raster.CapRound
	CapSquare = raster.CapSquare

	JoinMiter = raster.JoinMiter
	JoinRound = raster.JoinRound
	JoinBevel = raster.JoinBevel

	NonZero = raster.NonZero
	EvenOdd = raster.EvenOdd
)

func Identity() Matrix                       { return raster.Identity() }
func Translate(dx, dy float64) Matrix        { return raster.Translate(dx, dy) }
func Scale(sx, sy float64) Matrix            { return raster.Scale(sx, sy) }
func Rotate(deg float64) Matrix              { return raster.Rotate(deg) }
func SkewX(k float64) Matrix                 { return raster.SkewX(k) }
func TransformShape(s Shape, m Matrix) Shape { return raster.TransformShape(s, m) }
func Union(parts ...Shape) Shape             { return raster.Union(parts...) }

// Outline 沿折线的中心线描边(线宽/端点/接角/虚线); OutlinePath 对整条路径做同样的事。
func Outline(pts []Pt, opts StrokeOpts) Shape    { return raster.Outline(pts, opts) }
func OutlinePath(p *Path, opts StrokeOpts) Shape { return raster.OutlinePath(p, opts) }

// HGRadient / Radial 是另外两档填充(水平渐变与径向渐变, 逐像素求值)。
func HGRadient(left, right color.RGBA, x0, x1 float64) Row {
	return raster.HGRadient(left, right, x0, x1)
}
func Radial(center Pt, radius float64, inner, outer color.RGBA) Row {
	return raster.Radial(center, radius, inner, outer)
}

// NewPath 是一条空路径: MoveTo/LineTo/QuadTo/CubicTo/Close, 它就是一种外形。
func NewPath() *Path { return raster.NewPath() }

const (
	Over     = raster.Over
	Add      = raster.Add
	Multiply = raster.Multiply
	Screen   = raster.Screen
	Copy     = raster.Copy
	// Lean15 是 15° 斜切的正切。
	Lean15 = raster.Lean15
)

// 常用外形: 斜切圆角矩形是出图里最常见的那一种, 其余组合见 raster 包。
func PlainBox(b Rect) Shape                      { return raster.Rect(b) }
func RoundBox(b Rect, radius float64) Shape      { return raster.Round(b, radius) }
func SkewBox(b Rect, radius, lean float64) Shape { return raster.Skew(b, radius, lean) }
func ClipShape(s Shape, r image.Rectangle) Shape { return raster.ClipShape(s, r) }
func Outset(s Shape, d float64) Shape            { return raster.Outset(s, d) }
func ShiftBy(s Shape, dx float64, dy int) Shape  { return raster.ShiftBy(s, dx, dy) }

// 常用填充: 每行求值一次, 所以渐变的代价与纯色一样。
func Solid(c color.RGBA) Row                 { return raster.Solid(c) }
func VGradient(top, bottom color.RGBA) Row   { return raster.VGradient(top, bottom) }
func Falloff(c color.RGBA, hold float64) Row { return raster.Falloff(c, hold) }
func Ramp(stops ...color.RGBA) Row           { return raster.Ramp(stops...) }
