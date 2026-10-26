// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, err := Builtin()
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 1: %v, want core.ErrNotBuilt", err)
	}
	_, _, err = Load(context.Background(), true)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 2: %v, want core.ErrNotBuilt", err)
	}
	_, err = Compile(nil, nil, nil, nil)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 3: %v, want core.ErrNotBuilt", err)
	}
}
