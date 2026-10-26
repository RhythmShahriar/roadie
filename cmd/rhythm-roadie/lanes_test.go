// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/verify"
)

// laneProbes call one stub of each lane the end-to-end tests need. A
// probe that answers core.ErrNotBuilt means that lane hasn't merged yet
// (lanes.md 2.3), so the test skips with its name and turns on by itself
// after the merge.
var laneProbes = map[string]func() error{
	"io": func() error {
		_, err := dump.Open(context.Background(), strings.NewReader("-- probe\n"), dump.Options{})
		return err
	},
	"values": func() error {
		_, err := pseudo.NewKey()
		return err
	},
	"site": func() error {
		_, err := site.ParseEndpoint("probe.sql")
		return err
	},
	"detect": func() error {
		_, err := verify.Run(context.Background(), verify.Job{})
		return err
	},
	"engine": func() error {
		_, err := engine.Run(context.Background(), engine.Job{Mode: engine.Plan})
		return err
	},
	"testbed": func() error {
		return demo.Shop(nopWriter{}, demo.Options{Seed: 1, Customers: 1})
	},
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// requireLanes skips the test while any of lanes is still a stub.
func requireLanes(t *testing.T, lanes ...string) {
	t.Helper()
	var missing []string
	for _, l := range lanes {
		probe, ok := laneProbes[l]
		if !ok {
			t.Fatalf("no probe for the %s lane", l)
		}
		if err := probe(); errors.Is(err, core.ErrNotBuilt) {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		word := "lane"
		if len(missing) > 1 {
			word = "lanes"
		}
		t.Skipf("needs the %s %s: their functions aren't built yet", strings.Join(missing, ", "), word)
	}
}
