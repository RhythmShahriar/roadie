// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/seed"
)

// Fixture is what Validate needs to know about one fixture directory.
type Fixture struct {
	ID        string
	Stack     string
	Canaries  bool
	Generated bool
	// Files are the names seeding writes under runtime/fixtures/<id>/:
	// "db.sql", "prod.sql", "files/wp-config.php" and so on.
	Files map[string]bool
}

// ReadFixtures lists the fixtures under dir with the files seeding makes
// from each: a template loses its .tmpl suffix, files/ keeps its tree.
func ReadFixtures(dir string) (map[string]Fixture, error) {
	list, err := seed.Fixtures(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]Fixture{}
	for _, f := range list {
		fx := Fixture{ID: f.ID, Stack: f.Meta.Stack, Canaries: f.Meta.Canaries, Generated: f.Meta.Generated != "", Files: map[string]bool{}}
		err := filepath.WalkDir(f.Dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(f.Dir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "fixture.yaml" {
				return nil
			}
			fx.Files[strings.TrimSuffix(rel, ".tmpl")] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
		if fx.Generated {
			// testbed/gen writes the large dump as db.sql.gz.
			fx.Files["db.sql.gz"] = true
		}
		out[f.ID] = fx
	}
	return out, nil
}

// ScenarioIDs returns the PRD 22.2 scenario IDs, in the order they are scored and printed.
func ScenarioIDs() []string {
	var ids []string
	for i := 1; i <= 28; i++ {
		ids = append(ids, fmt.Sprintf("r%02d", i))
	}
	for i := 1; i <= 7; i++ {
		ids = append(ids, fmt.Sprintf("v%02d", i))
	}
	for i := 1; i <= 5; i++ {
		ids = append(ids, fmt.Sprintf("g%02d", i))
	}
	return ids
}

var (
	kinds   = []string{KindCopy, KindPrepare, KindVerify, KindEnv}
	runs    = []string{RunHost, RunStagingWP, RunLaravel, RunRunner}
	setups  = []string{SetupStagingSite, SetupStagingDB, SetupProdDB, SetupSiteDir, SetupLaravelSite, SetupLaravelDB, SetupConfig, SetupUnknownHost, SetupHostCron, SetupTraffic, SetupEchoDown, SetupEgressPlugin, SetupSnapshot, SetupReadOnlyUser, SetupProductionConfig, SetupEnvProduction}
	actions = []string{ActionInterrupt, ActionSSHCut, ActionDiskFull, ActionSIGKILL, ActionWatch, ActionTwice, ActionRekey, ActionEgress, ActionVerifyAll}
	makes   = []string{MakeGzipBomb, MakeWpress}
	outDBs  = []string{OutStagingDB, OutLaravelDB, OutPG}
	outTree = []string{OutStagingWP, OutLaravelApp}
	states  = []string{"untouched", "replaced", "mixed"}
	// Commands rhythm-roadie has (PRD 6.1) that a scenario may run.
	commands = []string{"copy", "prepare", "verify", "plan", "report"}
)

// Validate checks f against the fixtures and returns every problem:
// each PRD 22.2 scenario once and nothing else, a known kind, run and
// setup, an input fixture that exists (and every ${in}/file the commands
// name), gates for every scenario that runs and only finding IDs and
// error codes known says exist (core.KnownID).
func Validate(f *File, fixtures map[string]Fixture, known func(string) bool) []error {
	var errs []error
	bad := func(id, format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", id, fmt.Sprintf(format, a...)))
	}
	seen := map[string]bool{}
	for _, s := range f.Scenarios {
		if seen[s.ID] {
			bad(s.ID, "listed twice")
		}
		seen[s.ID] = true
		validateScenario(s, fixtures, known, bad)
	}
	for _, id := range ScenarioIDs() {
		if !seen[id] {
			bad(id, "missing from scenarios.yaml (PRD 22.2 lists it)")
		}
	}
	for id := range seen {
		if !slices.Contains(ScenarioIDs(), id) {
			bad(id, "not a PRD 22.2 scenario")
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

func validateScenario(s Scenario, fixtures map[string]Fixture, known func(string) bool, bad func(string, string, ...any)) {
	id := s.ID
	if s.Title == "" {
		bad(id, "no title")
	}
	if !slices.Contains(kinds, s.Kind) {
		bad(id, "kind %q, want one of %s", s.Kind, strings.Join(kinds, ", "))
	}
	if s.Stack == "" {
		bad(id, "no stack")
	}
	if !slices.Contains(runs, s.Run) {
		bad(id, "run %q, want one of %s", s.Run, strings.Join(runs, ", "))
	}
	var fx Fixture
	switch {
	case s.FromOutputs && s.Input != "":
		bad(id, "from_outputs and input both set")
	case s.FromOutputs:
	case s.Input == "":
		bad(id, "no input fixture")
	default:
		var ok bool
		if fx, ok = fixtures[s.Input]; !ok {
			bad(id, "input %q is not a fixture under testbed/fixtures", s.Input)
		}
	}
	if s.Good && s.Kind != KindVerify {
		bad(id, "a good (control) input must be a verify scenario")
	}
	if s.Good && fx.Canaries {
		bad(id, "a good input %s plants canaries", s.Input)
	}
	checkSetup(id, s.Setup, bad)
	if slices.Contains(s.Setup, SetupEgressPlugin) {
		if _, ok := fixtures["r23"]; !ok {
			bad(id, "setup egress-plugin needs fixture r23 (the plugin)")
		}
	}
	if s.Multisite != "" && s.Multisite != "subdirectory" && s.Multisite != "subdomain" {
		bad(id, "multisite %q, want subdirectory or subdomain", s.Multisite)
	}
	onStaging := s.Run == RunStagingWP
	if (s.Boot || s.Drill) && !onStaging {
		bad(id, "boot and drill need run: staging-wp")
	}
	for _, j := range s.Joins {
		if !isTableColumn(j.Left) || !isTableColumn(j.Right) {
			bad(id, "join %s = %s: want table.column on both sides", j.Left, j.Right)
		}
	}
	for _, db := range s.ImportInto {
		if db != "mysql" && db != "mariadb" {
			bad(id, "import_into %q, want mysql or mariadb", db)
		}
	}
	for _, a := range s.Absent {
		if !strings.HasPrefix(a, "/") {
			bad(id, "absent %q: want an absolute path", a)
		}
	}
	if len(s.Cases) > 0 && len(s.Command) > 0 {
		bad(id, "both a command and cases: put the command in each case")
	}
	names := map[string]bool{}
	for _, c := range s.AllCases() {
		cid := id
		if c.Name != "" {
			cid = id + "/" + c.Name
			if names[c.Name] {
				bad(cid, "case listed twice")
			}
			names[c.Name] = true
		} else if len(s.Cases) > 0 {
			bad(id, "a case without a name")
		}
		checkSetup(cid, c.Setup, bad)
		for _, a := range [][]string{c.Command, c.Verify, {c.Output.DB, c.Output.Tree}} {
			checkArgs(cid, a, fx, s.Input != "", bad)
		}
		if c.Output.DB != "" && !slices.Contains(outDBs, c.Output.DB) && !strings.Contains(c.Output.DB, "${") {
			bad(cid, "output.db %q is neither a service (%s) nor a path with a placeholder", c.Output.DB, strings.Join(outDBs, ", "))
		}
		if c.Output.Tree != "" && !slices.Contains(outTree, c.Output.Tree) && !strings.Contains(c.Output.Tree, "${") {
			bad(cid, "output.tree %q is neither a service (%s) nor a path with a placeholder", c.Output.Tree, strings.Join(outTree, ", "))
		}
		if len(c.WPChecks) > 0 && !onStaging {
			bad(cid, "wp_checks need run: staging-wp")
		}
		for _, w := range c.WPChecks {
			if w.Name == "" || (w.PHP == "") == (len(w.WP) == 0) {
				bad(cid, "wp check %q needs a name and exactly one of php or wp", w.Name)
			}
		}
		validateCase(cid, s, c, known, bad)
	}
}

// isTableColumn reports whether s is "table.column".
func isTableColumn(s string) bool {
	t, c, ok := strings.Cut(s, ".")
	return ok && t != "" && c != "" && !strings.Contains(c, ".")
}

func checkSetup(id string, steps []string, bad func(string, string, ...any)) {
	for _, st := range steps {
		if !slices.Contains(setups, st) {
			bad(id, "setup %q, want one of %s", st, strings.Join(setups, ", "))
		}
	}
}

// checkArgs checks the placeholders of args and that every ${in}/<file>
// is a file seeding makes for the fixture.
func checkArgs(id string, args []string, fx Fixture, hasInput bool, bad func(string, string, ...any)) {
	for _, a := range args {
		for _, m := range placeholderRE.FindAllStringSubmatch(a, -1) {
			if !slices.Contains(Placeholders, m[1]) {
				bad(id, "unknown placeholder ${%s} in %q", m[1], a)
			}
		}
		rest, ok := strings.CutPrefix(a, "${in}/")
		if !ok || !hasInput {
			continue
		}
		if !fx.Files[rest] && !fx.Files[strings.TrimSuffix(rest, ".gz")] && !isHarnessMade(rest) {
			bad(id, "%q names %s, which seeding fixture %s doesn't make", a, rest, fx.ID)
		}
	}
}

// isHarnessMade reports whether name is a file the harness writes next to
// the seeded ones for a case's make (see MakeGzipBomb and MakeWpress).
// The tool-*.sql dumps of r18 are written by the real dump tools in the
// containers at make up (make formats).
func isHarnessMade(name string) bool {
	switch name {
	case "bomb.sql.gz", "archive.sql", "tool-mysqldump.sql", "tool-mariadb-dump.sql", "tool-wp-db-export.sql":
		return true
	}
	return false
}

func validateCase(id string, s Scenario, c Case, known func(string) bool, bad func(string, string, ...any)) {
	runs := c.Pending == "" && s.Pending == ""
	if runs && len(c.Command) == 0 && c.Action.Name != ActionVerifyAll {
		bad(id, "no command (or a pending reason)")
	}
	if len(c.Command) > 0 && !slices.Contains(commands, c.Command[0]) {
		bad(id, "command %q is not a rhythm-roadie command a scenario runs (%s)", c.Command[0], strings.Join(commands, ", "))
	}
	if len(c.Command) > 0 && !slices.Contains(c.Command, "--json") && c.Command[0] != "report" {
		bad(id, "the command needs --json: the harness reads the envelope, never the screen")
	}
	if c.Action.Name != "" && !slices.Contains(actions, c.Action.Name) {
		bad(id, "action %q, want one of %s", c.Action.Name, strings.Join(actions, ", "))
	}
	if (c.Action.Name == ActionInterrupt || c.Action.Name == ActionSSHCut) && (c.Action.At <= 0 || c.Action.At >= 1) {
		bad(id, "action %s needs at between 0 and 1", c.Action.Name)
	}
	if c.Action.Name == ActionSIGKILL && c.Action.Runs < 1 {
		bad(id, "action sigkill needs runs")
	}
	if c.Make != "" && !slices.Contains(makes, c.Make) {
		bad(id, "make %q, want one of %s", c.Make, strings.Join(makes, ", "))
	}
	e := c.Expect
	if runs && e.ExitCode == nil && e.VerifyExit == nil {
		bad(id, "no exit_code or verify_exit: a scenario must say what passing looks like")
	}
	if e.TargetState != "" && !slices.Contains(states, e.TargetState) {
		bad(id, "target_state %q, want one of %s", e.TargetState, strings.Join(states, ", "))
	}
	if e.Canaries != nil && *e.Canaries != 0 {
		bad(id, "canaries %d: gate 1 allows no survivor", *e.Canaries)
	}
	if e.Sinkhole != nil && *e.Sinkhole != 0 {
		bad(id, "sinkhole %d: gate 3 allows no connection", *e.Sinkhole)
	}
	// The four gates for every copy and prepare scenario (PRD 22.1).
	if runs && (s.Kind == KindCopy || s.Kind == KindPrepare) && c.Name == "" {
		if e.Canaries == nil {
			bad(id, "a %s scenario needs gate 1 (canaries: 0)", s.Kind)
		}
		if !e.Integrity && !e.RowsOnly {
			bad(id, "a %s scenario needs gate 2 (integrity or rows_only)", s.Kind)
		}
		if e.VerifyExit == nil {
			bad(id, "a %s scenario needs gate 4 (verify_exit)", s.Kind)
		}
		if e.Sinkhole != nil && !s.Drill && !slices.Contains(s.Setup, SetupTraffic) {
			bad(id, "sinkhole is gate 3 of a drill or traffic run; this scenario has neither")
		}
	}
	if e.VerifyExit != nil && len(c.Verify) == 0 && c.Action.Name != ActionVerifyAll {
		bad(id, "verify_exit without a verify line")
	}
	if (e.Canaries != nil || e.Integrity || e.RowsOnly) && c.Output.DB == "" && c.Output.Tree == "" {
		bad(id, "canaries gate without an output for the oracle")
	}
	steps := append(slices.Clone(s.Setup), c.Setup...)
	if slices.ContainsFunc(c.Command, func(a string) bool { return strings.Contains(a, "${work}/roadie.yaml") }) &&
		!slices.Contains(steps, SetupConfig) {
		bad(id, "${work}/roadie.yaml needs the config setup")
	}
	if e.Unchanged && !slices.Contains(steps, SetupSnapshot) {
		bad(id, "unchanged needs the snapshot setup")
	}
	for _, l := range e.Locations {
		if p, ok := strings.CutPrefix(l, "file:"); ok && p != "" {
			continue
		}
		if !isTableColumn(l) {
			bad(id, "location %q: want table.column or file:<path>", l)
		}
	}
	ids := append(slices.Clone(e.FindingIDs), c.Forbid...)
	if e.ErrorCode != "" {
		ids = append(ids, e.ErrorCode)
	}
	for _, fid := range ids {
		if !known(fid) {
			bad(id, "%q is not a finding ID or error code core knows", fid)
		}
	}
	for _, fid := range e.FindingIDs {
		if slices.Contains(c.Forbid, fid) {
			bad(id, "%s is both expected and forbidden", fid)
		}
	}
}

// ErrInvalid wraps the problems Check found.
var ErrInvalid = errors.New("scenarios.yaml is not valid")

// Check loads path, reads the fixtures in fixturesDir and validates.
func Check(path, fixturesDir string, known func(string) bool) (*File, error) {
	f, err := Load(path)
	if err != nil {
		return nil, err
	}
	fx, err := ReadFixtures(fixturesDir)
	if err != nil {
		return nil, err
	}
	if errs := Validate(f, fx, known); len(errs) > 0 {
		return f, fmt.Errorf("%w:\n%w", ErrInvalid, errors.Join(errs...))
	}
	return f, nil
}
