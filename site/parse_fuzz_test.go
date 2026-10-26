// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"strings"
	"testing"
)

// FuzzParseFind: find output from a remote host never panics and every
// entry stays inside the listed folder.
func FuzzParseFind(f *testing.F) {
	f.Add([]byte("d 4096 uploads\nf 10 uploads/x.sql\n"), "wp-content")
	f.Add([]byte("f -1 a\nf 99999999999999999999 b\n\n  \n"), ".")
	f.Fuzz(func(t *testing.T, out []byte, base string) {
		r, err := cleanRel(base)
		if err != nil {
			return
		}
		for _, fi := range parseFind(out, r) {
			if r != "." && !strings.HasPrefix(fi.Rel, r) {
				t.Fatalf("%q outside %q", fi.Rel, r)
			}
			if fi.Dir && fi.Size != 0 {
				t.Fatalf("folder with a size: %+v", fi)
			}
		}
	})
}

// FuzzSplitWPHost: any DB_HOST value is read without panicking; a
// socket always starts with "/".
func FuzzSplitWPHost(f *testing.F) {
	for _, s := range []string{"localhost", "db:3307", "localhost:/run/x.sock", "[::1]:3306", "::1", ":", "["} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, port, socket := splitWPHost(s)
		if socket != "" && !strings.HasPrefix(socket, "/") {
			t.Fatalf("socket %q", socket)
		}
		_ = port
	})
}
