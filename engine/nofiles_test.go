// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// A dump or a database target gets the kill-switch steps of the stack it
// holds; a site with files, a sql stack or no stack gets none.
func TestNoFilesSteps(t *testing.T) {
	old := killSteps
	t.Cleanup(func() { killSteps = old })
	killSteps = killswitch.Steps
	ko := killswitch.Options{}
	if got := noFilesSteps(nil, core.StackWordPress, ko); len(got) == 0 {
		t.Error("a dump of a WordPress site has no steps")
	}
	dsn := testutil.FakeSite(nil)
	dsn.StackName = core.StackSQL
	if got := noFilesSteps(dsn, core.StackWordPress, ko); len(got) == 0 {
		t.Error("a WordPress database has no steps")
	}
	files := testutil.FakeSite(nil)
	files.StackName = core.StackWordPress
	if got := noFilesSteps(files, core.StackWordPress, ko); got != nil {
		t.Errorf("a site with files got %d more steps", len(got))
	}
	for _, st := range []core.Stack{core.StackSQL, ""} {
		if got := noFilesSteps(nil, st, ko); got != nil {
			t.Errorf("stack %q: %d steps", st, len(got))
		}
	}
}
