// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command bench is the testbed's harness (PRD 22, qa.md 6.3). It runs
// every scenario of testbed/scenarios.yaml against the sealed Docker
// testbed, judges each with the independent oracle, the side-effect
// drill, the boot check and Roadie's own verify, runs the control pass,
// judges the other tools on the same inputs (testbed/tools, -tools) and
// writes testbed/bench/work/results.json and docs/benchmark.md:
//
//	go run -tags testbed ./testbed/bench [-bin bin/rhythm-roadie] [-strict] [-only r01,v01] [-tools all|none|wp-cli,...]
//	go run -tags testbed ./testbed/bench -diff first/results.json second/results.json
//	go run -tags testbed ./testbed/bench -render testbed/bench/work/results.json [-doc docs/benchmark.md]
//	go run -tags testbed ./testbed/bench -drill [-for 5m]
//
// It needs the testbed up (make -C testbed up) and prints canary IDs and
// masked keys only, never a value. Exit codes: 0 every scenario that ran
// was caught and the control pass had no false alarm, 1 something was
// missed (or -diff found a difference, or -strict found a case Roadie
// answered not-built or a control pass with no check), 2 usage, 3 the testbed or the
// binary couldn't be used.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Ports the gate publishes (testbed/gate/nginx.conf).
const (
	gateStaging = "http://127.0.0.1:18921"
	gateEcho    = "http://127.0.0.1:18924"
)

// defaultPHP runs PHP for the integrity check in the image testbed/Makefile
// pins, with no network.
const defaultPHP = "docker run --rm -i --network none php:8.3-cli@sha256:98d555ff97b3865140ad31032508c3ca089fa97a8944b14ca9dc03ea05d788cd php"

