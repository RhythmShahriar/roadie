// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// A nil table has no columns, so nothing can be uncovered; callers pass
// whatever schema they read without a nil check.
func TestUncoveredNilTable(t *testing.T) {
	if got := Uncovered(nil, map[string]bool{"email": true}, map[string][][]byte{"email": {[]byte("a@b.co")}}); got != nil {
		t.Fatalf("Uncovered(nil table) = %v, want nil", got)
	}
}

// All returns every PII detector of PRD 15.2 once, in a fixed order.
func TestAllIDs(t *testing.T) {
	want := []string{
		core.FindingPIIEmail, core.FindingPIIPhone, core.FindingPIIIP, core.FindingPIICard,
		core.FindingPIIIBAN, core.FindingPIIName, core.FindingPIIAddress,
	}
	var got []string
	for _, d := range All() {
		if !core.KnownID(d.ID()) {
			t.Errorf("unknown detector ID %q", d.ID())
		}
		got = append(got, d.ID())
	}
	if !slices.Equal(got, want) {
		t.Errorf("All IDs %v, want %v", got, want)
	}
}
