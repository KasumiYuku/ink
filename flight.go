package ink

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// flight 把同一把 key 的并发调用合成一次: 后来者等第一个人的结果, 不再各画一遍。
type flight struct {
	mu        sync.Mutex
	calls     map[string]*call
	coalesced atomic.Int64
}

type call struct {
	wg  sync.WaitGroup
	out []byte
	err error
}

func newFlight() *flight { return &flight{calls: map[string]*call{}} }

func (f *flight) do(key string, fn func() ([]byte, error)) ([]byte, error) {
	f.mu.Lock()
	if c, ok := f.calls[key]; ok {
		f.mu.Unlock()
		f.coalesced.Add(1)
		c.wg.Wait()
		return c.out, c.err
	}
	c := &call{}
	c.wg.Add(1)
	f.calls[key] = c
	f.mu.Unlock()

	// 正常返回、出错、panic 三条路都要摘登记 + Done; 漏一条, 后来者会永远挂在这次调用上。
	defer func() {
		r := recover()
		if r != nil {
			c.err = fmt.Errorf("ink: 渲染 panic: %v", r)
		}
		f.mu.Lock()
		delete(f.calls, key)
		f.mu.Unlock()
		c.wg.Done()
		if r != nil {
			panic(r)
		}
	}()
	c.out, c.err = fn()
	return c.out, c.err
}
