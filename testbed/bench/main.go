// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command bench is the testbed's harness (PRD 22, qa.md 6.3). With -drill
// it runs only the side-effect drill against the running staging and
// prints gate 3 as JSON:
//
//	go run -tags testbed ./testbed/bench -drill [-for 5m]
//
// It needs the testbed up (make -C testbed up). It prints canary IDs and
// masked keys only, never a value.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Ports the gate publishes (testbed/gate/nginx.conf).
const (
	gateStaging = "http://127.0.0.1:18921"
	gateEcho    = "http://127.0.0.1:18924"
)

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("bench", flag.ContinueOnError)
	fl.SetOutput(stderr)
	runtimeDir := fl.String("runtime", "testbed/runtime", "the testbed's runtime folder (logs, secrets, seeded fixtures)")
	drill := fl.Bool("drill", false, "run only the side-effect drill on staging and print gate 3")
	drillFor := fl.Duration("for", 5*time.Minute, "how long the drill runs")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if !*drill {
		fmt.Fprintln(stderr, "bench: give -drill")
		return 2
	}
	ctx := context.Background()
	env, err := readEnvFile(filepath.Join(*runtimeDir, "echo.env"))
	if err != nil {
		fmt.Fprintln(stderr, "bench: the testbed isn't set up (make -C testbed up):", err)
		return 3
	}
	w := &Window{
		Logs: Logs{Sinkhole: filepath.Join(*runtimeDir, "log", "sinkhole.jsonl"), Stubs: filepath.Join(*runtimeDir, "log", "stubs.jsonl")},
		Echo: Echo{Base: gateEcho, Token: env["RHYTHM_ECHO_TOKEN"]},
	}
	ex := dockerExec{}
	paths, cookies := stagingPages(ctx, ex)
	t := Traffic{Base: gateStaging, Host: stagingHost, Paths: paths, Admin: []string{"/wp-admin/"}, Cookies: cookies, Every: 500 * time.Millisecond}
	if err := w.Start(ctx, t, 0); err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	dr := Drill{Exec: ex, For: *drillFor, Pause: 5 * time.Second}.Run(ctx)
	traffic := w.Stop()
	g, err := w.Judge(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(struct {
		Gate3   Gate3        `json:"gate3"`
		Drill   DrillResult  `json:"drill"`
		Traffic TrafficStats `json:"traffic"`
		Pass    bool         `json:"pass"`
	}{g, dr, traffic, g.Pass()})
	if !g.Pass() {
		return 1
	}
	return 0
}
