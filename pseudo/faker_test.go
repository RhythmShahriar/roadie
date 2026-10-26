// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// testKey is a fixed key, so the tests see the same fakes every run.
func testKey(b byte) Key {
	var k Key
	for i := range k {
		k[i] = b + byte(i)
	}
	return k
}

func mustFaker(t testing.TB, k Key, o Options) core.Faker {
	t.Helper()
	f, err := NewFaker(k, o)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func fake(t testing.TB, f core.Faker, kind core.FakeKind, v string, o core.FakeOpts) string {
	t.Helper()
	out, err := f.Fake(kind, []byte(v), o)
	if err != nil {
		t.Fatalf("Fake(%s, %q): %v", kind, v, err)
	}
	return string(out)
}

// scaled returns n, or a tenth of it under the race detector.
func scaled(n int) int {
	if raceEnabled {
		return n / 10
	}
	return n
}

func TestKeyRoundTrip(t *testing.T) {
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if k == (Key{}) {
		t.Fatal("NewKey gave the zero key")
	}
	k2, err := NewKey()
	if err != nil || k2 == k {
		t.Fatalf("two NewKey calls: %v, same=%v", err, k2 == k)
	}
	back, err := ParseKey(" " + k.String() + "\n")
	if err != nil || back != k {
		t.Fatalf("ParseKey(String()) = %v, %v", back, err)
	}
}

func TestParseKeyRejects(t *testing.T) {
	k := testKey(1)
	short := k.String()[:20]
	for _, in := range []string{"", "not base64!", short, k.String() + "AAAA", strings.Repeat("A", 44)} {
		_, err := ParseKey(in)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 {
			t.Errorf("ParseKey(%q) = %v, want a config error", in, err)
			continue
		}
		if in != "" && strings.Contains(ce.Msg, in) {
			t.Errorf("ParseKey error quotes the value: %s", ce.Msg)
		}
	}
}

func TestFingerprint(t *testing.T) {
	k := testKey(1)
	fp := k.Fingerprint()
	rest, ok := strings.CutPrefix(fp, "roadie-key:")
	if !ok || len(rest) != 12 || strings.Trim(rest, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		t.Fatalf("Fingerprint = %q", fp)
	}
	if fp != k.Fingerprint() || fp == testKey(2).Fingerprint() {
		t.Fatal("Fingerprint isn't stable per key or doesn't tell keys apart")
	}
	if strings.Contains(fp, k.String()[:8]) {
		t.Fatal("Fingerprint shows the key")
	}
}

func TestNewFakerOptions(t *testing.T) {
	bad := []Options{
		{EmailDomain: "gmail.com"},
		{EmailDomain: "shop.com"},
		{EmailDomain: "bad domain.test"},
		{EmailDomain: "a..test"},
		{Locale: "pl"},
		{PhoneStyle: "uk"},
	}
	for _, o := range bad {
		_, err := NewFaker(testKey(1), o)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 {
			t.Errorf("NewFaker(%+v) = %v, want a config error", o, err)
		}
	}
	for _, o := range []Options{{}, {EmailDomain: "Example.COM."}, {EmailDomain: "staging.test"}, {EmailDomain: "x.example"},
		{Locale: "de", PhoneStyle: "country", CacheSize: 10}} {
		if _, err := NewFaker(testKey(1), o); err != nil {
			t.Errorf("NewFaker(%+v): %v", o, err)
		}
	}
	f := mustFaker(t, testKey(1), Options{EmailDomain: "Example.COM."})
	if got := fake(t, f, core.KEmail, "jane@shop.com", core.FakeOpts{}); !strings.HasSuffix(got, "@example.com") {
		t.Errorf("email domain not normalised: %s", got)
	}
}

func TestFormats(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	isHexN := func(s string, n int) bool { return isHex(s, n, n) }
	cases := []struct {
		kind core.FakeKind
		in   string
		ok   func(string) bool
	}{
		{core.KEmail, "Jane.Doe@shop.com", func(s string) bool {
			l, d, _ := strings.Cut(s, "@")
			return l[0] == 'u' && isHexN(l[1:], 16) && d == "example.invalid"
		}},
		{core.KUsername, "janedoe", func(s string) bool { r, ok := strings.CutPrefix(s, "user_"); return ok && isHexN(r, 16) }},
		{core.KNicename, "janedoe", func(s string) bool { return len(s) <= 50 && s == strings.ToLower(s) && isFakeLogin(s) }},
		{core.KFirstName, "Jane", func(s string) bool { return isFakeName(core.KFirstName, s) && !strings.Contains(s, " ") }},
		{core.KLastName, "Doe", func(s string) bool { return isFakeName(core.KLastName, s) }},
		{core.KFullName, "Jane Doe", func(s string) bool { return len(strings.Fields(s)) == 2 && isFakeName(core.KFullName, s) }},
		{core.KPhone, "+44 7911 123456", func(s string) bool {
			return len(s) == 15 && strings.HasPrefix(s, "+1 555 01") && s[10] == ' ' && core.FictionalPhone(s)
		}},
		{core.KStreet, "1 Real Road", isFakeStreet},
		{core.KAddress, "Flat 2, Real House", isFakeStreet},
		{core.KCity, "Realtown", func(s string) bool { return in(lists.cities, s) }},
		{core.KCompany, "Real Holdings", isFakeCompany},
		{core.KIP, "81.2.69.142", func(s string) bool {
			a, err := netip.ParseAddr(s)
			return err == nil && a.Is4() && core.DocumentationIP(a)
		}},
		{core.KIP, "2a00:1450:4009:81f::200e", func(s string) bool {
			a, err := netip.ParseAddr(s)
			return err == nil && a.Is6() && strings.HasPrefix(s, "2001:db8:") && core.DocumentationIP(a)
		}},
		{core.KUserAgent, "Mozilla/5.0 (X11; Linux x86_64)", func(s string) bool { return s == "Mozilla/5.0 (Roadie staging copy)" }},
		{core.KURL, "https://jane.example.org/blog", func(s string) bool {
			r, ok := strings.CutPrefix(s, "https://u")
			h, ok2 := strings.CutSuffix(r, ".example.invalid/")
			return ok && ok2 && isHexN(h, 12)
		}},
		{core.KPostcode, "SW1A 1AA", func(s string) bool { return len(s) == 8 && s[4] == ' ' && s != "SW1A 1AA" }},
		{core.KDOB, "1984-07-14", func(s string) bool { return strings.HasPrefix(s, "1984-") && s != "1984-07-14" }},
	}
	for _, c := range cases {
		got := fake(t, f, c.kind, c.in, core.FakeOpts{})
		if !c.ok(got) {
			t.Errorf("Fake(%s, %q) = %q: wrong format", c.kind, c.in, got)
		}
		if !IsFake(c.kind, []byte(got)) && c.kind != core.KPostcode && c.kind != core.KDOB {
			t.Errorf("IsFake(%s, %q) = false for a fake", c.kind, got)
		}
	}
}

func TestPhoneStyles(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{PhoneStyle: PhoneCountry})
	for _, in := range []string{"+44 7911 123456", "07911 123456", "0044 7911 123456"} {
		got := fake(t, f, core.KPhone, in, core.FakeOpts{})
		if !strings.HasPrefix(got, "+44 7700 900") || len(got) != 15 || !core.FictionalPhone(got) {
			t.Errorf("country style %q = %q", in, got)
		}
	}
	if got := fake(t, f, core.KPhone, "030 1234567", core.FakeOpts{Country: "GB"}); !strings.HasPrefix(got, "+44 7700 900") {
		t.Errorf("country GB = %q", got)
	}
	for _, in := range []string{"+49 30 1234567", "+1 212 555 0000", "0612345678"} {
		if got := fake(t, f, core.KPhone, in, core.FakeOpts{}); !strings.HasPrefix(got, "+1 555 01") {
			t.Errorf("country style falls back to echo for %q: %q", in, got)
		}
	}
	got := fake(t, f, core.KPhone, "+44 7911 123456", core.FakeOpts{MaxLen: 13})
	if got != strings.ReplaceAll(fake(t, f, core.KPhone, "+44 7911 123456", core.FakeOpts{}), " ", "") {
		t.Errorf("narrow phone column = %q", got)
	}
	if _, err := f.Fake(core.KPhone, []byte("+44 7911 123456"), core.FakeOpts{MaxLen: 8}); err == nil {
		t.Error("an 8-byte phone column took a fake")
	}
}

