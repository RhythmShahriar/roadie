// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "context"

// ValueKind says how a field was written in the source.
type ValueKind uint8

// The value kinds. Expr is anything else a dump can hold in a value
// position, such as a function call, kept as literal text.
const (
	Null ValueKind = iota
	String
	Number
	Hex
	BinaryString
	Bit
	Expr
)

// Value is one field. Raw holds the decoded bytes for String and
// BinaryString (unescaped) and the literal text for Number, Hex, Bit and
// Expr. Dirty is set by whoever changes Raw, so the writer knows to encode
// the row again instead of copying its source bytes.
type Value struct {
	Kind  ValueKind
	Raw   []byte
	Dirty bool
}

// Row keeps the exact source bytes so an untouched row is written byte for byte.
type Row struct {
	Values []Value
	Src    []byte // exact source text of the row, "(...)" for MySQL, the line for COPY; nil from a live database
	Lead   []byte // bytes between the previous row (or the statement head) and Src
	Drop   bool   // set by the engine to leave the row out
}

// StmtKind names the kind of a statement that isn't row data.
type StmtKind string

// The statement kinds dump readers report.
const (
	StmtCreateTable StmtKind = "create-table"
	StmtDropTable   StmtKind = "drop-table"
	StmtInsert      StmtKind = "insert"
	StmtReplace     StmtKind = "replace"
	StmtCopy        StmtKind = "copy"
	StmtLock        StmtKind = "lock"
	StmtUnlock      StmtKind = "unlock"
	StmtSet         StmtKind = "set"
	StmtTransaction StmtKind = "transaction"
	StmtComment     StmtKind = "comment"
	StmtAlterKeys   StmtKind = "alter-keys"
	StmtCreateIndex StmtKind = "create-index"
	StmtSequence    StmtKind = "sequence"
	StmtRoutine     StmtKind = "routine"
	// StmtCommentOn is PostgreSQL's COMMENT ON, whose text is a literal
	// kept in the copy like a MySQL column COMMENT.
	StmtCommentOn StmtKind = "comment-on"
	StmtOther     StmtKind = "other"
)

// Statement is a statement passed through as text.
type Statement struct {
	Kind StmtKind
	Text []byte
	Line int64 // line of the statement's first byte in the input, from 1
}

// Batch is a run of rows of one table, or one passthrough statement.
type Batch struct {
	Seq     uint64 // input order; the writer restores it
	Table   *Table // nil for a passthrough statement
	Columns []int  // index into Table.Columns for each value position
	Rows    []Row
	Head    []byte     // statement text before the first row; set when First
	Tail    []byte     // text after the last row including ";\n" or "\\.\n"; set when Last
	First   bool       // the batch starts a statement
	Last    bool       // the batch ends a statement
	Pass    *Statement // passthrough statement (Table nil, Rows nil)
	Offset  int64      // input byte offset after this batch, for progress
}

// MaxBatchBytes caps a batch at a row boundary. A single row larger than
// this is a batch of its own.
const MaxBatchBytes = 4 << 20

// Fingerprint identifies the input or output of a run.
type Fingerprint struct {
	Kind  string // "sha256-file" or "db-tables"
	Value string
	Bytes int64
}

// Source streams a dump or a database as batches.
type Source interface {
	// Schema grows as CREATE TABLE statements are read; it is complete at io.EOF.
	Schema() *Schema
	// Next returns the next batch, or io.EOF at the end.
	Next(ctx context.Context) (*Batch, error)
	// Fingerprint is final after io.EOF.
	Fingerprint() Fingerprint
	Close() error
}

// TargetState says what a copy did to its target.
type TargetState string

// The target states. Mixed means some tables were replaced before the run
// stopped; running the same command again finishes it.
const (
	Untouched TargetState = "untouched"
	Replaced  TargetState = "replaced"
	Mixed     TargetState = "mixed"
)

// Sink writes batches to a dump file, a stream or a database.
type Sink interface {
	Write(ctx context.Context, b *Batch) error // batches arrive in Seq order
	Commit(ctx context.Context) error          // rename the temp file / finish the last table
	Abort(ctx context.Context) error           // remove a partial file; a database stays Mixed
	State() TargetState
}
