package ink

import (
	"errors"
	"image"
	"sync"
	"sync/atomic"
)

// Parallel 并行画一组"互不重叠"的算子; 不相交这件事由引擎核对, 核对不过就顺序画并 Warn。
func (c *Canvas) Parallel(ops []Op) {
	if len(ops) < 2 || !disjoint(ops) {
		if len(ops) >= 2 {
			c.Warn(errors.New("ink: Parallel 里有相交或改栈的算子, 已退回顺序绘制"))
		}
		for i := range ops {
			ops[i].Draw(c)
		}
		return
	}
	n := min(c.eng.gate.Cap(), len(ops))
	var wg sync.WaitGroup
	var next atomic.Int64
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := *c
			sub.groups = nil
			sub.serial = true // 块内不再分行并行, 免得核超订
			for {
				i := int(next.Add(1)) - 1
				if i >= len(ops) {
					return
				}
				ops[i].Draw(&sub)
			}
		}()
	}
	wg.Wait()
}

// disjoint 核对: 每个算子范围已知、不含改栈算子, 且两两包围盒不相交。
func disjoint(ops []Op) bool {
	boxes := make([]image.Rectangle, len(ops))
	for i := range ops {
		b, ok := opBounds(ops[i])
		if !ok {
			return false
		}
		boxes[i] = b
	}
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if !boxes[i].Intersect(boxes[j]).Empty() {
				return false
			}
		}
	}
	return true
}

// opBounds 给一个算子的落笔范围; 会改栈的算子(裁剪/遮罩/变换/图层/自定义/嵌套并行)一律返回未知。
func opBounds(o Op) (image.Rectangle, bool) {
	switch o.Kind {
	case KindFill, KindRound, KindRoundBinary, KindStroke, KindSkew, KindSprite, KindTint,
		KindBlur, KindDim, KindText, KindTextBox, KindParagraph, KindHighlight:
		return o.Rect.Bounds(), true
	case KindShape, KindStrokeShape, KindShadow, KindGlow, KindFrost:
		if o.Shape == nil {
			return image.Rectangle{}, false
		}
		return o.Shape.Bounds(), true
	}
	return image.Rectangle{}, false
}
