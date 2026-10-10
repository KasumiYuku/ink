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

const defaultCanvasMB = 64

type Options struct {
	Assets        theme.Options
	Encode        encode.Encoder
	Workers       int
	CanvasMB      int
	Version       string
	ContentDigest string
	OnWarn        func(error)
	// Trace 打开算子级统计(次数与耗时); 关着时每个算子只多一次判断。
	Trace bool
}

type Stats struct {
	Renders, Failures int64
	InFlight, Peak    int64
	Workers           int
	CanvasBytes       int64
	CanvasMax         int64
	DecodeBytes       int64
	DecodeMax         int64
	Masks             int
	// Ops 只有开着 Trace 时才有内容。
	Ops []OpStat
}

// OpStat 是一种算子的次数与总耗时。
type OpStat struct {
	Kind  string
	Count int64
	Nanos int64
}

// Engine 是这台机器上的一份绘图账: 缺省、缓存、并发、指纹都挂在它身上。
type Engine struct {
	assets  *theme.Assets
	enc     encode.Encoder
	pool    *budget.Pool
	gate    *budget.Gate
	scratch *raster.Scratch
	masks   *raster.MaskCache
	par     raster.Par
	epoch   string
	onWarn  func(error)

	renders, failures atomic.Int64
	closed            atomic.Bool

	trace   bool
	opCount [kindCount]atomic.Int64
	opNanos [kindCount]atomic.Int64
}

func New(opts Options) (*Engine, error) {
	assets, err := theme.New(opts.Assets)
	if err != nil {
		return nil, err
	}
	enc := opts.Encode
	if enc == nil {
		enc = encode.JPEG(85)
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = max(1, runtime.NumCPU()-2)
	}
	canvasMB := opts.CanvasMB
	if canvasMB <= 0 {
		canvasMB = defaultCanvasMB
	}
	e := &Engine{
		assets:  assets,
		enc:     enc,
		pool:    budget.NewPool(int64(canvasMB) << 20),
		gate:    budget.NewGate(workers),
		scratch: raster.NewScratch(),
		masks:   raster.NewMaskCache(32),
		par:     raster.Par{Workers: workers, MinRows: 32},
		onWarn:  opts.OnWarn,
		trace:   opts.Trace,
	}
	e.epoch = e.computeEpoch(opts.Version, opts.ContentDigest)
	return e, nil
}

// computeEpoch 是"画出来的字节由什么决定"的那份摘要: 代码版本 + 素材内容 + 编码器身份。
func (e *Engine) computeEpoch(version, digest string) string {
	h := sha256.New()
	io.WriteString(h, "ink=v1\nversion=")
	io.WriteString(h, version)
	io.WriteString(h, "\ncontent=")
	io.WriteString(h, digest)
	io.WriteString(h, "\nencoder=")
	io.WriteString(h, e.enc.ID())
	return hex.EncodeToString(h.Sum(nil))
}

// Epoch 是本实例的指纹前缀; 换版本/换素材/换编码器它就会变, 旧键一律不复用。
func (e *Engine) Epoch() string { return e.epoch }

// Fingerprint 把一次请求压成定长键: epoch + 图种 + spec 的 JSON。
//
// spec 是全量输入, 所以"同一把键"必然画出同一份字节; 反过来键一变就是一次新画, 不会发出旧图。
// 编码失败(理论上不会)返回空串, 调用方据此跳过缓存直接画。
func (e *Engine) Fingerprint(kind string, v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.New()
	io.WriteString(h, e.epoch)
	io.WriteString(h, "|")
	io.WriteString(h, kind)
	io.WriteString(h, "|")
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

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
	return nil
}

// Render 画完并编码; 画布走池子, 用完就还。
func (e *Engine) Render(s *Sheet) ([]byte, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	release := e.gate.Acquire()
	defer release()
	img := e.pool.Get(s.W, s.H)
	clear(img.Pix)
	defer e.pool.Put(img)

	c := &Canvas{eng: e, dst: img, xform: raster.Identity()}
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
	out, err := e.enc.Encode(img)
	if err != nil {
		e.fail(err)
		return nil, err
	}
	e.renders.Add(1)
	return out, nil
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

// NearestCanvasMB 估一块画布要多少 MB, 方便调用方按尺寸配额度。
func NearestCanvasMB(w, h int) int { return int(math.Ceil(float64(w*h*4) / (1 << 20))) }

// MB 是给配置项用的字节换算。
func MB(n int) int64 { return int64(n) << 20 }

// Size 是 w×h 的字符串, 日志里常用。
func Size(w, h int) string { return strconv.Itoa(w) + "x" + strconv.Itoa(h) }
