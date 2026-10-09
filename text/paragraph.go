package text

import (
	"image"
	"strings"
)

// Wrap 按 width 把 s 折成多行; width<=0 时按 \n 切段不折行。
func Wrap(f Face, s string, width float64, st Style) []string {
	if s == "" {
		return []string{}
	}
	segs := strings.Split(s, "\n")
	if width <= 0 {
		return segs
	}
	var out []string
	for _, seg := range segs {
		out = append(out, wrapSegment(f, seg, width, st)...)
	}
	return out
}

// DrawParagraph 在 r 内按 st.Align 对段落逐行落笔, 返回画出的行数与段落总高。
func DrawParagraph(dst *image.RGBA, f Face, r image.Rectangle, s string, st Style, lineHeight float64) (lines int, height float64) {
	lh := lineHeight
	if lh <= 0 {
		lh = f.Metrics().LineHeight
	}
	ascent := f.Metrics().Ascent
	ls := Wrap(f, s, float64(r.Dx()), st)
	height = float64(len(ls)) * lh
	for i, ln := range ls {
		baseline := float64(r.Min.Y) + ascent + float64(i)*lh
		if baseline > float64(r.Max.Y) {
			break
		}
		x := float64(r.Min.X)
		switch st.Align {
		case Center:
			x = float64(r.Min.X) + float64(r.Dx())/2
		case Right:
			x = float64(r.Max.X)
		}
		Draw(dst, f, x, baseline, ln, st)
		lines++
	}
	return lines, height
}

// MeasureParagraph 返回折行后的最大行宽、总高与行数。
func MeasureParagraph(f Face, s string, width float64, st Style, lineHeight float64) (w, h float64, lines int) {
	lh := lineHeight
	if lh <= 0 {
		lh = f.Metrics().LineHeight
	}
	ls := Wrap(f, s, width, st)
	for _, ln := range ls {
		if adv := f.Advance(ln, st); adv > w {
			w = adv
		}
	}
	return w, float64(len(ls)) * lh, len(ls)
}

type wrapToken struct {
	text  string
	space bool
}

// wrapSegment 在单段(无 \n)内贪心折行; 连字符后与每个宽字符后都是断点。
func wrapSegment(f Face, seg string, width float64, st Style) []string {
	var lines []string
	var line strings.Builder
	var pending string
	for _, tk := range tokens(seg) {
		if tk.space {
			if line.Len() > 0 {
				pending += tk.text
			}
			continue
		}
		if f.Advance(line.String()+pending+tk.text, st) <= width {
			line.WriteString(pending)
			line.WriteString(tk.text)
			pending = ""
			continue
		}
		pending = ""
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
		}
		if f.Advance(tk.text, st) <= width {
			line.WriteString(tk.text)
			continue
		}
		done, tail := hardBreak(f, tk.text, width, st)
		lines = append(lines, done...)
		line.WriteString(tail)
	}
	return kinsokuFix(append(lines, line.String()))
}

func tokens(seg string) []wrapToken {
	var toks []wrapToken
	var word []rune
	flush := func() {
		if len(word) > 0 {
			toks = append(toks, wrapToken{text: string(word)})
			word = word[:0]
		}
	}
	for _, r := range seg {
		switch {
		case r == ' ':
			flush()
			if n := len(toks); n > 0 && toks[n-1].space {
				toks[n-1].text += " "
			} else {
				toks = append(toks, wrapToken{text: " ", space: true})
			}
		case r == '-' || canBreakAnywhere(r):
			word = append(word, r)
			flush()
		default:
			word = append(word, r)
		}
	}
	flush()
	return toks
}

// canBreakAnywhere 判断宽字符(CJK/韩文/全角), 这类字符两侧可逐字断。
func canBreakAnywhere(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x9FFF:
		return true
	case r >= 0xAC00 && r <= 0xD7A3:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0xFF00 && r <= 0xFFEF:
		return true
	case r >= 0x20000 && r <= 0x2FA1F:
		return true
	}
	return false
}

// hardBreak 把放不下的词按 rune 断开; 单字放不下时仍独占一行(否则无法保证宽度)。
func hardBreak(f Face, word string, width float64, st Style) ([]string, string) {
	runes := []rune(word)
	var done []string
	for start := 0; start < len(runes); {
		end := start
		for end < len(runes) && f.Advance(string(runes[start:end+1]), st) <= width {
			end++
		}
		if end == start {
			end = start + 1
		}
		if end >= len(runes) {
			return done, string(runes[start:end])
		}
		done = append(done, string(runes[start:end]))
		start = end
	}
	return done, ""
}
