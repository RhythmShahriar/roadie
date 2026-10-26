// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// checkEvery is how often, in bytes written, a file sink checks the free
// space again.
const checkEvery = 64 << 20

// freeSpace returns the bytes free to this user in dir and false when
// the platform can't tell. Tests replace it.
var freeSpace = diskFree

// diskFull is the disk-full error (decisions.md): need and free in
// bytes, dir the target folder.
func diskFull(need, free int64, dir string) *core.Error {
	return core.Errorf(core.ErrDiskFull, 3,
		"disk-full: not enough free disk space: need about %s, %s free in %s. Free some space or write the copy to another disk; the target is untouched.",
		size.Human(need), size.Human(free), dir)
}

// encoder is the dialect writer under a sink.
type encoder interface {
	Write(b *core.Batch) error
	Finish() error
}

func newEncoder(w io.Writer, o WriterOptions) (encoder, error) {
	switch o.Target {
	case core.Postgres:
		return nil, core.ErrNotBuilt // the PostgreSQL writer lands with dump/pgsql
	case core.SQLite:
		return nil, core.Errorf(core.ErrDialectMismatch, 2, "dialect-mismatch: Roadie writes MySQL, MariaDB and PostgreSQL dumps, not SQLite ones.")
	}
	return mysql.NewWriter(w, mysql.WriterOptions{Target: o.Target, FixCollation: o.FixCollation}), nil
}

// counter counts the bytes that reach the file, checks the free space
// every checkEvery bytes and turns a full disk into disk-full.
type counter struct {
	dst    io.Writer
	dir    string
	expect int64
	n      int64
	next   int64
}

func (c *counter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) >= c.next {
		c.next = c.n + int64(len(p)) + checkEvery
		if free, ok := freeSpace(c.dir); ok {
			need := int64(checkEvery)
			if c.expect > 0 {
				need = max(c.expect-c.n, 0)
			}
			if free < need {
				return 0, diskFull(need, free, c.dir)
			}
		}
	}
	n, err := c.dst.Write(p)
	c.n += int64(n)
	if err != nil && isNoSpace(err) {
		free, _ := freeSpace(c.dir)
		need := max(c.expect-c.n, int64(len(p)-n))
		return n, diskFull(need, free, c.dir)
	}
	return n, err
}

// fileSink writes a dump to '<name>.roadie-tmp-<random>' next to the
// target with mode 0600 and renames it over the target on Commit.
type fileSink struct {
	mu    sync.Mutex
	path  string
	tmp   string
	f     *os.File
	cnt   *counter
	comp  io.WriteCloser
	bw    *bufio.Writer
	enc   encoder
	state core.TargetState
	done  bool
}

// newFileSink is NewFileSink.
func newFileSink(path string, o WriterOptions) (core.Sink, error) {
	kind := o.Compression
	if kind == "" {
		kind = compress.FromExt(path)
	}
	dir := filepath.Dir(path)
	if o.ExpectBytes > 0 {
		if free, ok := freeSpace(dir); ok && free < o.ExpectBytes {
			return nil, diskFull(o.ExpectBytes, free, dir)
		}
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	tmp := path + ".roadie-tmp-" + hex.EncodeToString(rnd[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the --to path the user named
	if err != nil {
		return nil, err
	}
	s := &fileSink{path: path, tmp: tmp, f: f, state: core.Untouched}
	s.cnt = &counter{dst: f, dir: dir, expect: o.ExpectBytes, next: checkEvery}
	if s.comp, err = compress.NewWriter(s.cnt, kind); err != nil {
		s.remove()
		return nil, err
	}
	s.bw = bufio.NewWriterSize(s.comp, 1<<20)
	if s.enc, err = newEncoder(s.bw, o); err != nil {
		s.remove()
		return nil, err
	}
	return s, nil
}

// remove closes and deletes the temp file.
func (s *fileSink) remove() {
	s.done = true
	_ = s.f.Close()
	_ = os.Remove(s.tmp)
}

// fail removes the temp file after an error and returns the error.
func (s *fileSink) fail(err error) error {
	s.remove()
	return err
}

func (s *fileSink) Write(ctx context.Context, b *core.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return errors.New("dump: write to a closed sink")
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if err := s.enc.Write(b); err != nil {
		return s.fail(err)
	}
	return nil
}

func (s *fileSink) Commit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return errors.New("dump: commit of a closed sink")
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	for _, step := range []func() error{s.enc.Finish, s.bw.Flush, s.comp.Close, s.f.Sync, s.f.Close} {
		if err := step(); err != nil {
			return s.fail(err)
		}
	}
	if err := os.Rename(s.tmp, s.path); err != nil {
		return s.fail(fmt.Errorf("dump: %w", err))
	}
	s.done = true
	s.state = core.Replaced
	syncDir(filepath.Dir(s.path))
	return nil
}

func (s *fileSink) Abort(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.remove()
	}
	return nil
}

func (s *fileSink) State() core.TargetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// streamSink writes a dump to a stream such as stdout.
type streamSink struct {
	mu    sync.Mutex
	comp  io.WriteCloser
	bw    *bufio.Writer
	enc   encoder
	err   error
	state core.TargetState
	done  bool
}

// newStreamSink is NewStreamSink. Errors setting it up come back from
// the first Write.
func newStreamSink(w io.Writer, o WriterOptions) core.Sink {
	s := &streamSink{state: core.Untouched}
	var err error
	if s.comp, err = compress.NewWriter(w, o.Compression); err != nil {
		s.err = err
		return s
	}
	s.bw = bufio.NewWriterSize(s.comp, 1<<20)
	s.enc, s.err = newEncoder(s.bw, o)
	return s
}

func (s *streamSink) Write(ctx context.Context, b *core.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.enc.Write(b); err != nil {
		s.err = err
		return err
	}
	return nil
}

func (s *streamSink) Commit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, step := range []func() error{s.enc.Finish, s.bw.Flush, s.comp.Close} {
		if err := step(); err != nil {
			s.err = err
			return err
		}
	}
	s.done, s.state = true, core.Replaced
	return nil
}

// Abort leaves what was already written to the stream; a reader of a
// stream that stops early sees it end without the trailer.
func (s *streamSink) Abort(context.Context) error { return nil }

func (s *streamSink) State() core.TargetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}
