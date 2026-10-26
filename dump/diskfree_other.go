// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux && !darwin && !windows

package dump

import (
	"errors"
	"syscall"
)

// diskFree can't tell on this platform; the write error still can.
func diskFree(string) (int64, bool) { return 0, false }

func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }

func syncDir(string) {}
