// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package pseudo

// raceEnabled scales the million-value tests down under the race
// detector, which makes them ten times slower.
const raceEnabled = true
