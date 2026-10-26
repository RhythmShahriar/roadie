// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, err := NewKey()
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 1: %v, want core.ErrNotBuilt", err)
	}
	_, err = ParseKey("")
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 2: %v, want core.ErrNotBuilt", err)
	}
	_, err = NewFaker(Key{}, Options{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 3: %v, want core.ErrNotBuilt", err)
	}
	_, err = ResetHash(core.StackWordPress)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 4: %v, want core.ErrNotBuilt", err)
	}
}
