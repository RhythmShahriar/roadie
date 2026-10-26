// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, err := NewSpool(t.TempDir())
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 4: %v, want core.ErrNotBuilt", err)
	}
}
