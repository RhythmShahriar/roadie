// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// TestDumpScenarios runs every scenario that reads a seeded dump with
// the real rhythm-roadie on this machine, no testbed needed: each copy
// writes a dump instead of the staging site and must pass the oracle
// (no canary, live key or broken serialized value left; the rows and
// joins intact), the fresh verify and its expected exit and error
// codes. The sealed testbed adds the site-only gates (make -C testbed
// bench).
func TestDumpScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("builds rhythm-roadie and copies every seeded dump")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the harness builds rhythm-roadie for Linux, as the testbed runs it")
	}
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var php oracle.PHP
	if p, err := exec.LookPath("php"); err == nil {
		php = oracle.PHP{Cmd: []string{p}}
	}
	scenarios := DumpScenarios(f, len(php.Cmd) > 0)
	if len(scenarios) < 20 {
		t.Fatalf("%d dump-level scenarios, want 20 or more", len(scenarios))
	}
	work := t.TempDir()
	h := &Harness{Runtime: filepath.Join(work, "runtime"), Fixtures: "../fixtures", Work: filepath.Join(work, "work"), Seed: 1, Rounds: 1, PHP: php}
	ctx := context.Background()
	if h.Bin, err = binary(ctx, options{root: "../.."}, h.Work); err != nil {
		t.Fatal(err)
	}
	if err := h.Init(ctx); err != nil {
		t.Fatal(err)
	}
	var only []string
	for _, s := range scenarios {
		only = append(only, s.ID)
	}
	res := h.RunAll(ctx, &manifest.File{Version: f.Version, Scenarios: scenarios}, only)
	for _, s := range res.Scored.Scenarios {
		if s.Outcome == Caught {
			continue
		}
		t.Errorf("%s: %s", s.ID, s.Outcome)
		for _, c := range s.Cases {
			if c.Detail != "" {
				t.Logf("  %s: %s", c.Name, c.Detail)
			}
			for _, g := range c.Gates {
				if !g.Pass {
					t.Logf("  %s %s: %s", c.Name, g.Name, g.Detail)
				}
			}
		}
	}
}

// A staging-site copy becomes a dump copy: the site target is the dump,
// the confirm flags go, site-only gates and finding IDs are left out and
// a run that stops has no output to judge. Scenarios that need the
// testbed (ssh, a database, an action) are not taken.
func TestDumpScenariosTranslate(t *testing.T) {
	zero, two := 0, 2
	f := &manifest.File{Scenarios: []manifest.Scenario{
		{ID: "r01", Kind: manifest.KindCopy, Input: "r01", Run: manifest.RunStagingWP, Setup: []string{manifest.SetupStagingSite},
			Command: []string{"copy", "--from", "${in}/db.sql", "--to", "${site}", "--url", "${staging_url}", "--confirm-target", "${confirm}", "--yes", "--json"},
			Output:  manifest.Output{DB: manifest.OutStagingDB, Tree: manifest.OutStagingWP}, Verify: []string{"${site}", "--from", "${in}/db.sql"},
			Boot:    true, Drill: true, WPChecks: []manifest.WPCheck{{Name: "x", PHP: "exit(0);"}},
			Expect:  manifest.Expect{ExitCode: &zero, Sinkhole: &zero, Integrity: true, VerifyExit: &zero, TargetState: "replaced", FindingIDs: []string{"uploads.personal", "data.unparseable"}},
			Forbid:  []string{"pii.email", "side-effect.mail"}},
		{ID: "r11", Kind: manifest.KindCopy, Input: "r11", Run: manifest.RunStagingWP, Cases: []manifest.Case{
			{Name: "unmapped", Setup: []string{manifest.SetupSnapshot}, Command: []string{"copy", "--from", "${in}/db.sql", "--to", "${site}"},
				Output: manifest.Output{DB: manifest.OutStagingDB}, Expect: manifest.Expect{ExitCode: &two, ErrorCode: "url-map", Unchanged: true}}}},
		{ID: "r19", Kind: manifest.KindCopy, Input: "r19", Command: []string{"copy", "--from", "${ssh_source}", "--to", "${work}/out.sql"}},
		{ID: "r13", Kind: manifest.KindPrepare, Input: "r13", Run: manifest.RunStagingWP, Command: []string{"prepare", "${site}"}},
		{ID: "r25", Kind: manifest.KindEnv, Input: "r25", Setup: []string{manifest.SetupStagingDB}, Command: []string{"copy", "--from", "${in}/db.sql", "--to", "${site}"}},
	}}
	got := DumpScenarios(f, false)
	if len(got) != 2 || got[0].ID != "r01" || got[1].ID != "r11" {
		t.Fatalf("scenarios %+v", got)
	}
	r01 := got[0]
	wantCmd := []string{"copy", "--from", "${in}/db.sql", "--to", dumpTarget, "--url", "${staging_url}", "--json"}
	if !slices.Equal(r01.Command, wantCmd) || !slices.Equal(r01.Verify, []string{dumpTarget, "--from", "${in}/db.sql"}) {
		t.Errorf("command %q verify %q", r01.Command, r01.Verify)
	}
	e := r01.Expect
	if r01.Run != manifest.RunHost || r01.Setup != nil || r01.Boot || r01.Drill || r01.WPChecks != nil || r01.Output.DB != dumpTarget || r01.Output.Tree != "" ||
		e.Sinkhole != nil || e.TargetState != "" || e.Integrity || !e.RowsOnly || !slices.Equal(e.FindingIDs, []string{"data.unparseable"}) || !slices.Equal(r01.Forbid, []string{"pii.email"}) {
		t.Errorf("r01 %+v", r01)
	}
	c := got[1].Cases[0]
	if c.Output != (manifest.Output{}) || !c.Expect.NoFinishedOutput || c.Expect.Unchanged || c.Expect.ErrorCode != "url-map" || c.Setup != nil {
		t.Errorf("r11 unmapped %+v", c)
	}
}

// The gates take fewer rows as meant only when the report says why: a
// truncated table left empty, or a rule that deleted exactly the rows
// that are gone.
func TestMeantFewer(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{"schema":"rhythm-roadie/v1","data":{"tables":[{"name":"wp_sessions","rows":50,"action":"truncate"},` +
		`{"name":"wp_usermeta","rows":1200,"action":"fake","columns":[{"changed":150},{"changed":40}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := []oracle.TableRows{{Table: "wp_sessions", Input: 50, Output: 0}, {Table: "wp_usermeta", Input: 1200, Output: 1050},
		{Table: "wp_posts", Input: 10, Output: 9}}
	for table, want := range map[string]bool{"wp_sessions": true, "wp_usermeta": true, "wp_posts": false} {
		if got := meantFewer(env, table, rows); got != want {
			t.Errorf("%s: %v", table, got)
		}
	}
	rows[1].Output = 1100
	if meantFewer(env, "wp_usermeta", rows) || meantFewer(nil, "wp_sessions", rows) {
		t.Error("an unexplained loss passed")
	}
}
