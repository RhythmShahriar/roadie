// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

func pgFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("pgsql", "testdata", name)) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func openPG(t testing.TB, in []byte, o Options) (core.Source, []*core.Batch, error) {
	t.Helper()
	if o.Name == "" {
		o.Name = "app.sql"
	}
	src, err := Open(context.Background(), bytes.NewReader(in), o)
	if err != nil {
		return nil, nil, err
	}
	bs, err := drainAll(t, src)
	return src, bs, err
}

// A pg_dump 16 dump opens as PostgreSQL with its server version, its
// schema and its rows; plain and gzip give the same batches.
func TestOpenPostgres(t *testing.T) {
	plain := pgFixture(t, "pg16-prisma.sql")
	var want []*core.Batch
	for _, in := range [][]byte{plain, pack(t, compress.Gzip, plain)} {
		src, bs, err := openPG(t, in, Options{KeepRoutines: true})
		if err != nil {
			t.Fatal(err)
		}
		sc := src.Schema()
		if sc.Dialect != core.Postgres || sc.Server != "16.14" || len(sc.Tables) != 5 || sc.Table("User") == nil {
			t.Fatalf("schema %+v", sc)
		}
		rows := 0
		for _, b := range bs {
			rows += len(b.Rows)
		}
		if rows != 10 {
			t.Errorf("%d rows", rows)
		}
		if fp := src.Fingerprint(); fp.Kind != "sha256-file" || fp.Bytes != int64(len(in)) {
			t.Errorf("fingerprint %+v", fp)
		}
		notes := Notes(src)
		if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "OWNER TO") {
			t.Errorf("notes %q", notes)
		}
		if want == nil {
			want = comparable(bs)
		} else if len(comparable(bs)) != len(want) {
			t.Error("gzip batches differ")
		}
	}
}

// The function and trigger of the fixture are dollar-quoted code: they
// pass only with --keep-routines.
func TestOpenPostgresRoutines(t *testing.T) {
	_, _, err := openPG(t, pgFixture(t, "pg16-prisma.sql"), Options{})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Exit != 3 ||
		ce.Msg != "dump-refused: app.sql line 37 holds a CREATE FUNCTION statement, which could run code on the staging database. Pass --keep-routines to keep it, or leave it out of the export." {
		t.Fatalf("err = %v", err)
	}
	_, bs, err := openPG(t, pgFixture(t, "pg16-prisma.sql"), Options{KeepRoutines: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range bs {
		if b.Pass != nil && bytes.Contains(b.Pass.Text, []byte("AS $$\nBEGIN\n    -- a ';' and a 'quote' inside the body")) {
			found = true
		}
	}
	if !found {
		t.Error("the function body was not passed through whole")
	}
}

// Every fixture copies through a PostgreSQL stream sink to a dump that
// reads back with the same rows and pg_dump's end line.
func TestPostgresSinkRoundTrip(t *testing.T) {
	for _, name := range []string{"pg16-prisma.sql", "pg16-inserts.sql", "pg16-column-inserts.sql"} {
		_, bs, err := openPG(t, pgFixture(t, name), Options{KeepRoutines: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out bytes.Buffer
		s := NewStreamSink(&out, WriterOptions{Target: core.Postgres, Source: core.Postgres})
		for _, b := range bs {
			if err := s.Write(context.Background(), b); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte("OWNER TO")) {
			t.Errorf("%s: OWNER TO kept", name)
		}
		_, again, err := openPG(t, out.Bytes(), Options{KeepRoutines: true})
		if err != nil {
			t.Fatalf("%s: reread: %v", name, err)
		}
		if n, m := countRows(bs), countRows(again); n != m || n != 10 {
			t.Errorf("%s: %d rows, then %d", name, n, m)
		}
	}
}

func countRows(bs []*core.Batch) int {
	n := 0
	for _, b := range bs {
		n += len(b.Rows)
	}
	return n
}

// reEntered finds the statements after which the reader is in a table:
// a whole CREATE TABLE or COPY line.
var reEntered = regexp.MustCompile(`(?m)^(?:CREATE TABLE|COPY) public\.("?)(\w+)("?)[^;]*;\n`)

// A pg_dump file cut anywhere before its end line is dump-truncated and
// names the table the reader was in.
func TestPostgresTruncated(t *testing.T) {
	in := pgFixture(t, "pg16-prisma.sql")
	end := bytes.Index(in, []byte("-- PostgreSQL database dump complete"))
	r := rand.New(rand.NewPCG(7, 26)) // #nosec G404 -- test offsets
	for range 10 {
		cut := 1 + r.IntN(end-1)
		_, _, err := openPG(t, in[:cut], Options{KeepRoutines: true, Name: "prod.sql"})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpTruncated || ce.Exit != 3 {
			t.Fatalf("cut at %d: %v", cut, err)
		}
		table := ""
		for _, m := range reEntered.FindAllSubmatchIndex(in, -1) {
			if m[1] <= cut {
				table = string(in[m[4]:m[5]])
			}
		}
		ok := strings.HasPrefix(ce.Msg, "dump-truncated: prod.sql ends inside table "+table+" at ")
		if table == "" {
			// Before any table: "ends early" or "ends inside a statement".
			ok = strings.HasPrefix(ce.Msg, "dump-truncated: prod.sql ends ") && !strings.Contains(ce.Msg, "inside table")
		}
		if !ok {
			t.Errorf("cut at %d: %s, want table %q", cut, ce.Msg, table)
		}
	}
	gz := pack(t, compress.Gzip, in)
	_, _, err := openPG(t, gz[:len(gz)/2], Options{KeepRoutines: true})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpTruncated || !strings.Contains(ce.Msg, "the gzip stream is cut or damaged") {
		t.Errorf("cut gzip: %v", err)
	}
}

// COPY that runs a program or reads a server file is refused, wherever
// it stands.
func TestPostgresCopyRefused(t *testing.T) {
	head := "--\n-- PostgreSQL database dump\n--\n\nCREATE TABLE public.t (a text);\n"
	for _, c := range []struct{ sql, kind string }{
		{"COPY public.t (a) FROM PROGRAM 'id';\n", "a COPY ... FROM PROGRAM statement"},
		{"COPY public.t FROM '/etc/passwd';\n", "a COPY statement that reads a server file"},
		{"\\copy t from 'x'\n", "a psql meta-command (\\copy)"},
	} {
		_, _, err := openPG(t, []byte(head+c.sql), Options{})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || !strings.Contains(ce.Msg, "line 6 holds "+c.kind) {
			t.Errorf("%q: %v", c.sql, err)
		}
	}
}

func TestCheckDialect(t *testing.T) {
	err := CheckDialect("prod.sql", core.MySQL, core.Postgres, true)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDialectMismatch || ce.Exit != 2 ||
		ce.Msg != "dialect-mismatch: prod.sql is a MySQL dump and --to is a PostgreSQL database. Roadie copies MySQL to MySQL or MariaDB and PostgreSQL to PostgreSQL." {
		t.Errorf("err = %v", err)
	}
	if err := CheckDialect("app.sql", core.Postgres, core.MariaDB, false); err == nil || !strings.Contains(err.Error(), "is a PostgreSQL dump and --to is a MariaDB dump.") {
		t.Errorf("err = %v", err)
	}
	for _, c := range [][2]core.Dialect{{core.MySQL, core.MariaDB}, {core.MariaDB, core.MySQL}, {core.Postgres, core.Postgres}, {"", core.Postgres}} {
		if err := CheckDialect("x.sql", c[0], c[1], true); err != nil {
			t.Errorf("%s to %s: %v", c[0], c[1], err)
		}
	}
}
