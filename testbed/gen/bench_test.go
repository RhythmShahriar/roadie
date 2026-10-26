// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package gen

import (
	"io"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/seed"
)

// BenchmarkWrite measures the generator alone (no gzip): MB/s of SQL.
func BenchmarkWrite(b *testing.B) {
	const size = 64 << 20
	b.SetBytes(size)
	for b.Loop() {
		if _, err := Write(io.Discard, Options{Size: size, Seed: 1, Values: seed.NewValues(1, Scenario)}); err != nil {
			b.Fatal(err)
		}
	}
}
