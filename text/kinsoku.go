package text

import (
	"strings"
	"unicode/utf8"
)

// noLineStart 是行首禁则: 这些字符不能出现在一行开头(要把它拽回上一行)。
const noLineStart = "，、。．；：？！）］｝〉》」』】〕〗〙〛’”！％‰℃°′″…—～·ー々,.;:?!)]}\"'%"

// noLineEnd 是行尾禁则: 这些字符不能出现在一行结尾(要把它推到下一行)。
const noLineEnd = "（［｛〈《「『【〔〖〘〚‘“([{<"

// NoLineStart 判断一个字符是否行首禁则。
func NoLineStart(r rune) bool { return strings.ContainsRune(noLineStart, r) }

// NoLineEnd 判断一个字符是否行尾禁则。
func NoLineEnd(r rune) bool { return strings.ContainsRune(noLineEnd, r) }

// kinsokuMaxMove 是单个边界最多挪几个字符(连着的标点一起挪, 到顶就放弃, 免得死循环)。
const kinsokuMaxMove = 8

// kinsokuFix 按禁则修折行结果的边界: 行首禁则字符拽回上一行, 行尾禁则字符推到下一行。
// 只动边界, 所以"每行宽度"至多多出一个被拽回来的字符; 空行会被丢掉(除 \n 产生的之外不会出现)。
func kinsokuFix(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	for i := 1; i < len(lines); i++ {
		for k := 0; k < kinsokuMaxMove; k++ {
			r, size := decodeFirst(lines[i])
			if size == 0 || !NoLineStart(r) || lines[i-1] == "" {
				break
			}
			lines[i-1] += string(r)
			lines[i] = lines[i][size:]
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		for k := 0; k < kinsokuMaxMove; k++ {
			r, size := decodeLast(lines[i])
			if size == 0 || !NoLineEnd(r) {
				break
			}
			lines[i+1] = string(r) + lines[i+1]
			lines[i] = lines[i][:len(lines[i])-size]
		}
	}
	out := lines[:0]
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func decodeFirst(s string) (rune, int) {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return 0, 0
	}
	return r, size
}

func decodeLast(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	r, size := utf8.DecodeLastRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return 0, 0
	}
	return r, size
}
