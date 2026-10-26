// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package testutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// Every fake must satisfy its core interface.
var (
	_ core.Source   = (*Source)(nil)
	_ core.Sink     = (*Sink)(nil)
	_ core.Site     = (*Site)(nil)
	_ core.Faker    = (*Faker)(nil)
	_ core.Known    = (*Known)(nil)
	_ core.Spanner  = Spanner{}
	_ core.Detector = (*Detector)(nil)
)

func TestJ(t *testing.T) {
	if got := J("sk_", "test_", "abc"); got != "sk_test_abc" {
		t.Fatalf("J = %q", got)
	}
}

func TestSourceAndSinkRoundTrip(t *testing.T) {
	ctx := context.Background()
	tbl := &core.Table{Name: "wp_users", Columns: []core.Column{{Name: "ID"}, {Name: "user_email"}}}
	b := &core.Batch{Table: tbl, Columns: []int{0, 1}, First: true, Last: true,
		Head: []byte("INSERT INTO `wp_users` VALUES "), Tail: []byte(";\n"),
		Rows: []core.Row{
			{Src: []byte("(1,'a@example.com')"), Values: []core.Value{{Kind: core.Number, Raw: []byte("1")}, {Kind: core.String, Raw: []byte("a@example.com")}}},
			{Lead: []byte(","), Src: []byte("(2,'b')"), Values: []core.Value{{Kind: core.Number, Raw: []byte("2")}, {Kind: core.String, Raw: []byte("it's"), Dirty: true}}},
		}}
	src := FakeSource(nil, []*core.Batch{b})
	sink := FakeSink()
	for {
		got, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := sink.Write(ctx, got); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	want := "INSERT INTO `wp_users` VALUES (1,'a@example.com'),(2,'it\\'s');\n"
	if got := string(sink.Bytes()); got != want {
		t.Fatalf("Bytes = %q, want %q", got, want)
	}
	if sink.State() != core.Replaced {
		t.Fatalf("State = %s", sink.State())
	}
}

func TestFakerDeterministicAcrossClones(t *testing.T) {
	f := FakeFaker()
	a, _ := f.Fake(core.KEmail, []byte("x@shop.example"), core.FakeOpts{})
	b, _ := f.Clone().Fake(core.KEmail, []byte("x@shop.example"), core.FakeOpts{})
	c, _ := f.Fake(core.KEmail, []byte("y@shop.example"), core.FakeOpts{MaxLen: 6})
	if !bytes.Equal(a, b) || string(a) != "fake-email-1@example.invalid" {
		t.Fatalf("fakes %q %q", a, b)
	}
	if string(c) != "fake-e" {
		t.Fatalf("MaxLen not applied: %q", c)
	}
}

func TestKnown(t *testing.T) {
	k := FakeKnown()
	k.Add(core.KEmail, []byte("Ann@Shop.example"))
	if _, _, ok := k.Contains([]byte("ann@shop.example")); ok {
		t.Fatal("Contains before Seal")
	}
	k.Seal()
	if _, enc, ok := k.Contains([]byte("ann@shop.example")); !ok || enc != "raw" {
		t.Fatalf("raw not found: %v %q", ok, enc)
	}
	if _, _, ok := k.Contains([]byte("nothing")); ok {
		t.Fatal("negative matched")
	}
	if k.Len() != 1 {
		t.Fatalf("Len = %d", k.Len())
	}
}

func TestSpanner(t *testing.T) {
	text := []byte("mail ann@shop.example. or bob@x, not @alone")
	spans := FakeSpanner().Spans(text)
	if len(spans) != 1 || string(text[spans[0].Start:spans[0].End]) != "ann@shop.example" {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestDetector(t *testing.T) {
	var hits []core.Hit
	FakeDetector("pii.email", core.SeverityHigh, "aa").Scan([]byte("aaxaa"), core.ScanContext{}, func(h core.Hit) { hits = append(hits, h) })
	if len(hits) != 2 || hits[1].Start != 3 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSite(t *testing.T) {
	ctx := context.Background()
	s := FakeSite(map[string][]byte{"wp-config.php": []byte("<?php"), "wp-content/debug.log": []byte("x")})
	if _, err := s.WP(ctx, "option", "get", "home"); !errors.Is(err, core.ErrNoWPCLI) {
		t.Fatalf("WP without WPFunc: %v", err)
	}
	list, _ := s.List(ctx, "", 1)
	if len(list) != 2 || !list[1].Dir || list[1].Rel != "wp-content" {
		t.Fatalf("List = %+v", list)
	}
	s.RO = true
	if err := s.WriteFile(ctx, "x", nil, 0o600); !errors.Is(err, core.ErrReadOnly) {
		t.Fatalf("read-only write: %v", err)
	}
	if len(s.Writes()) != 0 {
		t.Fatal("a refused write was recorded")
	}
}

func TestCanaries(t *testing.T) {
	a, b := Canaries(1), Canaries(1)
	for _, k := range core.FakeKinds() {
		if len(a[k]) != CanaryCount {
			t.Fatalf("%s: %d canaries", k, len(a[k]))
		}
		if strings.Join(a[k], "|") != strings.Join(b[k], "|") {
			t.Fatalf("%s: not deterministic", k)
		}
	}
	for _, e := range a[core.KEmail] {
		_, d, _ := strings.Cut(e, "@")
		if core.ReservedEmailDomain(d) {
			t.Fatalf("canary %s is in a reserved domain", e)
		}
	}
	for _, p := range a[core.KPhone] {
		if core.FictionalPhone(p) {
			t.Fatalf("canary %s is fictional", p)
		}
	}
}

func TestNoNetwork(t *testing.T) {
	ft := &fakeTB{TB: t}
	NoNetwork(ft)
	resp, err := http.Get("http://127.0.0.1:1/") // NoNetwork answers before any dial
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrNetwork) || !ft.failed {
		t.Fatalf("http.Get under NoNetwork: err %v, failed %v", err, ft.failed)
	}
	ft.cleanup()
	if http.DefaultTransport == nil {
		t.Fatal("transport not restored")
	}
}

// fakeTB records Errorf instead of failing the real test.
type fakeTB struct {
	testing.TB
	failed   bool
	cleanups []func()
}

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Errorf(string, ...any) { f.failed = true }
func (f *fakeTB) Cleanup(fn func())     { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) cleanup() {
	for _, fn := range f.cleanups {
		fn()
	}
}
