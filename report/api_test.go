// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	err := Check(nil, nil, nil)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("Check: %v, want core.ErrNotBuilt", err)
	}
}
