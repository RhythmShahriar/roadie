// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
)

// Pinned server images and the loopback ports this lane may use.
const (
	imgMySQL    = "mysql:8.4"
	imgMariaDB  = "mariadb:11.4.8"
	imgPostgres = "postgres:16"
)

// Loopback ports of this lane (33000-33019), a few per server: a port
// can be held for a moment by a client's ephemeral socket.
var (
	portsMySQL   = []int{33010, 33013, 33016}
	portsMariaDB = []int{33011, 33014, 33017}
	portsPG      = []int{33012, 33015, 33019}
)

// metaRows spans three keyset pages.
const metaRows = 2*pageRows + 1234

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// startContainer runs image on the first free 127.0.0.1 port of ports
// and removes it when the test ends. Nothing else on the machine is
// touched. It returns the port.
func startContainer(t *testing.T, image string, ports []int, inner int, env ...string) int {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	var last []byte
	for _, port := range ports {
		name := "roadie-io-test-" + randHex(t, 4)
		args := []string{"run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, inner)}
		for _, e := range env {
			args = append(args, "-e", e)
		}
		args = append(args, image)
		out, err := exec.Command("docker", args...).CombinedOutput()
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
		if err == nil {
			return port
		}
		last = out
	}
	t.Fatalf("docker run %s: no free port in %v\n%s", image, ports, last)
	return 0
}

// waitOpen opens e until the server answers, for up to three minutes.
func waitOpen(t *testing.T, e core.Endpoint, m Mode) *Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, err := Open(ctx, e, m)
		cancel()
		if err == nil {
			t.Cleanup(func() { _ = c.Close() })
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never answered: %v", e.Display, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func mustExec(t *testing.T, c *Conn, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := c.conn.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func copyAll(t *testing.T, src core.Source, sk core.Sink) {
	t.Helper()
	if err := copyDB(context.Background(), src, sk); err != nil {
		t.Fatal(err)
	}
	if sk.State() != core.Replaced {
		t.Fatalf("state %s", sk.State())
	}
}

func myEP(port int, user, pw, db string) core.Endpoint {
	d := fmt.Sprintf("mysql://%s@127.0.0.1:%d/%s", user, port, db)
	return core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "127.0.0.1", Port: port, User: user, Password: pw, Path: db, DSN: d, Display: d}
}

func TestDockerMySQL(t *testing.T)   { testMySQLFamily(t, imgMySQL, portsMySQL, core.MySQL) }
func TestDockerMariaDB(t *testing.T) { testMySQLFamily(t, imgMariaDB, portsMariaDB, core.MariaDB) }

