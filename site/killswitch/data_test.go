// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The WordPress checks that read only rows are DataSteps; those that read
// files are not, so a dump verify lists them as skipped. Gateways are read
// by the data scan's key.live-mode there.
func TestDataStepsOfWordPress(t *testing.T) {
	want := []string{core.FindingSideEffectSubscriptions, core.FindingSideEffectActionScheduler,
		core.FindingSideEffectWebhooks, core.FindingSideEffectAutomateWoo}
	var got []string
	for _, st := range Steps(core.StackWordPress, fixtureOptions()) {
		if _, ok := st.(core.DataStep); ok {
			got = append(got, st.ID())
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("data steps %v, want %v", got, want)
	}
	for _, st := range Steps(core.StackSQL, Options{}) {
		if _, ok := st.(core.DataStep); ok {
			t.Errorf("%s on a sql target is a data step", st.ID())
		}
	}
}

func TestNoFilesReasonFor(t *testing.T) {
	if NoFilesReasonFor(core.FindingSideEffectCron) != NoFilesReason || NoFilesReasonFor(core.FindingSideEffectGateways) == NoFilesReason {
		t.Error("reasons")
	}
}

func TestDataRead(t *testing.T) {
	cols, keep := DataRead("options")
	if !slices.Equal(cols, []string{"option_name", "option_value"}) {
		t.Fatalf("options cols %v", cols)
	}
	for name, kept := range map[string]bool{"siteurl": true, "wc_subscriptions_siteurl": true, "home": true,
		"woocommerce_stripe_settings": false, "_transient_x": false, "blogname": false} {
		if keep([][]byte{[]byte(name), nil}) != kept {
			t.Errorf("option %s kept %v", name, !kept)
		}
	}
	if _, keep := DataRead("actionscheduler_actions"); keep([][]byte{[]byte("complete")}) || !keep([][]byte{[]byte("pending")}) {
		t.Error("only pending actions are read")
	}
	if cols, keep := DataRead("posts"); cols != nil || keep != nil {
		t.Error("a table no check reads")
	}
	for _, base := range DataTables {
		if _, keep := DataRead(base); keep == nil {
			t.Errorf("%s has no read", base)
		}
	}
}
