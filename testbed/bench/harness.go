// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

// Harness runs scenarios against the testbed (qa.md 6.3):
//
//  1. reset: reseed the input from its template, clear the sinkhole and
//     stub logs, reset staging to its fresh install
//  2. run Roadie as the manifest says, with HOME, XDG and RHYTHM folders
//     under testbed/bench/work/<id>/ and secrets from runtime/ files
//  3. the oracle on the output (gates 1 and 2)
//  4. the side-effect drill (gate 3)
//  5. the boot check
//  6. Roadie's own verify and report check (gate 4)
//  7. score: caught when every gate held and nothing forbidden appeared
type Harness struct {
	Bin      string // rhythm-roadie for Linux (containers run it too)
	Runtime  string // testbed/runtime
	Fixtures string // testbed/fixtures
	Work     string // testbed/bench/work
	Seed     int64
	PHP      oracle.PHP
	DrillFor time.Duration
	Rounds   int // control rounds on each good input
	// PerRun leaves the consistency key per run. By default every copy
	// and prepare runs with a stable key (ROADIE_KEY and --keep-key), so
	// two benchmark runs give byte-identical outputs.
	PerRun bool
	// NeedDocker is false when every chosen scenario runs on this machine
	// without the testbed (the control pass on dump inputs).
	NeedDocker bool
	Ex         Execer
	Log        io.Writer
	// Tools are the other tools to judge after the scenarios (ToolNames),
	// with their committed configuration in ToolsDir (testbed/tools).
	Tools    []string
	ToolsDir string
	// GreenmaskVersion is the Greenmask release judged (testbed/Makefile).
	GreenmaskVersion string

	db       map[string]string // runtime/db.env
	echoTok  string
	signKey  string
	keys     [2]string // ROADIE_KEY, then the other key for r21
	sshPath  string
	control  Control
	kept     []keptOutput
	manifest map[string]manifest.Scenario
	perf     *Perf
	dockerUp bool
}

// keptOutput is an r-scenario's output dump g05 verifies again.
type keptOutput struct {
	ID   string // r01, r18/latin1
	Path string
}

// Init reads the runtime secrets (made at make up) and creates the
// harness's own keys under runtime/ when missing: the signing key and
// two consistency keys, base64 of 32 random bytes, 0600.
func (h *Harness) Init(ctx context.Context) error {
	if h.Ex == nil {
		h.Ex = dockerExec{}
	}
	if h.Log == nil {
		h.Log = io.Discard
	}
	if h.Rounds <= 0 {
		h.Rounds = 5
	}
	h.control = Control{Rounds: h.Rounds, ByInput: map[string]int{}}
	if err := os.MkdirAll(h.Work, 0o700); err != nil {
		return err
	}
	var err error
	for i, name := range []string{"signing.key", "roadie.key"} {
		v, err := secretFile(filepath.Join(h.Runtime, name))
		if err != nil {
			return err
		}
		if i == 0 {
			h.signKey = v
		} else {
			h.keys[0] = v
		}
	}
	if h.keys[1], err = secretFile(filepath.Join(h.Runtime, "roadie-other.key")); err != nil {
		return err
	}
	if !h.NeedDocker {
		return nil
	}
	if h.db, err = readEnvFile(filepath.Join(h.Runtime, "db.env")); err != nil {
		return fmt.Errorf("the testbed isn't set up (run make -C testbed up): %w", err)
	}
	env, err := readEnvFile(filepath.Join(h.Runtime, "echo.env"))
	if err != nil {
		return fmt.Errorf("the testbed isn't set up (run make -C testbed up): %w", err)
	}
	h.echoTok = env["RHYTHM_ECHO_TOKEN"]
	out, err := docker(ctx, nil, "inspect", "-f", "{{.State.Running}}", container(stagingHost))
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return errors.New("the testbed isn't running: make -C testbed up")
	}
	if h.sshPath, err = exec.LookPath("ssh"); err != nil {
		h.sshPath = ""
	}
	h.dockerUp = true
	return nil
}

// secretFile returns the base64 secret in path, making one first when
// the file doesn't exist. It never prints it.
func secretFile(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own runtime file
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	v := base64.StdEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return v, os.WriteFile(path, []byte(v+"\n"), 0o600)
}

func (h *Harness) logf(format string, a ...any) { fmt.Fprintf(h.Log, format+"\n", a...) }

