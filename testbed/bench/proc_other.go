// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// maxRSSKB is unknown off Unix: the benchmark's memory figures come from
// the Linux runs.
func maxRSSKB(*os.ProcessState) int64 { return 0 }

// exitCode returns the process's exit code.
func exitCode(ee *exec.ExitError) int { return ee.ExitCode() }
