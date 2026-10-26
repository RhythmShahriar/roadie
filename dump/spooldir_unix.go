// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package dump

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

// tightenSpoolDir sets an existing spool folder that others can read
// back to 0700. MkdirAll leaves an existing folder's mode as it is. Only
// a folder that is evidently Roadie's is changed: owned by this user,
// without the sticky bit (/tmp) and holding nothing but spool files, so
// a shared folder passed by mistake keeps its mode. The file inside is
// 0600 and encrypted either way.
func tightenSpoolDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || fi.Mode().Perm()&0o077 == 0 || fi.Mode()&os.ModeSticky != 0 || int(st.Uid) != os.Geteuid() {
		return nil
	}
	f, err := os.Open(dir) // #nosec G304 -- the spool folder the caller named
	if err != nil {
		return err
	}
	// A spool folder holds a few files; more than 64 entries is some
	// other folder.
	names, err := f.Readdirnames(65)
	_ = f.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(names) > 64 {
		return nil
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ".spool") {
			return nil
		}
	}
	return os.Chmod(dir, 0o700) // #nosec G302 -- a folder needs the x bit to be entered
}
