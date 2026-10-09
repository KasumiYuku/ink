package ink

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"github.com/KasumiYuku/ink/internal/rgba"
)

// 颜色助手: 本库一律直线 alpha, 所以这里只做字面换算, 不做预乘。

var (
	White = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	Black = color.RGBA{R: 0, G: 0, B: 0, A: 255}
	Gray  = color.RGBA{R: 140, G: 145, B: 152, A: 255}
	Clear = color.RGBA{}
)

// Hex 是 0xRRGGBB(不透明)。
func Hex(v uint32) color.RGBA {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// HexA 是 0xRRGGBBAA。
func HexA(v uint32) color.RGBA {
	return color.RGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}
}

func RGBA(r, g, b, a uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: a} }

func WithAlpha(c color.RGBA, a uint8) color.RGBA {
	c.A = a
	return c
}

// Mix 按 t 在两色之间插值。
func Mix(a, b color.RGBA, t float64) color.RGBA { return rgba.Lerp(a, b, t) }

// Parse 认 "#RRGGBB" 与 "#RRGGBBAA"(# 可省), 面板与配置里的色值直接喂进来。
func Parse(s string) (color.RGBA, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(t, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("ink: 颜色 %q 不是十六进制", s)
	}
	switch len(t) {
	case 6:
		return Hex(uint32(v)), nil
	case 8:
		return HexA(uint32(v)), nil
	}
	return color.RGBA{}, fmt.Errorf("ink: 颜色 %q 应当是 6 位或 8 位", s)
}
