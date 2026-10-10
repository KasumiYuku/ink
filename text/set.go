package text

import (
	"image"
	"os"
	"sort"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Metrics 是一族的行度量, 单位像素。
type Metrics struct{ Ascent, Descent, LineHeight float64 }

type Align uint8

const (
	Left Align = iota
	Center
	Right
)

type Mode uint8

const (
	ModeBitmap Mode = iota
	ModeBinary
)

type Options struct {
	CacheBytes int
}

const defaultCacheBytes = 8 << 20

type faceKey struct {
	name string
	size int
}

// faceState 是一个 (字体, 字号) 的底层 face; 字形光栅化必须串行, 故自带锁。
type faceState struct {
	id      uint32
	mu      sync.Mutex
	face    font.Face
	metrics Metrics
}

// Set 持有字体集合与字形缓存; 可并发使用。
type Set struct {
	mu     sync.Mutex
	fonts  map[string]*opentype.Font
	faces  map[faceKey]*faceState
	nextID uint32
	cache  *maskCache
	stats  struct{ rasterized int }

	kmu   sync.Mutex
	kerns map[kernKey]fixed.Int26_6
}

func NewSet(opts Options) *Set {
	limit := opts.CacheBytes
	if limit <= 0 {
		limit = defaultCacheBytes
	}
	return &Set{
		fonts: map[string]*opentype.Font{},
		faces: map[faceKey]*faceState{},
		cache: newMaskCache(limit),
		kerns: map[kernKey]fixed.Int26_6{},
	}
}

func (s *Set) Load(name string, ttf []byte) error {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.fonts[name] = f
	for k := range s.faces {
		if k.name == name {
			delete(s.faces, k)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Set) LoadFile(name, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return s.Load(name, b)
}

func (s *Set) Names() []string {
	s.mu.Lock()
	names := make([]string, 0, len(s.fonts))
	for n := range s.fonts {
		names = append(names, n)
	}
	s.mu.Unlock()
	sort.Strings(names)
	return names
}

type Chain struct {
	set   *Set
	names []string
}

func (s *Set) Chain(names ...string) *Chain {
	return &Chain{set: s, names: append([]string(nil), names...)}
}

func (c *Chain) Face(size float64) Face {
	sz := int(size + 0.5)
	if sz < 1 {
		sz = 1
	}
	faces := make([]*faceState, 0, len(c.names))
	for _, n := range c.names {
		if fs := c.set.faceState(n, sz); fs != nil {
			faces = append(faces, fs)
		}
	}
	return &glyphFace{set: c.set, size: float64(sz), faces: faces}
}

func (s *Set) faceState(name string, size int) *faceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := faceKey{name: name, size: size}
	if fs, ok := s.faces[k]; ok {
		return fs
	}
	f := s.fonts[name]
	if f == nil {
		return nil
	}
	of, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	s.nextID++
	fs := &faceState{id: s.nextID, face: of}
	fs.metrics = toMetrics(of.Metrics())
	s.faces[k] = fs
	return fs
}

func toMetrics(m font.Metrics) Metrics {
	return Metrics{
		Ascent:     float64(m.Ascent) / 64,
		Descent:    float64(m.Descent) / 64,
		LineHeight: float64(m.Height) / 64,
	}
}

// mask 是覆盖率掩码(直线 0..255, 非预乘): present=false 表示该 face 无此字形。
type mask struct {
	W, H      int
	Left, Top int
	Adv       fixed.Int26_6
	Pix       []uint8
	present   bool
}

func (m *mask) size() int { return len(m.Pix) + 64 }

const binaryThreshold = 128

func rasterize(fs *faceState, r rune, bin bool, phase uint8) *mask {
	fs.mu.Lock()
	dr, src, maskp, adv, ok := fs.face.Glyph(fixed.Point26_6{X: fixed.Int26_6(phase)}, r)
	if !ok {
		fs.mu.Unlock()
		return &mask{}
	}
	w, h := dr.Dx(), dr.Dy()
	pix := make([]uint8, w*h)
	copyCoverage(pix, w, h, src, maskp)
	fs.mu.Unlock()
	if bin {
		for i, v := range pix {
			if v >= binaryThreshold {
				pix[i] = 255
			} else {
				pix[i] = 0
			}
		}
	}
	return &mask{W: w, H: h, Left: dr.Min.X, Top: dr.Min.Y, Adv: adv, Pix: pix, present: true}
}

func copyCoverage(dst []uint8, w, h int, src image.Image, maskp image.Point) {
	if w == 0 || h == 0 {
		return
	}
	if a, ok := src.(*image.Alpha); ok {
		b := a.Bounds()
		for y := 0; y < h; y++ {
			sy := maskp.Y + y - b.Min.Y
			if sy < 0 || sy >= b.Dy() {
				continue
			}
			row := sy * a.Stride
			sx0 := maskp.X - b.Min.X
			for x := 0; x < w; x++ {
				sx := sx0 + x
				if sx < 0 || sx >= b.Dx() {
					continue
				}
				dst[y*w+x] = a.Pix[row+sx]
			}
		}
		return
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			_, _, _, a := src.At(maskp.X+x, maskp.Y+y).RGBA()
			dst[y*w+x] = uint8(a >> 8)
		}
	}
}

// glyph 取 r 在该族某个亚像素相位上的掩码; 命中的 miss 也会缓存, 避免重复查询。
func (s *Set) glyph(fs *faceState, r rune, bin bool, phase uint8) *mask {
	key := maskKey{id: fs.id, r: r, bin: bin, phase: phase}
	s.mu.Lock()
	if e := s.cache.get(key); e != nil {
		m := e.m
		s.mu.Unlock()
		return m
	}
	m := rasterize(fs, r, bin, phase)
	s.stats.rasterized++
	s.cache.put(key, m)
	s.mu.Unlock()
	return m
}

type maskKey struct {
	id    uint32
	r     rune
	bin   bool
	phase uint8
}

type kernKey struct {
	id     uint32
	r0, r1 rune
}

// kernCacheMax 是字距对的缓存上限; 成对数量由实际用到的字符决定, 到顶就不再记(只是变慢, 不会错)。
const kernCacheMax = 4096

// kern 取一对字形之间的字距调整; 字距只问链首那一族 —— 它是排版基准, 后备字体只补字形。
func (s *Set) kern(fs *faceState, r0, r1 rune) fixed.Int26_6 {
	key := kernKey{fs.id, r0, r1}
	s.kmu.Lock()
	v, cached := s.kerns[key]
	s.kmu.Unlock()
	if cached {
		return v
	}
	fs.mu.Lock()
	v = fs.face.Kern(r0, r1)
	fs.mu.Unlock()
	s.kmu.Lock()
	if len(s.kerns) < kernCacheMax {
		s.kerns[key] = v
	}
	s.kmu.Unlock()
	return v
}

type cacheEntry struct {
	key        maskKey
	m          *mask
	prev, next *cacheEntry
}

// maskCache 是按字节额度淘汰的 LRU; 所有访问都在 Set.mu 下, 掩码本身不可变。
type maskCache struct {
	m            map[maskKey]*cacheEntry
	head, tail   *cacheEntry
	bytes, limit int
}

func newMaskCache(limit int) *maskCache {
	return &maskCache{m: map[maskKey]*cacheEntry{}, limit: limit}
}

func (c *maskCache) get(key maskKey) *cacheEntry {
	e := c.m[key]
	if e == nil {
		return nil
	}
	if c.head != e {
		if e.prev != nil {
			e.prev.next = e.next
		}
		if e.next != nil {
			e.next.prev = e.prev
		}
		if c.tail == e {
			c.tail = e.prev
		}
		e.prev = nil
		e.next = c.head
		if c.head != nil {
			c.head.prev = e
		}
		c.head = e
	}
	return e
}

func (c *maskCache) put(key maskKey, m *mask) {
	sz := m.size()
	e := &cacheEntry{key: key, m: m}
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
	c.m[key] = e
	c.bytes += sz
	for c.bytes > c.limit && c.tail != nil {
		t := c.tail
		c.tail = t.prev
		if c.tail != nil {
			c.tail.next = nil
		} else {
			c.head = nil
		}
		c.bytes -= t.m.size()
		delete(c.m, t.key)
	}
}

func (c *maskCache) clear() {
	c.m = map[maskKey]*cacheEntry{}
	c.head, c.tail = nil, nil
	c.bytes = 0
}

// Close 丢掉字体与字形缓存, 内存立刻还回去。
func (s *Set) Close() {
	s.mu.Lock()
	s.fonts = map[string]*opentype.Font{}
	s.faces = map[faceKey]*faceState{}
	s.cache.clear()
	s.mu.Unlock()
	s.kmu.Lock()
	s.kerns = map[kernKey]fixed.Int26_6{}
	s.kmu.Unlock()
}
