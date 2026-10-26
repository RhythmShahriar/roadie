// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package site

import (
	"errors"
	"syscall"
)

// pidRunning reports a live process: signal 0 checks without sending.
// EPERM means it runs as another user.
func pidRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
