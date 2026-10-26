// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package manifest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// TestManifest checks the real scenarios.yaml: every PRD 22.2 scenario
// once, a fixture and expectations for each; only finding IDs and
// error codes core knows. It needs no Docker, so it runs everywhere (and
// under the testbed tag too).
func TestManifest(t *testing.T) {
	f, err := Check("../scenarios.yaml", "../fixtures", core.KnownID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(f.Scenarios), len(ScenarioIDs()); got != want {
		t.Fatalf("%d scenarios, want %d", got, want)
	}
	bad, good := 0, 0
	for _, s := range f.Scenarios {
		if s.Kind == KindVerify && !s.Good {
			bad++
			if e := s.Expect; e.ExitCode == nil || *e.ExitCode != 1 || len(e.FindingIDs) == 0 {
				t.Errorf("%s: a bad verify scenario expects exit 1 and finding IDs", s.ID)
			}
		}
		if s.Good {
			good++
			if e := s.Expect; e.ExitCode == nil || *e.ExitCode != 0 {
				t.Errorf("%s: a good input expects exit 0", s.ID)
			}
		}
	}
	if bad != 7 || good != 5 {
		t.Errorf("%d bad and %d good verify scenarios, PRD 22.3 counts 7 and 5", bad, good)
	}
}

// minimal is a manifest with every scenario as a passing verify, so a test
// can break one thing at a time.
func minimal() *File {
	f := &File{Version: Version}
	zero := 0
	for _, id := range ScenarioIDs() {
		f.Scenarios = append(f.Scenarios, Scenario{ID: id, Kind: KindVerify, Stack: "woocommerce", Title: "t", Input: "fx", Run: RunHost,
			Command: []string{"verify", "${in}/db.sql", "--json"}, Expect: Expect{ExitCode: &zero}})
	}
	return f
}

var fixtures = map[string]Fixture{
	"fx":  {ID: "fx", Files: map[string]bool{"db.sql": true, "files/roadie.yaml": true}},
	"r23": {ID: "r23", Files: map[string]bool{"files/wp-content/mu-plugins/testbed-egress.php": true}},
}

func TestValidateMinimal(t *testing.T) {
	if errs := Validate(minimal(), fixtures, core.KnownID); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestValidateCatches(t *testing.T) {
	one := 1
	cases := []struct {
		name string
		edit func(f *File)
		want string
	}{
		{"missing scenario", func(f *File) { f.Scenarios = f.Scenarios[1:] }, "r01: missing"},
		{"extra scenario", func(f *File) {
			f.Scenarios = append(f.Scenarios, f.Scenarios[0])
			f.Scenarios[len(f.Scenarios)-1].ID = "r99"
		}, "r99: not a PRD"},
		{"twice", func(f *File) { f.Scenarios = append(f.Scenarios, f.Scenarios[0]) }, "r01: listed twice"},
		{"unknown kind", func(f *File) { f.Scenarios[0].Kind = "copyish" }, `kind "copyish"`},
		{"unknown fixture", func(f *File) { f.Scenarios[0].Input = "nope" }, `input "nope" is not a fixture`},
		{"unknown file", func(f *File) { f.Scenarios[0].Command[1] = "${in}/other.sql" }, "doesn't make"},
		{"unknown placeholder", func(f *File) { f.Scenarios[0].Command[1] = "${inn}/db.sql" }, "unknown placeholder ${inn}"},
		{"unknown finding", func(f *File) { f.Scenarios[0].Expect.FindingIDs = []string{"pii.emial"} }, `"pii.emial" is not a finding ID`},
		{"unknown forbid", func(f *File) { f.Scenarios[0].Forbid = []string{"key.nobody-live"} }, `"key.nobody-live" is not`},
		{"unknown error code", func(f *File) { f.Scenarios[0].Expect.ErrorCode = "target-is-prod" }, `"target-is-prod" is not`},
		{"no exit", func(f *File) { f.Scenarios[0].Expect.ExitCode = nil }, "no exit_code or verify_exit"},
		{"no json", func(f *File) { f.Scenarios[0].Command = []string{"verify", "x"} }, "needs --json"},
		{"not a command", func(f *File) { f.Scenarios[0].Command = []string{"demo", "--json"} }, `command "demo"`},
		{"copy without gates", func(f *File) { f.Scenarios[0].Kind = KindCopy }, "needs gate 1"},
		{"canaries not zero", func(f *File) { f.Scenarios[0].Expect.Canaries = &one }, "canaries 1"},
		{"gate without output", func(f *File) { zero := 0; f.Scenarios[0].Expect.Canaries = &zero }, "without an output"},
		{"verify gate without verify", func(f *File) { f.Scenarios[0].Expect.VerifyExit = &one }, "without a verify line"},
		{"unknown setup", func(f *File) { f.Scenarios[0].Setup = []string{"warm-up"} }, `setup "warm-up"`},
		{"unchanged without snapshot", func(f *File) { f.Scenarios[0].Expect.Unchanged = true }, "snapshot setup"},
		{"bad target state", func(f *File) { f.Scenarios[0].Expect.TargetState = "half" }, `target_state "half"`},
		{"good with canaries", func(f *File) { f.Scenarios[0].Good = true; f.Scenarios[0].Input = "can" }, "plants canaries"},
		{"good copy", func(f *File) { f.Scenarios[0].Good = true; f.Scenarios[0].Kind = KindCopy }, "must be a verify"},
		{"expected and forbidden", func(f *File) {
			f.Scenarios[0].Expect.FindingIDs = []string{"pii.email"}
			f.Scenarios[0].Forbid = []string{"pii.email"}
		}, "both expected and forbidden"},
		{"wp check off staging", func(f *File) { f.Scenarios[0].WPChecks = []WPCheck{{Name: "x", PHP: "exit(0);"}} }, "need run: staging-wp"},
		{"wp check with both", func(f *File) {
			f.Scenarios[0].Run = RunStagingWP
			f.Scenarios[0].WPChecks = []WPCheck{{Name: "x", PHP: "exit(0);", WP: []string{"cli"}}}
		}, "exactly one of php or wp"},
		{"bad join", func(f *File) { f.Scenarios[0].Joins = []Join{{Left: "contacts", Right: "deals.email"}} }, "want table.column"},
		{"bad import", func(f *File) { f.Scenarios[0].ImportInto = []string{"sqlite"} }, `import_into "sqlite"`},
		{"bad location", func(f *File) { f.Scenarios[0].Expect.Locations = []string{"wp_users"} }, `location "wp_users"`},
		{"config without setup", func(f *File) {
			f.Scenarios[0].Command = []string{"verify", "--config", "${work}/roadie.yaml", "--json"}
		}, "needs the config setup"},
		{"bad action", func(f *File) { f.Scenarios[0].Action = Action{Name: "interrupt"} }, "at between 0 and 1"},
		{"cases and command", func(f *File) { f.Scenarios[0].Cases = []Case{{Name: "a"}} }, "both a command and cases"},
		{"duplicate case", func(f *File) {
			f.Scenarios[0].Command = nil
			c := Case{Name: "a", Command: []string{"verify", "--json"}, Expect: Expect{ExitCode: &one}}
			f.Scenarios[0].Cases = []Case{c, c}
		}, "case listed twice"},
		{"boot off staging", func(f *File) { f.Scenarios[0].Boot = true }, "boot and drill need run: staging-wp"},
	}
	fx := map[string]Fixture{"can": {ID: "can", Canaries: true, Files: map[string]bool{"db.sql": true}}}
	for k, v := range fixtures {
		fx[k] = v
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := minimal()
			c.edit(f)
			errs := Validate(f, fx, core.KnownID)
			if !slices.ContainsFunc(errs, func(e error) bool { return strings.Contains(e.Error(), c.want) }) {
				t.Errorf("want a problem with %q, got %v", c.want, errs)
			}
		})
	}
}