func TestDeterministic(t *testing.T) {
	a := mustFaker(t, testKey(1), Options{})
	b := mustFaker(t, testKey(1), Options{})
	c := a.Clone()
	other := mustFaker(t, testKey(2), Options{})
	for _, kind := range core.FakeKinds() {
		if kind == core.KText || kind == core.KUserAgent {
			continue
		}
		in := testutil.Canaries(7)[kind][0]
		x := fake(t, a, kind, in, core.FakeOpts{})
		if y := fake(t, b, kind, in, core.FakeOpts{}); y != x {
			t.Errorf("%s: two fakers with one key differ: %q %q", kind, x, y)
		}
		if y := fake(t, c, kind, in, core.FakeOpts{}); y != x {
			t.Errorf("%s: clone differs: %q %q", kind, x, y)
		}
		if y := fake(t, a, kind, in, core.FakeOpts{}); y != x {
			t.Errorf("%s: cached call differs: %q %q", kind, x, y)
		}
		if kind == core.KEmail || kind == core.KUsername || kind == core.KURL || kind == core.KFullName {
			if y := fake(t, other, kind, in, core.FakeOpts{}); y == x {
				t.Errorf("%s: another key gives the same fake %q", kind, x)
			}
		}
	}
}

func TestNormaliseEquivalents(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	nfc := norm.NFC.String("Zoë Ångström")
	nfd := norm.NFD.String("Zoë Ångström")
	if nfc == nfd {
		t.Fatal("test strings aren't different forms")
	}
	groups := []struct {
		kind core.FakeKind
		vals []string
	}{
		{core.KEmail, []string{"Jane@X.com", " jane@x.com ", "JANE@x.com", "jane@x.com\n"}},
		{core.KPhone, []string{"+44 7911 123456", "+447911123456", "+44 (7911) 123-456"}},
		{core.KIP, []string{"::ffff:81.2.69.142", "81.2.69.142", " 81.2.69.142"}},
		{core.KFullName, []string{nfc, nfd, "ZOË  ÅNGSTRÖM", " zoë ångström "}},
		{core.KFirstName, []string{"Straße", "STRASSE", "strasse"}},
		{core.KUsername, []string{"Jane@X.com", "jane@x.com"}},
	}
	for _, g := range groups {
		want := fake(t, f, g.kind, g.vals[0], core.FakeOpts{})
		for _, v := range g.vals[1:] {
			if got := fake(t, f, g.kind, v, core.FakeOpts{}); got != want {
				t.Errorf("%s: %q gives %q, %q gives %q", g.kind, g.vals[0], want, v, got)
			}
		}
	}
	if string(Normalise(core.KPhone, []byte(" +44 7911 123456 "))) != "+447911123456" {
		t.Error("phone normalisation")
	}
	if string(Normalise(core.KIP, []byte("::ffff:81.2.69.142"))) != "81.2.69.142" {
		t.Error("ip normalisation")
	}
	if string(Normalise(core.KIP, []byte("2001:DB8:0:0::1"))) != "2001:db8::1" {
		t.Error("ipv6 normalisation")
	}
	if string(Normalise(core.KPostcode, []byte(" SW1A 1AA "))) != "SW1A 1AA" {
		t.Error("other kinds are only trimmed")
	}
	in := []byte("Jane")
	out := Normalise(core.KPostcode, in)
	out[0] = 'X'
	if string(in) != "Jane" {
		t.Error("Normalise shares memory with its input")
	}
	if fake(t, f, core.KPhone, "+44 7911 123456", core.FakeOpts{}) == fake(t, f, core.KPhone, "07911 123456", core.FakeOpts{}) {
		t.Log("national and international forms happen to share a fake") // allowed, not required
	}
}

