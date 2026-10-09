package raster

import "sync"

// Scratch 是行缓冲的复用池: 多段外形的区间、可分离缩放的行缓存都从这里借, 免得每行/每块新建。
// 它由引擎持有并交给每个 Brush —— 池里放的是指针, 装进接口不产生装箱分配。
type Scratch struct {
	pool  sync.Pool
	fpool sync.Pool
}

type spanBuf struct{ spans []Span }

type floatBuf struct{ v []float32 }

func NewScratch() *Scratch {
	return &Scratch{
		pool:  sync.Pool{New: func() any { return &spanBuf{spans: make([]Span, 0, 16)} }},
		fpool: sync.Pool{New: func() any { return &floatBuf{v: make([]float32, 0, 4096)} }},
	}
}

func (s *Scratch) get() *spanBuf {
	if s == nil {
		return &spanBuf{spans: make([]Span, 0, 8)}
	}
	return s.pool.Get().(*spanBuf)
}

func (s *Scratch) put(b *spanBuf) {
	if s == nil || b == nil {
		return
	}
	b.spans = b.spans[:0]
	s.pool.Put(b)
}

// floats 借一块至少 n 长的 float32 缓冲(缩放的行缓存用)。
func (s *Scratch) floats(n int) *floatBuf {
	if s == nil {
		return &floatBuf{v: make([]float32, n)}
	}
	b := s.fpool.Get().(*floatBuf)
	if cap(b.v) < n {
		b.v = make([]float32, n)
	}
	return b
}

func (s *Scratch) putFloats(b *floatBuf) {
	if s == nil || b == nil {
		return
	}
	b.v = b.v[:0]
	s.fpool.Put(b)
}
