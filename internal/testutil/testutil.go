// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package testutil holds the helpers and fakes every lane's tests share,
// so a lane can test its code before the real implementations of the
// other lanes merge. It is frozen with the skeleton.
package testutil

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// update rewrites golden files: go test ./pkg -update. ROADIE_UPDATE=1
// does the same for every package at once (make golden), since a package
// whose tests don't import testutil would reject the flag.
var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden/")

// J joins parts. Tests build anything shaped like a live key from parts,
// so no literal live-format key is in the source for push protection to block.
func J(parts ...string) string { return strings.Join(parts, "") }

// Golden compares got with testdata/golden/<name> in the calling test's
// package, or rewrites the file with -update or ROADIE_UPDATE=1. Golden
// files are LF only; a CRLF checkout of one still compares equal.
func Golden(t testing.TB, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", filepath.FromSlash(name))
	if *update || os.Getenv("ROADIE_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) // #nosec G304 -- a golden file of the test itself
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update to create it)", name, err)
	}
	if !bytes.Contains(got, []byte("\r\n")) {
		want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden %s differs (run with -update to accept)\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// FixedClock returns a clock that always reads at.
func FixedClock(at time.Time) core.Clock { return func() time.Time { return at } }

// ErrNetwork is what every dial returns under NoNetwork.
var ErrNetwork = errors.New("testutil: network use in a test that must not touch the network")

// NoNetwork fails the test on any network use through http.DefaultTransport,
// http.DefaultClient or net.DefaultResolver until the test ends. A raw
// net.Dialer can't be hooked from outside; make no-network runs the tests
// in a namespace with no network to catch those. It changes package
// globals, so a test using it must not run in parallel.
func NoNetwork(t testing.TB) {
	t.Helper()
	fail := func(what string) error {
		t.Errorf("network use under NoNetwork: %s", what)
		return ErrNetwork
	}
	oldTransport, oldClient, oldResolver := http.DefaultTransport, http.DefaultClient.Transport, net.DefaultResolver
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fail(r.Method + " " + r.URL.Redacted())
	})
	http.DefaultTransport = rt
	http.DefaultClient.Transport = rt
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(_ context.Context, network, address string) (net.Conn, error) {
			return nil, fail("resolver dial " + network + " " + address)
		},
	}
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient.Transport, net.DefaultResolver = oldTransport, oldClient, oldResolver
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// RequirePHP returns the path of php, or skips the test when it isn't on
// PATH. With ROADIE_REQUIRE_PHP=1 (CI's php-oracle job) it fails instead,
// so a missing PHP can never pass as a skip.
func RequirePHP(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("php")
	if err == nil {
		return path
	}
	if os.Getenv("ROADIE_REQUIRE_PHP") == "1" {
		t.Fatalf("php not on PATH and ROADIE_REQUIRE_PHP=1: %v", err)
	}
	t.Skip("php not on PATH")
	return ""
}