func TestLoginsAndNicenames(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	email := fake(t, f, core.KEmail, "jane@shop.com", core.FakeOpts{})
	if got := fake(t, f, core.KUsername, "Jane@Shop.com", core.FakeOpts{}); got != email {
		t.Errorf("a login equal to the email gets %q, want the fake email %q", got, email)
	}
	login := fake(t, f, core.KUsername, "janedoe", core.FakeOpts{})
	if got := fake(t, f, core.KNicename, "janedoe", core.FakeOpts{}); got != login {
		t.Errorf("nicename equal to the login gets %q, want %q", got, login)
	}
	if got := fake(t, f, core.KNicename, "jane@shop.com", core.FakeOpts{}); !isFakeLogin(got) {
		t.Errorf("nicename of an email login = %q", got)
	}
}

func TestMaxLen(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	full := fake(t, f, core.KEmail, "jane@shop.com", core.FakeOpts{})
	short := fake(t, f, core.KEmail, "jane@shop.com", core.FakeOpts{MaxLen: 30})
	if len(short) != 30 || !strings.HasPrefix(full, short[:13]) || !IsFake(core.KEmail, []byte(short)) {
		t.Errorf("email cut to 30: %q (full %q)", short, full)
	}
	if _, err := f.Fake(core.KEmail, []byte("jane@shop.com"), core.FakeOpts{MaxLen: 28}); err == nil {
		t.Error("an email column too short for 12 hex took a fake")
	} else if ce := (*core.Error)(nil); !errors.As(err, &ce) || ce.Code != core.ErrConfig {
		t.Errorf("too short: %v", err)
	}
	if got := fake(t, f, core.KUsername, "janedoe", core.FakeOpts{MaxLen: 17}); len(got) != 17 || !isFakeLogin(got) {
		t.Errorf("login cut to 17: %q", got)
	}
	if _, err := f.Fake(core.KUsername, []byte("janedoe"), core.FakeOpts{MaxLen: 16}); err == nil {
		t.Error("a 16-byte login column took a fake")
	}
	if got := fake(t, f, core.KNicename, "janedoe", core.FakeOpts{MaxLen: 200}); len(got) > 50 {
		t.Errorf("nicename over 50 bytes: %q", got)
	}
	if got := fake(t, f, core.KURL, "https://jane.blog", core.FakeOpts{MaxLen: 37}); len(got) != 37 || !IsFake(core.KURL, []byte(got)) {
		t.Errorf("url without slash: %q", got)
	}
	if got := fake(t, f, core.KURL, "https://jane.blog", core.FakeOpts{MaxLen: 30}); len(got) != 30 || !IsFake(core.KURL, []byte(got)) {
		t.Errorf("url cut to 30: %q", got)
	}
	for _, kind := range []core.FakeKind{core.KFullName, core.KStreet, core.KCompany, core.KUserAgent} {
		got := fake(t, f, kind, "Some Real Value", core.FakeOpts{MaxLen: 6})
		if len(got) > 6 || got == "" || strings.HasSuffix(got, " ") {
			t.Errorf("%s cut to 6: %q", kind, got)
		}
	}
	if got := fake(t, f, core.KIP, "2a00:1450:4009:81f::200e", core.FakeOpts{MaxLen: 15}); !strings.Contains(got, ".") {
		t.Errorf("narrow column gets IPv4: %q", got)
	}
}

