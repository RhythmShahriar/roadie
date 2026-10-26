// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The spanner and the uncovered-column scan are still stubs.
func TestStubsZero(t *testing.T) {
	if TextSpans() != nil || Uncovered(nil, nil, nil) != nil {
		t.Fatal("stub returned a value")
	}
}

// All returns every PII detector of PRD 15.2 once, in a fixed order.
func TestAllIDs(t *testing.T) {
	want := []string{
		core.FindingPIIEmail,
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
