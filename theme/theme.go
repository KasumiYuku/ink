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
	"github.com/KasumiYuku/ink/raster"
	"github.com/KasumiYuku/ink/text"
)

const defaultDecodeBytes = 128 << 20

// Options 是素材口径的装配参数。
type Options struct {
	Dir         string
	Resolve     func(key string) string
	DecodeBytes int64
	// Par 是解码后缩放/裁切那个算子的并行策略(见 raster.Par); Workers <= 0 取默认值。
	Par         raster.Par
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

// Fit 是一张图怎么塞进给定尺寸。
type Fit uint8

const (
	// FitInside 等比缩到不超过目标尺寸(结果可能比目标小)。
	FitInside Fit = iota
	// FitCover 等比放大到盖住目标尺寸, 多出来的居中裁掉(结果正好是目标尺寸)。
	FitCover
)

type imgKey struct {
	key  string
	w, h int
	fit  Fit
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

// Assets 是素材口径: 按字节额度 LRU, 同一个 (key, 尺寸, fit) 并发只处理一次。
type Assets struct {
	dir     string
	resolve func(string) string
	ledger  *budget.Ledger
	fonts   *text.Set

	mu    sync.Mutex
	cache map[imgKey]*entry
	calls map[imgKey]*call
	seq   uint64

	// par 是解码后缩放/裁切那个算子的并行策略(接得上引擎就照引擎那份来)。
	par raster.Par

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
	par := opts.Par
	if par.Workers <= 0 {
		par = raster.Par{Workers: 4, MinRows: 32}
	}
	a := &Assets{
		dir:     opts.Dir,
		resolve: resolve,
		ledger:  budget.NewLedger(bytes),
		fonts:   text.NewSet(opts.FontOptions),
		cache:   map[imgKey]*entry{},
		calls:   map[imgKey]*call{},
		par:     par,
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

// Image 拿一张解码好的原尺寸画布; 命中缓存时零 IO 零解码。返回的是缓存里那一张, 只能读不能改。
func (a *Assets) Image(key string) (*image.RGBA, error) {
	return a.imageAt(imgKey{key: key}, key)
}

// ImageAt 拿一张按目标尺寸处理好的画布: fit 决定怎么塞, 结果按 (key, 尺寸, fit) 缓存。
// w/h 有任意一个 <= 0 时按原尺寸。返回的是缓存里那一张, 只能读不能改。
func (a *Assets) ImageAt(key string, w, h int, fit Fit) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return a.Image(key)
	}
	return a.imageAt(imgKey{key: key, w: w, h: h, fit: fit}, key)
}

func (a *Assets) imageAt(k imgKey, name string) (*image.RGBA, error) {
	a.mu.Lock()
	if e, ok := a.cache[k]; ok {
		a.seq++
		e.used = a.seq
		a.mu.Unlock()
		a.hit.Add(1)
		return e.img, nil
	}
	c, ok := a.calls[k]
	if !ok {
		c = &call{}
		a.calls[k] = c
	}
	a.mu.Unlock()
	a.miss.Add(1)

	c.once.Do(func() {
		src, err := a.decode(name)
		if err != nil {
			c.err = err
			return
		}
		c.img = k.fitTo(src, a.par)
		a.decoded.Add(1)
	})

	a.mu.Lock()
	delete(a.calls, k)
	if c.err == nil && c.img != nil {
		if !a.admitLocked(int64(len(c.img.Pix))) {
			a.tooBig.Add(1)
		} else {
			a.seq++
			a.cache[k] = &entry{img: c.img, used: a.seq}
		}
	}
	a.mu.Unlock()
	return c.img, c.err
}

// admitLocked 按最久没用淘汰到装得下 n 字节; 腾不出来(或单张就超额度)返回 false。
func (a *Assets) admitLocked(n int64) bool {
	if a.ledger.Max() > 0 && n > a.ledger.Max() {
		return false
	}
	for a.ledger.Used()+n > a.ledger.Max() && a.evictLocked() {
	}
	return a.ledger.Take(n)
}

func (a *Assets) decode(key string) (*image.RGBA, error) {
	raw, err := os.ReadFile(a.resolve(key))
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ink/theme: 解码 %s: %w", key, err)
	}
	return rgba.ToStraight(src), nil
}

// fitTo 按 key 里的尺寸与 fit 处理原图: 盖满那条走"只算留下那块"的路, 其余等比缩进。
func (k imgKey) fitTo(src *image.RGBA, par raster.Par) *image.RGBA {
	if k.w <= 0 || k.h <= 0 {
		return src
	}
	if k.fit == FitCover {
		return raster.ResizeCover(src, k.w, k.h, par)
	}
	w, h := fitInside(src.Rect.Dx(), src.Rect.Dy(), k.w, k.h)
	return raster.Resize(src, w, h, par)
}

// fitInside 把 srcW×srcH 等比缩到不超过 maxW×maxH。
func fitInside(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return maxW, maxH
	}
	scale := float64(maxW) / float64(srcW)
	if s := float64(maxH) / float64(srcH); s < scale {
		scale = s
	}
	w := int(float64(srcW)*scale + 0.5)
	h := int(float64(srcH)*scale + 0.5)
	return max(w, 1), max(h, 1)
}

// Drop 丢掉这个 key 的全部尺寸缓存(素材热更时用), 下次用到会重新处理。
func (a *Assets) Drop(key string) {
	a.mu.Lock()
	for k, e := range a.cache {
		if k.key == key {
			a.ledger.Give(int64(len(e.img.Pix)))
			delete(a.cache, k)
		}
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

// evictLocked 丢掉最久没用的一条; 没有可丢的返回 false。
func (a *Assets) evictLocked() bool {
	var oldest imgKey
	var minUsed uint64 = ^uint64(0)
	found := false
	for k, e := range a.cache {
		if e.used < minUsed {
			minUsed, oldest, found = e.used, k, true
		}
	}
	if !found {
		return false
	}
	a.ledger.Give(int64(len(a.cache[oldest].img.Pix)))
	delete(a.cache, oldest)
	return true
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
