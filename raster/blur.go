package raster

import (
	"image"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// blurDivShift / blurDivMaxN 把方框模糊里的 `sum / n` 换成"一次乘法 + 一次右移"; 这是逐值相等的恒等变换。
const (
	blurDivShift = 48
	blurDivMaxN  = 1024
)

// blurDivTab[n] = floor(2^48/n) + 1, n 取 1..blurDivMaxN; 读一次建好, 之后只读不改。
var blurDivTab = func() (t [blurDivMaxN + 1]uint64) {
	for n := 1; n <= blurDivMaxN; n++ {
		t[n] = (uint64(1)<<blurDivShift)/uint64(n) + 1
	}
	return
}()

// blurDivN 是 sum/n 的定点版: n 在表内走乘法加位移, 超出表就退回真除法。
func blurDivN(sum, n int) int {
	if n <= blurDivMaxN {
		return int(uint64(uint32(sum)) * blurDivTab[n] >> blurDivShift)
	}
	return sum / n
}

// blurRingMax 是栈上环缓冲能容纳的最大 (radius+1); 超出则退回堆上的环缓冲。
const blurRingMax = 256

// Blur 对 dst 里的一块做可分离方框模糊(只算 RGB, alpha 原样保留); 边缘按实际取到的窗口求平均。
func Blur(dst *image.RGBA, r image.Rectangle, radius int, par Par) {
	if dst == nil {
		return
	}
	r = r.Intersect(dst.Rect)
	if radius < 1 || r.Empty() {
		return
	}
	rad := radius + 1
	x0, y0, w, h := r.Min.X, r.Min.Y, r.Dx(), r.Dy()
	if par.Workers <= 1 {
		if rad <= blurRingMax {
			var rv, rh [3 * blurRingMax]byte
			blurVertCols(dst, x0, y0, h, radius, rad, rv[:rad*3], 0, w)
			blurHorzRows(dst, x0, y0, w, radius, rad, rh[:rad*3], 0, h)
		} else {
			rv, rh := make([]byte, rad*3), make([]byte, rad*3)
			blurVertCols(dst, x0, y0, h, radius, rad, rv, 0, w)
			blurHorzRows(dst, x0, y0, w, radius, rad, rh, 0, h)
		}
		return
	}
	par.Run(w, func(lo, hi int) {
		if rad <= blurRingMax {
			var ring [3 * blurRingMax]byte
			blurVertCols(dst, x0, y0, h, radius, rad, ring[:rad*3], lo, hi)
			return
		}
		blurVertCols(dst, x0, y0, h, radius, rad, make([]byte, rad*3), lo, hi)
	})
	par.Run(h, func(lo, hi int) {
		if rad <= blurRingMax {
			var ring [3 * blurRingMax]byte
			blurHorzRows(dst, x0, y0, w, radius, rad, ring[:rad*3], lo, hi)
			return
		}
		blurHorzRows(dst, x0, y0, w, radius, rad, make([]byte, rad*3), lo, hi)
	})
}

// blurVertCols 是纵向那一趟, 只处理列 [lo,hi); 就地写回, 原始值先进环缓冲再被后面的减窗读走。
func blurVertCols(dst *image.RGBA, x0, y0, h, radius, rad int, ring []byte, lo, hi int) {
	pix := dst.Pix
	stride := dst.Stride
	top := radius
	if top > h-1 {
		top = h - 1
	}
	for cx := lo; cx < hi; cx++ {
		x := x0 + cx
		var s0, s1, s2, n int
		i := y0*stride + x*4
		for y := 0; y <= top; y++ {
			s0 += int(pix[i])
			s1 += int(pix[i+1])
			s2 += int(pix[i+2])
			n++
			i += stride
		}
		for y := 0; y < h; y++ {
			base := (y0+y)*stride + x*4
			slot := (y % rad) * 3
			ring[slot], ring[slot+1], ring[slot+2] = pix[base], pix[base+1], pix[base+2]
			pix[base] = rgba.Clamp8(blurDivN(s0, n))
			pix[base+1] = rgba.Clamp8(blurDivN(s1, n))
			pix[base+2] = rgba.Clamp8(blurDivN(s2, n))
			if del := y - radius; del >= 0 {
				d := (del % rad) * 3
				s0 -= int(ring[d])
				s1 -= int(ring[d+1])
				s2 -= int(ring[d+2])
				n--
			}
			if add := y + radius + 1; add < h {
				ia := (y0+add)*stride + x*4
				s0 += int(pix[ia])
				s1 += int(pix[ia+1])
				s2 += int(pix[ia+2])
				n++
			}
		}
	}
}

// blurHorzRows 是横向那一趟, 只处理行 [lo,hi); 就地写回, 原始值先进环缓冲再被后面的减窗读走。
func blurHorzRows(dst *image.RGBA, x0, y0, w, radius, rad int, ring []byte, lo, hi int) {
	pix := dst.Pix
	stride := dst.Stride
	top := radius
	if top > w-1 {
		top = w - 1
	}
	for cy := lo; cy < hi; cy++ {
		base := (y0+cy)*stride + x0*4
		var s0, s1, s2, n int
		for x := 0; x <= top; x++ {
			i := base + x*4
			s0 += int(pix[i])
			s1 += int(pix[i+1])
			s2 += int(pix[i+2])
			n++
		}
		for x := 0; x < w; x++ {
			i := base + x*4
			slot := (x % rad) * 3
			ring[slot], ring[slot+1], ring[slot+2] = pix[i], pix[i+1], pix[i+2]
			pix[i] = rgba.Clamp8(blurDivN(s0, n))
			pix[i+1] = rgba.Clamp8(blurDivN(s1, n))
			pix[i+2] = rgba.Clamp8(blurDivN(s2, n))
			if del := x - radius; del >= 0 {
				d := (del % rad) * 3
				s0 -= int(ring[d])
				s1 -= int(ring[d+1])
				s2 -= int(ring[d+2])
				n--
			}
			if add := x + radius + 1; add < w {
				ia := base + add*4
				s0 += int(pix[ia])
				s1 += int(pix[ia+1])
				s2 += int(pix[ia+2])
				n++
			}
		}
	}
}
