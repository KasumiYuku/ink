package raster

import (
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// RoundMask 是一个半径的圆角覆盖率表: 只存"离角多远 → 覆盖率", 与矩形多大、放在哪儿无关。
// 覆盖率的来源只有一个 —— 像素中心到弧心的距离, 所以同一半径的卡片共用一张表, 且结果精确。
type RoundMask struct {
	R      int
	Radius float64
	pix    []float64
}

func NewRoundMask(radius float64) *RoundMask {
	if radius < 0 {
		radius = 0
	}
	r := int(math.Ceil(radius)) + 1
	m := &RoundMask{R: r, Radius: radius, pix: make([]float64, r*r)}
	for j := 0; j < r; j++ {
		for i := 0; i < r; i++ {
			m.pix[j*r+i] = cornerCover(radius, radius-(float64(i)+0.5), radius-(float64(j)+0.5))
		}
	}
	return m
}

// cornerCover 是圆角覆盖率的同一个式子: dx/dy 是像素中心到弧心的距离。
func cornerCover(radius, dx, dy float64) float64 {
	if dx <= 0 || dy <= 0 {
		return 1
	}
	switch dist := math.Hypot(dx, dy); {
	case dist <= radius-0.5:
		return 1
	case dist >= radius+0.5:
		return 0
	default:
		return radius + 0.5 - dist
	}
}

// left/right 都是"离角 i 个像素、离上/下缘 j 个像素"的覆盖率 —— 表按离角的距离索引, 左右对称。
func (m *RoundMask) left(i, j int) float64  { return m.pix[j*m.R+i] }
func (m *RoundMask) right(i, j int) float64 { return m.pix[j*m.R+i] }

// Fill 用这张表铺一个圆角矩形: 每行两端按表混覆盖率, 中间整段一次落笔。
func (m *RoundMask) Fill(dst *image.RGBA, r image.Rectangle, row Row, mode Mode, hard bool) {
	if dst == nil {
		return
	}
	r = r.Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	last := r.Dy() - 1
	for y := r.Min.Y; y < r.Max.Y; y++ {
		c := row.At(y, r.Min.Y, r.Max.Y)
		j := y - r.Min.Y
		if j > last-j {
			j = last - j
		}
		if j >= m.R {
			FillRectMode(dst, image.Rect(r.Min.X, y, r.Max.X, y+1), c, mode)
			continue
		}
		x0 := r.Min.X
		for i := 0; i < m.R && x0 < r.Max.X; i++ {
			cov := m.left(i, j)
			if cov >= 1 {
				break
			}
			if hard {
				if cov >= 0.5 {
					FillPixel(dst, x0, y, c, mode)
				}
			} else if cov > 0 {
				FillPixel(dst, x0, y, coverageInk(c, cov), mode)
			}
			x0++
		}
		x1 := r.Max.X
		for i := 0; i < m.R && x1 > x0; i++ {
			cov := m.right(i, j)
			if cov >= 1 {
				break
			}
			x1--
			if hard {
				if cov >= 0.5 {
					FillPixel(dst, x1, y, c, mode)
				}
			} else if cov > 0 {
				FillPixel(dst, x1, y, coverageInk(c, cov), mode)
			}
		}
		if x0 < x1 {
			FillRectMode(dst, image.Rect(x0, y, x1, y+1), c, mode)
		}
	}
}

// coverageInk 把覆盖率压进 alpha(截断取整)。
func coverageInk(c color.RGBA, cov float64) color.RGBA {
	c.A = rgba.Clamp8(int(float64(c.A) * cov))
	return c
}

// MaskCache 按半径缓存圆角覆盖率表: 一页几十张同尺寸卡片只算一次。
type MaskCache struct {
	mu    sync.Mutex
	m     map[float64]*RoundMask
	limit int
}

func NewMaskCache(limit int) *MaskCache {
	if limit < 1 {
		limit = 32
	}
	return &MaskCache{m: map[float64]*RoundMask{}, limit: limit}
}

// Round 取半径 radius 的角块表; 表本身不可变, 可以并发用。
func (c *MaskCache) Round(radius float64) *RoundMask {
	if c == nil {
		return NewRoundMask(radius)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.m[radius]; ok {
		return m
	}
	m := NewRoundMask(radius)
	if len(c.m) >= c.limit {
		c.m = map[float64]*RoundMask{}
	}
	c.m[radius] = m
	return m
}

// Len 是当前缓存的半径数。
func (c *MaskCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
