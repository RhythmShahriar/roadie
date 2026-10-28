// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "testing"

// key.live-mode keeps one finding per gateway: Stripe's and PayPal's
// options share wp_options.option_value; one title can't name both.
// Other IDs still group by column whatever their titles.
func TestAggregatorLiveModeByGateway(t *testing.T) {
	var a Aggregator
	at := func(p string) []LocationCount {
		return []LocationCount{{Location: Location{Table: "wp_options", Column: "option_value", Path: p}}}
	}
	a.Add(Finding{ID: FindingKeyLiveMode, Severity: SeverityHigh, Title: "Stripe is in live mode", Locations: at("serialized:testmode")})
	a.Add(Finding{ID: FindingKeyLiveMode, Severity: SeverityHigh, Title: "PayPal Payments is in live mode", Locations: at("serialized:sandbox_on")})
	a.Add(Finding{ID: FindingKeyLiveMode, Severity: SeverityHigh, Title: "Stripe is in live mode", Locations: at("serialized:testmode")})
	a.Add(Finding{ID: FindingPIIEmail, Severity: SeverityHigh, Title: "a", Locations: at("")})
	a.Add(Finding{ID: FindingPIIEmail, Severity: SeverityHigh, Title: "b", Locations: at("")})
	got := map[string]int64{}
	for _, f := range a.Findings() {
		got[f.ID+" "+f.Title] = f.Count
	}
	want := map[string]int64{
		FindingKeyLiveMode + " Stripe is in live mode": 2, FindingKeyLiveMode + " PayPal Payments is in live mode": 1,
		FindingPIIEmail + " a": 2,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d, want %d (all %v)", k, got[k], n, got)
		}
	}
}
