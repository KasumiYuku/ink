// Package encode 是出图编码: 只决定体积, 但"用哪个编码器"会改字节, 所以身份必须进指纹。
package encode

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("ink/encode: 编码器不可用")

// Encoder 把画布编成字节; ID 描述"是什么在编", 换编码器/换参数/换二进制都必须换 ID。
type Encoder interface {
	Encode(img *image.RGBA) ([]byte, error)
	ID() string
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

// WebPOptions 是 cwebp 那一档的参数; BudgetBytes > 0 时超了就用 QualityMin 重编一次。
type WebPOptions struct {
	Path        string
	Quality     int
	QualityMin  int
	Method      int
	Segments    int
	BudgetBytes int64
	Timeout     time.Duration
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
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	return &webpEncoder{opts: opts}
}

type webpEncoder struct {
	opts WebPOptions
	once sync.Once
	bin  string
}

func (e *webpEncoder) resolve() string {
	e.once.Do(func() { e.bin = Locate(e.opts.Path, "") })
	return e.bin
}

// ID 含二进制本身的路径/大小/修改时间: cwebp 换了版本, 压出来的字节就不一样。
func (e *webpEncoder) ID() string {
	params := fmt.Sprintf("webp:q%d:%d:m%d:seg%d", e.opts.Quality, e.opts.QualityMin, e.opts.Method, e.opts.Segments)
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

func (e *webpEncoder) Encode(img *image.RGBA) ([]byte, error) {
	bin := e.resolve()
	if bin == "" {
		return nil, ErrUnavailable
	}
	dir, err := os.MkdirTemp("", "ink-encode")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.png")
	out := filepath.Join(dir, "out.webp")
	f, err := os.Create(in)
	if err != nil {
		return nil, err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	raw, err := e.run(bin, in, out, e.opts.Quality)
	if err != nil {
		return nil, err
	}
	if e.opts.BudgetBytes > 0 && int64(len(raw)) > e.opts.BudgetBytes && e.opts.QualityMin < e.opts.Quality {
		if small, err := e.run(bin, in, out, e.opts.QualityMin); err == nil {
			return small, nil
		}
	}
	return raw, nil
}

func (e *webpEncoder) run(bin, in, out string, quality int) ([]byte, error) {
	cmd := exec.Command(bin, "-quiet", "-q", strconv.Itoa(quality),
		"-m", strconv.Itoa(e.opts.Method), "-segments", strconv.Itoa(e.opts.Segments),
		"-o", out, in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("cwebp: %w: %s", err, stderr.String())
		}
	case <-time.After(e.opts.Timeout):
		_ = cmd.Process.Kill()
		return nil, errors.New("cwebp: 超时")
	}
	return os.ReadFile(out)
}

// Locate 按 显式路径 → 素材目录(含 bin/) → PATH 的顺序找 cwebp, 找不到返回空串。
func Locate(explicit, assetDir string) string {
	if explicit != "" && executable(explicit) {
		return explicit
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
