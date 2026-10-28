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
	KeepRoutines bool          // keep triggers, procedures, functions and views, without DEFINER
	Schema       *core.Schema  // schema for a data-only dump, from --schema
	Progress     core.Progress // may be nil
	// SchemaOnly marks the --schema file itself: DDL a person may have
	// written by hand, with no dump tool's end to check. It must still
	// hold a statement and end at a statement's end.
	SchemaOnly bool
}

// Open sniffs compression and dialect and returns the dump as a source.
// The source's Fingerprint is the SHA-256 of the raw input once Next has
// returned io.EOF. Close releases the decompressor; r stays the caller's.
func Open(ctx context.Context, r io.Reader, o Options) (core.Source, error) {
	return open(ctx, r, o)
}

// Notes returns the plan lines of a dump source from Open: each kind of
// statement the allowlist dropped (GRANT, ALTER ... OWNER TO, CREATE
// EVENT) with its count and first line and each routine kept with
// --keep-routines. They are complete once Next has returned io.EOF, when
// they also go to Options.Progress.Note. Any other source gives nil.
func Notes(src core.Source) []string {
	if s, ok := src.(interface{ notes() []string }); ok {
		return s.notes()
	}
	return nil
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
	// Source is the dialect of the dump being copied, when known. A
	// dump to a target of another dialect (MySQL and MariaDB are one)
	// gives dialect-mismatch when the sink is made.
	Source core.Dialect
}

// CheckDialect refuses a copy between two of PostgreSQL, SQLite and
// MySQL or MariaDB with dialect-mismatch (exit 2). name is the dump's file name, from
// and to the dialects of the dump and the target, db whether --to is a
// database rather than a dump file. MySQL and MariaDB copy to each
// other; an empty dialect is not checked.
func CheckDialect(name string, from, to core.Dialect, db bool) error {
	return checkDialect(name, from, to, db)
}

// NewFileSink writes to a temp file in the same folder as path (0600) and
// renames it on Commit. The temp file is '<path>.roadie-tmp-<random>';
// Abort, a canceled context and any write error remove it. Compression
// "" takes gzip or zstd from the extension of path. With ExpectBytes set
// it refuses up front and every 64 MiB written, when the folder has less
// free space than is still to come (disk-full).
func NewFileSink(path string, o WriterOptions) (core.Sink, error) {
	return newFileSink(path, o)
}

// NewStreamSink writes to w, such as stdout, compressed when
// o.Compression says so. A setup error comes back from the first Write.
func NewStreamSink(w io.Writer, o WriterOptions) core.Sink {
	return newStreamSink(w, o)
}

// Spool is an encrypted temporary file holding a copy of the input, so a
// second pass can read production again without a second export. It is
// written in 1 MiB AES-256-GCM chunks under a key from crypto/rand that
// only this process holds, so the file is unreadable once the process is
// gone. The caller removes it on success, error and cancel.
type Spool struct{ spool }

// NewSpool creates dir (0700) when missing and a '<16 hex>.spool' file
// (0600) in it.
func NewSpool(dir string) (*Spool, error) {
	return newSpool(dir)
}

// Writer returns the writer that fills the spool. Close it before the
// first Reader; it is the same writer on every call.
func (s *Spool) Writer() io.WriteCloser {
	return s.writer()
}

// Reader returns a reader over the spool; it may be called more than
// once. A chunk that was changed, reordered or cut off gives an error.
func (s *Spool) Reader() (io.ReadCloser, error) {
	return s.reader()
}

// Remove deletes the spool file. A second call does nothing.
func (s *Spool) Remove() error {
	return s.remove()
}
