// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/engine/testdata/gen"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// The loopback ports of this test (39000-39049).
var prepareDockerPorts = []int{39011, 39012, 39013}

// startMariaDB runs MariaDB 10.11 on the first free port and returns the
// staging database's DSN endpoint and the container's name.
func startMariaDB(t *testing.T) (core.Endpoint, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	pw := hex.EncodeToString(b)
	var last []byte
	for _, port := range prepareDockerPorts {
		name := "roadie-prepare-test-" + hex.EncodeToString(b[:3]) + fmt.Sprint(port)
		out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:3306", port),
			"-e", "MARIADB_ROOT_PASSWORD="+pw, "-e", "MARIADB_DATABASE=staging", "-e", "MARIADB_USER=stg",
			"-e", "MARIADB_PASSWORD="+pw, "mariadb:10.11").CombinedOutput()
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
		if err != nil {
			last = out
			continue
		}
		d := fmt.Sprintf("mysql://stg@127.0.0.1:%d/staging", port)
		ep := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "127.0.0.1", Port: port, User: "stg", Password: pw,
			Path: "staging", DSN: d, Display: d, Raw: d}
		deadline := time.Now().Add(3 * time.Minute)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c, err := db.Open(ctx, ep, db.ReadOnly)
			cancel()
			if err == nil {
				_ = c.Close()
				return ep, name
			}
			if time.Now().After(deadline) {
				t.Fatalf("MariaDB never answered: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
	}
	t.Fatalf("docker run mariadb: no free port in %v\n%s", prepareDockerPorts, last)
	return core.Endpoint{}, ""
}

// prepare on a MariaDB staging reads the database, then imports into it.
// It once hung for ever: pass 2 read the database through a snapshot
// that held the metadata lock the import's DROP TABLE waited on. A run
// that doesn't finish in two minutes fails here.
func TestPrepareMariaDB(t *testing.T) {
	ep, name := startMariaDB(t)
	var dump bytes.Buffer
	if _, err := gen.Write(&dump, gen.Options{Size: 512 << 10, Seed: 7}); err != nil {
		t.Fatal(err)
	}
	load := exec.Command("docker", "exec", "-i", name, "mariadb", "-ustg", "-p"+ep.Password, "staging")
	load.Stdin = &dump
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	run := func(only ...string) *core.Result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		j := Job{Mode: Prepare, To: ep, Config: roadiecfg.Default(), ProjectDir: t.TempDir(),
			Opts: Options{URL: "https://staging.shop.test", ProductionURL: "https://shop.example.com", ConfirmTarget: "staging",
				NoBackup: true, Offline: true, FailOn: core.SeverityHigh, Only: only}}
		res, err := Run(ctx, j)
		if err != nil {
			t.Fatalf("prepare %v: %v", only, err)
		}
		if res.TargetState != core.Replaced {
			t.Errorf("prepare %v: state %s", only, res.TargetState)
		}
		return res
	}
	run()
	out, err := exec.Command("docker", "exec", name, "mariadb", "-ustg", "-p"+ep.Password, "-N", "staging",
		"-e", "SELECT option_value FROM wp_options WHERE option_name = 'siteurl'").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "https://staging.shop.test" {
		t.Errorf("siteurl after prepare: %q %v", out, err)
	}
	// Again, on the staging it now is: its own URL is no production URL.
	run()
	// A single site without wp_blogs, webhooks or AutomateWoo.
	run("side-effects")
}
