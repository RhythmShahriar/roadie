// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		sql   string
		kind  core.StmtKind
		table string
	}{
		{"CREATE TABLE public.\"User\" (id int);", core.StmtCreateTable, "User"},
		{"create unlogged table if not exists Shop.Orders (id int);", core.StmtCreateTable, "orders"},
		{"CREATE UNIQUE INDEX x ON t (a);", core.StmtCreateIndex, ""},
		{"CREATE OR REPLACE FUNCTION f() RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql;", core.StmtRoutine, ""},
		{"CREATE MATERIALIZED VIEW v AS SELECT 1;", core.StmtRoutine, ""},
		{"ALTER TABLE ONLY public.t ADD CONSTRAINT p PRIMARY KEY (id);", KindAlterTable, "t"},
		{"DROP TABLE IF EXISTS public.t;", core.StmtDropTable, "t"},
		{"COPY public.t (a) FROM stdin;", core.StmtCopy, "t"},
		{"INSERT INTO public.t VALUES (1);", core.StmtInsert, "t"},
		{"SELECT pg_catalog.set_config('search_path', '', false);", core.StmtSet, ""},
		{"SELECT pg_catalog.setval('public.s', 3, true);", core.StmtSequence, ""},
		{"-- only a comment\n", core.StmtComment, ""},
		{"BEGIN;", core.StmtTransaction, ""},
		{"LOCK TABLE public.t IN ACCESS EXCLUSIVE MODE;", core.StmtLock, "t"},
		{"GRANT ALL ON t TO x;", core.StmtOther, ""},
	} {
		k, tb := Classify([]byte(c.sql))
		if k != c.kind || tb != c.table {
			t.Errorf("%s: %s %q, want %s %q", c.sql, k, tb, c.kind, c.table)
		}
	}
}

func TestParseCopy(t *testing.T) {
	c, ok, opts := ParseCopy([]byte(`COPY public."T" (id, "Name", x) FROM stdin;`))
	if !ok || opts || c.Table != "T" || len(c.Columns) != 3 || c.Columns[1] != "Name" {
		t.Errorf("%+v %v %v", c, ok, opts)
	}
	for _, s := range []string{"COPY t TO stdout;", "COPY t FROM PROGRAM 'x';", "COPY (SELECT 1) TO stdout;", "COPY t (a FROM stdin;"} {
		if _, ok, _ := ParseCopy([]byte(s)); ok {
			t.Errorf("%s read as data", s)
		}
	}
	if _, ok, opts := ParseCopy([]byte("COPY t FROM stdin WITH CSV;")); !ok || !opts {
		t.Error("options not seen")
	}
}

func TestAlterAndIndex(t *testing.T) {
	tb := ParseCreateTable([]byte("CREATE TABLE t (id int NOT NULL, email text, name varchar(10) UNIQUE, data bytea, n int[]);"), Mode{})
	if tb == nil || len(tb.Columns) != 5 || !tb.Columns[2].Unique || tb.Columns[2].MaxLen != 40 || !tb.Columns[3].Binary || tb.Columns[4].MaxLen != 0 {
		t.Fatalf("%+v", tb)
	}
	t2 := AlterTable(tb, []byte("ALTER TABLE ONLY public.t\n    ADD CONSTRAINT t_pkey PRIMARY KEY (id);"), Mode{})
	if t2 == tb || len(t2.PrimaryKey) != 1 || !t2.Columns[0].Unique || tb.Columns[0].Unique {
		t.Errorf("primary key: %+v", t2)
	}
	t3 := AlterTable(t2, []byte("ALTER TABLE t ADD COLUMN extra char(2), DROP COLUMN data;"), Mode{})
	if len(t3.Columns) != 5 || t3.Col("extra") != 4 || t3.Columns[4].MaxLen != 8 || t3.Col("data") >= 0 {
		t.Errorf("add and drop: %+v", t3)
	}
	if AlterTable(t3, []byte("ALTER TABLE ONLY t ALTER COLUMN id SET DEFAULT 1;"), Mode{}) != t3 {
		t.Error("a default changed the table")
	}
	if AlterTable(t3, []byte("ALTER TABLE t ADD CONSTRAINT two UNIQUE (id, email);"), Mode{}) != t3 {
		t.Error("a two-column key made a column unique")
	}
	for _, c := range []struct{ sql, table, col string }{
		{`CREATE UNIQUE INDEX "User_email_key" ON public."User" USING btree (email);`, "User", "email"},
		{"CREATE UNIQUE INDEX x ON t (a, b);", "", ""},
		{"CREATE UNIQUE INDEX x ON t (lower(a));", "", ""},
		{"CREATE UNIQUE INDEX x ON t (a) WHERE b;", "", ""},
		{"CREATE INDEX x ON t (a);", "", ""},
	} {
		if tb, col := UniqueIndex([]byte(c.sql), Mode{}); tb != c.table || col != c.col {
			t.Errorf("%s: %q %q", c.sql, tb, col)
		}
	}
	if ParseCreateTable([]byte("CREATE TABLE t AS SELECT 1;"), Mode{}) != nil {
		t.Error("CREATE TABLE AS read as columns")
	}
}