type options struct {
	bin, root, runtime, work, manifest, only, php string
	seed                                          int64
	rounds                                        int
	drillFor                                      time.Duration
	perRun                                        bool
	baseline                                      string
	tools, greenmask, doc, render                 string
	strict                                        bool
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("bench", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var o options
	fl.StringVar(&o.bin, "bin", "", "rhythm-roadie for Linux (default: build one from this checkout with CGO_ENABLED=0)")
	fl.StringVar(&o.root, "root", ".", "the repository root")
	fl.StringVar(&o.runtime, "runtime", "testbed/runtime", "the testbed's runtime folder (logs, secrets, seeded fixtures)")
	fl.StringVar(&o.work, "work", "testbed/bench/work", "where each scenario's work folder and results.json go")
	fl.StringVar(&o.manifest, "manifest", "testbed/scenarios.yaml", "the scenario manifest")
	fl.StringVar(&o.only, "only", "", "comma-separated scenario IDs (default: all)")
	fl.StringVar(&o.php, "php", defaultPHP, "the PHP command for the integrity check, split on spaces")
	fl.Int64Var(&o.seed, "seed", 1, "the seed of the canaries, keys and kill points")
	fl.IntVar(&o.rounds, "rounds", 5, "control rounds on each good input")
	fl.DurationVar(&o.drillFor, "drill-for", 5*time.Minute, "how long the side-effect drill runs after each prepare and copy")
	fl.BoolVar(&o.perRun, "per-run", false, "leave the consistency key per run (outputs then differ between runs)")
	fl.StringVar(&o.baseline, "baseline", "", "an earlier results.json: fail when r17's MB/s drops more than 20% below it")
	fl.StringVar(&o.tools, "tools", "all", "the other tools to judge after the scenarios: "+strings.Join(ToolNames(), ", ")+", all or none")
	fl.StringVar(&o.greenmask, "greenmask", "0.2.26", "the Greenmask release judged (testbed/Makefile pins it)")
	fl.StringVar(&o.doc, "doc", "", "where to write the benchmark page (default: docs/benchmark.md for a run of every scenario or -render, none with -only); none for no page")
	fl.StringVar(&o.render, "render", "", "write the benchmark page from this results.json without running anything")
	fl.BoolVar(&o.strict, "strict", false, "also fail when Roadie answers not-built for a case or the control pass checks nothing")
	diff := fl.Bool("diff", false, "compare the scored parts of two results.json files (the arguments)")
	drill := fl.Bool("drill", false, "run only the side-effect drill on the running staging and print gate 3")
	fl.DurationVar(&o.drillFor, "for", 5*time.Minute, "with -drill: how long the drill runs")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()
	switch {
	case *diff:
		if fl.NArg() != 2 {
			fmt.Fprintln(stderr, "bench: -diff takes two results.json files")
			return 2
		}
		same, err := DiffResults(fl.Arg(0), fl.Arg(1), stdout)
		if err != nil {
			fmt.Fprintln(stderr, "bench:", err)
			return 3
		}
		if !same {
			return 1
		}
		return 0
	case *drill:
		return runDrill(ctx, o, stdout, stderr)
	case o.render != "":
		return renderDoc(o, stdout, stderr)
	}
	return runBench(ctx, o, stdout, stderr)
}

func runBench(ctx context.Context, o options, stdout, stderr io.Writer) int {
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(o.root, p)
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	o.root = root
	f, err := manifest.Check(abs(o.manifest), abs("testbed/fixtures"), core.KnownID)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	var only []string
	if o.only != "" {
		only = strings.Split(o.only, ",")
		for _, id := range only {
			if _, ok := f.Scenario(id); !ok {
				fmt.Fprintf(stderr, "bench: -only %s: no such scenario\n", id)
				return 2
			}
		}
	}
	tools, err := ParseTools(o.tools)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 2
	}
	h := &Harness{Runtime: abs(o.runtime), Fixtures: abs("testbed/fixtures"), Work: abs(o.work), Seed: o.seed,
		PHP: oracle.PHP{Cmd: strings.Fields(o.php)}, DrillFor: o.drillFor, Rounds: o.rounds, PerRun: o.perRun,
		NeedDocker: needsDocker(f, only) || toolsNeedDocker(tools, only), Log: stderr,
		Tools: tools, ToolsDir: abs("testbed/tools"), GreenmaskVersion: o.greenmask}
	if h.Bin, err = binary(ctx, o, h.Work); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	if err := h.Init(ctx); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	res := h.RunAll(ctx, f, only)
	res.Tools = h.RunTools(ctx, only)
	if o.baseline != "" {
		if err := CompareBaseline(res.Run.Perf, abs(o.baseline)); err != nil {
			fmt.Fprintln(stderr, "bench: -baseline:", err)
			return 3
		}
	}
	path := filepath.Join(h.Work, "results.json")
	if err := WriteResults(path, res); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	s := res.Scored.Summary
	fmt.Fprintf(stdout, "%d scenarios: %d caught, %d missed, %d error, %d pending\n",
		s.Scenarios, s.Outcomes[Caught], s.Outcomes[Missed], s.Outcomes[Errored], s.Outcomes[Pending])
	fmt.Fprintf(stdout, "Roadie left %d of %d seeded personal values and %d of %d live keys; verify caught %d of %d unsafe stagings with %d false alarms in %d checks of clean ones.\n",
		s.CanarySurvivors, s.Canaries, s.LiveKeysSurvived, s.LiveKeys, s.VerifyRecall, s.VerifyBad, s.FalseAlarms, s.ControlChecks)
	if p := res.Run.Perf; p != nil {
		for _, r := range p.Runs {
			fmt.Fprintf(stdout, "r17 %s: %d MB of SQL in %.1f s, %.1f MB/s, peak RSS %d MB\n", r.Input, r.InputBytes/1e6, float64(r.WallMS)/1000, r.MBps, r.MaxRSSKB/1024)
		}
		for _, n := range p.Notes {
			fmt.Fprintln(stdout, "r17:", n)
		}
	}
	for _, t := range res.Tools {
		if t.NotRun != "" {
			fmt.Fprintf(stdout, "%s: not run (%s)\n", t.Title, t.NotRun)
			continue
		}
		for _, in := range t.Inputs {
			fmt.Fprintf(stdout, "%s on %s: %s (%s)\n", t.Title, in.Input, in.Outcome, in.Detail)
		}
	}
	fmt.Fprintln(stdout, "results:", path)
	if doc := docPath(o); doc != "" {
		if err := writeDocFile(abs(doc), res, f); err != nil {
			fmt.Fprintln(stderr, "bench:", err)
			return 3
		}
		fmt.Fprintln(stdout, "page:", doc)
	}
	fail := s.Outcomes[Missed] > 0 || s.Outcomes[Errored] > 0 || s.FalseAlarms > 0 || res.Run.Perf != nil && !res.Run.Perf.Pass
	if o.strict {
		for _, p := range StrictProblems(res.Scored, h.manifest) {
			fmt.Fprintln(stdout, "strict:", p)
			fail = true
		}
	}
	if fail {
		return 1
	}
	return 0
}