// RunAll runs the scenarios in manifest order and the control pass and
// returns the results.
func (h *Harness) RunAll(ctx context.Context, f *manifest.File, only []string) Results {
	start := time.Now()
	h.manifest = map[string]manifest.Scenario{}
	res := Results{Version: ResultsVersion, Run: RunInfo{Scenarios: map[string]ScenarioRun{}}}
	for _, s := range f.Scenarios {
		h.manifest[s.ID] = s
	}
	ids := manifest.ScenarioIDs()
	for _, id := range ids {
		s, ok := h.manifest[id]
		if !ok || len(only) > 0 && !slices.Contains(only, id) {
			continue
		}
		h.logf("%s %s", s.ID, s.Title)
		t := time.Now()
		sr, run := h.runScenario(ctx, s)
		run.WallMS = time.Since(t).Milliseconds()
		h.logf("%s %s", s.ID, sr.Outcome)
		res.Scored.Scenarios = append(res.Scored.Scenarios, sr)
		res.Run.Scenarios[s.ID] = run
	}
	slices.Sort(h.control.IDs)
	h.control.IDs = slices.Compact(h.control.IDs)
	res.Scored.Control = h.control
	res.Run.Perf = h.perf
	summarize(&res.Scored, h.manifest)
	res.Run.Meta = collectMeta(ctx, h.Bin, h.Work)
	res.Run.Meta.WallMS = time.Since(start).Milliseconds()
	return res
}

// scenarioState is what one scenario's run shares between its cases.
type scenarioState struct {
	s        manifest.Scenario
	in       string // the seeded input on this machine
	work     string // work/<id>
	canaries []oracle.Canary
	real     map[string]bool
	snapshot string
	window   *Window
	traffic  bool
	cron     bool
}

func (h *Harness) runScenario(ctx context.Context, s manifest.Scenario) (ScenarioResult, ScenarioRun) {
	sr := ScenarioResult{ID: s.ID, Kind: s.Kind, Title: s.Title, Pending: s.Pending, Partial: s.Partial}
	run := ScenarioRun{Cases: map[string]CaseRun{}}
	if s.Pending != "" {
		scoreScenario(&sr)
		return sr, run
	}
	st, err := h.reset(ctx, s)
	if err == nil {
		err = h.setupScenario(ctx, st)
	}
	defer h.teardown(st)
	for _, c := range s.AllCases() {
		var cr CaseResult
		var crun CaseRun
		if err != nil {
			cr = CaseResult{Name: c.Name, Detail: "setup: " + err.Error()}
		} else {
			cr, crun = h.runCase(ctx, st, c)
		}
		cr.score()
		sr.Cases = append(sr.Cases, cr)
		run.Cases[cmpOr(c.Name, "-")] = crun
	}
	scoreScenario(&sr)
	return sr, run
}

// reset reseeds the input and clears the logs (qa.md 6.3 step 1).
func (h *Harness) reset(ctx context.Context, s manifest.Scenario) (*scenarioState, error) {
	st := &scenarioState{s: s, work: filepath.Join(h.Work, s.ID)}
	if err := os.RemoveAll(st.work); err != nil {
		return st, err
	}
	if err := os.MkdirAll(st.work, 0o700); err != nil {
		return st, err
	}
	if s.FromOutputs {
		return st, nil
	}
	st.in = filepath.Join(h.Runtime, "fixtures", s.Input)
	if err := h.reseed(ctx, s.Input); err != nil {
		return st, fmt.Errorf("reseeding %s: %w", s.Input, err)
	}
	if slices.Contains(s.Setup, manifest.SetupEgressPlugin) {
		if err := h.reseed(ctx, "r23"); err != nil {
			return st, fmt.Errorf("reseeding r23: %w", err)
		}
	}
	cs, err := oracle.LoadCanaries(filepath.Join(h.Runtime, "canaries.json"), s.Input)
	if err != nil {
		return st, err
	}
	st.canaries = cs
	st.real = map[string]bool{}
	for _, c := range cs {
		if c.Kind == "email" || c.Kind == "phone" {
			st.real[c.Value] = true
			st.real[strings.ToLower(c.Value)] = true
		}
	}
	for _, log := range []string{"sinkhole.jsonl", "stubs.jsonl"} {
		p := filepath.Join(h.Runtime, "log", log)
		if err := os.Truncate(p, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
			return st, err
		}
	}
	return st, nil
}

