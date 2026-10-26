// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package engine runs plan, copy and prepare: it reads the source, applies
// the compiled rules in parallel while keeping input order, writes the
// target, turns side effects off and hands the copy to verify.
//
// The exported names in this file are the frozen cross-lane API; the
// engine lane replaces the bodies, which return core.ErrNotBuilt until then.
package engine

import (
	"context"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/verify"
)

// Mode is the command being run.
type Mode string

// The modes.
const (
	Plan    Mode = "plan"
	Copy    Mode = "copy"
	Prepare Mode = "prepare"
)

// Options holds every copy, prepare and plan flag of PRD 6.4, 6.5 and 6.7;
// the CLI fills it from flags over config.
type Options struct {
	URL              string
	URLMap           map[string]string
	Stack            string
	Sites            []int
	SchemaFile       string
	KeepUsers        []string
	KeepKey          bool
	EmailDomain      string
	AdminPasswordEnv string
	AllowUncovered   bool
	Drop, Truncate   []string
	OnePass          bool
	KeepRoutines     bool
	KeepAppKey       bool
	Keys             string
	NoKillSwitch     bool
	EchoURL          string
	EchoProxy        string
	NoIndex          bool // true = --noindex=off
	BasicAuth        string
	ConfirmTarget    string
	TargetIsStaging  string
	Yes              bool
	FixCollation     bool
	Jobs             int
	MaxMemory        int64
	MaxValue         int64
	MaxOutputRatio   int
	NoVerify         bool
	FailOn           core.Severity
	Soundcheck       bool
	Only             []string // prepare
	ProductionURL    string   // prepare
	NoBackup         bool     // prepare
	SchemaOnly       bool     // plan
	Production       []string
	Offline          bool
}

// Job is one run.
type Job struct {
	Mode     Mode
	From, To core.Endpoint
	Config   *roadiecfg.Config
	Opts     Options
	Confirm  func(question, expect string) (bool, error) // nil: no terminal
	Progress core.Progress
	Clock    core.Clock
}

// Run runs the job.
func Run(ctx context.Context, j Job) (*core.Result, error) {
	return nil, core.ErrNotBuilt
}

// VerifyJob builds a standalone verify: opens the target read-only for its schema, detects stack and
// profiles, compiles the covering rules and the kill-switch Steps; with from set it runs pass 1 over
// production read-only to fill Known and ProductionPairs (exact mode).
func VerifyJob(ctx context.Context, j Job, target core.Endpoint, from *core.Endpoint) (verify.Job, error) {
	return verify.Job{}, core.ErrNotBuilt
}
