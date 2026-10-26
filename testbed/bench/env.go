// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"os"
	"strings"
)

// readEnvFile reads NAME=value lines (runtime/db.env, runtime/echo.env).
// The values are secrets: callers pass them on through the environment
// and never print them.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) // #nosec G304 -- the testbed's own runtime file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, sc.Err()
}
