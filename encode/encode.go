// Package encode 是出图编码: 只决定体积, 但"用哪个编码器"会改字节, 所以身份必须进指纹。
package encode

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrUnavailable = errors.New("ink/encode: 编码器不可用")

// Encoder 把画布编成字节; ID 描述"是什么在编", 换编码器/换参数/换二进制都必须换 ID。
type Encoder interface {
	Encode(img *image.RGBA) ([]byte, error)
	ID() string
}

// Budgeted 是"能按这一张的字节上限编"的编码器, 顺带能自报状态(供启动日志)。
//
// 上限与内容类型有关: 同样像素数, 密集小卡比大卡难压, 所以它是**每次调用**的参数, 不是编码器的属性。
type Budgeted interface {
	EncodeWithin(img *image.RGBA, budgetBytes int64) ([]byte, error)
	// Lowered 报有多少张因为超预算退到了下限档(体积没变小时先看这个数)。
	Lowered() int64
	// State 报当前后端; ok=false 表示这一支已经放弃, 出图会走别的编码器。
	State() (string, bool)
}

type jpegEncoder struct{ quality int }

// JPEG 标准库编码器, quality 夹在 45..100。
func JPEG(quality int) Encoder {
	if quality < 45 {
		quality = 45
	}
	if quality > 100 {
		quality = 100
	}
	return jpegEncoder{quality}
}

