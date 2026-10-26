// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The stubs still to build answer core.ErrNotBuilt.
func TestStubsNotBuilt(t *testing.T) {
	_, err := ResetHash(core.StackWordPress)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("ResetHash: %v, want core.ErrNotBuilt", err)
	}
}
