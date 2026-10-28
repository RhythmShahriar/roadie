// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// A PostgreSQL COMMENT ON literal is scrubbed by copy and scanned by
// verify under <table>.(schema), as a MySQL column COMMENT is.
func TestPGCommentOn(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	testutil.NoNetwork(t)
	email := testutil.J("jane.realperson", "@gmail.com")
	in := "--\n-- PostgreSQL database dump\n--\n\nSET standard_conforming_strings = on;\n\n" +
		"CREATE TABLE public.notes (\n    id integer NOT NULL,\n    body text\n);\n\n" +
		"COMMENT ON TABLE public.notes IS '" + email + "';\n\n" +
		"COMMENT ON COLUMN public.notes.body IS 'free text';\n\n" +
		"COPY public.notes (id, body) FROM stdin;\n1\thello\n\\.\n\n" +
		"--\n-- PostgreSQL database dump complete\n--\n\n"
	if err := os.WriteFile("pg.sql", []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCmd(t, "verify", "pg.sql", "--offline", "--plain")
	if code != 1 || !strings.Contains(out, "notes.(schema)") || !strings.Contains(out, "pii.email") {
		t.Errorf("verify of the input: exit %d\n%s%s", code, out, errOut)
	}
	out, errOut, code = runCmd(t, "copy", "--from", "pg.sql", "--to", "out.sql", "--stack", "sql", "--yes", "--offline", "--plain")
	if code != 0 {
		t.Fatalf("copy: exit %d\n%s%s", code, out, errOut)
	}
	b, _ := os.ReadFile("out.sql")
	if strings.Contains(string(b), email) || !strings.Contains(string(b), "COMMENT ON TABLE public.notes IS '") ||
		!strings.Contains(string(b), "COMMENT ON COLUMN public.notes.body IS 'free text';") {
		t.Errorf("copy:\n%s", b)
	}
	if out, errOut, code = runCmd(t, "verify", "out.sql", "--offline", "--plain"); code != 0 {
		t.Errorf("verify of the copy: exit %d\n%s%s", code, out, errOut)
	}
}
