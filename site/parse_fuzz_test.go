// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/site/remote"
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
			if fi.Rel == ".." || strings.HasPrefix(fi.Rel, "../") || strings.HasPrefix(fi.Rel, "/") ||
				(r != "." && !strings.HasPrefix(fi.Rel, r+"/")) {
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

// parseLs never panics; what it accepts always builds an allowlisted
// write command.
func FuzzParseLs(f *testing.F) {
	for _, s := range []string{"-rw-r--r-- 1 1001 33 10 Oct 26 10:00 /x\n", "-rwsr-xr-T 1 0 0", "drwx 1 0 0", "-"} {
		f.Add([]byte(s))
	}
	r := &remote.Runner{Host: "h"}
	f.Fuzz(func(t *testing.T, out []byte) {
		st, err := parseLs(out)
		if err != nil {
			return
		}
		if st.mode&^0o777 != 0 {
			t.Fatalf("mode %o", st.mode)
		}
		mode := fmt.Sprintf("%04o", st.mode)
		if _, err := r.Command(remote.WriteKeep, "/t", st.uid, st.gid, "/t", st.gid, "/t", mode, "/t", "/t", "/p"); err != nil {
			t.Fatal(err)
		}
	})
}
