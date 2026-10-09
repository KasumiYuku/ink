package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Digest 是素材目录的内容指纹: 每文件的(相对路径, 大小, 修改时间) + manifest.json 自身的 sha256。
//
// 它只服务一件事: 换了素材就必须换指纹(喂给 ink.Options.ContentDigest), 否则会拿旧图当新素材发出去。
// 不读文件内容: 素材几十上百 MB, 全读一遍等于拿启动时间换"内容变了但大小与 mtime 都没变"这个几乎
// 不会发生的场景 —— 而那种情况由 manifest 的摘要兜住。
func Digest(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("ink/theme: 素材目录为空")
	}
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("ink/theme: 素材目录不可用: %w", err)
	}
	h := sha256.New()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%d\n", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ink/theme: 遍历素材目录: %w", err)
	}
	sort.Strings(lines)
	for _, line := range lines {
		io.WriteString(h, line)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		sum := sha256.Sum256(raw)
		io.WriteString(h, "manifest="+hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
