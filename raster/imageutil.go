package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// Recolor 逐像素套 fn, 返回同尺寸新图(直存 alpha 口径)。
func Recolor(src *image.RGBA, fn func(color.RGBA) color.RGBA) *image.RGBA {
	if src == nil || fn == nil {
		return nil
	}
	b := src.Rect
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := src.PixOffset(x, y)
			c := fn(color.RGBA{src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3]})
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = c.R, c.G, c.B, c.A
		}
	}
	return out
}

// AlphaBox 返回 alpha >= threshold 那部分的包围盒; 没有这样的像素时 ok 为 false。
func AlphaBox(src *image.RGBA, threshold uint8) (image.Rectangle, bool) {
	if src == nil {
		return image.Rectangle{}, false
	}
	b := src.Rect
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, y):]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[(x-b.Min.X)*4+3] < threshold {
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

// Posterize 把每个 RGB 通道量化到 levels 档(夹到 2..256), alpha 原样保留。
func Posterize(src *image.RGBA, levels int) *image.RGBA {
	if src == nil {
		return nil
	}
	if levels < 2 {
		levels = 2
	}
	if levels > 256 {
		levels = 256
	}
	out := image.NewRGBA(src.Rect)
	n := levels - 1
	for i := 0; i+3 < len(src.Pix); i += 4 {
		out.Pix[i] = uint8(int(src.Pix[i]) * n / 255 * 255 / n)
		out.Pix[i+1] = uint8(int(src.Pix[i+1]) * n / 255 * 255 / n)
		out.Pix[i+2] = uint8(int(src.Pix[i+2]) * n / 255 * 255 / n)
		out.Pix[i+3] = src.Pix[i+3]
	}
	return out
}

// MirrorH 水平镜像, 返回同尺寸新图。
func MirrorH(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	b := src.Rect
	out := image.NewRGBA(b)
	w := b.Dx()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		si := src.PixOffset(b.Min.X, y)
		di := out.PixOffset(b.Min.X, y)
		for x := 0; x < w; x++ {
			copy(out.Pix[di+x*4:di+x*4+4], src.Pix[si+(w-1-x)*4:si+(w-1-x)*4+4])
		}
	}
	return out
}

// CropCenter 把 src 居中裁成 w×h; src 不小于目标时裁中间, 否则原样返回。
func CropCenter(src *image.RGBA, w, h int) *image.RGBA {
	if src == nil || w <= 0 || h <= 0 {
		return nil
	}
	sb := src.Rect
	if sb.Dx() < w || sb.Dy() < h {
		return src
	}
	x0 := sb.Min.X + (sb.Dx()-w)/2
	y0 := sb.Min.Y + (sb.Dy()-h)/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		si := src.PixOffset(x0, y0+y)
		di := dst.PixOffset(0, y)
		copy(dst.Pix[di:di+w*4], src.Pix[si:si+w*4])
	}
	return dst
}

// ResizeCover 把 src 按盖满缩放到 w×h 并居中裁切, 输出正好 w×h。
func ResizeCover(src *image.RGBA, w, h int, par Par) *image.RGBA {
	if src == nil || w <= 0 || h <= 0 {
		return nil
	}
	sb := src.Rect
	sw, sh := sb.Dx(), sb.Dy()
	if sw == w && sh == h {
		return src
	}
	cw, ch := coverSize(sw, sh, w, h)
	full := image.NewRGBA(image.Rect(0, 0, cw, ch))
	resize(src, full, par)
	return CropCenter(full, w, h)
}

// Resize 把 src 缩放到 w×h 并返回新画布; 尺寸相同则原样返回 src。
func Resize(src *image.RGBA, w, h int, par Par) *image.RGBA {
	if src == nil || w <= 0 || h <= 0 {
		return nil
	}
	if src.Rect.Dx() == w && src.Rect.Dy() == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	resize(src, dst, par)
	return dst
}

// ResizeInto 把 src 缩放到 dst 的尺寸(相同尺寸则原样不动)。
func ResizeInto(src, dst *image.RGBA, par Par) {
	if src == nil || dst == nil {
		return
	}
	db := dst.Rect
	if db.Dx() != src.Rect.Dx() || db.Dy() != src.Rect.Dy() {
		resize(src, dst, par)
	}
}

// resize 按哪一维缩小选路: 缩小走面积平均, 放大走 ScaleInto(双三次)。
func resize(src, dst *image.RGBA, par Par) {
	sx := float64(src.Rect.Dx()) / float64(dst.Rect.Dx())
	sy := float64(src.Rect.Dy()) / float64(dst.Rect.Dy())
	if sx >= 1 && sy >= 1 {
		resizeAreaInto(src, dst, sx, sy, par)
		return
	}
	ScaleInto(dst, src, dst.Rect, par)
}

// coverSize 给出盖满 w×h 要把 srcW×srcH 缩到的尺寸。
func coverSize(srcW, srcH, w, h int) (int, int) {
	if srcW <= 0 || srcH <= 0 || w <= 0 || h <= 0 {
		return w, h
	}
	if w*srcH >= h*srcW {
		return w, (w*srcH + srcW - 1) / srcW
	}
	return (h*srcW + srcH - 1) / srcH, h
}

// resizeAreaInto 是缩小那条路: 面积平均, RGB 按 alpha 加权后除以 alpha 之和。
func resizeAreaInto(src, dst *image.RGBA, sx, sy float64, par Par) {
	dh := dst.Rect.Dy()
	if par.Workers <= 1 || dh < par.MinRows {
		resizeAreaRows(dst, src, sx, sy, 0, dh)
		return
	}
	par.Run(dh, func(lo, hi int) { resizeAreaRows(dst, src, sx, sy, lo, hi) })
}

