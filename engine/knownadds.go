// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// addChunk is how many values, or bytes of them, parallelAdds gathers
// before it hands them to a worker: enough to make the hand-off cheap
// next to the keyed hashes, small enough to keep every worker busy.
const (
	addChunkValues = 512
	addChunkBytes  = 64 << 10
)

// parallelAdds is Known while pass 1 fills it. Pass 1 reads the source
// in one goroutine and Known.Add (an HMAC-SHA256 per token, three
// encodings, every word of a name) was most of its time. Add copies the
// value and hands it to workers; Known.Add is safe for concurrent use
// and the sealed set is the same in any order of adds, so the result
// doesn't change. Seal, Contains and Len wait for every add first; an
// Add after that goes straight to Known.
type parallelAdds struct {
	core.Known
	ch    chan *addBatch
	wg    sync.WaitGroup
	once  sync.Once
	mu    sync.Mutex // guards cur and drain: Add may come after wait
	cur   *addBatch
	pool  sync.Pool
	drain bool
}

// addBatch is a run of values back to back in buf.
type addBatch struct {
	buf   []byte
	kinds []core.FakeKind
	ends  []int
}

// newParallelAdds starts workers adders on k. workers below 2 adds in
// the caller's goroutine.
func newParallelAdds(k core.Known, workers int) core.Known {
	if workers < 2 {
		return k
	}
	p := &parallelAdds{Known: k, ch: make(chan *addBatch, workers*2)}
	p.pool.New = func() any { return &addBatch{buf: make([]byte, 0, addChunkBytes)} }
	p.wg.Add(workers)
	for range workers {
		go func() {
			defer p.wg.Done()
			for b := range p.ch {
				start := 0
				for i, end := range b.ends {
					p.Known.Add(b.kinds[i], b.buf[start:end])
					start = end
				}
				b.buf, b.kinds, b.ends = b.buf[:0], b.kinds[:0], b.ends[:0]
				p.pool.Put(b)
			}
		}()
	}
	return p
}

// Add queues value for the workers.
func (p *parallelAdds) Add(kind core.FakeKind, value []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.drain {
		p.Known.Add(kind, value)
		return
	}
	if p.cur == nil {
		p.cur = p.pool.Get().(*addBatch)
	}
	b := p.cur
	b.buf = append(b.buf, value...)
	b.kinds = append(b.kinds, kind)
	b.ends = append(b.ends, len(b.buf))
	if len(b.ends) >= addChunkValues || len(b.buf) >= addChunkBytes {
		p.cur = nil
		p.ch <- b
	}
}

// wait hands over what is left and waits for every worker; Adds after
// it go straight to Known.
func (p *parallelAdds) wait() {
	p.once.Do(func() {
		p.mu.Lock()
		if p.cur != nil {
			p.ch <- p.cur
			p.cur = nil
		}
		p.drain = true
		close(p.ch)
		p.mu.Unlock()
		p.wg.Wait()
	})
}

func (p *parallelAdds) Seal() {
	p.wait()
	p.Known.Seal()
}

func (p *parallelAdds) Contains(token []byte) (core.FakeKind, string, bool) {
	p.wait()
	return p.Known.Contains(token)
}

func (p *parallelAdds) Len() int {
	p.wait()
	return p.Known.Len()
}
