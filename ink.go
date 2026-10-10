// Package ink 是一个面向"出图"的 2D 绘图引擎
package ink

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"math"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/KasumiYuku/ink/budget"
	"github.com/KasumiYuku/ink/encode"
	"github.com/KasumiYuku/ink/raster"
	"github.com/KasumiYuku/ink/theme"
)

var ErrClosed = errors.New("ink: 引擎已关闭")

const (
	defaultCanvasBytes = 64 << 20
	// idleTick 是空闲巡检的节拍。
	idleTick = time.Minute
)

type Options struct {
	Assets theme.Options
	Encode encode.Encoder
	// Fallback 是主编码器出不来的那一档(通常是 JPEG); 为空表示没有退路。
	Fallback encode.Encoder
	// Workers 同时画几张; <=0 取"核数留 2 个"。它同时定闸门与算子分块。
	Workers int
	// CanvasBytes 画布池的字节额度; <=0 取默认值(见 defaultCanvasBytes)。
	CanvasBytes int64
	Version     string
	// ContentDigest 是素材内容的摘要, 进指纹(见 theme.Digest)。
	ContentDigest string
	OnWarn        func(error)
	// IdleAfter 多久没人出图就把两条缓存还回去; <=0 表示不回收。
	IdleAfter time.Duration
	// Trace 打开算子级统计(次数与耗时); 关着时每个算子只多一次判断。
	Trace bool
}

type Stats struct {
	Renders, Failures int64
	// Queued 是排过队的次数: 拿到名额时闸门正好是满的。
	Queued         int64
	InFlight, Peak int64
	Workers        int
	// Coalesced 是"同一把 key 的并发被合成一次"的次数。
	Coalesced   int64
	CanvasBytes int64
	CanvasMax   int64
	DecodeBytes int64
	DecodeMax   int64
	Masks       int
	// Ops 只有开着 Trace 时才有内容。
	Ops []OpStat
}

// OpStat 是一种算子的次数与总耗时。
type OpStat struct {
	Kind  string
	Count int64
	Nanos int64
}

// Job 是一次出图: 尺寸、收尾缩放、编码预算与画法都在里面(见 Engine.RenderJob)。
type Job struct {
	// Key 非空时, 同一把 key 的并发调用只画一次, 结果共用。
	Key string
	// W / H 是画布尺寸。
	W, H int
	// Scale 是收尾缩放: 编码的是缩过的那一版; <=0 或 1 表示不缩。
	Scale float64
	// Budget 是编码的字节预算; <=0 表示不限(见 encode.Budgeted)。
	Budget int64
	// Draw 拿到一块空画布; 它是独占的, 画完由引擎收回。
	Draw func(c *Canvas)
}

// Engine 是这台机器上的一份绘图账: 缺省、缓存、并发、指纹都挂在它身上。
type Engine struct {
	assets *theme.Assets
	enc    encode.Encoder
	// fallback 是主编码器出不来的那一档; 为空表示没有退路。
	fallback encode.Encoder
	pool     *budget.Pool
	gate     *budget.Gate
	scratch  *raster.Scratch
	masks    *raster.MaskCache
	par      raster.Par
	key      Key
	onWarn   func(error)

	// flight 是"同一把 key 的并发只画一次"的登记处(见 RenderJob)。
	flight *flight
	// idleAfter 是空闲回收的门槛, <=0 关掉; lastUse 是最后一次出图的时间。
	idleAfter time.Duration
	lastUse   atomic.Int64
	reaping   atomic.Bool

	renders, failures atomic.Int64
	closed            atomic.Bool

	trace   bool
	opCount [kindCount]atomic.Int64
	opNanos [kindCount]atomic.Int64
}

// DefaultWorkers 是没给 Workers 时引擎用的那个数: 核数留 2 个给主循环。
func DefaultWorkers() int { return max(1, runtime.NumCPU()-2) }

func New(opts Options) (*Engine, error) {
	enc := opts.Encode
	if enc == nil {
		enc = encode.JPEG(85)
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = DefaultWorkers()
	}
	// 素材口径的缩放也照这台引擎的并行策略来: 机器快慢只有一个出处(见 Par)。
	opts.Assets.Par = raster.Par{Workers: workers, MinRows: 32}
	assets, err := theme.New(opts.Assets)
	if err != nil {
		return nil, err
	}
	canvasBytes := opts.CanvasBytes
	if canvasBytes <= 0 {
		canvasBytes = defaultCanvasBytes
	}
	e := &Engine{
		assets:    assets,
		enc:       enc,
		fallback:  opts.Fallback,
		pool:      budget.NewPool(canvasBytes),
		gate:      budget.NewGate(workers),
		scratch:   raster.NewScratch(),
		masks:     raster.NewMaskCache(32),
		par:       raster.Par{Workers: workers, MinRows: 32},
		flight:    newFlight(),
		idleAfter: opts.IdleAfter,
		onWarn:    opts.OnWarn,
		trace:     opts.Trace,
	}
	e.key = NewKey(opts.Version, opts.ContentDigest, enc)
	return e, nil
}

