// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package engine

// raceOn is set under -race, whose shadow memory makes heap numbers
// meaningless and the 4 GB memory test take minutes.
const raceOn = true
