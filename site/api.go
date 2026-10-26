// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package site parses --from and --to, opens local, ssh, WP-CLI alias and
// DSN sites, detects their stack and guards targets: Roadie never writes
// to production or to its own source.
//
// The exported names in this file are the frozen cross-lane API; the site
// lane replaces the bodies, which return core.ErrNotBuilt until then.
package site

import (
	"context"

	"github.com/RhythmShahriar/roadie/core"
)

// ParseEndpoint parses one --from or --to value (PRD 6.2). A value of no
// known shape, or an ssh host that could be read as an option, is a
// kit cli.UsageError (exit 2). A DSN password moves from the value into
// Password and Display shows "•••" in its place; the caller fills
// Password from ROADIE_FROM_PASSWORD or ROADIE_TO_PASSWORD when the value
// has none. For a server DSN, Path is the database name.
func ParseEndpoint(s string) (core.Endpoint, error) {
	return parseEndpoint(s)
}

// Options configures Open.
type Options struct {
	ReadOnly  bool   // a source: every write is refused
	WP        string // remote.wp, the path of wp on the remote host
	BatchMode bool   // ssh -o BatchMode=yes: never prompt
}

// Open opens the site an endpoint names.
func Open(ctx context.Context, e core.Endpoint, o Options) (core.Site, error) {
	return nil, core.ErrNotBuilt
}

// DetectStack tells which stack a site runs.
func DetectStack(ctx context.Context, s core.Site) (core.Stack, error) {
	return "", core.ErrNotBuilt
}

// GuardInput is what the target guards look at.
type GuardInput struct {
	From, To         core.Endpoint
	FromSite, ToSite core.Site // nil for dump-file and stdio
	Production       []string
	ProductionURL    string
	TargetIsStaging  string
	Mode             string
}

// Guard refuses a target that is production or the source, with the
// *core.Error codes of PRD 10.4.
func Guard(ctx context.Context, in GuardInput) error {
	return core.ErrNotBuilt
}

// Lock takes the run lock for target; unlock releases it.
func Lock(target core.Endpoint) (unlock func(), err error) {
	return nil, core.ErrNotBuilt
}
