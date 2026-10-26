// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package demo

// raceEnabled skips the timing check: the race detector slows Shop down
// several times over.
const raceEnabled = true
