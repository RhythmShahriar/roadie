// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RhythmShahriar/roadie/core"
)

// The built MySQL DSN never turns on LOCAL INFILE, multi-statements or
// client-side interpolation, whatever the endpoint's query says.
func TestMySQLConfigSafe(t *testing.T) {
	pw := "s3cr" + "et-pw"
	e := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "db.internal", Port: 3307, User: "roadie", Path: "shop", Password: pw,
		DSN: "mysql://roadie@db.internal:3307/shop?allowAllFiles=true&multiStatements=true&interpolateParams=true&parseTime=true"}
	cfg, err := mysqlConfig(e)
	if err != nil {
		t.Fatal(err)
	}
	dsn := cfg.FormatDSN()
	for _, bad := range []string{"allowAllFiles=true", "multiStatements=true", "interpolateParams=true", "parseTime=true"} {
		if strings.Contains(dsn, bad) {
			t.Fatalf("DSN holds %s: %s", bad, strings.ReplaceAll(dsn, pw, "•••"))
		}
	}
	if cfg.AllowAllFiles || cfg.MultiStatements || cfg.InterpolateParams || cfg.ParseTime {
		t.Fatalf("unsafe config %+v", cfg)
	}
	if cfg.Passwd != pw || cfg.Addr != "db.internal:3307" || cfg.DBName != "shop" || cfg.Net != "tcp" {
		t.Fatalf("config %s %s %s", cfg.Addr, cfg.DBName, cfg.Net)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Fatalf("no utf8mb4 charset: %s", strings.ReplaceAll(dsn, pw, "•••"))
	}
	// A socket and TLS come from the DSN; an unknown TLS value is refused.
	e.DSN = "mysql://roadie@localhost/shop?socket=%2Frun%2Fmysqld.sock&tls=true"
	if cfg, err = mysqlConfig(e); err != nil || cfg.Net != "unix" || cfg.Addr != "/run/mysqld.sock" || cfg.TLSConfig != "true" {
		t.Fatalf("socket config %+v %v", cfg, err)
	}
	e.DSN = "mysql://roadie@localhost/shop?tls=custom"
	if _, err := mysqlConfig(e); err == nil {
		t.Fatal("tls=custom accepted")
	}
}

