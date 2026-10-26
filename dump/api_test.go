// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The frozen stubs answer core.ErrNotBuilt until the lane builds them.
func TestStubsNotBuilt(t *testing.T) {
	_, err := Open(context.Background(), strings.NewReader(""), Options{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 1: %v, want core.ErrNotBuilt", err)
	}
	_, _, err = Sniff(nil)
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 2: %v, want core.ErrNotBuilt", err)
	}
	_, err = NewFileSink("x.sql", WriterOptions{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 3: %v, want core.ErrNotBuilt", err)
	}
	_, err = NewSpool(t.TempDir())
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("call 4: %v, want core.ErrNotBuilt", err)
	}
}
