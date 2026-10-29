// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package db connects to live MySQL, MariaDB, PostgreSQL and SQLite
// databases, as read-only sources and as sinks that replace tables.
//
// MySQL and MariaDB go through go-sql-driver/mysql with LOCAL INFILE,
// multi-statements and client-side interpolation off; PostgreSQL through
// pgx; SQLite through the pure-Go modernc.org/sqlite. A ReadOnly
// connection reads inside one read-only transaction (a consistent
// snapshot) and refuses every write with core.ErrReadOnly. The password
// comes from core.Endpoint.Password only, never from the environment or
// a password file.
//
// The exported names in this file are the frozen cross-lane API.
// Errors from a sink are *core.Error with codes import and import-mixed.
package db

import (
	"context"
	"database/sql"
	"io"

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

// Conn is one database connection. It holds a single session: a Source
// or a Sink uses it until it ends, so call Identity and Schema before
// starting one (both are cached after the first call).
type Conn struct {
	ep     core.Endpoint
	mode   Mode
	engine core.Dialect // MariaDB when a mysql:// server says so
	server string       // "8.4.3", "11.4.8", "16.4", "3.50.4"
	// instance names the server process whatever address reached it:
	// MySQL's @@server_uuid, PostgreSQL's system identifier; "" when
	// the server didn't say.
	instance string
	dbName   string
	user     string
	db       *sql.DB
	conn     *sql.Conn
	inTx     bool // the read-only transaction is open
	schema   *core.Schema
	busy     bool      // a Source or Sink holds the session
	active   io.Closer // that Source or Sink, closed by Close
	closed   bool
	collate  map[string]bool // collations asked about, for a sink: known or not
}

// SinkOptions configures Conn.Sink.
type SinkOptions struct {
	Tables       []string // tables the import replaces; nil = all in the stream
	FixCollation bool
	// Total is the number of tables the stream holds, for the
	// import-mixed message ("after 37 of 61 tables"); 0 = unknown, then
	// the message gives the count replaced only.
	Total int
}

// Open connects to the database e names.
func Open(ctx context.Context, e core.Endpoint, m Mode) (*Conn, error) {
	return open(ctx, e, m)
}

// Identity names the server and database, for the target guards.
func (c *Conn) Identity(ctx context.Context) (core.DBIdentity, error) {
	return c.identity(ctx)
}

// Instance names the database on its server whatever host, port or
// address reached it (MySQL @@server_uuid, PostgreSQL's system
// identifier, then the database name), for target-is-source: two
// addresses of one server are one database. "" when the server didn't
// say.
func (c *Conn) Instance() string {
	if c.instance == "" {
		return ""
	}
	return c.instance + "/" + c.dbName
}

// Schema reads every table's columns and keys.
func (c *Conn) Schema(ctx context.Context) (*core.Schema, error) {
	return c.readSchema(ctx)
}

// Source streams the named tables (nil = all) as batches.
func (c *Conn) Source(ctx context.Context, tables []string) (core.Source, error) {
	return c.newSource(ctx, tables)
}

// Sink imports batches, one table at a time.
func (c *Conn) Sink(ctx context.Context, o SinkOptions) (core.Sink, error) {
	return c.newSink(ctx, o)
}

// Close ends the connection. A read-only transaction is rolled back. A
// second call does nothing.
func (c *Conn) Close() error {
	return c.close()
}
