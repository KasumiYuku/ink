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
	track := fixed.Int26_6(math.Round(st.Track * 64))
	var total fixed.Int26_6
	var prev rune
	first := true
	for _, r := range s {
		if fs := f.owner(r, bin); fs != nil {
			if st.Kern && !first {
				total += f.set.kern(f.faces[0], prev, r)
			}
			total += f.set.glyph(fs, r, bin, 0).Adv
			prev, first = r, false
		}
		total += track
	}
	return float64(total) / 64
}

func (f *glyphFace) Advance(s string, st Style) float64 { return f.advance(s, st) }

func (f *glyphFace) Measure(s string) (w, h float64) {
	return f.advance(s, Style{}), f.Metrics().LineHeight
}
