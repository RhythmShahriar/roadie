// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package dump reads SQL dumps (MySQL, MariaDB and plain PostgreSQL, raw
// or compressed) as a stream of row batches and writes them back, byte for
// byte where nothing changed. It also holds the encrypted spool a
// two-pass run reads production through.
//
// The exported names in this file are the frozen cross-lane API; the io
// lane replaces the bodies, which return core.ErrNotBuilt until then.
// Errors returned are *core.Error with codes dump-truncated, dump-format,
// dump-refused, dump-bomb, value-too-large and dialect-mismatch.
package dump

import (
	"context"
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// Options configures Open.
type Options struct {
	Name         string        // the file name, for messages
	MaxValue     int64         // largest single value in bytes; 0 = the default 256 MiB
	MaxRatio     int           // decompressed to compressed cap; 0 = the default 20
	KeepRoutines bool          // keep triggers, procedures and events, without DEFINER
	Schema       *core.Schema  // schema for a data-only dump, from --schema
	Progress     core.Progress // may be nil
}

// Open sniffs compression and dialect and returns the dump as a source.
func Open(ctx context.Context, r io.Reader, o Options) (core.Source, error) {
	return nil, core.ErrNotBuilt
}

// Sniff reads the head of a file and tells its dialect and compression.
// head is the first bytes of the file as stored (up to HeadSize; less is
// fine). A head that isn't SQL gives *core.Error dump-format. The dialect
// is "" when the head holds SQL without a marker of any dump tool.
func Sniff(head []byte) (dialect core.Dialect, compression string, err error) {
	dialect, compression, _, err = sniff("the input", head)
	return dialect, compression, err
}

// WriterOptions configures a dump writer.
type WriterOptions struct {
	Target       core.Dialect
	Compression  string // "", "gzip", "zstd"
	FixCollation bool
	KeepRoutines bool
	ExpectBytes  int64 // for the free-disk check; 0 = unknown
}

// NewFileSink writes to a temp file in the same folder as path (0600) and
// renames it on Commit.
func NewFileSink(path string, o WriterOptions) (core.Sink, error) {
	return nil, core.ErrNotBuilt
}

// NewStreamSink writes to w, such as stdout.
func NewStreamSink(w io.Writer, o WriterOptions) core.Sink {
	return nil
}

// Spool is an encrypted temporary file holding a copy of the input, so a
// second pass can read production again without a second export.
type Spool struct{} // its fields are unexported and added by the owning lane

// NewSpool creates a spool in dir.
func NewSpool(dir string) (*Spool, error) {
	return nil, core.ErrNotBuilt
}

// Writer returns the writer that fills the spool.
func (s *Spool) Writer() io.WriteCloser {
	return nil
}

// Reader returns a reader over the spool; it may be called more than once.
func (s *Spool) Reader() (io.ReadCloser, error) {
	return nil, core.ErrNotBuilt
}

// Remove deletes the spool.
func (s *Spool) Remove() error {
	return core.ErrNotBuilt
}
