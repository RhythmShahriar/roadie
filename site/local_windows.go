// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package site

import (
	"fmt"
	"io/fs"
	"os"
	"time"
)

// keepOwner does nothing on Windows, where the file's ACL comes from its
// folder.
func keepOwner(*os.Root, string, fs.FileInfo) error { return nil }

// renameTries and renameWait bound the retries: 10 over 2 seconds.
const (
	renameTries = 10
	renameWait  = 200 * time.Millisecond
)

// renameReplace renames tmp over name. Windows refuses to replace a file
// another program holds open (an editor, an antivirus scan, PHP itself),
// so the rename is retried for about 2 seconds before giving up.
func renameReplace(root *os.Root, tmp, name, rel string) error {
	var err error
	for i := 0; i < renameTries; i++ {
		if err = root.Rename(tmp, name); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return fmt.Errorf("site: couldn't replace %s: another program holds it open (%w). Close it and run again", rel, err)
}