// PGPASSWORD never supplies the password; the endpoint does.
func TestPGConfigPassword(t *testing.T) {
	t.Setenv("PGPASSWORD", "from-the-env")
	e := core.Endpoint{Kind: core.DSN, Driver: core.Postgres, Host: "pg.internal", User: "roadie", Path: "app", DSN: "postgres://roadie@pg.internal/app?sslmode=disable"}
	cfg, err := pgConfig(e, ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "" || cfg.Host != "pg.internal" || cfg.Port != 5432 || cfg.Database != "app" || cfg.TLSConfig != nil {
		t.Fatalf("config %q %s %d %s", cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	}
	if cfg.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Fatal("a read-only connection doesn't ask for read-only transactions")
	}
	e.Password = "from-" + "the-endpoint"
	if cfg, err = pgConfig(e, ReadWrite); err != nil || cfg.Password != e.Password || cfg.RuntimeParams["default_transaction_read_only"] != "" {
		t.Fatalf("read-write config %v", err)
	}
}

func TestSQLiteDSN(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "db.sqlite")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(file)
	if !strings.HasPrefix(want, "/") {
		want = "/" + want // C:/x is the URI path /C:/x
	}
	ro, err := sqliteDSN(core.Endpoint{Path: file}, ReadOnly)
	if err != nil || !strings.HasPrefix(ro, "file://"+want+"?") || !strings.Contains(ro, "mode=ro") || !strings.Contains(ro, "query_only") {
		t.Fatalf("read-only DSN %q %v", ro, err)
	}
	rw, err := sqliteDSN(core.Endpoint{Path: file}, ReadWrite)
	if err != nil || !strings.Contains(rw, "mode=rw") || strings.Contains(rw, "query_only") {
		t.Fatalf("read-write DSN %q %v", rw, err)
	}
	if _, err := sqliteDSN(core.Endpoint{}, ReadOnly); err == nil {
		t.Fatal("an empty path was accepted")
	}
	// A missing file is not-found with the way out, not the driver's
	// "unable to open database file".
	_, err = sqliteDSN(core.Endpoint{Path: filepath.Join(dir, "new.sqlite")}, ReadWrite)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrNotFound || ce.Exit != 2 || !strings.Contains(ce.Msg, "create it (touch ") {
		t.Fatalf("missing file: %v", err)
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	if _, err := Open(context.Background(), core.Endpoint{Kind: core.DumpFile, Display: "prod.sql"}, ReadOnly); err == nil {
		t.Fatal("a dump file opened as a database")
	}
}

func TestServerVersion(t *testing.T) {
	for in, want := range map[string]string{
		"8.4.3":                          "8.4.3",
		"8.0.36-0ubuntu0.22.04.1":        "8.0.36",
		"11.4.8-MariaDB-ubu2404":         "11.4.8",
		"5.5.5-10.6.12-MariaDB-log":      "10.6.12",
		"16.4 (Debian 16.4-1.pgdg120+2)": "16.4",
		"3.50.4":                         "3.50.4",
	} {
		if got := serverVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestGuessPrefix(t *testing.T) {
	tb := func(names ...string) []*core.Table {
		var out []*core.Table
		for _, n := range names {
			out = append(out, &core.Table{Name: n})
		}
		return out
	}
	cases := []struct {
		tables []*core.Table
		want   string
	}{
		{tb("wp_options", "wp_posts", "wp_users"), "wp_"},
		{tb("wp_options", "wp_posts", "wp_2_options", "wp_2_posts"), "wp_"},
		{tb("shop_options", "shop_posts", "other_x"), "shop_"},
		{tb("options", "posts"), ""},         // no prefix ending in _
		{tb("users", "posts", "orders"), ""}, // no options table
		{tb("wpoptions", "wpposts"), ""},
	}
	for _, c := range cases {
		if got := guessPrefix(c.tables); got != c.want {
			t.Errorf("%v: %q, want %q", c.tables[0].Name, got, c.want)
		}
	}
}

func TestObjectName(t *testing.T) {
	cases := []struct {
		text, raw, name string
		kw              []string
	}{
		{"CREATE TABLE `wp_users` (\n `ID` bigint", "`wp_users`", "wp_users", []string{"table"}},
		{"CREATE TABLE IF NOT EXISTS `a``b` (x int)", "`a``b`", "a`b", []string{"table"}},
		{"DROP TABLE IF EXISTS `wp_posts`;\n", "`wp_posts`", "wp_posts", []string{"table"}},
		{`CREATE TABLE public."User" (`, `public."User"`, "User", []string{"table"}},
		{"CREATE UNLOGGED TABLE public.empty_table (", "public.empty_table", "empty_table", []string{"table"}},
		{"CREATE TABLE [my table] (x)", "[my table]", "my table", []string{"table"}},
		{"/*!40101 SET x */; CREATE TABLE t1(a)", "t1", "t1", []string{"table"}},
		{"-- a table comment\nCREATE TABLE t2 (a)", "t2", "t2", []string{"table"}},
		{"INSERT INTO `wp_users` (`ID`) VALUES ", "`wp_users`", "wp_users", []string{"into", "copy"}},
		{"INSERT IGNORE INTO wp_x VALUES ", "wp_x", "wp_x", []string{"into", "copy"}},
		{"COPY public.\"Post\" (id, title) FROM stdin;\n", `public."Post"`, "Post", []string{"into", "copy"}},
		{"ALTER TABLE ONLY public.t ADD x", "public.t", "t", []string{"table"}},
		// Negative: the keyword inside a string or comment doesn't count.
		{"SELECT 'CREATE TABLE x'", "", "", []string{"table"}},
		{"/* table y */ SELECT 1", "", "", []string{"table"}},
		{"CREATE TABLE", "", "", []string{"table"}},
		{"CREATE TABLE `open", "", "", []string{"table"}},
		{"CREATE TABLE (x int)", "", "", []string{"table"}},
	}
	for _, c := range cases {
		raw, name := objectName([]byte(c.text), c.kw...)
		if raw != c.raw || name != c.name {
			t.Errorf("%q: %q %q, want %q %q", c.text, raw, name, c.raw, c.name)
		}
	}
}

func FuzzObjectName(f *testing.F) {
	for _, s := range []string{"CREATE TABLE `t` (", `COPY public."P" (a) FROM stdin;`, "/*!40101 x */ table [a].b", "table 'x", "table a.", "table `a``"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		raw, name := objectName([]byte(s), "table", "into", "copy")
		if (raw == "") != (name == "") && name != "" {
			t.Fatalf("raw %q name %q", raw, name)
		}
		if raw != "" && !strings.Contains(s, raw) {
			t.Fatalf("raw %q isn't in the input", raw)
		}
	})
}

func TestInsertVerb(t *testing.T) {
	for head, want := range map[string]string{
		"INSERT INTO `t` VALUES ":         "INSERT INTO",
		"insert ignore into `t` VALUES ":  "INSERT IGNORE INTO",
		"REPLACE INTO `t` VALUES ":        "REPLACE INTO",
		"INSERT INTO `ignore_me` VALUES ": "INSERT INTO",
	} {
		if got := insertVerb([]byte(head)); got != want {
			t.Errorf("%q: %q, want %q", head, got, want)
		}
	}
}

func TestArgOf(t *testing.T) {
	v := func(k core.ValueKind, s string) core.Value { return core.Value{Kind: k, Raw: []byte(s)} }
	ok := []struct {
		d    core.Dialect
		v    core.Value
		want any
	}{
		{core.MySQL, core.Value{Kind: core.Null}, nil},
		{core.MySQL, v(core.String, "a'b"), "a'b"},
		{core.MySQL, v(core.Number, "-12.5"), "-12.5"},
		{core.SQLite, v(core.Number, "42"), int64(42)},
		{core.SQLite, v(core.Number, "0.25"), 0.25},
		{core.MySQL, v(core.Hex, "0x4142"), []byte("AB")},
		{core.MySQL, v(core.Hex, "X'41'"), []byte("A")},
		{core.MySQL, v(core.Bit, "b'0101'"), uint64(5)},
		{core.MySQL, v(core.Bit, "0b11"), uint64(3)},
		{core.MySQL, v(core.Expr, "TRUE"), int64(1)},
		{core.MySQL, v(core.BinaryString, "\x00\xff"), []byte("\x00\xff")},
	}
	for _, c := range ok {
		got, err := argOf(c.d, c.v)
		if err != nil || fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", c.want) {
			t.Errorf("%v %q: %#v %v, want %#v", c.v.Kind, c.v.Raw, got, err, c.want)
		}
	}
	// Negative: SQL in a value position is never sent.
	for _, bad := range []core.Value{v(core.Expr, "(SELECT load_file('/etc/passwd'))"), v(core.Expr, "NOW()"), v(core.Hex, "0xZZ"), v(core.Bit, "b'012'")} {
		if _, err := argOf(core.MySQL, bad); err == nil {
			t.Errorf("%q was accepted", bad.Raw)
		}
	}
}

func TestCopyField(t *testing.T) {
	v := func(k core.ValueKind, s string) core.Value { return core.Value{Kind: k, Raw: []byte(s)} }
	for _, c := range []struct {
		v    core.Value
		want string
	}{
		{core.Value{Kind: core.Null}, `\N`},
		{v(core.String, "a\tb\nc\\"), `a\tb\nc\\`},
		{v(core.BinaryString, "\x01\xff"), `\\x01ff`},
		{v(core.Number, "3.5"), "3.5"},
		{v(core.Bit, "B'101'"), "101"},
		{v(core.Hex, "X'0F'"), "00001111"},
		{v(core.Expr, "true"), "t"},
		{v(core.Expr, "'2024-01-02'::date"), "2024-01-02"},
		{v(core.Expr, "'it''s'::text"), "it's"},
	} {
		got, err := copyField(nil, c.v)
		if err != nil || string(got) != c.want {
			t.Errorf("%q: %q %v, want %q", c.v.Raw, got, err, c.want)
		}
	}
	for _, bad := range []core.Value{v(core.Expr, "nextval('s')"), v(core.Expr, "'a'::text || 'b'"), v(core.String, "nul\x00")} {
		if _, err := copyField(nil, bad); err == nil {
			t.Errorf("%q was accepted", bad.Raw)
		}
	}
}

// The error table of the import messages (PRD 17).
func TestImportError(t *testing.T) {
	maria := importState{engine: core.MariaDB, server: "10.6.12", target: "staging@localhost", total: 3}
	mixed := maria
	mixed.committed, mixed.changed = 2, true

	cases := []struct {
		name  string
		err   error
		st    importState
		code  string
		state core.TargetState
		msg   string
	}{
		{"collation 1273", &mysql.MySQLError{Number: 1273, Message: "Unknown collation: 'utf8mb4_0900_ai_ci'"}, maria, core.ErrImport, core.Untouched,
			"import: MariaDB 10.6 doesn't know collation utf8mb4_0900_ai_ci (MySQL 8). Run again with --fix-collation. The target is untouched."},
		{"collation check", &collationError{name: "utf8mb4_uca1400_ai_ci"}, importState{engine: core.MySQL, server: "8.4.3", target: "s@h"}, core.ErrImport, core.Untouched,
			"import: MySQL 8.4 doesn't know collation utf8mb4_uca1400_ai_ci (MariaDB 10.10 and later). Change it in the dump to one the server knows. The target is untouched."},
		{"collation after tables", &collationError{name: "utf8mb4_0900_bin"}, mixed, core.ErrImportMixed, core.Mixed,
			"import: MariaDB 10.6 doesn't know collation utf8mb4_0900_bin (MySQL 8). Run again with --fix-collation. The target is in a mixed state after 2 of 3 tables: some tables are pseudonymised, the rest still hold the earlier copy."},
		{"lost after 2 of 3", fmt.Errorf("exec: %w", driver.ErrBadConn), mixed, core.ErrImportMixed, core.Mixed,
			"import: the connection to staging@localhost was lost after 2 of 3 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
		{"lost before any", mysql.ErrInvalidConn, maria, core.ErrImport, core.Untouched,
			"import: the connection to staging@localhost was lost before any table was replaced. The target is untouched. Run the same command again."},
		{"server gone", &mysql.MySQLError{Number: 2013, Message: "Lost connection"}, mixed, core.ErrImportMixed, core.Mixed,
			"import: the connection to staging@localhost was lost after 2 of 3 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
		{"pg admin shutdown", &pgconn.PgError{Code: "57P01", Message: "terminating connection"}, maria, core.ErrImport, core.Untouched,
			"import: the connection to staging@localhost was lost before any table was replaced. The target is untouched. Run the same command again."},
		{"duplicate masked", &mysql.MySQLError{Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: "Duplicate entry 'jane@example.invalid' for key 'wp_users.user_email'"},
			importState{engine: core.MySQL, current: "wp_users"}, core.ErrImport, core.Untouched,
			"import: table wp_users: MySQL error 1062 (23000): Duplicate entry '•••' for key 'wp_users.user_email'. The target is untouched."},
		{"duplicate with a quote masked", &mysql.MySQLError{Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: "Duplicate entry 'o'brien@example.test' for key 'wp_users.user_email'"},
			importState{engine: core.MySQL, current: "wp_users"}, core.ErrImport, core.Untouched,
			"import: table wp_users: MySQL error 1062 (23000): Duplicate entry '•••' for key 'wp_users.user_email'. The target is untouched."},
		{"pg value masked", &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type integer: "abc"`}, importState{engine: core.Postgres, current: "users", committed: 1, total: 4, changed: true},
			core.ErrImportMixed, core.Mixed,
			"import: table users: PostgreSQL error 22P02: invalid input syntax for type integer: '•••'. The target is in a mixed state after 1 of 4 tables: some tables are pseudonymised, the rest still hold the earlier copy. Fix the cause and run the same command again."},
		{"pg table missing", &pgconn.PgError{Code: "42P01", Message: `relation "migrations" does not exist`}, importState{engine: core.Postgres, current: "migrations", total: 4},
			core.ErrImport, core.Untouched,
			"import: the target has no table migrations and the input doesn't create it. A copy from a live PostgreSQL database brings rows only, so create the schema on the target first (run the app's migrations there or load pg_dump --schema-only), then run the same command again. The target is untouched."},
		{"pg table missing after one", &pgconn.PgError{Code: "42P01", Message: `relation "users" does not exist`}, importState{engine: core.Postgres, current: "users", committed: 1, total: 4, changed: true},
			core.ErrImportMixed, core.Mixed,
			"import: the target has no table users and the input doesn't create it. A copy from a live PostgreSQL database brings rows only, so create the schema on the target first (run the app's migrations there or load pg_dump --schema-only), then run the same command again. The target is in a mixed state after 1 of 4 tables: some tables are pseudonymised, the rest still hold the earlier copy."},
		{"pg duplicate type", &pgconn.PgError{Code: "42710", Message: `type "post_status" already exists`}, importState{engine: core.Postgres},
			core.ErrImport, core.Untouched,
			"import: PostgreSQL error 42710: type \"post_status\" already exists. The target already has the schema this dump creates. Export with pg_dump --clean --if-exists, or empty the target's schema first, then run the same command again. The target is untouched."},
		{"pg duplicate table", &pgconn.PgError{Code: "42P07", Message: `relation "users" already exists`}, importState{engine: core.Postgres},
			core.ErrImport, core.Untouched,
			"import: PostgreSQL error 42P07: relation \"users\" already exists. The target already has the schema this dump creates. Export with pg_dump --clean --if-exists, or empty the target's schema first, then run the same command again. The target is untouched."},
		{"pg duplicate odd message masked", &pgconn.PgError{Code: "42710", Message: `constraint "a'b" for relation "x" already exists`}, importState{engine: core.Postgres},
			core.ErrImport, core.Untouched,
			"import: PostgreSQL error 42710: constraint '•••'. The target already has the schema this dump creates. Export with pg_dump --clean --if-exists, or empty the target's schema first, then run the same command again. The target is untouched."},
		{"pg unique violation still masked", &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "users_email_key"`}, importState{engine: core.Postgres},
			core.ErrImport, core.Untouched,
			"import: PostgreSQL error 23505: duplicate key value violates unique constraint '•••'. The target is untouched."},
		{"unknown total", driver.ErrBadConn, importState{target: "app@db", committed: 5, changed: true}, core.ErrImportMixed, core.Mixed,
			"import: the connection to app@db was lost after 5 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
		{"one table, unknown total", &mysql.MySQLError{Number: 1064, SQLState: [5]byte{'4', '2', '0', '0', '0'}, Message: "You have an error in your SQL syntax"},
			importState{engine: core.MySQL, target: "app@db", committed: 1, changed: true}, core.ErrImportMixed, core.Mixed,
			"import: MySQL error 1064 (42000): You have an error in your SQL syntax. The target is in a mixed state after 1 table: some tables are pseudonymised, the rest still hold the earlier copy. Fix the cause and run the same command again."},
		{"one of one", driver.ErrBadConn, importState{target: "app@db", committed: 1, total: 1, changed: true}, core.ErrImportMixed, core.Mixed,
			"import: the connection to app@db was lost after 1 of 1 table. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
		{"first table broke", driver.ErrBadConn, importState{target: "app@db", current: "wp_posts", changed: true}, core.ErrImportMixed, core.Mixed,
			"import: the connection to app@db was lost during table wp_posts. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
	}
	for _, c := range cases {
		err := importError(c.err, c.st)
		var ce *core.Error
		if !errors.As(err, &ce) {
			t.Fatalf("%s: %v is not a *core.Error", c.name, err)
		}
		if ce.Code != c.code || ce.Exit != 3 || ce.State != c.state || ce.Msg != c.msg {
			t.Errorf("%s:\n got %s %d %s %q\nwant %s 3 %s %q", c.name, ce.Code, ce.Exit, ce.State, ce.Msg, c.code, c.state, c.msg)
		}
	}
	// Negative: a canceled context and an error that is already worded
	// pass through unchanged.
	if err := importError(context.Canceled, mixed); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	own := core.Errorf(core.ErrDumpRefused, 3, "dump-refused: x")
	if err := importError(own, mixed); err != own { //nolint:errorlint // the same value must come back
		t.Fatalf("worded: %v", err)
	}
	if importError(nil, mixed) != nil {
		t.Fatal("nil error worded")
	}
	// Negative: a plain driver error is not a lost connection.
	if lostConnection(&mysql.MySQLError{Number: 1062}) || lostConnection(errors.New("syntax error")) {
		t.Fatal("an ordinary error counted as a lost connection")
	}
}

func TestBitLiteral(t *testing.T) {
	if got := string(bitLiteral([]byte{0x05})); got != "b'00000101'" {
		t.Fatalf("%q", got)
	}
	if got := string(bitLiteral(nil)); got != "b'0'" {
		t.Fatalf("%q", got)
	}
}

func TestValueKind(t *testing.T) {
	for _, c := range []struct {
		d    core.Dialect
		col  core.Column
		want core.ValueKind
	}{
		{core.MySQL, core.Column{Type: "bigint unsigned"}, core.Number},
		{core.MySQL, core.Column{Type: "decimal(10,2)"}, core.Number},
		{core.MySQL, core.Column{Type: "bit(8)"}, core.Bit},
		{core.MySQL, core.Column{Type: "longblob", Binary: true}, core.BinaryString},
		{core.MySQL, core.Column{Type: "point"}, core.BinaryString},
		{core.MySQL, core.Column{Type: "datetime"}, core.String},
		{core.Postgres, core.Column{Type: "double precision"}, core.Number},
		{core.Postgres, core.Column{Type: "numeric(12,2)"}, core.Number},
		{core.Postgres, core.Column{Type: "bytea", Binary: true}, core.BinaryString},
		{core.Postgres, core.Column{Type: "boolean"}, core.String},
		{core.Postgres, core.Column{Type: "timestamp without time zone"}, core.String},
	} {
		if got := valueKind(c.d, c.col); got != c.want {
			t.Errorf("%s %s: %d, want %d", c.d, c.col.Type, got, c.want)
		}
	}
}

// The frozen API says so: no call on a closed connection works.
func TestClosedConn(t *testing.T) {
	c := &Conn{closed: true}
	if _, err := c.Schema(context.Background()); !errors.Is(err, errClosed) {
		t.Fatal(err)
	}
	if _, err := c.Source(context.Background(), nil); !errors.Is(err, errClosed) {
		t.Fatal(err)
	}
	if _, err := c.Sink(context.Background(), SinkOptions{}); !errors.Is(err, errClosed) {
		t.Fatal(err)
	}
	if _, err := c.Identity(context.Background()); !errors.Is(err, errClosed) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestQualifiedName(t *testing.T) {
	for raw, want := range map[string]bool{
		"`t`": false, "t": false, "`a.b`": false, `"x.y"`: false, "[a.b]": false,
		"`prod`.`t`": true, "mysql.user": true, `public."P"`: true, "`a.b`.c": true, "`open": false,
	} {
		if got := qualifiedName(raw); got != want {
			t.Errorf("%q: %v, want %v", raw, got, want)
		}
	}
}

// A MySQL sink builds its SQL from the bare table name and refuses a
// name with a database in front; PostgreSQL keeps its schema names.
func TestTableName(t *testing.T) {
	my := &sink{c: &Conn{engine: core.MySQL}}
	for _, c := range [][3]string{
		{"`wp_users`", "wp_users", "`wp_users`"},
		{"wp_x", "wp_x", "`wp_x`"},
		{"`a``b`", "a`b", "`a``b`"},
		{`"wp_y"`, "wp_y", "`wp_y`"},
		{"", "", ""},
	} {
		if q, err := my.tableName(c[0], c[1], 7); err != nil || q != c[2] {
			t.Errorf("%q: %q %v, want %q", c[0], q, err, c[2])
		}
	}
	for _, raw := range []string{"`prod`.`wp_options`", "mysql.user", "`shop`.t"} {
		_, err := my.tableName(raw, "x", 7)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Exit != 3 || ce.Line != 7 ||
			!strings.HasPrefix(ce.Msg, "dump-refused: line 7 of the input names a table with its database") {
			t.Errorf("%q: %v", raw, err)
		}
	}
	if _, err := (&sink{c: &Conn{engine: core.MariaDB}}).tableName("a.b", "b", 0); err == nil || !strings.Contains(err.Error(), "dump-refused: the input names") {
		t.Errorf("MariaDB: %v", err)
	}
	pg := &sink{c: &Conn{engine: core.Postgres}}
	if q, err := pg.tableName(`public."Post"`, "Post", 1); err != nil || q != `public."Post"` {
		t.Errorf("postgres: %q %v", q, err)
	}
}

// A source table's CREATE TABLE passes the dump allowlist before it is
// recreated or written: the DDL SHOW CREATE TABLE prints for an ordinary
// table is kept and one that reaches another server or the file system
// is refused without quoting it.
func TestCheckDDL(t *testing.T) {
	for _, create := range []string{
		"CREATE TABLE `wp_users` (\n  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_email` varchar(100) NOT NULL DEFAULT '',\n  PRIMARY KEY (`ID`)\n) ENGINE=InnoDB AUTO_INCREMENT=3 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci",
		"CREATE TABLE `m` (\n  `a` int DEFAULT NULL\n) ENGINE=MRG_MyISAM DEFAULT CHARSET=latin1 UNION=(`t1`,`t2`)",
	} {
		if err := checkDDL(core.MySQL, "t", create); err != nil {
			t.Errorf("%q: %v", create, err)
		}
	}
	pw := "s3cr" + "et-pw"
	for _, c := range []struct{ create, want string }{
		{"CREATE TABLE `f` (\n  `a` int\n) ENGINE=FEDERATED DEFAULT CHARSET=utf8mb4 CONNECTION='mysql://app:" + pw + "@prod.internal:3306/shop/f'", "holds a table with ENGINE=FEDERATED"},
		{"CREATE TABLE `d` (\n  `a` int\n) ENGINE=InnoDB DATA DIRECTORY='/var/lib/other/'", "holds a CREATE TABLE ... DATA DIRECTORY statement"},
		{"CREATE TABLE `m` (\n  `a` int\n) ENGINE=MRG_MyISAM UNION=(`other`.`t1`)", "holds a CREATE TABLE ... UNION statement"},
	} {
		err := checkDDL(core.MariaDB, "t", c.create)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused ||
			!strings.HasPrefix(ce.Msg, "dump-refused: table t on the source database "+c.want) {
			t.Errorf("%q: %v", c.create, err)
			continue
		}
		if strings.Contains(ce.Msg, pw) || strings.Contains(ce.Msg, "/var/lib") {
			t.Errorf("message quotes the DDL: %s", ce.Msg)
		}
	}
	if err := checkDDL(core.SQLite, "t", "CREATE TABLE t (a)"); err != nil {
		t.Error(err)
	}
}

func TestDelimiter(t *testing.T) {
	for in, want := range map[string]string{
		"DELIMITER ;;\n": ";;", "delimiter $$": "$$", "DELIMITER ;\n": "", "DELIMITER": "", "DELIMITER // trailing\n": "//",
	} {
		if got := delimiterArg([]byte(in)); got != want {
			t.Errorf("delimiterArg(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct{ in, delim, want string }{
		{"END */;;\n", ";;", "END */"},
		{"END$$\r\n", "$$", "END"},
		{"SELECT 1;\n", "", "SELECT 1"},
		{"SELECT ';;' AS a\n", ";;", "SELECT ';;' AS a"},
	} {
		if got := stmtText(trimDelim([]byte(c.in), c.delim)); got != c.want {
			t.Errorf("trimDelim(%q, %q) = %q, want %q", c.in, c.delim, got, c.want)
		}
	}
}