func TestParse(t *testing.T) {
	ok := "version: 1\nx-a: &a [verify, --json]\nscenarios:\n  - id: r01\n    command: *a\n"
	f, err := Parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := f.Scenario("r01"); s.Run != RunHost || !slices.Equal(s.Command, []string{"verify", "--json"}) {
		t.Errorf("got %+v", s)
	}
	for _, bad := range []struct{ yaml, want string }{
		{"version: 2\nscenarios: []\n", "version 2"},
		{"version: 1\nscenario: []\n", `unknown key "scenario"`},
		{"version: 1\nscenarios:\n  - id: r01\n    comand: [verify]\n", "comand"},
		{"version: 1\nscenarios:\n  - id: r01\n    expect: {exitcode: 0}\n", "exitcode"},
	} {
		if _, err := Parse([]byte(bad.yaml)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%q: got %v, want an error with %q", bad.yaml, err, bad.want)
		}
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"in": "/in", "work": "/w"}
	got, err := ExpandAll([]string{"copy", "--from", "${in}/db.sql", "--to=${work}/out.sql", "$HOME"}, vars)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"copy", "--from", "/in/db.sql", "--to=/w/out.sql", "$HOME"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := Expand("${site}/x", vars); err == nil || !strings.Contains(err.Error(), "${site}") {
		t.Errorf("a missing value must be an error, got %v", err)
	}
}

func TestAllCasesInherit(t *testing.T) {
	s := Scenario{Command: []string{"copy"}, Output: Output{DB: OutStagingDB}, Verify: []string{"v"}, Forbid: []string{"pii.email"},
		Cases: []Case{{Name: "a"}, {Name: "b", Command: []string{"plan"}, Output: Output{Tree: OutStagingWP}}}}
	cs := s.AllCases()
	if cs[0].Command[0] != "copy" || cs[0].Output.DB != OutStagingDB || cs[0].Verify[0] != "v" || cs[0].Forbid[0] != "pii.email" {
		t.Errorf("case a didn't inherit: %+v", cs[0])
	}
	if cs[1].Command[0] != "plan" || cs[1].Output.DB != "" {
		t.Errorf("case b must keep its own: %+v", cs[1])
	}
}

func TestCheckWrapsProblems(t *testing.T) {
	_, err := Check("../scenarios.yaml", "../no-such-fixtures", core.KnownID)
	if err == nil {
		t.Fatal("no error for a missing fixtures folder")
	}
	if errors.Is(err, ErrInvalid) {
		t.Errorf("a read error isn't a validation error: %v", err)
	}
}
