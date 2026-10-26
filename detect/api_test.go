// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import "testing"

// The frozen stubs return zero values until the detect lane builds them.
func TestStubsZero(t *testing.T) {
	if All() != nil || TextSpans() != nil || Uncovered(nil, nil, nil) != nil {
		t.Fatal("stub returned a value")
	}
}
