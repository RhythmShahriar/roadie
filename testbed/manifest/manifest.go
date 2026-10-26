// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package manifest reads testbed/scenarios.yaml, the list of every
// testbed scenario (PRD 22.2): what kind it is, which fixture it reads,
// the rhythm-roadie command it runs and the gates it must pass.
//
// Expectations match by finding ID and error code, never by wording, so a
// reworded message can't make a scenario pass or fail. The harness
// (testbed/bench) runs what the manifest says; this package only reads
// and checks it, so tests can do that without Docker.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version is the scenarios.yaml version this package reads.
const Version = 1

// The scenario kinds (qa.md 6.2).
const (
	KindCopy    = "copy"    // production input to a new staging
	KindPrepare = "prepare" // an existing raw staging made safe in place
	KindVerify  = "verify"  // a staging Roadie did not make; verify's verdict
	KindEnv     = "env"     // the environment around Roadie goes wrong
)

// Where Roadie runs. A container run puts the binary, the seeded input
// and the site inside that container, on the sealed network.
const (
	RunHost      = "host"       // this machine, against the gate's ports
	RunStagingWP = "staging-wp" // the staging WordPress container
	RunLaravel   = "laravel"    // the Laravel container
	RunRunner    = "runner"     // a throwaway container, no network at all
)

// Setup steps the harness takes before Roadie runs.
const (
	// SetupStagingSite resets the staging docroot, overlays the fixture's
	// files/ and writes a plain wp-config.php when the fixture has none.
	SetupStagingSite = "staging-site"
	// SetupStagingDB loads the input's db.sql into staging-db.
	SetupStagingDB = "staging-db"
	// SetupProdDB loads the input's db.sql into prod-db (the ssh source).
	SetupProdDB = "prod-db"
	// SetupSiteDir renders the fixture's files/ into work/<id>/site with
	// the database reachable from this machine (a host run).
	SetupSiteDir = "site-dir"
	// SetupLaravelSite puts the fixture's files/ into the Laravel
	// container with the database on the sealed network.
	SetupLaravelSite = "laravel-site"
	// SetupLaravelDB loads the input's db.sql into laravel-db.
	SetupLaravelDB = "laravel-db"
	// SetupConfig writes ${work}/roadie.yaml with url: and production:.
	SetupConfig = "config"
	// SetupUnknownHost gives the run an empty known_hosts.
	SetupUnknownHost = "unknown-host"
	// SetupHostCron starts the host-cron container (wp-cron.php every 30 s).
	SetupHostCron = "host-cron"
	// SetupTraffic runs the traffic generator for the whole Roadie run.
	SetupTraffic = "traffic"
	// SetupEchoDown stops Echo for the run and starts it again after.
	SetupEchoDown = "echo-down"
	// SetupEgressPlugin installs r23's egress drill plugin on staging.
	SetupEgressPlugin = "egress-plugin"
	// SetupSnapshot hashes the staging database and docroot before the
	// run, for expect.unchanged.
	SetupSnapshot = "snapshot"
	// SetupReadOnlyUser points the staging wp-config.php at a database
	// user with SELECT only.
	SetupReadOnlyUser = "readonly-user"
	// SetupProductionConfig writes a .roadie/config.yaml whose
	// production: list names the staging host.
	SetupProductionConfig = "production-config"
	// SetupEnvProduction defines WP_ENVIRONMENT_TYPE production in the
	// staging wp-config.php.
	SetupEnvProduction = "env-production"
)

// Actions the harness takes while or after Roadie runs.
const (
	ActionInterrupt = "interrupt"  // SIGINT at At of a timed baseline run
	ActionSSHCut    = "ssh-cut"    // kill the remote export at At
	ActionDiskFull  = "disk-full"  // the target folder is a 1 MiB tmpfs
	ActionSIGKILL   = "sigkill"    // SIGKILL at a random point, Runs times
	ActionWatch     = "watch"      // scan the local disk for canaries during the run
	ActionTwice     = "twice"      // run again with the same key, outputs must match
	ActionRekey     = "rekey"      // run again with another key, no fake may carry over
	ActionEgress    = "egress"     // run wp testbed-egress after the run
	ActionVerifyAll = "verify-all" // verify every kept r-scenario output (g05)
)

// Files the harness makes for a case instead of the seeder.
const (
	MakeGzipBomb = "gzip-bomb" // a small gzip that inflates past any sane ratio
	MakeWpress   = "wpress"    // an All-in-One WP Migration archive named .sql
)

// File is testbed/scenarios.yaml. Top-level keys starting with x- hold
// YAML anchors the scenarios refer to; any other extra key is an error.
type File struct {
	Version   int            `yaml:"version"`
	Scenarios []Scenario     `yaml:"scenarios"`
	Anchors   map[string]any `yaml:",inline"`
}

