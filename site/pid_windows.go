// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package site

import (
	"errors"
	"syscall"
)

// processQueryLimited is PROCESS_QUERY_LIMITED_INFORMATION; stillActive is
// the exit code Windows reports for a process that hasn't exited.
const (
	processQueryLimited = 0x1000
	stillActive         = 259
)

// pidRunning reports a live process. Access denied means it exists and
// belongs to someone else.
func pidRunning(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimited, false, uint32(pid)) // #nosec G115 -- a pid read from our own lock file
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
