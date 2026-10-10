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

func imageBytes(w, h int) int64 { return int64(w) * int64(h) * 4 }

// Pool 是画布池: 按尺寸分桶复用, 额度按**字节**共享, 取不到就新建(绝不阻塞)。
// 额度满时优先淘汰**别的尺寸**的块 —— 池于是跟着当下的负载走: 谁在被渲染, 谁的块就留在池里。
type Pool struct {
	mu     sync.Mutex
	bucket map[[2]int][]*image.RGBA
	ledger *Ledger
}

func NewPool(canvasBytes int64) *Pool {
	return &Pool{bucket: map[[2]int][]*image.RGBA{}, ledger: NewLedger(canvasBytes)}
}

// Get 取一块 w×h 的画布; 复用到的块内容不定, 调用方自己铺底。
func (p *Pool) Get(w, h int) *image.RGBA {
	key := [2]int{w, h}
	p.mu.Lock()
	if list := p.bucket[key]; len(list) > 0 {
		last := len(list) - 1
		img := list[last]
		list[last] = nil
		p.bucket[key] = list[:last]
		p.mu.Unlock()
		p.ledger.Give(imageBytes(w, h))
		return img
	}
	p.mu.Unlock()
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// Put 归还一块画布; 装不下时先淘汰别的尺寸, 还是不装就丢掉。
func (p *Pool) Put(img *image.RGBA) {
	if img == nil || img.Rect.Dx() <= 0 || img.Rect.Dy() <= 0 {
		return
	}
	key := [2]int{img.Rect.Dx(), img.Rect.Dy()}
	n := imageBytes(key[0], key[1])
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ledger.Max() > 0 && n > p.ledger.Max() {
		return
	}
	for !p.ledger.Take(n) {
		if !p.evictOther(key) {
			return
		}
	}
	p.bucket[key] = append(p.bucket[key], img)
}

// evictOther 从别的尺寸桶里淘汰一块(挑尺寸最大的那桶)。
func (p *Pool) evictOther(keep [2]int) bool {
	victim, found := [2]int{}, false
	var victimBytes int64
	for k, list := range p.bucket {
		if k == keep || len(list) == 0 {
			continue
		}
		if b := imageBytes(k[0], k[1]); !found || b > victimBytes {
			victim, victimBytes, found = k, b, true
		}
	}
	if !found {
		return false
	}
	list := p.bucket[victim]
	last := len(list) - 1
	list[last] = nil
	p.bucket[victim] = list[:last]
	p.ledger.Give(victimBytes)
	return true
}

// Trim 丢掉全部空闲块, 把额度还回去(空闲回收用)。
func (p *Pool) Trim() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, list := range p.bucket {
		if len(list) == 0 {
			delete(p.bucket, k)
			continue
		}
		p.ledger.Give(imageBytes(k[0], k[1]) * int64(len(list)))
		for i := range list {
			list[i] = nil
		}
		delete(p.bucket, k)
	}
}

func (p *Pool) Bytes() (used, max int64) { return p.ledger.Used(), p.ledger.Max() }

// Gate 是并发闸门: 同时画几张由它说了算, 排队发生在调用方的队列里。
type Gate struct {
	sem      chan struct{}
	inFlight atomic.Int64
	peak     atomic.Int64
	// queued 记"拿到名额时闸门正好是满的"次数, 也就是真排过队的那些。
	queued atomic.Int64
}

func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{sem: make(chan struct{}, n)}
}

// Acquire 拿到一个名额, 返回的释放函数只能调用一次; 满员时先记一笔排队再等。
func (g *Gate) Acquire() func() {
	select {
	case g.sem <- struct{}{}:
	default:
		g.queued.Add(1)
		g.sem <- struct{}{}
	}
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

// Queued 是排过队的次数。
func (g *Gate) Queued() int64 { return g.queued.Load() }

func (g *Gate) Cap() int { return cap(g.sem) }
