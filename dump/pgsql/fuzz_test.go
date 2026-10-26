// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// FuzzCopy reads arbitrary COPY data: it never panics and whatever reads
// without error writes back byte for byte, untouched or with every value
// changed to what it decoded to.
func FuzzCopy(f *testing.F) {
	for _, s := range []string{
		"a\tb\n", "\\N\t\n", "x\\ty\\\\z\t\\101\\x41\n", "\t\n\\.\n", "a\tb\r\n", "\\", "a\tb\tc\n", "",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		in := append([]byte("CREATE TABLE t (a text, b text);\nCOPY t (a, b) FROM stdin;\n"), data...)
		in = append(in, "\n\\.\n"...)
		bs, _, err := drain(t, in, SourceOptions{})
		if err != nil {
			return
		}
		if out := write(t, bs); !bytes.Equal(out, in) {
			t.Fatalf("identity round trip differs:\n%q\n%q", in, out)
		}
		// Encode every value again: the data reads back the same.
		var want [][]core.Value
		for _, b := range bs {
			for i := range b.Rows {
				vs := b.Rows[i].Values
				want = append(want, append([]core.Value(nil), vs...))
				for j := range vs {
					if bytes.IndexByte(vs[j].Raw, 0) >= 0 {
						return // PostgreSQL text can't hold NUL
					}
					vs[j].Dirty = true
				}
			}
		}
		again, _, err := drain(t, write(t, bs), SourceOptions{})
		if err != nil {
			t.Fatalf("encoded rows don't read: %v", err)
		}
		k := 0
		for _, b := range again {
			for _, r := range b.Rows {
				for j, v := range r.Values {
					if v.Kind != want[k][j].Kind || !bytes.Equal(v.Raw, want[k][j].Raw) {
						t.Fatalf("row %d value %d: %q, want %q", k, j, v.Raw, want[k][j].Raw)
					}
				}
				k++
			}
		}
	})
}

// FuzzCopyField: the COPY encoder and decoder are inverses.
func FuzzCopyField(f *testing.F) {
	f.Add([]byte("a\tb\\n\x01\xff"))
	f.Fuzz(func(t *testing.T, b []byte) {
		enc, err := AppendCopyField(nil, core.Value{Kind: core.String, Raw: b})
		if bytes.IndexByte(b, 0) >= 0 {
			if err == nil {
				t.Fatal("NUL accepted")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsAny(enc, "\t\n\r") {
			t.Fatalf("raw separator in %q", enc)
		}
		if got := AppendCopyUnescape(nil, enc); !bytes.Equal(got, b) {
			t.Fatalf("%q -> %q -> %q", b, enc, got)
		}
	})
}

// FuzzStatements reads arbitrary dumps: never a panic; a dump that
// reads writes back byte for byte.
func FuzzStatements(f *testing.F) {
	for _, s := range []string{
		"SELECT 1;\n", "CREATE TABLE t (a text);\nINSERT INTO t VALUES ('x'), (E'\\n');\n",
		"CREATE FUNCTION f() AS $a$ ; $a$;\n", "/* /* */ */;\n", "\\! x\n", "COPY t (a) FROM stdin;\nx\n\\.\n",
		"SET standard_conforming_strings = off;\nCREATE TABLE t (a text);\nINSERT INTO t VALUES ('\\'');\n",
		"CREATE TABLE \"q\"\"t\" (\"a\" int);\nINSERT INTO \"q\"\"t\" (\"a\") VALUES (1);\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		bs, _, err := drain(t, in, SourceOptions{MaxValue: 1 << 20})
		if err != nil {
			return
		}
		if out := write(t, bs); !bytes.Equal(out, in) {
			t.Fatalf("round trip differs:\n%q\n%q", in, out)
		}
	})
}

// FuzzSQLString: a string encoded for an INSERT reads back as itself in
// both quoting modes.
func FuzzSQLString(f *testing.F) {
	f.Add([]byte("it's \\ fine"), false)
	f.Fuzz(func(t *testing.T, b []byte, off bool) {
		if bytes.IndexByte(b, 0) >= 0 {
			return
		}
		m := Mode{NoStdStrings: off}
		enc, err := AppendString(nil, b, m)
		if err != nil {
			t.Fatal(err)
		}
		in := append([]byte("CREATE TABLE t (a text);\nINSERT INTO t VALUES ("), enc...)
		in = append(in, ");\n"...)
		if off {
			in = append([]byte("SET standard_conforming_strings = off;\n"), in...)
		}
		bs, _, err := drain(t, in, SourceOptions{})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		r := rowsOf(bs, "t")
		if len(r) != 1 || !bytes.Equal(r[0].Values[0].Raw, b) {
			t.Fatalf("%q read back as %v", b, r)
		}
	})
}
