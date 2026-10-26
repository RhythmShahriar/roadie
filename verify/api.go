// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package verify scans a copy for real people, live keys, production URLs
// and side effects left on. It returns the findings, checks and coverage
// a pass or fail verdict rests on.
//
// The exported names in this file are the frozen cross-lane API; the
// detect lane replaces the bodies, which return core.ErrNotBuilt until then.
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
	Baseline    string
	Ignore      []Ignore
	Jobs        int
	Clock       core.Clock
}

// Ignore hides a finding by ID and location until a date.
type Ignore struct {
	ID, Location, Reason string
	Until                time.Time
}

// Run scans the copy.
func Run(ctx context.Context, j Job) (*core.VerifyResult, error) {
	return nil, core.ErrNotBuilt
}

// Soundcheck runs rhythm-soundcheck against url when it is installed.
func Soundcheck(ctx context.Context, url, echoURL string) *core.SoundcheckResult {
	return nil
}
