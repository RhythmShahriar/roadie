// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bufio"
	"context"
	"errors"
	"io"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// open is Open: sniff the raw head, decompress with the cap, sniff the
// decompressed head and hand it to the dialect's reader.
func open(ctx context.Context, r io.Reader, o Options) (core.Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := o.Name
	if name == "" {
		name = "the input"
	}
	raw := bufio.NewReaderSize(r, HeadSize)
	head, err := raw.Peek(HeadSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	comp, err := sniffRaw(name, head)
	if err != nil {
		return nil, err
	}
	cr, err := compress.NewReader(raw, comp, compress.Options{Name: name, MaxRatio: o.MaxRatio, Progress: o.Progress})
	if err != nil {
		return nil, streamError(name, "", err)
	}
	// The decompressed head, read from the real stream: a zstd block
	// larger than the raw head decodes nothing from the head alone.
	plain := bufio.NewReaderSize(cr, HeadSize)
	ph, err := plain.Peek(HeadSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		_ = cr.Close()
		return nil, streamError(name, "", err)
	}
	dialect, server, err := sniffPlain(name, ph)
	if err != nil {
		_ = cr.Close()
		return nil, err
	}
	switch dialect {
	case core.Postgres:
		_ = cr.Close()
		return nil, core.ErrNotBuilt // the PostgreSQL reader lands with dump/pgsql
	case "":
		dialect = core.MySQL
	}
	src := mysql.NewSource(plain, mysql.SourceOptions{
		Name:     name,
		MaxValue: o.MaxValue,
		Schema:   o.Schema,
		Dialect:  dialect,
		Server:   server,
		Progress: o.Progress,
		Offset:   cr.RawBytes,
	})
	return &source{name: name, cr: cr, my: src}, nil
}

// source is a dump as a core.Source: the dialect reader plus the raw
// input's fingerprint.
type source struct {
	name string
	cr   *compress.Reader
	my   *mysql.Source
	fp   core.Fingerprint
	err  error
}

func (s *source) Schema() *core.Schema { return s.my.Schema() }

func (s *source) Next(ctx context.Context) (*core.Batch, error) {
	if s.err != nil {
		return nil, s.err
	}
	b, err := s.my.Next(ctx)
	switch {
	case err == nil:
		return b, nil
	case errors.Is(err, io.EOF):
		// The whole file is hashed, even bytes after the end of the
		// compressed stream.
		if err := s.cr.Drain(); err != nil {
			s.err = err
			return nil, err
		}
		s.fp = s.cr.Fingerprint()
		s.err = io.EOF
		return nil, io.EOF
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil, err
	}
	s.err = streamError(s.name, s.my.Table(), err)
	return nil, s.err
}

func (s *source) Fingerprint() core.Fingerprint { return s.fp }

func (s *source) Close() error { return s.cr.Close() }

// streamError words a broken compressed stream as dump-truncated; other
// errors pass through.
func streamError(name, table string, err error) error {
	var se *compress.StreamError
	if !errors.As(err, &se) {
		return err
	}
	where := "ends early"
	if table != "" {
		where = "ends inside table " + table
	}
	return core.Errorf(core.ErrDumpTruncated, 3, `dump-truncated: %s %s at %s: the %s stream is cut or damaged. Export it again; the target is untouched.`,
		name, where, size.GB(se.Raw), se.Kind)
}