// Scenario is one entry of PRD 22.2. A scenario with Cases runs each case
// and is caught when every case is; one without runs its own Command.
type Scenario struct {
	ID    string `yaml:"id"`
	Kind  string `yaml:"kind"`
	Stack string `yaml:"stack"`
	Title string `yaml:"title"`
	// Input is the fixture under testbed/fixtures whose seeded copy the
	// scenario reads. Empty only with FromOutputs.
	Input string `yaml:"input"`
	// FromOutputs (g05) reads the kept outputs of the r-scenarios instead.
	FromOutputs bool `yaml:"from_outputs"`
	// Good marks a clean input: the control pass verifies it five times
	// and any finding at or above low is a false alarm.
	Good bool `yaml:"good"`

	Run string `yaml:"run"`
	// Multisite (subdirectory or subdomain) makes the harness's staging
	// wp-config.php a multisite one.
	Multisite string `yaml:"multisite"`
	// StableKey runs Roadie with ROADIE_KEY set from runtime/, so fakes
	// stay the same across runs (r21).
	StableKey bool `yaml:"stable_key"`
	// Perf records wall time, MB/s and peak RSS for the benchmark (r17).
	Perf bool `yaml:"perf"`
	// Setup runs once before the first case.
	Setup []string `yaml:"setup"`

	// The rest are the defaults of every case (see AllCases).
	Command  []string  `yaml:"command"`
	Action   Action    `yaml:"action"`
	Output   Output    `yaml:"output"`
	Verify   []string  `yaml:"verify"`
	WPChecks []WPCheck `yaml:"wp_checks"`
	Expect   Expect    `yaml:"expect"`
	Forbid   []string  `yaml:"forbid"`
	Pending  string    `yaml:"pending"`

	// Boot asks for the boot check after each case that has gate 1: home,
	// shop, cart and checkout (and every multisite blog's home) answer 200.
	Boot bool `yaml:"boot"`
	// Drill runs the side-effect drill on staging after each case that
	// has gate 3.
	Drill bool `yaml:"drill"`
	// Joins are columns whose matching rows must stay the same (r16).
	Joins []Join `yaml:"joins"`
	// ImportInto loads each case's output dump into a scratch database:
	// mysql (prod-db, MySQL 8.4) and mariadb (staging-db, MariaDB 11.4).
	ImportInto []string `yaml:"import_into"`
	// Absent are path globs that must not exist after any case, on this
	// machine or in the run's container (r24's markers).
	Absent []string `yaml:"absent"`
	// Partial lists what the testbed can't check of this scenario yet; it
	// is printed next to the score, never hidden.
	Partial []string `yaml:"partial"`

	Cases []Case `yaml:"cases"`
}

// Case is one run of a scenario with several (r20, r24, r25 and so on).
// An empty field takes the scenario's value.
type Case struct {
	Name     string    `yaml:"name"`
	Setup    []string  `yaml:"setup"`
	Command  []string  `yaml:"command"`
	Action   Action    `yaml:"action"`
	Make     string    `yaml:"make"`
	Output   Output    `yaml:"output"`
	Verify   []string  `yaml:"verify"`
	WPChecks []WPCheck `yaml:"wp_checks"`
	Expect   Expect    `yaml:"expect"`
	Forbid   []string  `yaml:"forbid"`
	Pending  string    `yaml:"pending"`
}

// WPCheck is a check run with WP-CLI in the staging container: PHP for
// wp eval, or a WP-CLI command. It passes when it exits 0.
type WPCheck struct {
	Name string   `yaml:"name"`
	PHP  string   `yaml:"php"`
	WP   []string `yaml:"wp"`
}

// Join is a pair of table.column names: the rows of Right whose value is
// one of Left's values must be as many after the copy as before.
type Join struct {
	Left  string `yaml:"left"`
	Right string `yaml:"right"`
}

// Action is something the harness does to a run.
type Action struct {
	Name string  `yaml:"name"`
	At   float64 `yaml:"at"`   // fraction of a baseline run, 0 to 1
	Runs int     `yaml:"runs"` // sigkill: how many runs
}

// Output says where the oracle finds what Roadie wrote. DB is a dump path
// (with placeholders) or a database service to dump; Tree is a folder or
// a service whose site to copy out.
type Output struct {
	DB   string `yaml:"db"`
	Tree string `yaml:"tree"`
}

// The services an Output names instead of a path.
const (
	OutStagingDB  = "staging-db"
	OutLaravelDB  = "laravel-db"
	OutPG         = "pg"
	OutStagingWP  = "staging-wp"
	OutLaravelApp = "laravel-site"
)