func (e jpegEncoder) Encode(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: e.quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (e jpegEncoder) ID() string { return "jpeg:q" + strconv.Itoa(e.quality) }

// WebPOptions 是 cwebp 那一档的参数。
//
// 默认值对应的是一组量过的"文字向"参数: 关掉去块滤波(-f 0), 关掉降噪(-sns 0) ——
// 同体积下小字边缘更实。Filter/Sns 传 -1 表示不传该开关, 用 cwebp 自己的默认。
type WebPOptions struct {
	Path        string
	Quality     int
	QualityMin  int
	Method      int
	Segments    int
	Filter      int
	Sns         int
	Timeout     time.Duration
	Concurrency int
	// OnError 首次失败时通知一次(之后不再试), 可为 nil。
	OnError func(error)
}

// WebP 走外部 cwebp 子进程(静态 PIE, 无外部依赖); 找不到二进制时 Encode 返回 ErrUnavailable。
func WebP(opts WebPOptions) Encoder {
	if opts.Quality <= 0 {
		opts.Quality = 82
	}
	if opts.QualityMin <= 0 || opts.QualityMin > opts.Quality {
		opts.QualityMin = opts.Quality
	}
	if opts.Method <= 0 {
		opts.Method = 4
	}
	if opts.Segments <= 0 {
		opts.Segments = 4
	}
	if opts.Filter == 0 {
		opts.Filter = 0
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	e := &webpEncoder{opts: opts, sem: make(chan struct{}, opts.Concurrency)}
	e.run = e.runCWEBP
	return e
}

type webpEncoder struct {
	opts WebPOptions
	sem  chan struct{}
	// run 抽成字段是为了让档位/体积的测试能注入假编码器 —— 单测不该依赖机器上有没有 cwebp。
	run func(img *image.RGBA, quality int) ([]byte, error)

	locate  sync.Once
	bin     string
	lowered atomic.Int64

	mu   sync.Mutex
	dead bool
	err  error
}

// Encode 按构造时的参数编(没有字节上限)。
func (e *webpEncoder) Encode(img *image.RGBA) ([]byte, error) { return e.encode(img, 0) }

// EncodeWithin 同 Encode, 但超了 budgetBytes 就退到 QualityMin 再编一次(两个档封顶, 不做二分)。
func (e *webpEncoder) EncodeWithin(img *image.RGBA, budgetBytes int64) ([]byte, error) {
	return e.encode(img, budgetBytes)
}

func (e *webpEncoder) encode(img *image.RGBA, budget int64) ([]byte, error) {
	if img == nil {
		return nil, errors.New("ink/encode: 空画布")
	}
	e.mu.Lock()
	if e.dead {
		err := e.err
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()

	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	blob, err := e.run(img, e.opts.Quality)
	if err != nil {
		e.giveUp(err)
		return nil, err
	}
	if len(blob) == 0 {
		e.giveUp(errors.New("cwebp 没有输出"))
		return nil, ErrUnavailable
	}
	if budget > 0 && int64(len(blob)) > budget {
		// 下限档反而更大(理论上不该发生)时保留主档结果 —— 预算只是上限, 不是非达到不可。
		if low, lowErr := e.run(img, e.opts.QualityMin); lowErr == nil && len(low) > 0 && len(low) < len(blob) {
			e.lowered.Add(1)
			return low, nil
		}
	}
	return blob, nil
}

// runCWEBP fork 一次 cwebp, 从 stdin 读 PPM、从 stdout 收 WebP。
func (e *webpEncoder) runCWEBP(img *image.RGBA, quality int) ([]byte, error) {
	bin := e.resolve()
	if bin == "" {
		return nil, ErrUnavailable
	}
	var out, errBuf bytes.Buffer
	out.Grow(1 << 20)
	args := []string{"-q", strconv.Itoa(quality), "-m", strconv.Itoa(e.opts.Method)}
	if e.opts.Filter >= 0 {
		args = append(args, "-f", strconv.Itoa(e.opts.Filter))
	}
	if e.opts.Sns >= 0 {
		args = append(args, "-sns", strconv.Itoa(e.opts.Sns))
	}
	args = append(args, "-segments", strconv.Itoa(e.opts.Segments), "-quiet", "-o", "-", "--", "-")
	cmd := exec.Command(bin, args...)
	cmd.Stdin = newPPMStream(img)
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			if msg := strings.TrimSpace(errBuf.String()); msg != "" {
				return nil, fmt.Errorf("cwebp: %w: %s", err, msg)
			}
			return nil, fmt.Errorf("cwebp: %w", err)
		}
	case <-time.After(e.opts.Timeout):
		_ = cmd.Process.Kill()
		return nil, errors.New("cwebp: 超时")
	}
	if out.Len() == 0 {
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

// giveUp 记下失败并从此不再试: 起不来、被拦、二进制不对路这类问题不会自己好,
// 每张图都去 fork 一次再失败只是把出图拖慢。
func (e *webpEncoder) giveUp(err error) {
	e.mu.Lock()
	first := !e.dead
	e.dead, e.err = true, err
	e.mu.Unlock()
	if first && e.opts.OnError != nil {
		e.opts.OnError(err)
	}
}

// State 报后端当前状态; ok=false 表示已经放弃。
func (e *webpEncoder) State() (string, bool) {
	e.mu.Lock()
	dead := e.dead
	e.mu.Unlock()
	if dead {
		return "", false
	}
	s := fmt.Sprintf("cwebp q%d(超预算退 %d) -m%d", e.opts.Quality, e.opts.QualityMin, e.opts.Method)
	if n := e.lowered.Load(); n > 0 {
		s += fmt.Sprintf(", 已退 %d 张", n)
	}
	return s, true
}

// Lowered 报有多少张因为超预算退到了下限档。
func (e *webpEncoder) Lowered() int64 { return e.lowered.Load() }

func (e *webpEncoder) resolve() string {
	e.locate.Do(func() { e.bin = Locate(e.opts.Path, "") })
	return e.bin
}

// ID 覆盖所有会改变输出字节的东西: cwebp 的参数与二进制本身(路径 + 大小 + 修改时间)。
func (e *webpEncoder) ID() string {
	params := fmt.Sprintf("webp:q%d:%d:m%d:f%d:sns%d:seg%d",
		e.opts.Quality, e.opts.QualityMin, e.opts.Method, e.opts.Filter, e.opts.Sns, e.opts.Segments)
	bin := e.resolve()
	if bin == "" {
		return params + ":bin=missing"
	}
	st, err := os.Stat(bin)
	if err != nil {
		return params + ":bin=unknown"
	}
	return fmt.Sprintf("%s:bin=%s:%d:%d", params, bin, st.Size(), st.ModTime().UnixNano())
}

// Locate 按 显式路径 → 素材目录(含 bin/) → PATH 的顺序找 cwebp, 找不到返回空串。
//
// 显式给了但不可执行时直接返回空串, 不再退回 PATH: 调用方明确指定了这一个, 换一个来跑不是它的意思。
func Locate(explicit, assetDir string) string {
	if explicit != "" {
		if executable(explicit) {
			return explicit
		}
		return ""
	}
	if assetDir != "" {
		for _, rel := range []string{"cwebp", filepath.Join("bin", "cwebp")} {
			if p := filepath.Join(assetDir, rel); executable(p) {
				return p
			}
		}
	}
	if p, err := exec.LookPath("cwebp"); err == nil {
		return p
	}
	return ""
}

func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

// ppmStream 把画布按 PPM(P6) 流出去: 头部 + 每行剥掉 alpha 的 RGB。
//
// 不走 PNG: PNG 要过 DEFLATE(同一张 4Mpx 画布实测 988ms, 比整个 JPEG 编码还贵三倍),
// 而 PPM 只是把字节搬一遍(22ms); 实现成 io.Reader 之后连中间缓冲都省了, 一次只摊开一行。
type ppmStream struct {
	img        *image.RGBA
	w, h       int
	head       []byte
	hi, y, off int
	row        []byte
}

func newPPMStream(img *image.RGBA) *ppmStream {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	return &ppmStream{
		img:  img,
		w:    w,
		h:    h,
		head: []byte(fmt.Sprintf("P6\n%d %d\n255\n", w, h)),
		row:  make([]byte, w*3),
	}
}

func (s *ppmStream) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if s.hi < len(s.head) {
			c := copy(p[n:], s.head[s.hi:])
			s.hi += c
			n += c
			continue
		}
		if s.y >= s.h {
			break
		}
		if s.off == 0 {
			// 源是直存 alpha 的 RGBA, PPM 没有 alpha 通道 —— 直接丢掉(与 JPEG 编码器对 RGBA 的处理一致)。
			src := s.img.Pix[s.img.PixOffset(s.img.Rect.Min.X, s.img.Rect.Min.Y+s.y):]
			for i, j := 0, 0; j < len(s.row); i, j = i+4, j+3 {
				s.row[j], s.row[j+1], s.row[j+2] = src[i], src[i+1], src[i+2]
			}
		}
		c := copy(p[n:], s.row[s.off:])
		s.off += c
		n += c
		if s.off == len(s.row) {
			s.off, s.y = 0, s.y+1
		}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