func testMySQLFamily(t *testing.T, image string, ports []int, engine core.Dialect) {
	ctx := context.Background()
	root := randHex(t, 12)
	reader := randHex(t, 12)
	envName := "MYSQL_ROOT_PASSWORD"
	if engine == core.MariaDB {
		envName = "MARIADB_ROOT_PASSWORD"
	}
	port := startContainer(t, image, ports, 3306, envName+"="+root)
	admin := waitOpen(t, myEP(port, "root", root, "mysql"), ReadWrite)
	if admin.engine != engine {
		t.Fatalf("engine %s, want %s", admin.engine, engine)
	}
	// sql_mode '' allows the zero date the fixture holds, as old
	// WordPress databases do.
	mustExec(t, admin,
		"SET SESSION sql_mode = ''",
		"CREATE DATABASE src CHARACTER SET utf8mb4",
		"CREATE DATABASE dst CHARACTER SET utf8mb4",
		"CREATE DATABASE fixture CHARACTER SET utf8mb4",
		"CREATE TABLE src.wp_options (option_id bigint unsigned NOT NULL AUTO_INCREMENT, option_name varchar(191) NOT NULL, option_value longtext NOT NULL, PRIMARY KEY (option_id), UNIQUE KEY option_name (option_name)) DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE src.wp_postmeta (meta_id bigint unsigned NOT NULL AUTO_INCREMENT, post_id bigint unsigned NOT NULL, meta_key varchar(255), meta_value longtext, PRIMARY KEY (meta_id)) DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE src.wp_mixed (a int NOT NULL, b varchar(20) NOT NULL, f float, d decimal(10,2), flags bit(8), raw varbinary(16), at datetime, latin varchar(10) CHARACTER SET latin1, twice int GENERATED ALWAYS AS (a * 2) VIRTUAL, PRIMARY KEY (a, b))",
		"CREATE TABLE src.nopk (x text, y int)",
		`INSERT INTO src.wp_options (option_name, option_value) VALUES ('siteurl', 'https://shop.example.invalid'), ('blogname', 'It''s a \\ shop'), ('empty', '')`,
		"INSERT INTO src.wp_mixed (a, b, f, d, flags, raw, at, latin) VALUES (1, 'x', 1.1, 12.50, b'00000101', 0x00FF10, '2026-10-26 12:00:00', 'café'), (1, 'y', NULL, NULL, NULL, NULL, '0000-00-00 00:00:00', NULL), (2, 'a', -0.5, -1.00, b'11111111', X'', '2001-01-01 00:00:00', '')",
		"INSERT INTO src.nopk VALUES ('n1', 1), (NULL, NULL), ('n3', 3)",
		"CREATE USER 'reader'@'%' IDENTIFIED BY '"+reader+"'",
		"GRANT SELECT ON src.* TO 'reader'@'%'",
	)
	for i := 0; i < metaRows; i += 1000 {
		var sb strings.Builder
		sb.WriteString("INSERT INTO src.wp_postmeta (post_id, meta_key, meta_value) VALUES ")
		for j := i; j < i+1000 && j < metaRows; j++ {
			if j > i {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "(%d, '_billing_email', 'a:1:{s:5:\"email\";s:29:\"customer-%05d@example.invalid\";}')", j/3, j%100000)
		}
		mustExec(t, admin, sb.String())
	}

	// The SELECT-only user runs the source.
	rc := waitOpen(t, myEP(port, "reader", reader, "src"), ReadOnly)
	id, err := rc.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Engine != engine || id.Name != "src" || id.User != "reader" || id.Port != port || id.Prefix != "wp_" || id.Server == "" {
		t.Fatalf("identity %+v", id)
	}
	s, err := rc.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mixed := s.Table("wp_mixed")
	if mixed == nil || len(mixed.PrimaryKey) != 2 || !mixed.Columns[mixed.Col("twice")].Generated || !mixed.Columns[mixed.Col("raw")].Binary {
		t.Fatalf("wp_mixed %+v", mixed)
	}
	opt := s.Table("wp_options")
	if c := opt.Columns[opt.Col("option_name")]; !c.Unique || c.MaxLen != 764 {
		t.Fatalf("option_name %+v", c)
	}
	if c := mixed.Columns[mixed.Col("latin")]; c.MaxLen != 10 {
		t.Fatalf("latin1 varchar(10) MaxLen %d", c.MaxLen)
	}

	// Root reading read-only can't write either: the transaction is
	// read-only, not just the user.
	ro := waitOpen(t, myEP(port, "root", root, "src"), ReadOnly)
	if _, err := ro.conn.ExecContext(ctx, "INSERT INTO nopk VALUES ('w', 9)"); err == nil {
		t.Fatal("a write inside the read-only transaction worked")
	}

	src, err := rc.Source(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wc := waitOpen(t, myEP(port, "root", root, "dst"), ReadWrite)
	mustExec(t, wc, "SET SESSION sql_mode = ''")
	sk, err := wc.Sink(ctx, SinkOptions{Total: len(s.Tables)})
	if err != nil {
		t.Fatal(err)
	}
	copyAll(t, src, sk)
	for _, tb := range []string{"wp_options", "wp_postmeta", "wp_mixed", "nopk"} {
		var n1, n2 string
		var c1, c2 sql.NullInt64
		if err := admin.conn.QueryRowContext(ctx, "CHECKSUM TABLE src."+tb).Scan(&n1, &c1); err != nil {
			t.Fatal(err)
		}
		if err := admin.conn.QueryRowContext(ctx, "CHECKSUM TABLE dst."+tb).Scan(&n2, &c2); err != nil {
			t.Fatal(err)
		}
		if c1 != c2 {
			t.Errorf("%s: checksum %v, copy %v", tb, c1, c2)
		}
	}
	var n int
	if err := admin.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM dst.wp_postmeta").Scan(&n); err != nil || n != metaRows {
		t.Fatalf("wp_postmeta copy has %d rows: %v", n, err)
	}

	// A dump of the live source reads back through dump.Open.
	rc2 := waitOpen(t, myEP(port, "reader", reader, "src"), ReadOnly)
	src2, _ := rc2.Source(ctx, []string{"wp_options", "wp_mixed"})
	var buf strings.Builder
	ds := dump.NewStreamSink(&buf, dump.WriterOptions{Target: engine})
	copyAll(t, src2, ds)
	back, err := dump.Open(ctx, strings.NewReader(buf.String()), dump.Options{Name: "live.sql"})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for {
		b, err := back.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("the dump of a live source doesn't read back: %v", err)
		}
		rows += len(b.Rows)
	}
	if rows != 6 {
		t.Fatalf("%d rows read back, want 6", rows)
	}

	// A mysqldump fixture through dump.Open loads into the server.
	f, err := os.Open("../dump/mysql/testdata/mysqldump8.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fsrc, err := dump.Open(ctx, f, dump.Options{Name: "mysqldump8.sql"})
	if err != nil {
		t.Fatal(err)
	}
	fc := waitOpen(t, myEP(port, "root", root, "fixture"), ReadWrite)
	fsk, err := fc.Sink(ctx, SinkOptions{FixCollation: engine == core.MariaDB})
	if err != nil {
		t.Fatal(err)
	}
	copyAll(t, fsrc, fsk)

	// Triggers and routines between DELIMITER lines load: the DELIMITER
	// lines stay with the client and ;; or $$ is cut off each body.
	rf, err := os.Open("../dump/mysql/testdata/routines.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	rsrc, err := dump.Open(ctx, rf, dump.Options{Name: "routines.sql", KeepRoutines: true})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, admin, "CREATE DATABASE routines")
	routC := waitOpen(t, myEP(port, "root", root, "routines"), ReadWrite)
	rsk, err := routC.Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	copyAll(t, rsrc, rsk)
	for q, want := range map[string]int{
		"SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = 'routines' AND TRIGGER_NAME = 'keep_email'": 1,
		"SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = 'routines' AND ROUTINE_NAME = 'p'":          1,
	} {
		if err := admin.conn.QueryRowContext(ctx, q).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}

	// A collation the server doesn't know stops the import before the
	// table is dropped.
	bad := "utf8mb4_uca1400_ai_ci"
	if engine == core.MariaDB {
		bad = "utf8mb4_0900_zz_ci"
	}
	bc := waitOpen(t, myEP(port, "root", root, "dst"), ReadWrite)
	bsk, err := bc.Sink(ctx, SinkOptions{Total: 4})
	if err != nil {
		t.Fatal(err)
	}
	_ = bsk.Write(ctx, &core.Batch{Seq: 1, Pass: &core.Statement{Kind: core.StmtDropTable, Text: []byte("DROP TABLE IF EXISTS `wp_options`;\n")}})
	err = bsk.Write(ctx, &core.Batch{Seq: 2, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte("CREATE TABLE `wp_options` (`a` int) DEFAULT CHARSET=utf8mb4 COLLATE=" + bad + ";\n")}})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrImport || !strings.Contains(ce.Msg, "doesn't know collation "+bad) || !strings.HasSuffix(ce.Msg, "The target is untouched.") {
		t.Fatalf("collation error %v", err)
	}
	_ = bsk.Abort(ctx)
	if bsk.State() != core.Untouched {
		t.Fatalf("state %s", bsk.State())
	}
	if err := admin.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM dst.wp_options").Scan(&n); err != nil || n != 3 {
		t.Fatalf("wp_options after the refused import: %d %v", n, err)
	}

	// --fix-collation turns a MySQL 8 collation into one both know.
	xc := waitOpen(t, myEP(port, "root", root, "dst"), ReadWrite)
	xsk, _ := xc.Sink(ctx, SinkOptions{FixCollation: true})
	if err := xsk.Write(ctx, &core.Batch{Seq: 1, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte("CREATE TABLE `fixed` (`a` varchar(5) COLLATE utf8mb4_0900_ai_ci) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;\n")}}); err != nil {
		t.Fatal(err)
	}
	if err := xsk.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var coll string
	if err := admin.conn.QueryRowContext(ctx, "SELECT TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'dst' AND TABLE_NAME = 'fixed'").Scan(&coll); err != nil || coll != "utf8mb4_unicode_520_ci" {
		t.Fatalf("collation %q %v", coll, err)
	}

	// The DSN the driver gets never allows LOCAL INFILE.
	cfg, _ := mysqlConfig(myEP(port, "root", root, "dst"))
	if strings.Contains(cfg.FormatDSN(), "allowAllFiles=true") {
		t.Fatal("allowAllFiles in the DSN")
	}
}