// Key 是一份出图身份: 代码版本 + 素材内容 + 编码器身份。它是**值**, 可以提前算好、可以传给别人。
type Key struct{ prefix string }

// NewKey 把"画出来的字节由什么决定"压成一份身份; 三段里任何一段变了, 旧键一律不复用。
//
// 它是值而不是引擎的方法, 因为"渲染之前就要算出同一把键"的地方(直链缓存、能力层)拿不到引擎,
// 而它们要的本来也只是这把键。
func NewKey(version, digest string, enc encode.Encoder) Key {
	h := sha256.New()
	io.WriteString(h, "ink=v1\nversion=")
	io.WriteString(h, version)
	io.WriteString(h, "\ncontent=")
	io.WriteString(h, digest)
	io.WriteString(h, "\nencoder=")
	if enc != nil {
		io.WriteString(h, enc.ID())
	}
	return Key{prefix: hex.EncodeToString(h.Sum(nil))}
}

// Fingerprint 把一次请求压成定长键: 身份 + 图种 + spec 的 JSON。
//
// spec 是全量输入, 所以"同一把键"必然画出同一份字节; 反过来键一变就是一次新画, 不会发出旧图。
// 编码失败(理论上不会)返回空串, 调用方据此跳过缓存直接画。
func (k Key) Fingerprint(kind string, v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.New()
	io.WriteString(h, k.prefix)
	io.WriteString(h, "|")
	io.WriteString(h, kind)
	io.WriteString(h, "|")
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// Short 是身份的前八位, 只给日志用。
func (k Key) Short() string {
	if len(k.prefix) > 8 {
		return k.prefix[:8]
	}
	return k.prefix
}

// Key 是本实例的身份; 换版本/换素材/换编码器它就会变, 旧键一律不复用。
func (e *Engine) Key() Key { return e.key }

// Epoch 是身份的十六进制前缀(日志与调试用)。
func (e *Engine) Epoch() string { return e.key.prefix }

// Fingerprint 见 Key.Fingerprint。
func (e *Engine) Fingerprint(kind string, v any) string { return e.key.Fingerprint(kind, v) }

func (e *Engine) Assets() *theme.Assets { return e.assets }

func (e *Engine) Encoder() encode.Encoder { return e.enc }

// Draw 只画不编码, 返回**独占**的画布(池里那份已经归还)。
func (e *Engine) Draw(s *Sheet) (*image.RGBA, error) {
	out := image.NewRGBA(image.Rect(0, 0, s.W, s.H))
	if err := e.RenderInto(out, s); err != nil {
		return nil, err
	}
	return out, nil
}

// RenderJob 画一张图并编码: 过闸门 → 池里取画布 → 画 → 按需收尾缩放 → 按预算编码。
//
// 不需要构造 Sheet 的调用方走这条; 同一把 Key 的并发调用只画一次(见 flight)。
func (e *Engine) RenderJob(j Job) ([]byte, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	if j.Key == "" {
		return e.renderJob(j)
	}
	return e.flight.do(j.Key, func() ([]byte, error) { return e.renderJob(j) })
}

func (e *Engine) renderJob(j Job) ([]byte, error) {
	release := e.gate.Acquire()
	defer release()
	// 张数记在**拿到名额之后**: 它统计的是"真画了几张"。
	e.renders.Add(1)
	e.markUse()

	img := e.pool.Get(j.W, j.H)
	clear(img.Pix)
	defer e.pool.Put(img)

	c := &Canvas{eng: e, dst: img, xform: raster.Identity()}
	defer func() {
		if r := recover(); r != nil {
			e.fail(errors.New("ink: 渲染 panic"))
			panic(r)
		}
	}()
	j.Draw(c)

	// 收尾缩放只管编码的那一版, 两块都还给池子。scale 在取整后与源相同时原样返回 src,
	// 那条路上只还一次 —— 还两次会让池子的空闲表里出现同一个指针两份。
	out := img
	if j.Scale > 0 && j.Scale != 1 {
		if scaled := e.scale(img, j.Scale); scaled != img {
			defer e.pool.Put(scaled)
			out = scaled
		}
	}
	b, err := e.encode(out, j.Budget)
	if err != nil {
		e.fail(err)
		return nil, err
	}
	return b, nil
}

// RenderInto 把整张图直接画进调用方给的画布(零拷贝, 工具与测试走这条)。
func (e *Engine) RenderInto(dst *image.RGBA, s *Sheet) error {
	if e.closed.Load() {
		return ErrClosed
	}
	release := e.gate.Acquire()
	defer release()
	c := &Canvas{eng: e, dst: dst, xform: raster.Identity()}
	defer func() {
		if r := recover(); r != nil {
			e.fail(errors.New("ink: 渲染 panic"))
			panic(r)
		}
	}()
	e.drawOps(s, c)
	return nil
}

// Render 画完并编码; 画布走池子, 用完就还。
func (e *Engine) Render(s *Sheet) ([]byte, error) {
	return e.RenderJob(Job{W: s.W, H: s.H, Draw: func(c *Canvas) { e.drawOps(s, c) }})
}

// Par 是这台引擎的并行策略(按行分块的行数与块数): 调用方要自己并行画时照它来, 机器快慢只有一个出处。
func (e *Engine) Par() raster.Par { return e.par }

func (e *Engine) drawOps(s *Sheet, c *Canvas) {
	for i := range s.ops {
		op := &s.ops[i]
		if e.trace {
			t0 := time.Now()
			op.Draw(c)
			e.spend(op.Kind, time.Since(t0))
			continue
		}
		op.Draw(c)
	}
}

// scale 把画布按比例缩一版, 返回池里新的一块; 取整后与源同尺寸时原样返回 src。
func (e *Engine) scale(src *image.RGBA, f float64) *image.RGBA {
	w := int(math.Round(float64(src.Rect.Dx()) * f))
	h := int(math.Round(float64(src.Rect.Dy()) * f))
	if w == src.Rect.Dx() && h == src.Rect.Dy() {
		return src
	}
	dst := e.pool.Get(w, h)
	raster.ResizeInto(src, dst, e.par)
	return dst
}

// encode 有预算且编码器支持按预算退档就先试它, 失败退回不限预算的那一次。
func (e *Engine) encode(img *image.RGBA, budget int64) ([]byte, error) {
	if budget > 0 {
		if b, ok := e.enc.(encode.Budgeted); ok {
			out, err := b.EncodeWithin(img, budget)
			if err == nil {
				return out, nil
			}
			// 按预算那一趟失败就换人: 同一支再试一遍只会再赔一次(见 encode.WebP 的 giveUp)。
			if e.fallback != nil {
				return e.fallback.Encode(img)
			}
		}
	}
	out, err := e.enc.Encode(img)
	if err == nil || e.fallback == nil {
		return out, err
	}
	return e.fallback.Encode(img)
}

// markUse 记下"刚刚有人出图", 并按需拉起空闲回收。
func (e *Engine) markUse() {
	if e.idleAfter <= 0 {
		return
	}
	e.lastUse.Store(time.Now().UnixNano())
	if e.reaping.CompareAndSwap(false, true) {
		go e.reapLoop()
	}
}

// reapLoop 空闲一段时间后把缓存额度还回去, 然后自己退出。
func (e *Engine) reapLoop() {
	defer e.reaping.Store(false)
	// 节拍不超过门槛: 门槛比节拍短时, 巡检不该还等满一个节拍才去看。
	tick := min(idleTick, e.idleAfter)
	for {
		time.Sleep(tick)
		if time.Since(time.Unix(0, e.lastUse.Load())) < e.idleAfter {
			continue
		}
		e.Trim()
		return
	}
}

// Trim 把两条缓存能还的额度都还回去(画布池、解码图)。
func (e *Engine) Trim() {
	e.pool.Trim()
	e.assets.DropAll()
}

func (e *Engine) fail(err error) {
	e.failures.Add(1)
	if e.onWarn != nil {
		e.onWarn(err)
	}
}

// spend 记一个算子的耗时(只在开着 Trace 时被调用)。
func (e *Engine) spend(k Kind, d time.Duration) {
	if k >= kindCount {
		return
	}
	e.opCount[k].Add(1)
	e.opNanos[k].Add(int64(d))
}

func (e *Engine) opStats() []OpStat {
	out := make([]OpStat, 0, kindCount)
	for k := Kind(0); k < kindCount; k++ {
		n := e.opCount[k].Load()
		if n == 0 {
			continue
		}
		out = append(out, OpStat{Kind: k.String(), Count: n, Nanos: e.opNanos[k].Load()})
	}
	return out
}

func (e *Engine) Stats() Stats {
	inFlight, peak := e.gate.Stats()
	used, maxBytes := e.pool.Bytes()
	dec := e.assets.Stats()
	return Stats{
		Renders: e.renders.Load(), Failures: e.failures.Load(),
		Queued: e.gate.Queued(), Coalesced: e.flight.coalesced.Load(),
		InFlight: inFlight, Peak: peak, Workers: e.gate.Cap(),
		CanvasBytes: used, CanvasMax: maxBytes,
		DecodeBytes: dec.Bytes, DecodeMax: dec.Max,
		Masks: e.masks.Len(),
		Ops:   e.opStats(),
	}
}

func (e *Engine) Close() error {
	if e.closed.Swap(true) {
		return nil
	}
	return e.assets.Close()
}

// borrow / giveBack 是引擎内部的缓冲口径: 只有引擎自己往外发。
func (e *Engine) borrow(w, h int) *image.RGBA { return e.pool.Get(w, h) }

func (e *Engine) giveBack(img *image.RGBA) {
	if img != nil {
		e.pool.Put(img)
	}
}

// MB 是给配置项用的字节换算。
func MB(n int) int64 { return int64(n) << 20 }

// Size 是 w×h 的字符串, 日志里常用。
func Size(w, h int) string { return strconv.Itoa(w) + "x" + strconv.Itoa(h) }
