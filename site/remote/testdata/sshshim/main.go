// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command sshshim stands in for ssh in the remote tests: it records its
// argv and stdin and replays a canned answer, so no test ever reaches a
// real host. The tests build it as "ssh" first on PATH.
//
//	SHIM_DIR     folder for calls.jsonl and stdin-<n> files
//	SHIM_CANARY  a password that must never be in argv (exit 99 if it is)
//	SHIM_STDOUT  file copied to stdout
//	SHIM_STDERR  text written to stderr
//	SHIM_EXIT    exit code
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dir := os.Getenv("SHIM_DIR")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "sshshim: SHIM_DIR not set")
		os.Exit(97)
	}
	stdin, _ := io.ReadAll(os.Stdin)
	n := 0
	if b, err := os.ReadFile(filepath.Join(dir, "calls.jsonl")); err == nil {
		n = strings.Count(string(b), "\n")
	}
	_ = os.WriteFile(filepath.Join(dir, "stdin-"+strconv.Itoa(n)), stdin, 0o600)
	line, _ := json.Marshal(os.Args[1:])
	f, err := os.OpenFile(filepath.Join(dir, "calls.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(97)
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()

	for _, a := range os.Args[1:] {
		if c := os.Getenv("SHIM_CANARY"); c != "" && strings.Contains(a, c) {
			fmt.Fprintln(os.Stderr, "sshshim: the canary password is in argv")
			os.Exit(99)
		}
		if strings.Contains(a, "StrictHostKeyChecking") || strings.Contains(a, "UserKnownHostsFile") {
			fmt.Fprintln(os.Stderr, "sshshim: a host key option is in argv")
			os.Exit(98)
		}
	}
	if p := os.Getenv("SHIM_STDOUT"); p != "" {
		b, _ := os.ReadFile(p)
		_, _ = os.Stdout.Write(b)
	}
	_, _ = os.Stderr.WriteString(os.Getenv("SHIM_STDERR"))
	code, _ := strconv.Atoi(os.Getenv("SHIM_EXIT"))
	os.Exit(code)
}
