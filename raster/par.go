package raster

import "sync"

// Par 是并行策略: 按行分块, 块数固定, 结果与串行逐位相同。
type Par struct {
	Workers int // <=1 表示串行
	MinRows int // 少于这么多行就不值得起 goroutine
}

// Run 值得并行才按行分块, 否则 lo=0,hi=rows 内联调用。
func (p Par) Run(rows int, fn func(lo, hi int)) {
	if rows <= 0 {
		return
	}
	if p.Workers <= 1 || rows < p.MinRows {
		fn(0, rows)
		return
	}
	workers := p.Workers
	if workers > rows {
		workers = rows
	}
	chunk := (rows + workers - 1) / workers
	var wg sync.WaitGroup
	for a := 0; a < rows; a += chunk {
		b := a + chunk
		if b > rows {
			b = rows
		}
		wg.Add(1)
		go func(a, b int) {
			defer wg.Done()
			fn(a, b)
		}(a, b)
	}
	wg.Wait()
}
