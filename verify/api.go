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
}

// Options are verify's flags and config.
type Options struct {
	FailOn      core.Severity
	Sample      int
	Skip        []string
	Allow       detect.Allow
	SiteURL     string
	InsecureTLS string
	EchoURL     string
	Echo        bool
	// EchoSince is when the copy started: verify --echo counts the
	// messages Echo let through from then on. Zero means unknown: the
	// count starts when verify does, which the check's detail says.
	EchoSince time.Time
	Baseline  string
	Ignore    []Ignore
	Jobs      int
	Clock     core.Clock
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
