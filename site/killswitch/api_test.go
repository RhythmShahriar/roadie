// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs return zero values until the site lane builds them.
func TestStubsZero(t *testing.T) {
	if Steps(core.StackWordPress, Options{}) != nil || MuPlugin(Options{}) != nil {
		t.Fatal("stub returned a value")
	}
}
