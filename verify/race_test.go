// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package verify

// raceEnabled stretches the time bounds: the race detector slows plain
// loops several times over.
const raceEnabled = true