func TestUniqueListKinds(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	seen := map[string]bool{}
	for i := range 2000 {
		got := fake(t, f, core.KFullName, fmt.Sprintf("Person %d", i), core.FakeOpts{Unique: true, MaxLen: 40})
		if seen[got] || len(got) > 40 || !IsFake(core.KFullName, []byte(got)) {
			t.Fatalf("unique name %q (seen %v)", got, seen[got])
		}
		seen[got] = true
	}
	if _, err := f.Fake(core.KCity, []byte("Realtown"), core.FakeOpts{Unique: true, MaxLen: 9}); err == nil {
		t.Error("a 9-byte unique city column took a fake")
	}
}

func TestCollision(t *testing.T) {
	u := newUniqueSet()
	a, b := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	if err := u.add(core.KEmail, []byte("u0@example.invalid"), a); err != nil {
		t.Fatal(err)
	}
	if err := u.add(core.KEmail, []byte("u0@example.invalid"), a); err != nil {
		t.Fatalf("a repeat of one original: %v", err)
	}
	if err := u.add(core.KUsername, []byte("u0@example.invalid"), b); err != nil {
		t.Fatalf("another kind: %v", err)
	}
	err := u.add(core.KEmail, []byte("u0@example.invalid"), b)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrFakeCollision || ce.Exit != 3 {
		t.Fatalf("collision = %v", err)
	}
	const want = "fake-collision: two different emails got the same fake in this run. Run again (a new key fixes it); this should happen about once in 100 million runs."
	if ce.Msg != want {
		t.Errorf("message\n got %s\nwant %s", ce.Msg, want)
	}
	if exit, ok := core.CodeExit(ce.Code); !ok || exit != ce.Exit {
		t.Errorf("exit %d does not match PRD 17", ce.Exit)
	}
}

