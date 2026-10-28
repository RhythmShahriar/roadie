// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// selfInput is the input of a run that reads and writes one database
// (prepare): pass 1 reads the live database once, writes every batch to
// the encrypted spool (PRD 10.3) and closes its read transaction; pass 2
// reads the spool. Reading the database while the import replaces its
// tables would wait on its own snapshot's metadata locks for ever.
type selfInput struct {
	in  input // the live database (siteInput or tablesInput)
	dir string

	sp      spool
	spooled bool
	schema  *core.Schema
	fp      core.Fingerprint
}

// spooledBatch is one batch on the spool. Table is the table's name; the
// table itself goes once, with the first batch that names it.
type spooledBatch struct {
	Seq     uint64
	Table   string
	Def     *core.Table
	Columns []int
	Rows    []core.Row
	Head    []byte
	Tail    []byte
	First   bool
	Last    bool
	Pass    *core.Statement
	Offset  int64
}

func (s *selfInput) open(ctx context.Context, pass int) (core.Source, error) {
	if pass == 1 || !s.spooled {
		if s.sp != nil && pass != 1 {
			return nil, errors.New("engine: pass 2 after a pass 1 that stopped early")
		}
		src, err := s.in.open(ctx, 1)
		if err != nil {
			return nil, err
		}
		sp, err := newSpool(s.dir)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		s.sp = sp
		w := sp.Writer()
		bw := bufio.NewWriterSize(w, 1<<20)
		tee := &teeSource{Source: src, w: w, bw: bw, enc: gob.NewEncoder(bw), sent: map[string]bool{}, in: s}
		if pass == 1 {
			return tee, nil
		}
		// --one-pass reads only once: drain the database into the
		// spool first, then replay it.
		for {
			if _, err := tee.Next(ctx); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				_ = tee.Close()
				return nil, err
			}
		}
		if err := tee.Close(); err != nil {
			return nil, err
		}
	}
	rc, err := s.sp.Reader()
	if err != nil {
		return nil, err
	}
	return &replaySource{r: rc, dec: gob.NewDecoder(bufio.NewReaderSize(rc, 1<<20)), schema: s.schema, fp: s.fp,
		defs: map[string]*core.Table{}}, nil
}

func (s *selfInput) close() error {
	err := s.in.close()
	if s.sp == nil {
		return err
	}
	if rerr := s.sp.Remove(); err == nil {
		err = rerr
	}
	return err
}

// teeSource is pass 1 over the live database: each batch also goes to
// the spool. Close ends the database's read before the sink opens.
type teeSource struct {
	core.Source
	w    io.WriteCloser
	bw   *bufio.Writer
	enc  *gob.Encoder
	sent map[string]bool
	in   *selfInput
	eof  bool
	err  error
}

func (t *teeSource) Next(ctx context.Context) (*core.Batch, error) {
	b, err := t.Source.Next(ctx)
	if errors.Is(err, io.EOF) {
		t.eof = true
		return nil, err
	}
	if err != nil || b == nil {
		return b, err
	}
	sb := spooledBatch{Seq: b.Seq, Columns: b.Columns, Rows: b.Rows, Head: b.Head, Tail: b.Tail,
		First: b.First, Last: b.Last, Pass: b.Pass, Offset: b.Offset}
	if b.Table != nil {
		sb.Table = b.Table.Name
		if !t.sent[sb.Table] {
			t.sent[sb.Table] = true
			sb.Def = b.Table
		}
	}
	if err := t.enc.Encode(&sb); err != nil {
		t.err = fmt.Errorf("spool: %w", err)
		return nil, t.err
	}
	return b, nil
}

func (t *teeSource) Close() error {
	t.in.schema, t.in.fp = t.Schema(), t.Fingerprint()
	err := t.Source.Close()
	if ferr := t.bw.Flush(); err == nil && t.err == nil {
		err = ferr
	}
	if cerr := t.w.Close(); err == nil {
		err = cerr
	}
	if err == nil && t.err == nil && t.eof {
		t.in.spooled = true
	}
	return err
}

// replaySource is pass 2: the batches pass 1 read, from the spool.
type replaySource struct {
	r      io.ReadCloser
	dec    *gob.Decoder
	schema *core.Schema
	fp     core.Fingerprint
	defs   map[string]*core.Table
}

func (r *replaySource) Schema() *core.Schema          { return r.schema }
func (r *replaySource) Fingerprint() core.Fingerprint { return r.fp }
func (r *replaySource) Close() error                  { return r.r.Close() }

func (r *replaySource) Next(ctx context.Context) (*core.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var sb spooledBatch
	if err := r.dec.Decode(&sb); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("spool: %w", err)
	}
	b := &core.Batch{Seq: sb.Seq, Columns: sb.Columns, Rows: sb.Rows, Head: sb.Head, Tail: sb.Tail,
		First: sb.First, Last: sb.Last, Pass: sb.Pass, Offset: sb.Offset}
	if sb.Def != nil {
		r.defs[sb.Table] = sb.Def
	}
	if sb.Table != "" {
		b.Table = r.schema.Table(sb.Table)
		if b.Table == nil {
			b.Table = r.defs[sb.Table]
		}
		if b.Table == nil {
			return nil, fmt.Errorf("spool: batch of unknown table %s", sb.Table)
		}
	}
	return b, nil
}
