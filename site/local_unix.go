// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package site

import (
	"io/fs"
	"os"
	"syscall"
)

// keepOwner gives the temp file the owner of the file it replaces, when
// Roadie runs as root: else a root run would leave wp-config.php owned by
// root and the web server unable to read it.
func keepOwner(root *os.Root, tmp string, orig fs.FileInfo) error {
	if orig == nil || os.Geteuid() != 0 {
		return nil
	}
	st, ok := orig.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return root.Chown(tmp, int(st.Uid), int(st.Gid))
}

// renameReplace renames tmp over name; on Unix a rename replaces an open
// file atomically.
func renameReplace(root *os.Root, tmp, name, _ string) error {
	return root.Rename(tmp, name)
}
