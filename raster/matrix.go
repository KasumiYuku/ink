package raster

import (
	"fmt"
	"math"
)

// Matrix 是 2x3 仿射矩阵: x' = A*x + C*y + E, y' = B*x + D*y + F(与 CSS/Skia 同一套排列)。
// 零值**不是**恒等矩阵(那是全零的退化变换): 要用恒等请取 Identity()。
type Matrix struct{ A, B, C, D, E, F float64 }

// Identity 是单位矩阵。
func Identity() Matrix { return Matrix{A: 1, D: 1} }

// Translate 是平移 dx/dy。
func Translate(dx, dy float64) Matrix { return Matrix{A: 1, D: 1, E: dx, F: dy} }

// Scale 是横纵缩放。
func Scale(sx, sy float64) Matrix { return Matrix{A: sx, D: sy} }

// Rotate 是绕原点旋转 deg 度, 顺时针为正(屏幕坐标 y 向下)。
func Rotate(deg float64) Matrix {
	// 90° 的整数倍给精确矩阵: 否则 cos/sin 的 1e-16 残差会让轴上边缘的覆盖率从 255 掉到 254。
	switch int(math.Round(deg)) % 360 {
	case 0:
		if math.Abs(deg-math.Round(deg/360)*360) < 1e-9 {
			return Identity()
		}
	case 90, -270:
		if math.Abs(deg-math.Round(deg/90)*90) < 1e-9 {
			return Matrix{A: 0, B: 1, C: -1, D: 0}
		}
	case 180, -180:
		if math.Abs(deg-math.Round(deg/180)*180) < 1e-9 {
			return Matrix{A: -1, D: -1}
		}
	case 270, -90:
		if math.Abs(deg-math.Round(deg/90)*90) < 1e-9 {
			return Matrix{A: 0, B: -1, C: 1, D: 0}
		}
	}
	r := deg * math.Pi / 180
	s, c := math.Sin(r), math.Cos(r)
	return Matrix{A: c, B: s, C: -s, D: c}
}

// SkewX 是水平斜切: x' = x + k*y。
func SkewX(k float64) Matrix { return Matrix{A: 1, C: k, D: 1} }

// Mul 合成变换: 先作用 n, 再作用 m。
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

// Apply 把点 (x,y) 映射到新坐标。
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// ApplyPt 是 Apply 的 Pt 版。
func (m Matrix) ApplyPt(p Pt) Pt {
	x, y := m.Apply(p.X, p.Y)
	return Pt{x, y}
}

// IsIdentity 判断是否为单位矩阵。
func (m Matrix) IsIdentity() bool { return m == Identity() }

// Invert 求逆矩阵; 退化(行列式为 0)时返回 false。
func (m Matrix) Invert() (Matrix, bool) {
	det := m.A*m.D - m.B*m.C
	if det == 0 {
		return Matrix{}, false
	}
	return Matrix{
		A: m.D / det,
		B: -m.B / det,
		C: -m.C / det,
		D: m.A / det,
		E: (m.C*m.F - m.D*m.E) / det,
		F: (m.B*m.E - m.A*m.F) / det,
	}, true
}

// String 按 CSS matrix() 写法给出六个分量。
func (m Matrix) String() string {
	return fmt.Sprintf("matrix(%g, %g, %g, %g, %g, %g)", m.A, m.B, m.C, m.D, m.E, m.F)
}
