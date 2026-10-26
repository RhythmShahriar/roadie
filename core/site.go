// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"io/fs"
)

// ErrNoWPCLI is returned by Site.WP when WP-CLI isn't available.
var ErrNoWPCLI = errors.New("wp-cli not available")

// ErrReadOnly is returned by every writing method of a source site.
var ErrReadOnly = errors.New("this endpoint is a read-only source")

// FileInfo is one entry of Site.List.
type FileInfo struct {
	Rel  string // path relative to the site root, with forward slashes
	Size int64
	Dir  bool
}

// Pair is a define() in wp-config.php or a line of .env.
type Pair struct {
	Name   string
	Value  string
	Line   int
	Quoted bool
}

// DBIdentity names the database a site or DSN reaches, for the target guards.
type DBIdentity struct {
	Engine Dialect
	Server string
	Host   string
	Port   int
	Name   string
	User   string
	Prefix string
}

// Stack is the application a site or dump belongs to.
type Stack string

// The stacks Roadie knows.
const (
	StackWordPress Stack = "wordpress"
	StackLaravel   Stack = "laravel"
	StackSQL       Stack = "sql"
)

// Site is a local, ssh, WP-CLI alias or DSN endpoint Roadie reads from or writes to.
type Site interface {
	Endpoint() Endpoint
	Stack() Stack
	ReadOnly() bool
	ReadFile(ctx context.Context, rel string) ([]byte, error)
	// WriteFile writes atomically; ErrReadOnly on a source.
	WriteFile(ctx context.Context, rel string, b []byte, perm fs.FileMode) error
	Remove(ctx context.Context, rel string) error
	Exists(ctx context.Context, rel string) (bool, error)
	List(ctx context.Context, rel string, depth int) ([]FileInfo, error)
	// WP runs an allowlisted WP-CLI command; ErrNoWPCLI when WP-CLI is absent.
	WP(ctx context.Context, args ...string) ([]byte, error)
	DBIdentity(ctx context.Context) (DBIdentity, error)
	// Source is a read-only export; nil tables means all of them.
	Source(ctx context.Context, tables []string) (Source, error)
	// Sink imports into the site's database; ErrReadOnly on a source.
	Sink(ctx context.Context) (Sink, error)
	// RemoteCommands lists every remote command run so far, for plan and tests.
	RemoteCommands() []string
	// Backup writes a gzip dump of the site's database before prepare: local and DSN targets to
	// .roadie/backups/<UTC ts>.sql.gz (0600, folder 0700), ssh and alias targets to
	// ~/.roadie-backups/<UTC ts>.sql.gz on the remote host with umask 077. restore is the command to undo it.
	Backup(ctx context.Context) (path, restore string, err error) // ErrReadOnly on a source
	Close() error
}

// PlannedEdit is one change a Step would make, for plan.
type PlannedEdit struct {
	Step string
	File string
	What string
}

// Step is one kill-switch or exposure step.
type Step interface {
	ID() string    // a side-effect or config finding ID, e.g. "side-effect.cron"
	Phase() string // "files" (before the import) or "after" (after the import)
	Plan(ctx context.Context, s Site) ([]PlannedEdit, error)
	Apply(ctx context.Context, s Site) error
	Check(ctx context.Context, s Site) (SideEffect, []Finding, error)
}
