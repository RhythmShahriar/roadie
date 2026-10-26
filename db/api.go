// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package db connects to live MySQL, MariaDB, PostgreSQL and SQLite
// databases, as read-only sources and as sinks that replace tables.
//
// The exported names in this file are the frozen cross-lane API; the io
// lane replaces the bodies, which return core.ErrNotBuilt until then.
package db

import (
	"context"

	"github.com/RhythmShahriar/roadie/core"
)

// Mode says whether a connection may write.
type Mode int

// The connection modes. A ReadOnly connection runs in a read-only
// transaction, so even a bug can't write to production.
const (
	ReadOnly Mode = iota
	ReadWrite
)

// Conn is one database connection.
type Conn struct{} // its fields are unexported and added by the owning lane

// SinkOptions configures Conn.Sink.
type SinkOptions struct {
	Tables       []string // tables the import replaces; nil = all in the stream
	FixCollation bool
}

// Open connects to the database e names.
func Open(ctx context.Context, e core.Endpoint, m Mode) (*Conn, error) {
	return nil, core.ErrNotBuilt
}

// Identity names the server and database, for the target guards.
func (c *Conn) Identity(ctx context.Context) (core.DBIdentity, error) {
	return core.DBIdentity{}, core.ErrNotBuilt
}

// Schema reads every table's columns and keys.
func (c *Conn) Schema(ctx context.Context) (*core.Schema, error) {
	return nil, core.ErrNotBuilt
}

// Source streams the named tables (nil = all) as batches.
func (c *Conn) Source(ctx context.Context, tables []string) (core.Source, error) {
	return nil, core.ErrNotBuilt
}

// Sink imports batches, one table at a time.
func (c *Conn) Sink(ctx context.Context, o SinkOptions) (core.Sink, error) {
	return nil, core.ErrNotBuilt
}

// Close ends the connection.
func (c *Conn) Close() error {
	return core.ErrNotBuilt
}