func TestFakeEdgeCases(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	for _, in := range []string{"", "   ", "\n"} {
		if got := fake(t, f, core.KEmail, in, core.FakeOpts{}); got != in {
			t.Errorf("blank %q became %q", in, got)
		}
	}
	for _, kind := range []core.FakeKind{core.KText, "card", ""} {
		if _, err := f.Fake(kind, []byte("x"), core.FakeOpts{}); err == nil {
			t.Errorf("kind %q gave a fake", kind)
		}
	}
	out, _ := f.Fake(core.KEmail, []byte("jane@shop.com"), core.FakeOpts{})
	out[0] = 'X'
	if got := fake(t, f, core.KEmail, "jane@shop.com", core.FakeOpts{}); got[0] != 'u' {
		t.Error("a caller's change reached the cache")
	}
}

func TestDOB(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	for i := range 2000 {
		d := time.Date(1950+i%60, time.Month(1+i%12), 1+i%28, 0, 0, 0, 0, time.UTC)
		in := d.Format("2006-01-02")
		got := fake(t, f, core.KDOB, in, core.FakeOpts{})
		g, err := time.Parse("2006-01-02", got)
		if err != nil || g.Year() != d.Year() || g.Equal(d) || absDays(g.Sub(d)) > 180 {
			t.Fatalf("dob %s became %s", in, got)
		}
	}
	cases := map[string]func(string) bool{
		"1984-07-14 00:00:00": func(s string) bool { return strings.HasPrefix(s, "1984-") && strings.HasSuffix(s, " 00:00:00") },
		"14.07.1984":          func(s string) bool { return len(s) == 10 && s[2] == '.' && strings.HasSuffix(s, ".1984") },
		"14/07/1984":          func(s string) bool { _, err := time.Parse("02/01/2006", s); return err == nil },
		"07/14/1984":          func(s string) bool { _, err := time.Parse("01/02/2006", s); return err == nil },
		"19840714":            func(s string) bool { return len(s) == 8 && strings.HasPrefix(s, "1984") },
		"0000-00-00":          func(s string) bool { return s == "0000-00-00" },
		"born in spring 84":   func(s string) bool { return strings.HasPrefix(s, "born in spring ") && s != "born in spring 84" },
	}
	for in, ok := range cases {
		if got := fake(t, f, core.KDOB, in, core.FakeOpts{}); !ok(got) {
			t.Errorf("dob %q became %q", in, got)
		}
	}
	us := fake(t, f, core.KDOB, "03/04/1984", core.FakeOpts{Country: "US"})
	if _, err := time.Parse("01/02/2006", us); err != nil {
		t.Errorf("US date %q", us)
	}
}

