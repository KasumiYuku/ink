package text

import (
	"math"

	"golang.org/x/image/math/fixed"
)

// Face 是某一族在某个字号上的度量与排版视图。
type Face interface {
	Measure(s string) (w, h float64)
	Advance(s string, st Style) float64
	Metrics() Metrics
	Size() float64
}

// glyphFace 是链在某个字号上的视图, 实现 Face。
type glyphFace struct {
	set   *Set
	size  float64
	faces []*faceState
}

func (f *glyphFace) Size() float64 { return f.size }

func (f *glyphFace) Metrics() Metrics {
	if len(f.faces) == 0 {
		return Metrics{}
	}
	return f.faces[0].metrics
}

// owner 沿链找到出这个字的那一族: 掩码与字距都得问对应那一族。
func (f *glyphFace) owner(r rune, bin bool) *faceState {
	for _, fs := range f.faces {
		if f.set.glyph(fs, r, bin, 0).present {
			return fs
		}
	}
	return nil
}

// advance 是唯一一份推进宽度算法: 定点累加字形宽度与字距, 缺字不进位。
func (f *glyphFace) advance(s string, st Style) float64 {
	if len(f.faces) == 0 {
		return 0
	}
	bin := st.Mode == ModeBinary
	track := stepPX(st.Track, st.Quantize)
	var total fixed.Int26_6
	var prev rune
	first := true
	for _, r := range s {
		if fs := f.owner(r, bin); fs != nil {
			if st.Kern && !first {
				total += step26(f.set.kern(f.faces[0], prev, r), st.Quantize)
			}
			total += step26(f.set.glyph(fs, r, bin, 0).Adv, st.Quantize)
			prev, first = r, false
		}
		total += track
	}
	return float64(total) / 64
}

// step26 是一笔推进: Quantize 时吸附到整像素(定点里的整像素是 64 的整数倍)。
func step26(v fixed.Int26_6, quantize bool) fixed.Int26_6 {
	if !quantize {
		return v
	}
	return fixed.Int26_6(math.Round(float64(v)/64) * 64)
}

// stepPX 同 step26, 但输入是浮点像素(字距那种)。
func stepPX(px float64, quantize bool) fixed.Int26_6 {
	if quantize {
		return fixed.Int26_6(math.Round(px) * 64)
	}
	return fixed.Int26_6(math.Round(px * 64))
}

func (f *glyphFace) Advance(s string, st Style) float64 { return f.advance(s, st) }

func (f *glyphFace) Measure(s string) (w, h float64) {
	return f.advance(s, Style{}), f.Metrics().LineHeight
}
