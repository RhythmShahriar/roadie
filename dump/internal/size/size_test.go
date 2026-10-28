// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package size

import "testing"

func TestHuman(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 999: "999 B", 1000: "1.0 KB", 1_700_000_000: "1.7 GB",
		64 << 20: "67.1 MB", 999_999: "1.0 MB", 40_000_000_000: "40.0 GB",
	} {
		if got := Human(n); got != want {
			t.Errorf("Human(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestBinary(t *testing.T) {
	for n, want := range map[int64]string{
		256 << 20: "256 MiB", 1 << 30: "1 GiB", 3 << 10: "3 KiB", 1000: "1000 bytes", 1<<20 + 1: "1048577 bytes",
	} {
		if got := Binary(n); got != want {
			t.Errorf("Binary(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 4120: "4,120", 1234567: "1,234,567", -4120: "-4,120"} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}
