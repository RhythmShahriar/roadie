// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"math"
	"runtime/debug"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// windowBytes is how much batch memory pass 2 holds in flight by
// default: read, waiting for a worker, being changed or waiting for the
// writer. A batch is at most 4 MB of SQL but its rows, held as one
// core.Value per field, take several times that, so a window counted in
// batches alone set the run's memory (PRD 22.3: 300 MB at eight jobs).
const windowBytes = 96 << 20

// windowFor is the window of a run: windowBytes, or a quarter of
// --max-memory when that is set and smaller.
func windowFor(maxMemory int64) int64 {
	if maxMemory > 0 && maxMemory/4 < windowBytes {
		return max(maxMemory/4, core.MaxBatchBytes)
	}
	return windowBytes
}

// defaultMemory is the soft memory limit of a run without --max-memory
// (PRD 22.3: peak RSS 300 MB at eight jobs). Without one the Go heap
// grows to twice what is live before it collects; the window keeps what
// is live near half of this.
const defaultMemory = 300 << 20

// memoryLimit is the soft limit a run sets: --max-memory, else
// defaultMemory unless GOMEMLIMIT already set one, which it leaves
// alone (0).
func memoryLimit(maxMemory int64) int64 {
	if maxMemory > 0 {
		return maxMemory
	}
	if debug.SetMemoryLimit(-1) != math.MaxInt64 {
		return 0
	}
	return defaultMemory
}

// The in-memory size of the parts of a batch that aren't its bytes: a
// row (its slices and Drop) and a value (Kind, Raw and Dirty).
const (
	rowOverhead   = 80
	valueOverhead = 40
)

// batchCost estimates the memory a batch holds. It counts a value's
// bytes and its row's source text both, though a reader may share them:
// the window then holds a little less than it could, never more.
func batchCost(b *core.Batch) int64 {
	n := int64(len(b.Head) + len(b.Tail))
	if b.Pass != nil {
		n += int64(len(b.Pass.Text))
	}
	for i := range b.Rows {
		r := &b.Rows[i]
		n += int64(rowOverhead + len(r.Src) + len(r.Lead) + valueOverhead*len(r.Values))
		for j := range r.Values {
			n += int64(len(r.Values[j].Raw))
		}
	}
	return n
}

// window bounds the batch memory in flight. One goroutine (the reader)
// takes; another (the writer) gives back. A batch larger than the whole
// window still goes when nothing else is in flight, so a huge row can't
// stall the run.
type window struct {
	mu    sync.Mutex
	used  int64
	max   int64
	freed chan struct{}
}

func newWindow(limit int64) *window { return &window{max: limit, freed: make(chan struct{}, 1)} }

// take waits until n bytes fit, or ctx ends.
func (w *window) take(ctx context.Context, n int64) error {
	for {
		w.mu.Lock()
		if w.used == 0 || w.used+n <= w.max {
			w.used += n
			w.mu.Unlock()
			return nil
		}
		w.mu.Unlock()
		select {
		case <-w.freed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// give returns n bytes and wakes the reader if it waits.
func (w *window) give(n int64) {
	w.mu.Lock()
	w.used -= n
	w.mu.Unlock()
	select {
	case w.freed <- struct{}{}:
	default:
	}
}
