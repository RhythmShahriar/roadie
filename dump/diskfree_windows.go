// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package dump

import (
	"errors"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskFree is the space available to this user in dir.
func diskFree(dir string) (int64, bool) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var avail, total, free uint64
	r, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), // #nosec G103 -- the documented Win32 call
		uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free))) // #nosec G103
	if r == 0 {
		return 0, false
	}
	if avail > 1<<62 {
		avail = 1 << 62
	}
	return int64(avail), true // #nosec G115 -- capped above
}

// The Win32 errors for a full disk.
const (
	errorHandleDiskFull syscall.Errno = 39
	errorDiskFull       syscall.Errno = 112
)

func isNoSpace(err error) bool {
	return errors.Is(err, errorDiskFull) || errors.Is(err, errorHandleDiskFull) || errors.Is(err, syscall.ENOSPC)
}

// syncDir does nothing: Windows has no folder fsync and MoveFileEx is
// durable once the file itself was synced.
func syncDir(string) {}
