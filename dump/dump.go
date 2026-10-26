// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/allow"
	"github.com/RhythmShahriar/roadie/dump/compress"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
	"github.com/RhythmShahriar/roadie/dump/pgsql"
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
	ends := newEndCheck(ph)
	s := &source{name: name, cr: cr, ends: ends, progress: o.Progress}
	switch dialect {
	case core.Postgres:
		pg := pgsql.NewSource(plain, pgsql.SourceOptions{
			Name:      name,
			NoTrailer: ends.trailer == "",
			MaxValue:  o.MaxValue,
			Schema:    o.Schema,
			Server:    server,
			Progress:  o.Progress,
			Offset:    cr.RawBytes,
		})
		s.rd, s.pg = pg, pg
		s.mode = func() mysql.Mode { return mysql.Mode{} }
	default:
		if dialect == "" {
			dialect = core.MySQL
		}
		my := mysql.NewSource(plain, mysql.SourceOptions{
			Name:      name,
			NoTrailer: ends.trailer == "",
			MaxValue:  o.MaxValue,
			Schema:    o.Schema,
			Dialect:   dialect,
			Server:    server,
			Progress:  o.Progress,
			Offset:    cr.RawBytes,
		})
		s.rd = my
		s.mode = my.Lexer().Mode
	}
	s.allow = allow.NewFilter(allow.Options{
		Name:         name,
		Dialect:      dialect,
		KeepRoutines: o.KeepRoutines,
		Database:     databaseOf(ph),
	})
	return s, nil
}

// reader is the dialect reader under a dump source.
type reader interface {
	Next(ctx context.Context) (*core.Batch, error)
	Schema() *core.Schema
	Table() string // the table the last batch belonged to
}

// source is a dump as a core.Source: the dialect reader plus the raw
// input's fingerprint.
type source struct {
	name     string
	cr       *compress.Reader
	rd       reader
	pg       *pgsql.Source     // set for a PostgreSQL dump
	mode     func() mysql.Mode // the MySQL sql_mode after the last statement
	allow    *allow.Filter
	ends     *endCheck
	progress core.Progress
	fp       core.Fingerprint
	err      error
}

func (s *source) Schema() *core.Schema { return s.rd.Schema() }

func (s *source) Next(ctx context.Context) (*core.Batch, error) {
	if s.err != nil {
		return nil, s.err
	}
	for {
		b, err := s.rd.Next(ctx)
		if err == nil {
			s.ends.see(b)
			// A COPY head is judged too, although the reader only reads
			// COPY ... FROM stdin as rows.
			if s.pg != nil && b.First && b.Pass == nil && bytes.HasPrefix(bytes.ToLower(bytes.TrimSpace(b.Head)), []byte("copy")) {
				if err := s.allow.Head(core.Statement{Kind: core.StmtCopy, Text: b.Head, Line: s.pg.Line()}); err != nil {
					s.err = err
					return nil, err
				}
			}
			// Every statement that isn't row data passes the allowlist
			// before it leaves the source; a dropped one is skipped.
			out, err := s.allow.Add(b, s.mode())
			if err != nil {
				s.err = err
				return nil, err
			}
			if out == nil {
				continue
			}
			return out, nil
		}
		return nil, s.end(err)
	}
}

// end handles the error that ended the dialect reader: io.EOF finishes
// the dump, anything else is worded and kept.
func (s *source) end(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		// The whole file is hashed, even bytes after the end of the
		// compressed stream.
		if err := s.cr.Drain(); err != nil {
			s.err = err
			return err
		}
		// A dump that ended between statements but not where its tool
		// ends one was cut short: the error comes from the last Next, so
		// no sink has committed anything.
		if !s.ends.whole() {
			s.err = s.ends.truncatedError(s.name, s.rd.Table(), s.cr.RawBytes())
			return s.err
		}
		if err := s.allow.Finish(); err != nil {
			s.err = err
			return err
		}
		if s.progress != nil {
			for _, n := range s.allow.Notes() {
				s.progress.Note(n)
			}
		}
		s.fp = s.cr.Fingerprint()
		s.err = io.EOF
		return io.EOF
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	s.err = streamError(s.name, s.rd.Table(), err)
	return s.err
}

// notes are the allowlist's plan lines so far.
func (s *source) notes() []string { return s.allow.Notes() }

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
