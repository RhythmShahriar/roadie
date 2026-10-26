// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The uncovered-column scan is still a stub; the detect lane builds it
// with verify.
func TestStubsZero(t *testing.T) {
	if Uncovered(nil, nil, nil) != nil {
		t.Fatal("stub returned a value")
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
