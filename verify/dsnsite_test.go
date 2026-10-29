// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/exposure"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// dsnSteps are the steps a copy into a WordPress database hands verify:
// the sql stack's own, the exposure steps and the WordPress kill switch.
func dsnSteps() []core.Step {
	ko := killswitch.Options{}
	steps := append(killswitch.Steps(core.StackSQL, ko), exposure.Steps(ko)...)
	return append(steps, killswitch.Steps(core.StackWordPress, ko)...)
}

// A copy into a database hands verify the database's site, which has no
// files: verify checks it by its rows alone, as a standalone verify of
// the same database does. No file check fails it or tells the user to
// install files with prepare --only side-effects.
func TestRunDSNSiteIsRowsOnly(t *testing.T) {
	rows := func() *testutil.Source {
		return source(options([]string{"1", "blogname", "Shop"}, []string{"2", "siteurl", "https://staging.shop.test"}))
	}
	dsn := testutil.FakeSite(nil)
	dsn.EP.Kind, dsn.StackName = core.DSN, core.StackSQL
	viaCopy := runJob(t, Job{Site: dsn, Source: rows(), Steps: dsnSteps()})
	standalone := runJob(t, Job{Source: rows(), Steps: dsnSteps()})

	for _, f := range viaCopy.Findings {
		if f.Severity > core.SeverityInfo {
			t.Errorf("finding %s %s: %s", f.ID, f.Severity, f.Title)
		}
		if strings.Contains(f.Fix, "--only side-effects") {
			t.Errorf("%s suggests installing files into a database: %q", f.ID, f.Fix)
		}
	}
	if find(viaCopy, core.FindingSideEffectNotApplicable) == nil {
		t.Errorf("no side-effect.not-applicable: %v", ids(viaCopy))
	}
	for _, id := range []string{core.FindingSideEffectMail, core.FindingSideEffectCron, core.FindingConfigEnvironment,
		core.FindingConfigSalts, core.FindingSearchIndexable, core.FindingUploadsPersonal} {
		c, want := checkByID(viaCopy, id), checkByID(standalone, id)
		if c.Status != "skip" || c.Checked != 0 {
			t.Errorf("%s via copy %+v, want skipped", id, c)
		}
		if c != want {
			t.Errorf("%s via copy %+v, standalone %+v", id, c, want)
		}
	}

	// Negative: a site with files still runs its file checks.
	files := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\n")})
	files.StackName = core.StackWordPress
	res := runJob(t, Job{Site: files, Source: rows(), Steps: killswitch.Steps(core.StackWordPress, killswitch.Options{})})
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "fail" {
		t.Errorf("mail on a site with files %+v", c)
	}
}

// A step check that can't tell or that fails looked at nothing: the
// checks list skips it with the reason and counts nothing for it.
func TestRunUnknownCheckNotCounted(t *testing.T) {
	st := testutil.FakeSite(nil)
	st.StackName = core.StackWordPress
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})),
		Steps: killswitch.Steps(core.StackWordPress, killswitch.Options{})})
	for _, id := range []string{core.FindingConfigEnvironment, core.FindingSideEffectCron, core.FindingConfigSalts} {
		c := checkByID(res, id)
		if c.Status != "skip" || c.Checked != 0 || !strings.Contains(c.Detail, "can't be read") {
			t.Errorf("%s %+v, want skip with the reason and nothing checked", id, c)
		}
	}
	// Negative: a check that read its file and found the switch off passes.
	st = testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\ndefine('WP_ENVIRONMENT_TYPE', 'staging');\n")})
	st.StackName = core.StackWordPress
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})),
		Steps: killswitch.Steps(core.StackWordPress, killswitch.Options{})})
	if c := checkByID(res, core.FindingConfigEnvironment); c.Status != "pass" || c.Checked != 1 {
		t.Errorf("environment %+v", c)
	}
}

// An Echo connector that differs from Roadie's fails the Echo seal's row:
// the row never says "sealed" above the finding that it isn't Roadie's.
func TestConnectorChangedFailsEchoRow(t *testing.T) {
	steps := killswitch.Steps(core.StackWordPress, killswitch.Options{})
	st := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\n"), killswitch.EchoPluginPath: []byte("<?php"),
		killswitch.EchoSettingsPath: []byte("<?php\nreturn array(\n\t'mode' => 'seal',\n);\n")})
	st.StackName = core.StackWordPress
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), Steps: steps})
	if !slices.ContainsFunc(res.Findings, func(f core.Finding) bool { return f.ID == core.FindingSideEffectConnectorChange }) {
		t.Fatalf("no connector-changed finding: %v", ids(res))
	}
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "fail" || !strings.Contains(c.Detail, core.FindingSideEffectConnectorChange) {
		t.Errorf("changed connector: mail row %+v", c)
	}
	// Negative: Roadie's own connector, as the step installs it, passes
	// the row.
	st = testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\n")})
	st.StackName = core.StackWordPress
	for _, step := range steps {
		if step.ID() == core.FindingSideEffectMail {
			if err := step.Apply(context.Background(), st); err != nil {
				t.Fatal(err)
			}
		}
	}
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), Steps: steps})
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "pass" {
		t.Errorf("Roadie's connector: mail row %+v (%v)", c, ids(res))
	}
}