// resizeAreaRows 只算 [lo,hi) 行, 供并行分段; 每行各算各的, 与串行逐像素相同。
func resizeAreaRows(dst, src *image.RGBA, sx, sy float64, lo, hi int) {
	sb := src.Rect
	for iy := lo; iy < hi; iy++ {
		y0s := sb.Min.Y + int(float64(iy)*sy)
		y1s := sb.Min.Y + int(float64(iy+1)*sy)
		if y1s <= y0s {
			y1s = y0s + 1
		}
		if y1s > sb.Max.Y {
			y1s = sb.Max.Y
		}
		for ix := 0; ix < dst.Rect.Dx(); ix++ {
			x0s := sb.Min.X + int(float64(ix)*sx)
			x1s := sb.Min.X + int(float64(ix+1)*sx)
			if x1s <= x0s {
				x1s = x0s + 1
			}
			if x1s > sb.Max.X {
				x1s = sb.Max.X
			}
			var sr, sg, sb2, sa, n float64
			for yy := y0s; yy < y1s; yy++ {
				row := src.Pix[yy*src.Stride:]
				for xx := x0s; xx < x1s; xx++ {
					i := (xx - sb.Min.X) * 4
					a := float64(row[i+3])
					sr += float64(row[i]) * a
					sg += float64(row[i+1]) * a
					sb2 += float64(row[i+2]) * a
					sa += a
					n++
				}
			}
			if n == 0 || sa == 0 {
				continue
			}
			o := dst.PixOffset(dst.Rect.Min.X+ix, dst.Rect.Min.Y+iy)
			dst.Pix[o] = rgba.Clamp8(int(sr/sa + 0.5))
			dst.Pix[o+1] = rgba.Clamp8(int(sg/sa + 0.5))
			dst.Pix[o+2] = rgba.Clamp8(int(sb2/sa + 0.5))
			dst.Pix[o+3] = rgba.Clamp8(int(sa/n + 0.5))
		}
	}
}

// MeanLuma 返回一块区域的平均亮度(0..255, Rec.601 权重); 越界按画布内实际范围算。
func MeanLuma(img *image.RGBA, r image.Rectangle) float64 {
	if img == nil {
		return 0
	}
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return 0
	}
	var sum float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[y*img.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			i := (x - img.Rect.Min.X) * 4
			sum += 0.299*float64(row[i]) + 0.587*float64(row[i+1]) + 0.114*float64(row[i+2])
		}
	}
	return sum / float64(r.Dx()*r.Dy())
}

// Compress 把画布向自身均值收缩对比度(k=1 不变, k=0 压成一片均值), 只动 RGB 不动 A。
// 均值按隔点采样(4 像素一跳)求 —— 背景压平不需要逐像素精度。
func Compress(dst *image.RGBA, k float64) {
	if dst == nil || k >= 1 || k < 0 {
		return
	}
	const step = 4
	b := dst.Bounds()
	sum, n := [3]float64{}, 0
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			i := dst.PixOffset(x, y)
			sum[0] += float64(dst.Pix[i])
			sum[1] += float64(dst.Pix[i+1])
			sum[2] += float64(dst.Pix[i+2])
			n++
		}
	}
	if n == 0 {
		return
	}
	var mean [3]float64
	for c := range mean {
		mean[c] = sum[c] / float64(n)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(b.Min.X, y):dst.PixOffset(b.Max.X, y)]
		for i := 0; i+2 < len(row); i += 4 {
			for c := 0; c < 3; c++ {
				row[i+c] = compressByte(mean[c] + (float64(row[i+c])-mean[c])*k)
			}
		}
	}
}

func compressByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(math.Round(v))
	}
}

// LumaMode 是这块区域里亮度直方图的**众数**与最小值: 众数就是画面里最多的那一档(板面/底色),
// 最小值是笔画最深的地方。亮度按 Rec.601 权重取整(299/587/114), 与渲染层同一个口径。
func LumaMode(img *image.RGBA, r image.Rectangle) (mode, lo int) {
	if img == nil {
		return 0, 0
	}
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return 0, 0
	}
	var hist [256]int
	lo = 255
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := lumaAt(img, x, y)
			hist[v]++
			if v < lo {
				lo = v
			}
		}
	}
	best := -1
	for v, n := range hist {
		if n > best {
			mode, best = v, n
		}
	}
	return mode, lo
}

// InkFromLuma 把一块图化成"纯黑的墨": RGB 一律 0, alpha 是像素相对 mode 的深度 —— mode 那一档
// 全透明(底色原样透出来), 越暗越实, 到底 lo 为满; 再乘上像素自己的 alpha。mode==lo 时全透明。
//
// 回来的是 0 起点的图(与原图尺寸相同, 见 CropCenter 那类原语的惯例), 可以当贴图直接用。
func InkFromLuma(img *image.RGBA, r image.Rectangle, mode, lo int) *image.RGBA {
	if img == nil {
		return nil
	}
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	span := float64(mode - lo)
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			i := img.PixOffset(r.Min.X+x, r.Min.Y+y)
			if span <= 0 {
				continue
			}
			t := float64(mode-lumaAt(img, r.Min.X+x, r.Min.Y+y)) / span
			switch {
			case t <= 0:
				continue
			case t > 1:
				t = 1
			}
			out.Pix[out.PixOffset(x, y)+3] = uint8(t*float64(img.Pix[i+3]) + 0.5)
		}
	}
	return out
}

// lumaAt 是 Rec.601 亮度的整数口径(与 LumaMode / InkFromLuma 共用)。
func lumaAt(img *image.RGBA, x, y int) int {
	i := img.PixOffset(x, y)
	return (int(img.Pix[i])*299 + int(img.Pix[i+1])*587 + int(img.Pix[i+2])*114) / 1000
}
