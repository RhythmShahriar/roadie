// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !race

package mysql

// raceEnabled skips the tests that only measure speed or memory.
const raceEnabled = false
