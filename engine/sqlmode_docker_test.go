// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/engine/testdata/gen"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// The loopback ports of the MySQL 8 test (39000-39049).
var mysql8DockerPorts = []int{39014, 39015, 39016}

// startMySQL8 runs MySQL 8.4 with its default sql_mode on the first free
// port and returns the root password, the port and the container name.
func startMySQL8(t *testing.T) (string, int, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	pw := hex.EncodeToString(b)
	var last []byte
	for _, port := range mysql8DockerPorts {
		name := "roadie-sqlmode-test-" + hex.EncodeToString(b[:3]) + fmt.Sprint(port)
		out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:3306", port),
			"-e", "MYSQL_ROOT_PASSWORD="+pw, "mysql:8.4").CombinedOutput()
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
		if err != nil {
			last = out
			continue
		}
		ep := mysql8EP(pw, port, "mysql")
		deadline := time.Now().Add(3 * time.Minute)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c, err := db.Open(ctx, ep, db.ReadOnly)
			cancel()
			if err == nil {
				_ = c.Close()
				return pw, port, name
			}
			if time.Now().After(deadline) {
				t.Fatalf("MySQL never answered: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
	}
	t.Fatalf("docker run mysql: no free port in %v\n%s", mysql8DockerPorts, last)
	return "", 0, ""
}

func mysql8EP(pw string, port int, name string) core.Endpoint {
	d := fmt.Sprintf("mysql://root@127.0.0.1:%d/%s", port, name)
	return core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "127.0.0.1", Port: port, User: "root", Password: pw,
		Path: name, DSN: d, Display: d, Raw: d}
}

// mysqlClient runs the mysql client in the container on database name.
func mysqlClient(t *testing.T, container, pw, name string, stdin []byte, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("docker", append([]string{"exec", "-i", container, "mysql", "-uroot", "-p" + pw, "-N", name}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A WordPress schema keeps DEFAULT '0000-00-00 00:00:00'. Under MySQL
// 8's default sql_mode a copy from the live database, a prepare, the
// prepare's backup and a dump written from the live database all load:
// once each failed with error 1067 (Invalid default value).
func TestMySQL8DefaultSQLModeZeroDates(t *testing.T) {
	t.Chdir(t.TempDir()) // the prepare's backup goes to .roadie/backups here
	pw, port, name := startMySQL8(t)
	if _, err := mysqlClient(t, name, pw, "mysql", []byte("CREATE DATABASE prod; CREATE DATABASE staging; CREATE DATABASE restored; CREATE DATABASE fromfile;")); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if _, err := gen.Write(&dump, gen.Options{Size: 256 << 10, Seed: 11}); err != nil {
		t.Fatal(err)
	}
	if out, err := mysqlClient(t, name, pw, "prod", dump.Bytes()); err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	zero := "SET SESSION sql_mode = '';\n" +
		"ALTER TABLE wp_actionscheduler_actions ADD scheduled_date_gmt datetime NOT NULL DEFAULT '0000-00-00 00:00:00';\n" +
		"ALTER TABLE wp_comments ADD comment_date datetime NOT NULL DEFAULT '0000-00-00 00:00:00';\n" +
		"ALTER TABLE wp_users ALTER user_registered SET DEFAULT '0000-00-00 00:00:00';\n"
	if out, err := mysqlClient(t, name, pw, "prod", []byte(zero)); err != nil {
		t.Fatalf("zero dates: %v\n%s", err, out)
	}
	if out, _ := mysqlClient(t, name, pw, "prod", nil, "-e", "SELECT @@GLOBAL.sql_mode"); !strings.Contains(out, "NO_ZERO_DATE") {
		t.Fatalf("the server's sql_mode is not MySQL 8's default: %s", out)
	}
	tables := func(db string) string {
		t.Helper()
		out, err := mysqlClient(t, name, pw, db, nil, "-e",
			"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = '"+db+"'")
		if err != nil {
			t.Fatalf("%s: %v %s", db, err, out)
		}
		return strings.TrimSpace(out)
	}
	want := tables("prod")
	cfg := roadiecfg.Default()
	run := func(j Job) *core.Result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		j.Config = cfg
		res, err := Run(ctx, j)
		if err != nil {
			t.Fatalf("%s to %s: %v", j.Mode, j.To.Display, err)
		}
		return res
	}
	opts := Options{URL: "https://staging.shop.test", ConfirmTarget: "staging", Yes: true, Offline: true, FailOn: core.SeverityHigh}

	// 1. copy from the live database into a database.
	run(Job{Mode: Copy, From: mysql8EP(pw, port, "prod"), To: mysql8EP(pw, port, "staging"), ProjectDir: t.TempDir(), Opts: opts})
	if got := tables("staging"); got != want {
		t.Fatalf("copy: staging has %s tables, prod %s", got, want)
	}

	// 2. prepare it, taking a backup; 3. the backup loads with mysql.
	project := t.TempDir()
	popts := opts
	popts.ProductionURL = "https://shop.example.com"
	res := run(Job{Mode: Prepare, To: mysql8EP(pw, port, "staging"), ProjectDir: project, Opts: popts})
	if got := tables("staging"); got != want {
		t.Fatalf("prepare: staging has %s tables, prod %s", got, want)
	}
	if res.Backup == "" {
		t.Fatal("prepare took no backup")
	}
	gz, err := os.ReadFile(res.Backup)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	var plain bytes.Buffer
	if _, err := plain.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	if out, err := mysqlClient(t, name, pw, "restored", plain.Bytes()); err != nil {
		t.Fatalf("restore the backup: %v\n%s", err, out)
	}
	if got := tables("restored"); got != want {
		t.Fatalf("restored backup has %s tables, prod %s", got, want)
	}

	// 4. a dump file written from the live database loads with mysql.
	out := filepath.Join(t.TempDir(), "dsn.sql")
	run(Job{Mode: Copy, From: mysql8EP(pw, port, "prod"), ProjectDir: t.TempDir(), Opts: opts,
		To: core.Endpoint{Kind: core.DumpFile, Raw: out, Display: "dsn.sql", Path: out}})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := mysqlClient(t, name, pw, "fromfile", b); err != nil {
		t.Fatalf("load the dump: %v\n%s", err, msg)
	}
	if got := tables("fromfile"); got != want {
		t.Fatalf("the dump file loads %s tables, prod %s", got, want)
	}
}
