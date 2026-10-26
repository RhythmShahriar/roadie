// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, err := Open(context.Background(), core.Endpoint{}, ReadOnly)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 1: %v, want core.ErrNotBuilt", err)
	}
}
