// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package dump

import (
	"errors"
	"os"
	"syscall"
)

// diskFree is the space available to this user in dir (statfs f_bavail).
func diskFree(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	free := st.Bavail * uint64(st.Bsize) // #nosec G115 -- block size is positive
	if free > 1<<62 {
		free = 1 << 62
	}
	return int64(free), true // #nosec G115 -- capped above
}

func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }

// syncDir makes a rename in dir durable.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { // #nosec G304 -- the target's own folder
		_ = d.Sync()
		_ = d.Close()
	}
}
