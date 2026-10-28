// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// Every check verify lists has a readable label and a name without a
// verdict, never its raw ID.
func TestEveryCheckHasALabel(t *testing.T) {
	ids := []string{core.FindingExactSurvivors, core.FindingPIIEmail, core.FindingPIIPhone, core.FindingPIIName,
		core.FindingPIIAddress, core.FindingPIIIP, core.FindingPIICard, core.FindingPIIIBAN, core.FindingDataEncrypted,
		core.FindingDataUnparseable, core.FindingUncoveredColumn, "key.live", core.FindingKeyGeneric, core.FindingKeyLiveMode,
		core.FindingKeyUnknownEnv, core.FindingURLProduction, core.FindingURLProductionSiteURL, core.FindingConfigEnvironment,
		core.FindingConfigPointsAtProduction, core.FindingConfigSalts, core.FindingSearchIndexable, core.FindingAuthNone,
		core.FindingUploadsPersonal, core.FindingSoundcheckFailed, core.FindingSideEffectNotApplicable,
		core.FindingSideEffectMail, core.FindingSideEffectCron, core.FindingSideEffectActionScheduler,
		core.FindingSideEffectWebhooks, core.FindingSideEffectGateways, core.FindingSideEffectSubscriptions,
		core.FindingSideEffectAutomateWoo, core.FindingSideEffectQueue, core.FindingSideEffectJetpack}
	for _, id := range ids {
		for _, st := range []string{"pass", "fail"} {
			if got := CheckTitle(id, st); got == id || got == "" {
				t.Errorf("%s (%s) has no label", id, st)
			}
		}
	}
	if CheckTitle(core.FindingSideEffectCron, "pass") != "Off: WP-Cron" || CheckTitle(core.FindingSideEffectCron, "fail") != "WP-Cron" {
		t.Errorf("cron: %q %q", CheckTitle(core.FindingSideEffectCron, "pass"), CheckTitle(core.FindingSideEffectCron, "fail"))
	}
}

// The Markdown table gives one total per check, not each finding's title.
func TestCheckTally(t *testing.T) {
	loc := func(tb, col, file string) core.LocationCount {
		return core.LocationCount{Location: core.Location{Table: tb, Column: col, File: file}, Count: 1}
	}
	fs := []core.Finding{
		{Count: 200, Locations: []core.LocationCount{loc("wp_users", "user_email", ""), loc("wp_usermeta", "meta_value", "")}},
		{Count: 80, Locations: []core.LocationCount{loc("wp_users", "user_email", "")}},
	}
	if got := checkTally(fs); got != "280 hits in 2 columns" {
		t.Errorf("columns: %q", got)
	}
	if got := checkTally([]core.Finding{{Count: 1, Locations: []core.LocationCount{loc("", "", "wp-config.php")}}}); got != "1 hit in 1 place" {
		t.Errorf("file: %q", got)
	}
	if got := checkTally([]core.Finding{{}}); got != "1 hit" {
		t.Errorf("no location: %q", got)
	}
	if checkTally(nil) != "" {
		t.Error("none")
	}
}
