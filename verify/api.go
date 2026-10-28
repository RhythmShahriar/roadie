// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package verify scans a copy for real people, live keys, production URLs
// and side effects left on. It returns the findings, checks and coverage
// a pass or fail verdict rests on.
//
// The exported names in this file are the cross-lane API other lanes
// build on; new fields are only ever added.
package verify

import (
	"context"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
)

// Job is one verify run.
type Job struct {
	Target          core.Endpoint
	Site            core.Site   // optional; opened from Target when nil
	Source          core.Source // optional: the written copy to scan; opened from Target when nil
	Known           core.Known  // nil means heuristic mode
	Production      []string
	ProductionPairs []core.Pair // production's wp-config.php/.env pairs (exact mode): config.salts, config.points-at-production
	Rules           []core.Rule // covering rules, for column kinds and uncovered
	Steps           []core.Step // kill-switch and exposure checks
	Opts            Options
	Progress        core.Progress

	// ScanTable says which tables the uncovered-column scan reads; nil
	// reads them all. A copy leaves out the tables it truncated or
	// dropped (PRD 15.2) and, for prepare --only without data, every
	// table: no column rule ran.
	ScanTable func(table string) bool
	// Stack is what a standalone verify found the copy to be, for the
	// report; verify itself doesn't read it.
	Stack core.StackInfo
	// URL is the URL a standalone verify's site calls itself (siteurl or
	// APP_URL), "" when it says none: the screen names the copy by its
	// host. verify itself doesn't read it.
	URL string
}

// Options are verify's flags and config.
type Options struct {
	FailOn core.Severity
	// Sample, when above 0, scans only the first Sample rows of each
	// table. It limits scanning, not reading: the source has no row
	// limit yet, so the rest of each table is still read and dropped.
	Sample      int
	Skip        []string
	Allow       detect.Allow
	SiteURL     string
	InsecureTLS string
	EchoURL     string
	// EchoToken is sent to an Echo that asks for one (it listens beyond
	// loopback); the CLI reads it from RHYTHM_ECHO_TOKEN, as Echo's own
	// tools do. It never appears in a finding or a report.
	EchoToken string
	Echo      bool
	// EchoSince is when the copy started: verify --echo counts the
	// messages Echo let through from then on. Zero means unknown: the
	// count starts when verify does, which the check's detail says.
	EchoSince time.Time
	Baseline  string
	Ignore    []Ignore
	Jobs      int
	Clock     core.Clock
	// MaxValue and MaxRatio cap a dump target's largest value and its
	// decompressed growth, as --max-value and --max-output-ratio do for
	// copy; 0 is the dump package's default.
	MaxValue int64
	MaxRatio int
}

// Ignore hides a finding by ID and location until a date.
type Ignore struct {
	ID, Location, Reason string
	Until                time.Time
}

// Soundcheck runs rhythm-soundcheck against url when it is installed.
//
// It runs "rhythm-soundcheck run <url> --json" (with --echo=<echoURL>
// when given) for at most 10 minutes and records the exit code and the
// schema and summary counts of its JSON. When Soundcheck is not on PATH
// the result has Ran false and the note "Soundcheck not run: install it
// with rhythm install soundcheck", never a finding. SoundcheckFinding
// turns an exit 1 into soundcheck.failed. url is read as --site is (a
// bare host means https) and always passed on with its scheme, so it
// can't be read as a flag; anything else, or a value that starts with
// "-", gets the note SoundcheckNotURL and no run.
func Soundcheck(ctx context.Context, url, echoURL string) *core.SoundcheckResult {
	return soundcheck(ctx, url, echoURL)
}