// Expect holds the gates. A nil pointer or an empty value is not checked.
type Expect struct {
	// ExitCode is rhythm-roadie's exit code for Command.
	ExitCode *int `yaml:"exit_code"`
	// ErrorCode is data.error.code of a run that stops.
	ErrorCode string `yaml:"error_code"`
	// FindingIDs must each appear in the run's findings (or as its error
	// code or a check's ID).
	FindingIDs []string `yaml:"finding_ids"`
	// Canaries is gate 1: canaries the oracle may find (always 0); it also
	// covers live keys and test keys that changed.
	Canaries *int `yaml:"canaries"`
	// Integrity is gate 2: serialized values still unserialize and row
	// counts match.
	Integrity bool `yaml:"integrity"`
	// RowsOnly is gate 2 for an input without PHP values (PostgreSQL).
	RowsOnly bool `yaml:"rows_only"`
	// Sinkhole is gate 3: sinkhole connections, stub calls with a live key
	// and Echo captures to non-reserved recipients allowed (always 0).
	Sinkhole *int `yaml:"sinkhole"`
	// VerifyExit is gate 4: the exit code of the fresh verify.
	VerifyExit *int `yaml:"verify_exit"`
	// TargetState is data.target_state: untouched, replaced or mixed.
	TargetState string `yaml:"target_state"`
	// Unchanged says the staging database and docroot hash the same after
	// the run as before (needs the snapshot setup).
	Unchanged bool `yaml:"unchanged"`
	// Locations must each be a location of some finding: "table.column"
	// or "file:<path>" (v02, v04, v06).
	Locations []string `yaml:"locations"`
	// NoFinishedOutput says no output file that looks finished and no
	// temp or spool file is left.
	NoFinishedOutput bool `yaml:"no_finished_output"`
}

// AllCases returns the runs of s: its Cases with the scenario's values
// filled in where a case leaves them empty, or s itself as one unnamed
// case.
func (s Scenario) AllCases() []Case {
	if len(s.Cases) == 0 {
		return []Case{{Command: s.Command, Action: s.Action, Output: s.Output, Verify: s.Verify,
			WPChecks: s.WPChecks, Expect: s.Expect, Forbid: s.Forbid, Pending: s.Pending}}
	}
	out := make([]Case, len(s.Cases))
	for i, c := range s.Cases {
		if len(c.Command) == 0 {
			c.Command = s.Command
		}
		if c.Action.Name == "" {
			c.Action = s.Action
		}
		if c.Output == (Output{}) {
			c.Output = s.Output
		}
		if len(c.Verify) == 0 {
			c.Verify = s.Verify
		}
		if len(c.WPChecks) == 0 {
			c.WPChecks = s.WPChecks
		}
		if len(c.Forbid) == 0 {
			c.Forbid = s.Forbid
		}
		if c.Pending == "" {
			c.Pending = s.Pending
		}
		out[i] = c
	}
	return out
}

// Load reads and decodes a scenarios.yaml. Unknown keys are an error, so
// a typo can't drop a gate.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own manifest
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes a scenarios.yaml from b.
func Parse(b []byte) (*File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("scenarios.yaml: %w", err)
	}
	if f.Version != Version {
		return nil, fmt.Errorf("scenarios.yaml: version %d, this harness reads %d", f.Version, Version)
	}
	for k := range f.Anchors {
		if !strings.HasPrefix(k, "x-") {
			return nil, fmt.Errorf("scenarios.yaml: unknown key %q (only x- keys may hold anchors)", k)
		}
	}
	for i := range f.Scenarios {
		if f.Scenarios[i].Run == "" {
			f.Scenarios[i].Run = RunHost
		}
	}
	return &f, nil
}

// Scenario returns the scenario with id.
func (f *File) Scenario(id string) (Scenario, bool) {
	i := slices.IndexFunc(f.Scenarios, func(s Scenario) bool { return s.ID == id })
	if i < 0 {
		return Scenario{}, false
	}
	return f.Scenarios[i], true
}

// Placeholders are the ${name} values a command, an output or a verify
// line may hold; Expand fills them.
var Placeholders = []string{
	"in",             // the seeded fixture directory, as the run sees it
	"site",           // the site Roadie works on
	"work",           // the scenario's work folder, as the run sees it
	"staging_url",    // the staging site's URL (http://staging-wp)
	"production_url", // production's URL (https://shop.example.com)
	"confirm",        // the target name for --confirm-target
	"ssh_source",     // ssh://roadie@127.0.0.1:18925/var/www/html
	"staging_dsn",    // mysql://shop@127.0.0.1:18927/shop (password from the environment)
	"output",         // a kept r-scenario output (g05 only)
}

var placeholderRE = regexp.MustCompile(`\$\{([a-z_]+)\}`)

// Expand replaces every ${name} in s with vars[name]. A name vars lacks
// is an error, so a typo never reaches Roadie as literal text.
func Expand(s string, vars map[string]string) (string, error) {
	var missing []string
	out := placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("no value for ${%s}", strings.Join(missing, "}, ${"))
	}
	return out, nil
}

// ExpandAll expands every argument.
func ExpandAll(args []string, vars map[string]string) ([]string, error) {
	out := make([]string, len(args))
	var errs []error
	for i, a := range args {
		v, err := Expand(a, vars)
		if err != nil {
			errs = append(errs, fmt.Errorf("argument %d %q: %w", i+1, a, err))
		}
		out[i] = v
	}
	return out, errors.Join(errs...)
}