// docPath is where the page goes: -doc, else docs/benchmark.md when the
// run covers every scenario (a partial run never replaces the page).
func docPath(o options) string {
	switch {
	case o.doc == "none":
		return ""
	case o.doc != "":
		return o.doc
	case o.only == "" || o.render != "":
		return "docs/benchmark.md"
	}
	return ""
}

// renderDoc writes the page from an earlier results.json (-render).
func renderDoc(o options, stdout, stderr io.Writer) int {
	r, err := ReadResults(o.render)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	f, err := manifest.Load(filepath.Join(o.root, o.manifest))
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	doc := docPath(o)
	if doc == "" {
		fmt.Fprintln(stderr, "bench: -render with -doc none writes nothing")
		return 2
	}
	if !filepath.IsAbs(doc) {
		doc = filepath.Join(o.root, doc)
	}
	if err := writeDocFile(doc, r, f); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	fmt.Fprintln(stdout, "page:", doc)
	return 0
}

// needsDocker reports whether any chosen scenario needs the testbed.
func needsDocker(f *manifest.File, only []string) bool {
	for _, s := range f.Scenarios {
		if len(only) > 0 && !slices.Contains(only, s.ID) {
			continue
		}
		if s.Run != manifest.RunHost || len(s.Setup) > 0 || s.Drill || s.Boot || len(s.ImportInto) > 0 || s.FromOutputs {
			return true
		}
		for _, c := range s.AllCases() {
			if len(c.Setup) > 0 || c.Action.Name != "" || c.Output.DB != "" || c.Expect.Integrity {
				return true
			}
		}
	}
	return false
}

// binary returns -bin, or builds rhythm-roadie for Linux without cgo
// into the work folder (a static binary runs in every container).
func binary(ctx context.Context, o options, work string) (string, error) {
	if o.bin != "" {
		p, err := filepath.Abs(o.bin)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("-bin: %w", err)
		}
		return p, nil
	}
	out := filepath.Join(work, "bin", "rhythm-roadie")
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/usr/local/go/bin/go"
	}
	cmd := exec.CommandContext(ctx, goBin, "build", "-o", out, "./cmd/rhythm-roadie") // #nosec G204 -- go build of this checkout
	cmd.Dir = o.root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building rhythm-roadie: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return out, nil
}

func runDrill(ctx context.Context, o options, stdout, stderr io.Writer) int {
	env, err := readEnvFile(filepath.Join(o.runtime, "echo.env"))
	if err != nil {
		fmt.Fprintln(stderr, "bench: the testbed isn't set up (make -C testbed up):", err)
		return 3
	}
	w := &Window{
		Logs: Logs{Sinkhole: filepath.Join(o.runtime, "log", "sinkhole.jsonl"), Stubs: filepath.Join(o.runtime, "log", "stubs.jsonl")},
		Echo: Echo{Base: gateEcho, Token: env["RHYTHM_ECHO_TOKEN"]},
	}
	ex := dockerExec{}
	paths, cookies := stagingPages(ctx, ex)
	t := Traffic{Base: gateStaging, Host: stagingHost, Paths: paths, Admin: []string{"/wp-admin/"}, Cookies: cookies, Every: 500 * time.Millisecond}
	if err := w.Start(ctx, t, 0); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	dr := Drill{Exec: ex, For: o.drillFor, Pause: 5 * time.Second}.Run(ctx)
	traffic := w.Stop()
	g, err := w.Judge(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(struct {
		Gate3   Gate3        `json:"gate3"`
		Drill   DrillResult  `json:"drill"`
		Traffic TrafficStats `json:"traffic"`
		Pass    bool         `json:"pass"`
	}{g, dr, traffic, g.Pass()})
	if !g.Pass() {
		return 1
	}
	return 0
}