// portPG is the port the PostgreSQL container got.
var portPG int

func pgEP(user, pw, db string) core.Endpoint {
	d := fmt.Sprintf("postgres://%s@127.0.0.1:%d/%s?sslmode=disable", user, portPG, db)
	return core.Endpoint{Kind: core.DSN, Driver: core.Postgres, Host: "127.0.0.1", Port: portPG, User: user, Password: pw, Path: db, DSN: d, Display: d}
}

// pgSchemaSQL is created in the source and, since a live PostgreSQL
// source sends no DDL, in the target too.
var pgSchemaSQL = []string{
	`CREATE TYPE "Role" AS ENUM ('USER', 'ADMIN')`,
	`CREATE TABLE "User" (id serial PRIMARY KEY, email varchar(120) UNIQUE NOT NULL, role "Role" NOT NULL DEFAULT 'USER', avatar bytea, score numeric(8,2), ratio double precision, active boolean, created timestamp, tags text[], bio text)`,
	`CREATE TABLE pair (a int, b text, v text, PRIMARY KEY (a, b))`,
	`CREATE TABLE loose (x text, n int)`,
}

func TestDockerPostgres(t *testing.T) {
	ctx := context.Background()
	pw := randHex(t, 12)
	reader := randHex(t, 12)
	portPG = startContainer(t, imgPostgres, portsPG, 5432, "POSTGRES_PASSWORD="+pw)
	admin := waitOpen(t, pgEP("postgres", pw, "postgres"), ReadWrite)
	mustExec(t, admin, "CREATE DATABASE src", "CREATE DATABASE dst", "CREATE DATABASE fixture")
	sc := waitOpen(t, pgEP("postgres", pw, "src"), ReadWrite)
	dc := waitOpen(t, pgEP("postgres", pw, "dst"), ReadWrite)
	mustExec(t, sc, pgSchemaSQL...)
	mustExec(t, dc, pgSchemaSQL...)
	mustExec(t, sc,
		fmt.Sprintf(`INSERT INTO "User" (email, role, avatar, score, ratio, active, created, tags, bio)
SELECT 'customer-' || g || '@example.invalid', CASE WHEN g %% 7 = 0 THEN 'ADMIN'::"Role" ELSE 'USER'::"Role" END,
CASE WHEN g %% 3 = 0 THEN decode('00ff' || lpad(to_hex(g %% 256), 2, '0'), 'hex') END, g / 4.0, 'NaN'::float8 * (g %% 2) + g,
g %% 2 = 0, '2026-10-26'::timestamp + g * interval '1 minute', ARRAY['a', 'b\c'], E'tab\there\nline ' || g FROM generate_series(1, %d) g`, metaRows),
		`INSERT INTO pair VALUES (1, 'x', 'one'), (1, 'y', NULL), (2, 'a', '')`,
		`INSERT INTO loose VALUES ('l1', 1), (NULL, NULL)`,
		"CREATE ROLE reader LOGIN PASSWORD '"+reader+"'",
		"GRANT SELECT ON ALL TABLES IN SCHEMA public TO reader",
	)
	rc := waitOpen(t, pgEP("reader", reader, "src"), ReadOnly)
	if _, err := rc.conn.ExecContext(ctx, `INSERT INTO loose VALUES ('w', 9)`); err == nil {
		t.Fatal("a write through the read-only source worked")
	}
	rc = waitOpen(t, pgEP("reader", reader, "src"), ReadOnly)
	s, err := rc.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Table("User")
	if u == nil || len(u.PrimaryKey) != 1 || !u.Columns[u.Col("email")].Unique || u.Columns[u.Col("email")].MaxLen != 480 || !u.Columns[u.Col("avatar")].Binary {
		t.Fatalf("User %+v", u)
	}
	if p := s.Table("pair"); len(p.PrimaryKey) != 2 {
		t.Fatalf("pair key %v", p.PrimaryKey)
	}
	src, err := rc.Source(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := dc.Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	copyAll(t, src, sk)
	for _, q := range []string{
		`SELECT count(*)::text || ' ' || md5(string_agg(t::text, '|' ORDER BY id)) FROM "User" t`,
		`SELECT count(*)::text || ' ' || md5(string_agg(t::text, '|' ORDER BY a, b)) FROM pair t`,
		`SELECT count(*)::text || ' ' || md5(string_agg(coalesce(t::text, '-'), '|' ORDER BY x)) FROM loose t`,
	} {
		var a, b string
		if err := sc.conn.QueryRowContext(ctx, q).Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := dc.conn.QueryRowContext(ctx, q).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Errorf("%s: %s, copy %s", q, a, b)
		}
	}

	// The pg_dump 16 fixture through dump.Open loads into an empty database.
	f, err := os.Open("../dump/pgsql/testdata/pg16-prisma.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fsrc, err := dump.Open(ctx, f, dump.Options{Name: "pg16-prisma.sql", KeepRoutines: true})
	if err != nil {
		t.Fatal(err)
	}
	fc := waitOpen(t, pgEP("postgres", pw, "fixture"), ReadWrite)
	fsk, err := fc.Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	copyAll(t, fsrc, fsk)
	var n int
	if err := fc.conn.QueryRowContext(ctx, `SELECT count(*) FROM public."User"`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("User rows from the fixture: %d %v", n, err)
	}
}