func absDays(d time.Duration) int {
	n := int(d.Hours() / 24)
	if n < 0 {
		return -n
	}
	return n
}

func TestPostcodeShape(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	for _, in := range []string{"SW1A 1AA", "10115", "75008", "1012 AB", "K1A 0B1", "sw1a 1aa", "12-345", "A"} {
		got := fake(t, f, core.KPostcode, in, core.FakeOpts{})
		if len(got) != len(in) || got == in {
			t.Errorf("postcode %q became %q", in, got)
			continue
		}
		for i := range in {
			if class(in[i]) != class(got[i]) {
				t.Errorf("postcode %q became %q: shape changed at %d", in, got, i)
			}
		}
	}
}

func class(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return 'd'
	case c >= 'A' && c <= 'Z':
		return 'A'
	case c >= 'a' && c <= 'z':
		return 'a'
	}
	return c
}

func TestCacheEviction(t *testing.T) {
	small := mustFaker(t, testKey(1), Options{CacheSize: 2})
	big := mustFaker(t, testKey(1), Options{})
	for round := range 3 {
		for i := range 10 {
			v := fmt.Sprintf("p%d@shop.com", i)
			if a, b := fake(t, small, core.KEmail, v, core.FakeOpts{}), fake(t, big, core.KEmail, v, core.FakeOpts{}); a != b {
				t.Fatalf("round %d: evicting cache changed %s: %s vs %s", round, v, a, b)
			}
		}
	}
	c := small.(*faker).cache
	if len(c.ents) != 2 || len(c.idx) != 2 {
		t.Errorf("cache holds %d entries, %d indexed; want 2", len(c.ents), len(c.idx))
	}
}

