// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// maxRSSKB returns a finished process's peak resident memory in KiB
// (getrusage's ru_maxrss: KiB on Linux, bytes on macOS).
func maxRSSKB(ps *os.ProcessState) int64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return ru.Maxrss / 1024
	}
	return ru.Maxrss
}

// exitCode returns the exit code, or 128 plus the signal for a process a
// signal ended (as a shell reports it).
func exitCode(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}
