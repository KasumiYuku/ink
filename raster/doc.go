// Package raster 是像素机制层。它有两层, 别混着用:
//
// # 给调用方的一层(推荐)
//
// 画任何东西都只有三步: 造外形、给每行墨色、交给 Brush 落笔。
//
//	box := raster.B(40, 30, 320, 180)               // 浮点坐标
//	card := raster.Skew(box, 24, raster.Lean15)     // 外形(可 Round/Outset/ShiftBy/ClipShape/Ring/Profile)
//	br := raster.Brush{Dst: canvas, Par: par}       // 落笔上下文(画布+裁剪+合成+并发+缓存)
//	br.Fill(card, raster.VGradient(top, bottom))    // 填充
//	br.Stroke(card, 3, raster.Solid(rim))           // 描边(就是"外形减内缩形"那个环)
//	br.Shadow(card, raster.ShadowSpec{...})         // 装饰
//	br.Glow(card, raster.GlowSpec{...})
//	br.Sprite(at, win, src, img, raster.SpriteOpts{Lean: raster.Lean15})
//
// 需要认识的名字就这些: Box / B / Shape(Rect·Round·Skew·Outset·ShiftBy·ClipShape·Ring·Profile·
// Path·Area·Outline) / Row(Solid·VGradient·HGRadient·Radial·Falloff·Ramp) / Mode / Brush / Path /
// SpriteOpts / ShadowSpec / GlowSpec / Par / Matrix / ClipMask / StrokeOpts / FillRule。
//
// # 底层一层(给本库自己、对拍测试与基准)
//
// FillSpan / FillPixel / FillRectMode / SkewDX / Fill / FillSkewed / StrokeRect / ScaleInto /
// Blur / Tint 这些是**内循环原语**: Brush 与特效都建在它们上面, 逐字节对拍的参考实现也建在
// 它们上面。它们不隐藏(否则没法证明等价), 但**上层代码不该直接调** —— 直接调就没有裁剪、
// 没有并发的统一口径、也没有 Hard 与掩码缓存。
//
// 唯一一个"看起来多余但必须留"的是 FillSkewed: 它按行取整, 而 Skew 形状是浮点不取整,
// 两者的像素不同。要像素级锯齿边缘用前者, 要平滑边缘用后者。
package raster
