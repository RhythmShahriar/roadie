// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package mysql

import "time"

// cpuTime is 0 where the benchmarks don't read the process CPU time.
func cpuTime() time.Duration { return 0 }
