// Package theme 把素材与图幅解耦: 调用方只认 key, key 落到哪个文件由 Resolve 决定。
package theme

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/KasumiYuku/ink/budget"
	"github.com/KasumiYuku/ink/internal/rgba"
	"github.com/KasumiYuku/ink/text"
)

const defaultDecodeBytes = 128 << 20

type Options struct {
	Dir         string
	Resolve     func(key string) string
	DecodeBytes int64
	Fonts       map[string]string
	FontOptions text.Options
}

type Stats struct {
	Hit     int64
	Miss    int64
	Decoded int64
	TooBig  int64
	Bytes   int64
	Max     int64
}

type entry struct {
	img  *image.RGBA
	used uint64
}

type call struct {
	once sync.Once
	img  *image.RGBA
	err  error
}

// Assets 是素材口径: 解码图按字节额度 LRU, 同一个 key 并发只解一次。
type Assets struct {
	dir     string
	resolve func(string) string
	ledger  *budget.Ledger
	fonts   *text.Set

	mu    sync.Mutex
	cache map[string]*entry
	calls map[string]*call
	seq   uint64

	hit, miss, decoded, tooBig atomic.Int64
}

func New(opts Options) (*Assets, error) {
	bytes := opts.DecodeBytes
	if bytes <= 0 {
		bytes = defaultDecodeBytes
	}
	resolve := opts.Resolve
	if resolve == nil {
		dir := opts.Dir
		resolve = func(key string) string { return filepath.Join(dir, key) }
	}
	a := &Assets{
		dir:     opts.Dir,
		resolve: resolve,
		ledger:  budget.NewLedger(bytes),
		fonts:   text.NewSet(opts.FontOptions),
		cache:   map[string]*entry{},
		calls:   map[string]*call{},
	}
	for name, path := range opts.Fonts {
		if !filepath.IsAbs(path) {
			path = filepath.Join(opts.Dir, path)
		}
		if err := a.fonts.LoadFile(name, path); err != nil {
			return nil, fmt.Errorf("ink/theme: 载入字体 %s: %w", name, err)
		}
	}
	return a, nil
}

// Path 是 key 对应的文件路径; Has 只问"文件在不在"。
func (a *Assets) Path(key string) string { return a.resolve(key) }

func (a *Assets) Has(key string) bool {
	st, err := os.Stat(a.resolve(key))
	return err == nil && !st.IsDir()
}

// Missing 返回这批 key 里找不到文件的那些(启动自检用, 不给渲染路径拖后腿)。
func (a *Assets) Missing(keys []string) []string {
	var out []string
	for _, key := range keys {
		if !a.Has(key) {
			out = append(out, key)
		}
	}
	return out
}

func (a *Assets) Read(key string) ([]byte, error) { return os.ReadFile(a.resolve(key)) }

// Image 拿一张解码好的画布; 命中缓存时零 IO 零解码。返回的是缓存里那一张, 只能读不能改。
func (a *Assets) Image(key string) (*image.RGBA, error) {
	a.mu.Lock()
	if e, ok := a.cache[key]; ok {
		a.seq++
		e.used = a.seq
		a.mu.Unlock()
		a.hit.Add(1)
		return e.img, nil
	}
	c, ok := a.calls[key]
	if !ok {
		c = &call{}
		a.calls[key] = c
	}
	a.mu.Unlock()
	a.miss.Add(1)

	c.once.Do(func() {
		raw, err := os.ReadFile(a.resolve(key))
		if err != nil {
			c.err = err
			return
		}
		src, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			c.err = fmt.Errorf("ink/theme: 解码 %s: %w", key, err)
			return
		}
		c.img = rgba.ToStraight(src)
		a.decoded.Add(1)
	})

	a.mu.Lock()
	delete(a.calls, key)
	if c.err == nil && c.img != nil {
		n := int64(len(c.img.Pix))
		if a.ledger.Take(n) {
			a.seq++
			a.cache[key] = &entry{img: c.img, used: a.seq}
			a.evictLocked()
		} else {
			a.tooBig.Add(1)
		}
	}
	a.mu.Unlock()
	return c.img, c.err
}

// Drop 丢掉一条缓存(素材热更时用), 下次用到会重新解码。
func (a *Assets) Drop(key string) {
	a.mu.Lock()
	if e, ok := a.cache[key]; ok {
		a.ledger.Give(int64(len(e.img.Pix)))
		delete(a.cache, key)
	}
	a.mu.Unlock()
}

func (a *Assets) DropAll() {
	a.mu.Lock()
	for key, e := range a.cache {
		a.ledger.Give(int64(len(e.img.Pix)))
		delete(a.cache, key)
	}
	a.mu.Unlock()
}

func (a *Assets) evictLocked() {
	for a.ledger.Used() > a.ledger.Max() && len(a.cache) > 0 {
		oldest := ""
		var minUsed uint64 = ^uint64(0)
		for key, e := range a.cache {
			if e.used < minUsed {
				minUsed, oldest = e.used, key
			}
		}
		if oldest == "" {
			return
		}
		a.ledger.Give(int64(len(a.cache[oldest].img.Pix)))
		delete(a.cache, oldest)
	}
}

func (a *Assets) Fonts() *text.Set { return a.fonts }

func (a *Assets) Stats() Stats {
	return Stats{
		Hit: a.hit.Load(), Miss: a.miss.Load(), Decoded: a.decoded.Load(), TooBig: a.tooBig.Load(),
		Bytes: a.ledger.Used(), Max: a.ledger.Max(),
	}
}

func (a *Assets) Close() error {
	a.DropAll()
	a.fonts.Close()
	return nil
}
