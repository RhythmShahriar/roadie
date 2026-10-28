// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"io"
)

// ctxReader reads r in a goroutine of its own, so a Read waiting on a
// pipe that nothing writes to returns as soon as ctx ends, with ctx's
// error, instead of when the writer closes the pipe. The goroutine ends
// with ctx, or stays blocked on r until r returns.
type ctxReader struct {
	ctx  context.Context
	reqs chan []byte
	res  chan readResult
	buf  []byte
}

type readResult struct {
	n   int
	err error
}

func newCtxReader(ctx context.Context, r io.Reader) io.Reader {
	c := &ctxReader{ctx: ctx, reqs: make(chan []byte), res: make(chan readResult, 1)}
	go func() {
		for {
			select {
			case p := <-c.reqs:
				n, err := r.Read(p)
				c.res <- readResult{n, err}
			case <-ctx.Done():
				return
			}
		}
	}()
	return c
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	// The goroutine reads into a buffer of its own: once a Read gives up
	// on ctx, the caller's p is never written behind its back.
	if cap(c.buf) < len(p) {
		c.buf = make([]byte, len(p))
	}
	buf := c.buf[:len(p)]
	select {
	case c.reqs <- buf:
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	}
	select {
	case r := <-c.res:
		return copy(p, buf[:r.n]), r.err
	case <-c.ctx.Done():
		// buf may still be written: never hand it out again.
		c.buf = nil
		return 0, c.ctx.Err()
	}
}
