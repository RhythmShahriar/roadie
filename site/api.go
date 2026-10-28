// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package site parses --from and --to, opens local, ssh, WP-CLI alias and
// DSN sites, detects their stack and guards targets: Roadie never writes
// to production or to its own source.
//
// The exported names in this file are the frozen cross-lane API; new
// names are added, never changed.
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
	// FixCollation is --fix-collation, passed to every import the site runs.
	FixCollation bool
	// BackupDir is where Backup writes for local and DSN targets; "" is
	// .roadie/backups in the working folder.
	BackupDir string
}

// Open opens the site an endpoint names: a local-site folder, an ssh-site,
// a wp-alias from wp-cli.yml or a DSN (no files, stack sql). A dump file
// or stdio is not a site.
func Open(ctx context.Context, e core.Endpoint, o Options) (core.Site, error) {
	return open(ctx, e, o)
}

// DetectStack tells which stack a site runs: wordpress for wp-config.php
// or wp-includes/version.php, laravel for artisan plus .env, else sql.
func DetectStack(ctx context.Context, s core.Site) (core.Stack, error) {
	return detectStack(ctx, s)
}

// GuardInput is what the target guards look at.
type GuardInput struct {
	From, To         core.Endpoint
	FromSite, ToSite core.Site // nil for dump-file and stdio
	Production       []string
	ProductionURL    string
	TargetIsStaging  string
	Mode             string // "copy", "prepare" or "plan"
	// URL is --url, the staging URL.
	URL string
	// SiteURL is the target's siteurl when the caller already read it;
	// "" makes Guard read it through WP-CLI or the options table.
	SiteURL string
	// SourceURLs are the source's own site URLs (siteurl, home, APP_URL)
	// once pass 1 has read them. A copy refuses a target that answers
	// to one of them: that is production's URL even when production:
	// is empty. One equal to --url or on this machine doesn't count.
	SourceURLs []string
}

// Guard refuses a target that is production or the source, with the
// *core.Error codes of PRD 10.4.
func Guard(ctx context.Context, in GuardInput) error {
	return guard(ctx, in)
}

// SiteURL is the site's siteurl (home when siteurl is empty), through
// WP-CLI or the options table; "" when neither tells.
func SiteURL(ctx context.Context, s core.Site) string {
	u, _ := readSiteURL(ctx, s)
	return u
}

// Lock takes the run lock for target; unlock releases it.
func Lock(target core.Endpoint) (unlock func(), err error) {
	return LockNotify(target, nil)
}