// reseed renders one fixture again. The real-tool dumps of r18 (made by
// make formats) and a generated fixture are kept: reseeding gives the
// same data for the same seed, so they stay valid.
func (h *Harness) reseed(ctx context.Context, id string) error {
	dir := filepath.Join(h.Runtime, "fixtures", id)
	fx, err := seed.Fixtures(h.Fixtures)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(fx, func(f seed.Fixture) bool { return f.ID == id })
	if i < 0 {
		return fmt.Errorf("no fixture %s", id)
	}
	if fx[i].Meta.Generated != "" {
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("%s is generated by %s at make up: %w", id, fx[i].Meta.Generated, err)
		}
		return nil
	}
	keep := map[string][]byte{}
	tools, _ := filepath.Glob(filepath.Join(dir, "tool-*.sql"))
	for _, t := range tools {
		b, err := os.ReadFile(t) // #nosec G304 -- the testbed's own runtime file
		if err != nil {
			return err
		}
		keep[filepath.Base(t)] = b
	}
	_, err = seed.Run(ctx, seed.Options{Fixtures: h.Fixtures, Out: h.Runtime, Seed: h.Seed, Only: []string{id}, PHP: h.PHP})
	if err != nil {
		return err
	}
	for name, b := range keep {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// runEnv returns where a case runs.
func (h *Harness) runEnv(st *scenarioState, c manifest.Case, work string) RunEnv {
	if c.Action.Name == manifest.ActionDiskFull {
		return &runnerEnv{bin: h.Bin, in: st.in, work: work, tmpfs: diskFullSize(st.in)}
	}
	switch st.s.Run {
	case manifest.RunStagingWP:
		return &containerEnv{name: st.s.Run, ctr: container(stagingHost), user: "www-data", site: "/var/www/html",
			bin: h.Bin, in: st.in, work: work, ex: h.Ex, dsnHost: "staging-db"}
	case manifest.RunLaravel:
		return &containerEnv{name: st.s.Run, ctr: container("laravel"), user: "root", site: ctrRoot + "/site",
			bin: h.Bin, in: st.in, work: work, ex: h.Ex, dsnHost: "laravel-db"}
	}
	return &hostEnv{bin: h.Bin, in: st.in, work: work, path: filepath.Join(work, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")}
}

// diskFullSize is the tmpfs for r20's disk-full case: a quarter of the
// input, so the copy can't fit.
func diskFullSize(in string) int64 {
	n := size(filepath.Join(in, "db.sql")) / 4
	return max(n, 64<<10)
}

// localVars are the placeholders as the harness sees them on this
// machine, for the oracle and the checks.
func localVars(st *scenarioState, work string) map[string]string {
	return map[string]string{
		"in": st.in, "work": work, "site": filepath.Join(work, "site"),
		"staging_url": stagingURL, "production_url": productionURL, "confirm": confirmName,
		"ssh_source": sshSourceHost, "staging_dsn": "", "output": "",
	}
}

// roadieEnv is the secrets every Roadie run gets, from runtime/ files,
// through the environment only.
func (h *Harness) roadieEnv(key string) []string {
	env := []string{"ROADIE_SIGNING_KEY=" + h.signKey}
	if !h.PerRun && key != "" {
		env = append(env, "ROADIE_KEY="+key)
	}
	if pw := h.db["MYSQL_PASSWORD"]; pw != "" {
		env = append(env, "ROADIE_FROM_PASSWORD="+pw, "ROADIE_TO_PASSWORD="+pw)
	}
	return env
}

// withStableKey adds --keep-key to a copy or prepare, so ROADIE_KEY is
// used (PRD 11.1: a stable key comes from ROADIE_KEY with --keep-key or
// consistency: stable).
func (h *Harness) withStableKey(args []string) []string {
	if h.PerRun || len(args) == 0 || (args[0] != "copy" && args[0] != "prepare") || slices.Contains(args, "--keep-key") {
		return args
	}
	return append(slices.Clone(args), "--keep-key")
}

// hashFile returns the sha256 of a file in hex, or "".
func hashFile(path string) string {
	f, err := os.Open(path) // #nosec G304 -- an output the harness wrote or named
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	hh := sha256.New()
	if _, err := io.Copy(hh, f); err != nil {
		return ""
	}
	return hex.EncodeToString(hh.Sum(nil))
}
