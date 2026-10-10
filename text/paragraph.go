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

// roundW 把一行字的宽度落到整像素(与落笔同一个口径: int(v+0.5))。
func roundW(v float64) int { return int(v + 0.5) }

// WrapLimit 按宽度把一段文字折成至多 maxLines 行: 每行贪心取尽量多的字, 最后一行的余量接省略号。
//
// 判据与落笔同口径(整像素, 见 roundW): 量出来"刚好放得下"的那一行, 画出来也刚好放得下。
func WrapLimit(f Face, s string, width float64, maxLines int, st Style) []string {
	if f == nil || strings.TrimSpace(s) == "" || maxLines <= 0 {
		return nil
	}
	limit := int(width)
	runes := []rune(s)
	lines := make([]string, 0, maxLines)
	for len(runes) > 0 && len(lines) < maxLines {
		last := len(lines) == maxLines-1
		take := len(runes)
		for take > 0 && roundW(Width(f, string(runes[:take]), st)) > limit {
			take--
		}
		if take <= 0 {
			take = 1
		}
		if take >= len(runes) {
			lines = append(lines, string(runes))
			break
		}
		if last {
			lines = append(lines, Ellipsize(f, string(runes), float64(limit), st))
			break
		}
		lines = append(lines, string(runes[:take]))
		runes = runes[take:]
	}
	return lines
}

// Balance 把已经折好的几行重新摊匀: 行数不变、段(按 sep 拆出来的原子段)顺序不变, 枚举所有切法,
// 取"最长那一行最短"的那个; 段数不超过行数、或找不到合法切法时原样返回。
//
// 摊匀是折行的第二步: 贪心装出来的行往往最后一行很短, 观感上像一句被切断的话。切法只在
// 段边界上挪 —— 段内部的字不会被拆开, 所以它不改变每一段的内容, 只改变它们分到哪一行。
func Balance(f Face, lines []string, sep string, width float64, st Style) []string {
	if f == nil || len(lines) < 2 {
		return lines
	}
	segments := make([]string, 0, len(lines))
	for _, line := range lines {
		segments = append(segments, strings.Split(line, sep)...)
	}
	if len(segments) <= len(lines) {
		return lines // 一段一行, 没有可挪的
	}
	limit := int(width)
	rowWidth := func(segs []string) int { return roundW(Width(f, strings.Join(segs, sep), st)) }

	rows := len(lines)
	cuts := make([]int, rows)    // 试探中的切法: 每一行结束在哪个段(不含)
	best := make([]int, rows)    // 目前最好的切法
	bestWorst, found := 0, false // 最好的切法里最长那一行有多宽
	var walk func(row, from, worst int)
	walk = func(row, from, worst int) {
		if row == rows-1 {
			if from >= len(segments) {
				return
			}
			w := rowWidth(segments[from:])
			if w > limit {
				return
			}
			if w > worst {
				worst = w
			}
			if !found || worst < bestWorst {
				copy(best, cuts)
				best[row] = len(segments)
				bestWorst, found = worst, true
			}
			return
		}
		// 最后几行各至少要留一段, 所以这一行最多断到 len(segments)-(rows-1-row)。
		for end := from + 1; end <= len(segments)-(rows-1-row); end++ {
			w := rowWidth(segments[from:end])
			if w > limit {
				break
			}
			cuts[row] = end
			next := worst
			if w > next {
				next = w
			}
			walk(row+1, end, next)
		}
	}
	walk(0, 0, 0)
	if !found {
		return lines
	}

	out := make([]string, 0, rows)
	from := 0
	for _, end := range best {
		out = append(out, strings.Join(segments[from:end], sep))
		from = end
	}
	return out
}
