// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package nested

import (
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, _, err := Rewrite(nil, nil, Options{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 1: %v, want core.ErrNotBuilt", err)
	}
	err = Leaves(nil, nil, Options{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 2: %v, want core.ErrNotBuilt", err)
	}
}
