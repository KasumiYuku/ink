// Package budget 管住两件事: 允许常驻多少字节, 以及同时画几张。
package budget

import (
	"image"
	"sync"
	"sync/atomic"
)

// Split 把总额度切成画布池与解码图两份: 画布那份额按"同时在用的一两种尺寸"算, 其余全给解码图。
func Split(total, oneCanvas int64, blocks int64) (canvas, decode int64) {
	if blocks < 1 {
		blocks = 1
	}
	canvas = oneCanvas * blocks
	if canvas > total {
		canvas = total
	}
	return canvas, total - canvas
}

// Ledger 是一份字节账: Take 失败表示该放弃这次缓存, 而不是超支。
type Ledger struct {
	used atomic.Int64
	max  atomic.Int64
}

func NewLedger(maxBytes int64) *Ledger {
	l := &Ledger{}
	l.max.Store(maxBytes)
	return l
}

func (l *Ledger) Take(n int64) bool {
	for {
		cur := l.used.Load()
		next := cur + n
		if next > l.max.Load() {
			return false
		}
		if l.used.CompareAndSwap(cur, next) {
			return true
		}
	}
}

func (l *Ledger) Give(n int64) {
	if n > 0 {
		l.used.Add(-n)
	}
}

func (l *Ledger) Used() int64 { return l.used.Load() }
func (l *Ledger) Max() int64  { return l.max.Load() }

// resize 是额度变更(面板热更): 只改上限, 已经占用的等归还时自然收敛。
func (l *Ledger) resize(maxBytes int64) { l.max.Store(maxBytes) }

func imageBytes(w, h int) int64 { return int64(w) * int64(h) * 4 }

// Pool 是画布池: 按尺寸分桶复用, 只用不改额度的一份账记字节。
type Pool struct {
	mu     sync.Mutex
	bucket map[[2]int][]*image.RGBA
	ledger *Ledger
}

func NewPool(canvasBytes int64) *Pool {
	return &Pool{bucket: map[[2]int][]*image.RGBA{}, ledger: NewLedger(canvasBytes)}
}

func (p *Pool) Get(w, h int) *image.RGBA {
	key := [2]int{w, h}
	p.mu.Lock()
	if list := p.bucket[key]; len(list) > 0 {
		img := list[len(list)-1]
		p.bucket[key] = list[:len(list)-1]
		p.mu.Unlock()
		for i := range img.Pix {
			img.Pix[i] = 0
		}
		return img
	}
	p.mu.Unlock()
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

func (p *Pool) Put(img *image.RGBA) {
	if img == nil {
		return
	}
	key := [2]int{img.Rect.Dx(), img.Rect.Dy()}
	n := imageBytes(key[0], key[1])
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bucket[key] == nil {
		if !p.ledger.Take(n) {
			return
		}
	}
	p.bucket[key] = append(p.bucket[key], img)
}

func (p *Pool) Bytes() (used, max int64) { return p.ledger.Used(), p.ledger.Max() }

// Gate 是并发闸门: 同时画几张由它说了算, 排队发生在调用方的队列里。
type Gate struct {
	sem      chan struct{}
	inFlight atomic.Int64
	peak     atomic.Int64
}

func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{sem: make(chan struct{}, n)}
}

// Acquire 拿到一个名额, 返回的释放函数只能调用一次。
func (g *Gate) Acquire() func() {
	g.sem <- struct{}{}
	cur := g.inFlight.Add(1)
	for {
		peak := g.peak.Load()
		if cur <= peak || g.peak.CompareAndSwap(peak, cur) {
			break
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			g.inFlight.Add(-1)
			<-g.sem
		})
	}
}

func (g *Gate) Stats() (inFlight, peak int64) { return g.inFlight.Load(), g.peak.Load() }

func (g *Gate) Cap() int { return cap(g.sem) }
