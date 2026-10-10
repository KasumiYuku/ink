# ink

面向**服务端出图**的 2D 光栅引擎：把一份渲染声明变成一张确定的位图。

[![Go Reference](https://pkg.go.dev/badge/github.com/KasumiYuku/ink.svg)](https://pkg.go.dev/github.com/KasumiYuku/ink)
[![Go Version](https://img.shields.io/badge/go-1.26%2B-00ADD8)](https://go.dev/dl/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

ink 不是一个通用 GUI 框架。它只做一件事：在服务器上把结构化数据渲染成图片，并且保证这件事
**快、省、可复现、可诊断**。

---

## 特性

- **确定性输出**：同样的声明与同样的素材，在任何一次运行、任何一台机器上产出逐字节相同的
  位图；amd64 与 arm64 的输出也一致。
- **纯 Go，无 CGO**：`go build` 即可，不依赖图形驱动、窗口系统或本地工具链。WebP 编码是唯一
  的可选外部依赖，缺失时明确报错而非静默降级。
- **无包级状态**：引擎实例持有全部状态，一个进程内可以有多个互不干扰的引擎，且全部 API
  并发安全。
- **热路径零分配**：画布、解码图与字形掩码都按额度池化，稳态渲染不产生堆分配。
- **声明式为主，逃生舱为辅**：`Sheet` 描述"画什么"，`Canvas` 在需要时直接下笔。
- **面向目标的向量化**：amd64 使用 SSE2、arm64 使用 NEON —— 都是各自平台的**基线**指令集，
  不需要运行时特征探测，也不存在"老 CPU 上崩掉"的风险。
- **内建诊断**：渲染指纹、算子级耗时统计、画布/解码配额与命中统计。

## 安装

```bash
go get github.com/KasumiYuku/ink
```

要求 Go 1.26 或更高。支持 `amd64` 与 `arm64`（`SSE2` / `NEON` 加速路径）；其它架构可直接编译
运行，自动走标量路径，输出与加速路径逐字节相同。

## 快速开始

```go
package main

import (
	"os"

	"github.com/KasumiYuku/ink"
	"github.com/KasumiYuku/ink/encode"
	"github.com/KasumiYuku/ink/text"
	"github.com/KasumiYuku/ink/theme"
)

func main() {
	eng, err := ink.New(ink.Options{
		Assets: theme.Options{
			Dir:   "assets", // 素材根目录
			Fonts: map[string]string{"sans": "font/Sans-Regular.ttf"},
		},
		Encode:  encode.JPEG(88),
		Workers: 4,
		Version: "v1",
	})
	if err != nil {
		panic(err)
	}
	defer eng.Close()

	face := eng.Assets().Fonts().Chain("sans").Face(36)
	card := ink.RoundBox(ink.R(40, 60, 320, 180), 16)

	sheet := ink.NewSheet(1200, 630).
		Bg(ink.Hex(0x101216)).
		Shape(card, ink.VGradient(ink.Hex(0x23262E), ink.Hex(0x14161B)), ink.Over).
		StrokeShape(card, 2, ink.HexA(0xFFFFFF33), ink.Over).
		Text(60, 120, "Hello, ink", face, text.Style{Ink: ink.White, Kern: true})

	jpg, err := eng.Render(sheet)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("out.jpg", jpg, 0o644); err != nil {
		panic(err)
	}
}
```

`Engine` 提供三种出图方式：

| 方法 | 用途 |
|---|---|
| `Render(sheet) ([]byte, error)` | 画完并编码 |
| `Draw(sheet) (*image.RGBA, error)` | 只要像素 |
| `RenderInto(dst, sheet) error` | 画进调用方给的画布，零拷贝 |

## 核心概念

ink 把绘制拆成四个**正交**概念，所有视觉构件都是它们的组合；库里不存在"卡片控件"这类东西。

| 概念 | 类型 | 说明 |
|---|---|---|
| 外形 | `Shape` | 矩形 / 圆角 / 斜切 / 任意路径 / 多段轮廓，可外扩、平移、裁剪、并集、变换 |
| 填充 | `Row` | 纯色 / 竖直与水平渐变 / 径向 / 衰减 / 色阶 |
| 合成 | `Mode` | `Over` / `Add` / `Multiply` / `Screen` / `Copy` |
| 装饰 | 方法 | 描边（外形内缘一圈）、投影、辉光、内高光、磨砂 |

四者的组合点只有一个：`Brush`（落笔上下文，携带画布、裁剪、合成方式、并发策略与缓冲池）。
`Sheet` 是它的声明式外壳，`Canvas` 是它的直接写入口。

```go
card := ink.SkewBox(ink.R(40, 30, 320, 180), 20, ink.Lean15) // 斜切 + 圆角，浮点不取整
sheet.
	Shadow(card, ink.ShadowSpec{Steps: 6, StepW: 5, Ink: shadowInk}).
	Shape(card, ink.VGradient(top, bottom), ink.Over).
	StrokeShape(card, 2, rimInk, ink.Over).
	Highlight(ink.R(40, 30, 320, 180), 20, 2, topInk, rimInk)
```

路径不是第二套渲染器，它只是 `Shape` 的又一个实现，因此渐变、描边、投影、辉光、裁剪与并发
对路径**自动可用**：

```go
p := ink.NewPath()
p.MoveTo(20, 20).CubicTo(80, 0, 160, 120, 220, 40).Close()
sheet.Shape(p, ink.Solid(ink.Hex(0x3A7BD5)), ink.Over)
```

### Sheet 能画什么

`Bg` `Image` `Dim` `Blur` `Card` `CardHard` `Rim` `Skew` `Sprite` `Tinted` `Text` `TextBox`
`Paragraph` `Shape` `StrokeShape` `Shadow` `Glow` `Highlight` `Frost`，以及四个**作用域**算子：

| 作用域 | 语义 |
|---|---|
| `Clip` / `ClipShape` / `Mask` | 按矩形、外形或图像遮罩限定后续绘制 |
| `Group(alpha, fn)` | 开离屏图层，按整体透明度合成，组内重叠不会露两层 |
| `Transform(m, fn)` | 对矢量外形施加矩阵（位图吃平移与轴向缩放，文字吃平移） |
| `Parallel(fn)` | 声明块内算子互不重叠，允许并行绘制；引擎会自行核对，不满足则顺序执行并告警 |

## 文本

`text` 负责度量、排版与落笔，字形掩码按字号缓存：

```go
st := text.Style{Ink: ink.White, Align: text.Center, Kern: true}
sheet.TextBox(ink.R(0, 300, 1200, 40), "标题", face, st)
sheet.Paragraph(ink.R(60, 360, 600, 200), "自动折行并遵守中日韩行首行尾禁则。", face, st, 34)
```

| 能力 | API |
|---|---|
| 单行 / 框内对齐 | `Text` / `TextBox` |
| 折行与段落 | `Wrap` / `Paragraph` / `MeasureParagraph` |
| 截断 | `Ellipsize` |
| 度量 | `Measure` / `Face.Measure` / `Face.Advance` |
| 批量同样式 | `DrawRuns` |

字体集合通过 `theme.Options.Fonts` 声明（逻辑名 → 相对路径），`Chain` 支持缺字回退。

## 素材与编码

素材与图幅解耦：调用方只认 **key**，key 落到哪个文件由 `Resolve` 决定。解码图按字节额度做
LRU，同一个 key 并发只解一次。

```go
theme.Options{
	Dir:         "assets",
	Resolve:     func(key string) string { return key + ".jpg" }, // 可选
	DecodeBytes: 64 << 20,                                        // 解码图常驻额度
	Fonts:       map[string]string{"sans": "font/Sans-Regular.ttf"},
}
```

编码只影响体积，但"用哪个编码器"会改变字节，因此编码器身份参与指纹：

| 编码器 | 说明 |
|---|---|
| `encode.JPEG(quality)` | 标准库 JPEG |
| `encode.WebP(opts)` | 调用外部 `cwebp`（画布以 PPM 从 stdin 流过去，不留临时文件）；实现 `encode.Budgeted`：按这一张的体积上限退档重编；不可用时返回 `encode.ErrUnavailable` |

## 确定性与指纹

`Engine.Fingerprint(kind, spec)` 由三部分哈希而成：

```
fingerprint = hash(epoch | kind | json(spec))
epoch       = hash(version | contentDigest | encoder.ID())
```

`spec` 覆盖**决定输出的每一个值**：尺寸、颜色、坐标、字体、素材 key、配额、编码参数。渲染栈
自身的内容摘要（`contentDigest`）与编码器身份也参与其中，因此**升级库或换编码器会自动换掉
全部指纹**——缓存不会把旧实现的产物当成新实现的产物。

`Options.Version` 是调用方的版本标签，建议随业务声明一起递增。

## 并发模型

| 层级 | 机制 |
|---|---|
| 引擎级 | `Options.Workers` 限定同时渲染张数；`Stats().InFlight/Peak` 可观测 |
| 单图行级 | 模糊、缩放、逐行填充按行分块，块数固定，结果与串行逐位相同 |
| 算子级 | `Sheet.Parallel` 声明互不重叠的算子集合；引擎逐对核对包围盒，不满足则退回顺序并告警 |

所有层级都遵守一条规则：**并行不改变结果**。

## 资源配额与诊断

```go
ink.Options{CanvasMB: 96} // 画布池额度；0 表示按典型图幅自动推导
```

`engine.Stats()` 给出渲染次数、失败次数、并发峰值、画布与解码的已用/上限字节、缓存的掩码数；
打开 `Options.Trace` 后还会给出**每个算子**的调用次数与耗时，用于定位单张图里的热点。

`Options.OnWarn` 是唯一的诊断出口：能力降级（例如对含旋转的变换贴图）会继续渲染并如实报告，
而不是静默产出一张错的图。

## 性能

同一份 2880×1329 的满档页：整屏底图缩放 + 整屏模糊 + 压暗 + 50 张斜切卡（投影/填充/描边）+
50 张曲绘 + 300 行文字 + JPEG 编码。

| 平台 | 满档页 |
|---|---|
| x86-64，16 线程 | 约 305 ms |
| arm64，4 核 | 约 205 ms |

关键路径的实测收益：

| 优化 | 收益 |
|---|---|
| 整屏缩放（可分离两趟 + 源行缓存） | 5.3× |
| 投影（各层嵌套关系 → 复合表一次填充） | 2.3–3.2× |
| 半透明填充内循环（SSE2 / NEON） | 1.8–3.1× |
| 圆角覆盖率（按半径缓存） | 4.5× |

热路径的零分配由测试守住，不靠自觉。

## 版本与兼容

当前处于 `v0.x`：API 在大版本发布前仍可能调整，每次不兼容变更都会在 CHANGELOG 中列出。
从 `v1.0.0` 起遵循语义化版本。

## 许可证

[MIT](LICENSE)