func TestClonesInParallel(t *testing.T) {
	f := mustFaker(t, testKey(1), Options{})
	want := map[string]string{}
	for i := range 200 {
		v := fmt.Sprintf("p%d@shop.com", i)
		want[v] = fake(t, f, core.KEmail, v, core.FakeOpts{})
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		c := f.Clone()
		wg.Go(func() {
			for v, w := range want {
				got, err := c.Fake(core.KEmail, []byte(v), core.FakeOpts{})
				if err != nil || string(got) != w {
					errs <- fmt.Errorf("%s: %s %w, want %s", v, got, err, w)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Every generated email, phone and IP is in a reserved range; a
// million distinct emails give a million distinct fakes.
func TestMillionFakes(t *testing.T) {
	n := scaled(1_000_000)
	f := mustFaker(t, testKey(9), Options{})
	seen := make(map[string]struct{}, n)
	for i := range n {
		e := fake(t, f, core.KEmail, fmt.Sprintf("person%d@shop%d.com", i, i%97), core.FakeOpts{})
		at := strings.IndexByte(e, '@')
		if !core.ReservedEmailDomain(e[at+1:]) {
			t.Fatalf("email %q isn't reserved", e)
		}
		if _, dup := seen[e]; dup {
			t.Fatalf("two emails share the fake %q", e)
		}
		seen[e] = struct{}{}
		p := fake(t, f, core.KPhone, fmt.Sprintf("+44 7911 %06d", i), core.FakeOpts{})
		if !core.FictionalPhone(p) {
			t.Fatalf("phone %q isn't fictional", p)
		}
		ip := fake(t, f, core.KIP, fmt.Sprintf("81.%d.%d.%d", i>>16&255, i>>8&255, i&255), core.FakeOpts{})
		if a, err := netip.ParseAddr(ip); err != nil || !core.DocumentationIP(a) {
			t.Fatalf("ip %q isn't documentation", ip)
		}
		if i%10 == 0 {
			ip6 := fake(t, f, core.KIP, fmt.Sprintf("2a00:1450::%x", i), core.FakeOpts{})
			if a, err := netip.ParseAddr(ip6); err != nil || !core.DocumentationIP(a) {
				t.Fatalf("ipv6 %q isn't documentation", ip6)
			}
		}
	}
	gb := mustFaker(t, testKey(9), Options{PhoneStyle: PhoneCountry})
	for i := range n / 10 {
		if p := fake(t, gb, core.KPhone, fmt.Sprintf("07911 %06d", i), core.FakeOpts{}); !core.FictionalPhone(p) {
			t.Fatalf("UK phone %q isn't fictional", p)
		}
	}
}

// No fake is a plain hash of its email (security.md D1): the fakes are
// keyed, so md5, sha1 and sha256 of the inputs never show up in them.
func TestFakesAreNotPlainHashes(t *testing.T) {
	f := mustFaker(t, testKey(3), Options{})
	emails := testutil.Canaries(11)[core.KEmail]
	for i := range scaled(20000) {
		emails = append(emails, fmt.Sprintf("customer%d@shop.com", i))
	}
	for _, e := range emails {
		got := fake(t, f, core.KEmail, e, core.FakeOpts{})
		local := got[1:strings.IndexByte(got, '@')]
		n := Normalise(core.KEmail, []byte(e))
		for _, h := range hashesOf(n, []byte(e)) {
			if strings.Contains(h, local) || strings.Contains(got, h) {
				t.Fatalf("fake %q of %q holds a plain hash", got, e)
			}
		}
	}
}

func hashesOf(vals ...[]byte) []string {
	var out []string
	for _, v := range vals {
		m, s1, s2 := md5.Sum(v), sha1.Sum(v), sha256.Sum256(v) // #nosec G401 -- what an attacker would try
		out = append(out, hex.EncodeToString(m[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(s2[:]))
	}
	return out
}

// BenchmarkFake measures one fake of each common kind, from the cache
// and as a miss (the first sight of a value in a run).
func BenchmarkFake(b *testing.B) {
	kinds := []struct {
		kind core.FakeKind
		val  string
	}{
		{core.KEmail, "customer%d@shop.com"},
		{core.KFullName, "Jane Roe%d"},
		{core.KPhone, "+44 7911 %06d"},
		{core.KIP, "10.0.%d.%d"},
		{core.KPostcode, "SW%d %dAA"},
	}
	for _, k := range kinds {
		b.Run(string(k.kind)+"/cached", func(b *testing.B) {
			f := mustFaker(b, testKey(1), Options{})
			v := benchValue(k.val, 7)
			if _, err := f.Fake(k.kind, v, core.FakeOpts{}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = f.Fake(k.kind, v, core.FakeOpts{})
			}
		})
		b.Run(string(k.kind)+"/uncached", func(b *testing.B) {
			vals := make([][]byte, 1<<16)
			for i := range vals {
				vals[i] = benchValue(k.val, i)
			}
			f := mustFaker(b, testKey(1), Options{})
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				// A fresh faker per pass keeps every call a miss.
				if i%len(vals) == 0 {
					b.StopTimer()
					f = mustFaker(b, testKey(byte(i)), Options{})
					b.StartTimer()
				}
				_, _ = f.Fake(k.kind, vals[i%len(vals)], core.FakeOpts{})
				i++
			}
		})
	}
}

// benchValue fills format with i, or with i's high and low bytes when it
// takes two numbers, so every i gives a distinct value of the kind.
func benchValue(format string, i int) []byte {
	if strings.Count(format, "%d") == 2 {
		return fmt.Appendf(nil, format, i>>8, i&0xff)
	}
	return fmt.Appendf(nil, format, i)
}
