package raster

import (
	"image"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// ScaleInto 把 src 用双三次(Catmull-Rom)缩放到 dst 的 win 矩形里; RGB 在预乘空间插值, 边缘不渗黑边。
// 它是便捷入口: 每次借一块行缓存(2 次分配)。热路径请用 ScaleIntoPool 带上缓冲池。
func ScaleInto(dst, src *image.RGBA, win image.Rectangle, par Par) {
	if src == nil {
		return
	}
	scaleInto(dst, src, src.Rect, win, par, nil)
}

// ScaleIntoPool 同 ScaleInto, 但行缓存从调用方的池里借 —— 稳态零分配。
func ScaleIntoPool(dst, src *image.RGBA, win image.Rectangle, par Par, sc *Scratch) {
	if src == nil {
		return
	}
	scaleInto(dst, src, src.Rect, win, par, sc)
}

// scaleInto 是 ScaleInto 的内核: 采样矩形与缓冲池显式给。
func scaleInto(dst, src *image.RGBA, crop, win image.Rectangle, par Par, sc *Scratch) {
	if dst == nil || src == nil {
		return
	}
	win = win.Intersect(dst.Rect)
	if win.Empty() {
		return
	}
	sw, sh := crop.Dx(), crop.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	dh := win.Dy()
	sx := float64(sw) / float64(win.Dx())
	sy := float64(sh) / float64(dh)
	if par.Workers <= 1 || dh < par.MinRows {
		scaleRows(dst, src, crop, win, sx, sy, sc, 0, dh)
		return
	}
	par.Run(dh, func(lo, hi int) { scaleRows(dst, src, crop, win, sx, sy, sc, lo, hi) })
}

// scaleRows 只算 win 的 [lo,hi) 行, 走可分离两趟:
// 横趟结果**按源行缓存**(相邻输出行共用 3~4 条源行), 竖趟每像素只读 4 个连续位置。
// 老写法在像素循环里现算 4 条源行的横趟 —— 每个输出像素 16 次散点读, 是内存带宽瓶颈;
// 这里把横趟降到"每个源行一次", 且算式与顺序逐位不变。
func scaleRows(dst, src *image.RGBA, crop, win image.Rectangle, sx, sy float64, sc *Scratch, lo, hi int) {
	sw, sh := crop.Dx(), crop.Dy()
	dw := win.Dx()
	spix, stride := src.Pix, src.Stride
	need := dw * 4
	fb := sc.floats(need * 6)
	defer sc.putFloats(fb)
	tab := fb.v[:need*6]
	wx := tab[:need]
	cx := tab[need : need*2]
	rows := tab[need*2:]
	for x := 0; x < dw; x++ {
		fx := (float64(x)+0.5)*sx - 0.5
		i0 := int(math.Floor(fx))
		w := cubicWeights(fx - float64(i0))
		wx[x*4+0], wx[x*4+1], wx[x*4+2], wx[x*4+3] = w[0], w[1], w[2], w[3]
		cx[x*4+0] = float32(clampInt(i0-1, 0, sw-1) * 4)
		cx[x*4+1] = float32(clampInt(i0, 0, sw-1) * 4)
		cx[x*4+2] = float32(clampInt(i0+1, 0, sw-1) * 4)
		cx[x*4+3] = float32(clampInt(i0+2, 0, sw-1) * 4)
	}
	var slot [4]int
	for i := range slot {
		slot[i] = -1
	}
	next := 0
	for iy := lo; iy < hi; iy++ {
		fy := (float64(iy)+0.5)*sy - 0.5
		j0 := int(math.Floor(fy))
		wy := cubicWeights(fy - float64(j0))
		js := [4]int{
			clampInt(j0-1, 0, sh-1),
			clampInt(j0, 0, sh-1),
			clampInt(j0+1, 0, sh-1),
			clampInt(j0+2, 0, sh-1),
		}
		var ms [4][]float32
		for k := 0; k < 4; k++ {
			j := js[k]
			hit := -1
			for s := 0; s < 4; s++ {
				if slot[s] == j {
					hit = s
					break
				}
			}
			if hit < 0 {
				for s := 0; s < 4; s++ {
					if slot[s] < 0 {
						hit = s
						break
					}
				}
			}
			if hit < 0 {
				hit, next = next, (next+1)%4
			}
			if slot[hit] != j {
				slot[hit] = j
				filterRow(rows[hit*need:(hit+1)*need], spix, crop.Min.X, crop.Min.Y+j, stride, wx, cx, dw)
			}
			ms[k] = rows[hit*need : (hit+1)*need]
		}
		di := dst.PixOffset(win.Min.X, win.Min.Y+iy)
		for x := 0; x < dw; x++ {
			m0 := ms[0][x*4 : x*4+4]
			m1 := ms[1][x*4 : x*4+4]
			m2 := ms[2][x*4 : x*4+4]
			m3 := ms[3][x*4 : x*4+4]
			w0, w1, w2, w3 := wy[0], wy[1], wy[2], wy[3]
			a0 := wsum(m0[0], m1[0], m2[0], m3[0], w0, w1, w2, w3)
			a1 := wsum(m0[1], m1[1], m2[1], m3[1], w0, w1, w2, w3)
			a2 := wsum(m0[2], m1[2], m2[2], m3[2], w0, w1, w2, w3)
			a3 := wsum(m0[3], m1[3], m2[3], m3[3], w0, w1, w2, w3)
			if a3 <= 0.5 {
				di += 4
				continue
			}
			inv := 255 / a3
			dst.Pix[di] = rgba.Clamp8(int(float32(a0*inv) + 0.5))
			dst.Pix[di+1] = rgba.Clamp8(int(float32(a1*inv) + 0.5))
			dst.Pix[di+2] = rgba.Clamp8(int(float32(a2*inv) + 0.5))
			dst.Pix[di+3] = rgba.Clamp8(int(a3 + 0.5))
			di += 4
		}
	}
}

// filterRow 是一条源行的横趟: 预乘加权, 4 抽头。
//
// 每一项乘积都**显式**舍入到 float32: arm64 上 Go 会把 `d + x*w` 合成一条 FMA, 那会让结果
// 与 amd64 差 1 个 ULP(实测 41e6c9fe 对 41e6c9ff)。Go 规范里写明显式类型转换会挡住这种融合
// ("preventing fusion that would discard that rounding"), 于是两个架构的输出逐字节相同。
func filterRow(dst []float32, spix []uint8, x0, y, stride int, wx, cx []float32, dw int) {
	base := y*stride + x0*4
	for x := 0; x < dw; x++ {
		c0 := int(cx[x*4+0])
		c1 := int(cx[x*4+1])
		c2 := int(cx[x*4+2])
		c3 := int(cx[x*4+3])
		w0, w1, w2, w3 := wx[x*4], wx[x*4+1], wx[x*4+2], wx[x*4+3]
		d := dst[x*4 : x*4+4]
		d[0], d[1], d[2], d[3] = 0, 0, 0, 0
		a := float32(spix[base+c0+3])
		d[0] += float32(float32(spix[base+c0]) * a / 255 * w0)
		d[1] += float32(float32(spix[base+c0+1]) * a / 255 * w0)
		d[2] += float32(float32(spix[base+c0+2]) * a / 255 * w0)
		d[3] += float32(a * w0)
		a = float32(spix[base+c1+3])
		d[0] += float32(float32(spix[base+c1]) * a / 255 * w1)
		d[1] += float32(float32(spix[base+c1+1]) * a / 255 * w1)
		d[2] += float32(float32(spix[base+c1+2]) * a / 255 * w1)
		d[3] += float32(a * w1)
		a = float32(spix[base+c2+3])
		d[0] += float32(float32(spix[base+c2]) * a / 255 * w2)
		d[1] += float32(float32(spix[base+c2+1]) * a / 255 * w2)
		d[2] += float32(float32(spix[base+c2+2]) * a / 255 * w2)
		d[3] += float32(a * w2)
		a = float32(spix[base+c3+3])
		d[0] += float32(float32(spix[base+c3]) * a / 255 * w3)
		d[1] += float32(float32(spix[base+c3+1]) * a / 255 * w3)
		d[2] += float32(float32(spix[base+c3+2]) * a / 255 * w3)
		d[3] += float32(a * w3)
	}
}

// wsum 是 4 个乘积的加权和; 每一项乘积都显式舍入到 float32 —— 与 filterRow 同一个理由,
// 挡住 arm64 上把 `t + x*w` 合成 FMA(那会让两个架构的输出差 1 个 ULP)。
func wsum(m0, m1, m2, m3, w0, w1, w2, w3 float32) float32 {
	return float32(m0*w0) + float32(m1*w1) + float32(m2*w2) + float32(m3*w3)
}

// cubicWeights 返回 Catmull-Rom 的 4 个权重, t 是采样点到左侧第一个点的距离(0..1)。
func cubicWeights(t float64) [4]float32 {
	t2 := t * t
	t3 := t2 * t
	return [4]float32{
		float32(-0.5*t3 + t2 - 0.5*t),
		float32(1.5*t3 - 2.5*t2 + 1),
		float32(-1.5*t3 + 2*t2 + 0.5*t),
		float32(0.5*t3 - 0.5*t2),
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
