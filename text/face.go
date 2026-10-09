package text

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

// resolve 沿链找到出这个字的那一族: 掩码与字距都得问对应那一族。
func (f *glyphFace) resolve(r rune, bin bool) (*mask, *faceState) {
	for _, fs := range f.faces {
		if m := f.set.glyph(fs, r, bin); m.present {
			return m, fs
		}
	}
	return nil, nil
}

// advance 是唯一一份推进宽度算法: 逐字取整推进 + 可选字距调整 + 字距; 缺字不进位。
func (f *glyphFace) advance(s string, st Style) float64 {
	if len(f.faces) == 0 {
		return 0
	}
	bin := st.Mode == ModeBinary
	var total float64
	var prev rune
	first := true
	for _, r := range s {
		if m, fs := f.resolve(r, bin); m != nil {
			if st.Kern && !first && fs != nil {
				total += float64(f.set.kern(fs, prev, r))
			}
			total += float64(m.Advance)
			prev, first = r, false
		}
		total += st.Track
	}
	return total
}

func (f *glyphFace) Advance(s string, st Style) float64 { return f.advance(s, st) }

func (f *glyphFace) Measure(s string) (w, h float64) {
	return f.advance(s, Style{}), f.Metrics().LineHeight
}
