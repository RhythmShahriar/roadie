// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package urlrw

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func mustMap(t testing.TB, pairs ...Pair) *Map {
	t.Helper()
	m, err := NewMap(pairs)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRewriteForms(t *testing.T) {
	m := mustMap(t, Pair{"https://shop.com", "https://staging.shop.test"})
	cases := []struct {
		in, want string
		n        int
	}{
		{"https://shop.com/cart", "https://staging.shop.test/cart", 1},
		{"http://shop.com", "https://staging.shop.test", 1},
		{"HTTPS://SHOP.COM/x", "HTTPS://staging.shop.test/x", 1},
		{`<a href="//shop.com/x">`, `<a href="//staging.shop.test/x">`, 1},
		{`{"u":"https:\/\/shop.com\/x"}`, `{"u":"https:\/\/staging.shop.test\/x"}`, 1},
		{`{"u":"\/\/shop.com"}`, `{"u":"\/\/staging.shop.test"}`, 1},
		{`"https:\\\/\\\/shop.com"`, `"https:\\\/\\\/staging.shop.test"`, 1},
		{"next=https%3A%2F%2Fshop.com%2Fa", "next=https%3A%2F%2Fstaging.shop.test%2Fa", 1},
		{"next=http%3a%2f%2fshop.com", "next=https%3a%2f%2fstaging.shop.test", 1},
		{"https:&#x2F;&#x2F;shop.com&#x2F;", "https:&#x2F;&#x2F;staging.shop.test&#x2F;", 1},
		{"http&#58;&#47;&#47;shop.com", "https&#58;&#47;&#47;staging.shop.test", 1},
		{"https:&sol;&sol;shop.com", "https:&sol;&sol;staging.shop.test", 1},
		{"https://www.shop.com/", "https://staging.shop.test/", 1},
		{"https://shop.com:8443/x", "https://staging.shop.test:8443/x", 1},
		{"https://shop.com.", "https://staging.shop.test.", 1},
		{"a https://shop.com b http://shop.com", "a https://staging.shop.test b https://staging.shop.test", 2},
		{"ftp://shop.com", "ftp://staging.shop.test", 1},
		// left alone
		{"jane@shop.com", "jane@shop.com", 0},
		{"mailto:jane@shop.com", "mailto:jane@shop.com", 0},
		{"https://jane@shop.com", "https://jane@shop.com", 0},
		{"https://myshop.com", "https://myshop.com", 0},
		{"https://shop.com.au", "https://shop.com.au", 0},
		{"https://shop.community", "https://shop.community", 0},
		{"https://shop.com-x.net", "https://shop.com-x.net", 0},
		{"https://fr.shop.com", "https://fr.shop.com", 0},
		{"https://staging.shop.test", "https://staging.shop.test", 0},
		{"visit shop.com today", "visit shop.com today", 0},
		{"", "", 0},
	}
	for _, c := range cases {
		got, n := m.Rewrite([]byte(c.in))
		if string(got) != c.want || n != c.n {
			t.Errorf("Rewrite(%q) = %q, %d; want %q, %d", c.in, got, n, c.want, c.n)
		}
	}
}

func TestRewritePairs(t *testing.T) {
	m := mustMap(t,
		Pair{"https://shop.com", "http://localhost:8080"},
		Pair{"https://shop.com/blog", "http://localhost:8080/b"},
		Pair{"fr.shop.com", "fr.staging.shop.test"},
		Pair{"https://bücher.de", "https://buecher.test"},
	)
	cases := []struct{ in, want string }{
		{"https://shop.com/x", "http://localhost:8080/x"},
		{"https://shop.com:8443/x", "http://localhost:8080/x"},
		{"https://shop.com/blog/post", "http://localhost:8080/b/post"},
		{"https://shop.com/blog", "http://localhost:8080/b"},
		{"https://shop.com/blogger", "http://localhost:8080/blogger"},
		{`https:\/\/shop.com\/blog\/post`, `http:\/\/localhost:8080\/b\/post`},
		{"https%3A%2F%2Fshop.com%2Fblog%2Fp", "http%3A%2F%2Flocalhost:8080%2Fb%2Fp"},
		{"https://fr.shop.com/a", "https://fr.staging.shop.test/a"},
		{"http://fr.shop.com/a", "http://fr.staging.shop.test/a"},
		{"https://www.fr.shop.com", "https://fr.staging.shop.test"},
		{"https://bücher.de/x", "https://buecher.test/x"},
		{"https://xn--bcher-kva.de/x", "https://buecher.test/x"},
		{"https://XN--BCHER-KVA.DE", "https://buecher.test"},
	}
	for _, c := range cases {
		if got, _ := m.Rewrite([]byte(c.in)); string(got) != c.want {
			t.Errorf("Rewrite(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExplicitWWW(t *testing.T) {
	m := mustMap(t, Pair{"shop.com", "staging.test"}, Pair{"www.shop.com", "www.staging.test"})
	got, _ := m.Rewrite([]byte("//shop.com //www.shop.com"))
	if string(got) != "//staging.test //www.staging.test" {
		t.Errorf("got %q", got)
	}
}

func TestAllLetterHost(t *testing.T) {
	m := mustMap(t, Pair{"http://localhost", "http://staging"})
	got, n := m.Rewrite([]byte("LOCALHOST http://Localhost/a http://localhost:3000"))
	if string(got) != "LOCALHOST http://staging/a http://staging:3000" || n != 2 {
		t.Errorf("got %q %d", got, n)
	}
}

func TestNewMapErrors(t *testing.T) {
	bad := []Pair{
		{"", "x.test"},
		{"shop.com", ""},
		{"ftp://shop.com", "x.test"},
		{"https://shop.com?a=1", "x.test"},
		{"https://user@shop.com", "x.test"},
		{"shop com", "x.test"},
		{"shop.com", "SHOP.com"},
		{"https://shop.com", "http://shop.com/staging"},
		{"https://shop.com:99999", "x.test"},
		{"sh_op.com", "x.test"},
	}
	for _, p := range bad {
		_, err := NewMap([]Pair{p})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 {
			t.Errorf("NewMap(%v): %v, want a config error", p, err)
		}
	}
	if _, err := NewMap([]Pair{{"localhost:8080", "localhost:8081"}}); err != nil {
		t.Errorf("two ports of one host: %v", err)
	}
	var m *Map
	if out, n := m.Rewrite([]byte("https://shop.com")); n != 0 || string(out) != "https://shop.com" {
		t.Error("nil map changed text")
	}
	if m.Find([]byte("x")) != nil {
		t.Error("nil map found spans")
	}
	if _, ok := m.RewriteHost([]byte("x")); ok {
		t.Error("nil map rewrote a host")
	}
}

func TestRewriteHost(t *testing.T) {
	m := mustMap(t, Pair{"https://shop.com", "https://staging.shop.test"}, Pair{"fr.shop.com", "fr.staging.shop.test:8080"})
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"shop.com", "staging.shop.test", true},
		{"SHOP.COM", "staging.shop.test", true},
		{"www.shop.com", "staging.shop.test", true},
		{"shop.com:8443", "staging.shop.test:8443", true},
		{"fr.shop.com", "fr.staging.shop.test:8080", true},
		{"de.shop.com", "de.shop.com", false},
		{"myshop.com", "myshop.com", false},
		{"shop.com.au", "shop.com.au", false},
	}
	for _, c := range cases {
		got, ok := m.RewriteHost([]byte(c.in))
		if string(got) != c.want || ok != c.ok {
			t.Errorf("RewriteHost(%q) = %q %v", c.in, got, ok)
		}
	}
}

func TestFind(t *testing.T) {
	m := mustMap(t, Pair{"https://shop.com", "https://staging.shop.test"})
	text := []byte(`see https://shop.com/a and {"u":"\/\/www.shop.com"} not jane@shop.com`)
	spans := m.Find(text)
	var got []string
	for _, s := range spans {
		if s.Kind != core.KURL {
			t.Errorf("kind %q", s.Kind)
		}
		got = append(got, string(text[s.Start:s.End]))
	}
	if strings.Join(got, " | ") != `https://shop.com | \/\/www.shop.com` {
		t.Errorf("spans %q", got)
	}
	out, _ := m.Rewrite(text)
	if len(m.Find(out)) != 0 {
		t.Error("found spans after Rewrite")
	}
}

func TestHosts(t *testing.T) {
	got, err := Hosts([]string{"https://shop.com/path", "*.shop.com", "https://*.cdn.shop.com", "www.Blog.example", "bücher.de", "shop.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := "shop.com www.shop.com *.shop.com *.cdn.shop.com www.blog.example blog.example xn--bcher-kva.de www.xn--bcher-kva.de"
	if strings.Join(got, " ") != want {
		t.Errorf("Hosts = %q\nwant    %q", strings.Join(got, " "), want)
	}
	for _, c := range []struct {
		host string
		ok   bool
	}{
		{"shop.com", true}, {"SHOP.COM:8443", true}, {"a.shop.com", true}, {"a.b.shop.com", true},
		{"myshop.com", false}, {"shop.com.au", false}, {"bücher.de", true}, {"blog.example", true}, {"x.blog.example", false},
	} {
		if MatchHost(got, c.host) != c.ok {
			t.Errorf("MatchHost(%q) != %v", c.host, c.ok)
		}
	}
	globOnly, _ := Hosts([]string{"*.shop.com"})
	if MatchHost(globOnly, "shop.com") {
		t.Error("*.shop.com matched shop.com itself")
	}
	for _, bad := range []string{"", "*", "*.", "ftp://x.com", "a b", "*.*.shop.com"} {
		if _, err := Hosts([]string{bad}); err == nil {
			t.Errorf("Hosts(%q) accepted", bad)
		}
	}
}

func TestPunycode(t *testing.T) {
	for in, want := range map[string]string{
		"bücher":        "bcher-kva",
		"münchen":       "mnchen-3ya",
		"例え":            "r8jz45g",
		"ελληνικά":      "hxargifdar",
		"café":          "caf-dma",
		"straße":        "strae-oqa",
		"правительство": "80aealotwbjpid2k",
	} {
		if got, ok := punycode(in); !ok || got != want {
			t.Errorf("punycode(%q) = %q, want %q", in, got, want)
		}
	}
}

// decodeAll spells every separator form plainly, so a URL that survived in
// any spelling shows as //host: JSON's \/ (escaped any number of times),
// %2F and %3A in either case and HTML's numeric and named entities.
func decodeAll(b []byte) []byte {
	s := string(b)
	for {
		n := strings.NewReplacer(`\/`, "/", "%2F", "/", "%2f", "/", "%3A", ":", "%3a", ":", "&sol;", "/", "&colon;", ":").Replace(s)
		n = decodeEntities(n)
		if n == s {
			return []byte(s)
		}
		s = n
	}
}

// decodeEntities turns &#47; &#x2F; &#X2f; (any leading zeros) into '/'
// and the same for ':', leaving every other entity.
func decodeEntities(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '&' || i+1 >= len(s) || s[i+1] != '#' {
			out.WriteByte(s[i])
			continue
		}
		j, hex := i+2, false
		if j < len(s) && (s[j] == 'x' || s[j] == 'X') {
			j, hex = j+1, true
		}
		base := 10
		if hex {
			base = 16
		}
		v, k := 0, j
		for k < len(s) && (s[k] >= '0' && s[k] <= '9' || hex && isHex(s[k])) && v <= 0x10ffff {
			v = v*base + hexVal(s[k])
			k++
		}
		if k == j || k >= len(s) || s[k] != ';' || (v != '/' && v != ':') {
			out.WriteByte(s[i])
			continue
		}
		out.WriteByte(byte(v))
		i = k
	}
	return out.String()
}

// survivor reports a //shop.com at a host boundary in text.
func survivor(text []byte, host string) bool {
	low := asciiLower(text)
	for i := 0; ; {
		j := bytes.Index(low[i:], []byte("//"+host))
		if j < 0 {
			return false
		}
		end := i + j + 2 + len(host)
		if hostEnds(text, end) {
			return true
		}
		i += j + 1
	}
}

// asciiLower folds ASCII letters only, so indexes stay where they were.
func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = lower(c)
	}
	return out
}

func FuzzURLRewrite(f *testing.F) {
	for _, s := range []string{
		"https://shop.com/x", `https:\/\/shop.com`, "https%3A%2F%2Fshop.com", "https:&#x2F;&#x2F;shop.com",
		"//www.shop.com", "jane@shop.com", "https://myshop.com", "https://shop.com.au", "https://shop.community",
		`/\/shop.com`, "&#0047;/shop.com", "%2f/SHOP.com:80", "https://staging.shop.test",
	} {
		f.Add([]byte(s))
	}
	m := mustMap(f, Pair{"https://shop.com", "https://staging.shop.test"})
	f.Fuzz(func(t *testing.T, b []byte) {
		out, n := m.Rewrite(b)
		for _, h := range []string{"shop.com", "www.shop.com"} {
			if survivor(decodeAll(out), h) {
				t.Fatalf("%s survived in %q (from %q)", h, out, b)
			}
		}
		if n == 0 && !bytes.Equal(out, b) {
			t.Fatalf("no match but changed: %q", out)
		}
		if !survivor(decodeAll(b), "shop.com") && !survivor(decodeAll(b), "www.shop.com") && n != 0 {
			t.Fatalf("rewrote %q without a production URL: %q", b, out)
		}
		again, n2 := m.Rewrite(out)
		if n2 != 0 || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: %q then %q", out, again)
		}
		for _, l := range []string{"myshop.com", "shop.com.au", "shop.community", "@shop.com"} {
			if bytes.Count(asciiLower(out), []byte(l)) < bytes.Count(asciiLower(b), []byte(l)) {
				t.Fatalf("lookalike %s changed: %q to %q", l, b, out)
			}
		}
	})
}

func BenchmarkURLRewrite(b *testing.B) {
	m := mustMap(b, Pair{"https://shop.com", "https://staging.shop.test"}, Pair{"https://fr.shop.com", "https://fr.staging.shop.test"})
	prose := []byte(strings.Repeat("The quick brown fox jumps over the lazy dog. Orders ship in two days, see our policy page for more. ", 10000))
	b.Run("no-match", func(b *testing.B) {
		b.SetBytes(int64(len(prose)))
		for b.Loop() {
			m.Rewrite(prose)
		}
	})
	urls := []byte(strings.Repeat(`<a href="https://shop.com/product/1">Product</a> <img src="https:\/\/shop.com\/img.png"> `, 5000))
	b.Run("urls", func(b *testing.B) {
		b.SetBytes(int64(len(urls)))
		b.ReportAllocs()
		for b.Loop() {
			m.Rewrite(urls)
		}
	})
}

// Allocation guards: text with no production URL costs no allocation in
// Rewrite or Find, however long it is; "shop.com" without two slashes
// before it and "//" alone are near misses that must stay cheap.
func TestAllocs(t *testing.T) {
	m := mustMap(t, Pair{"https://shop.com", "https://staging.shop.test"})
	prose := []byte(strings.Repeat("Orders from shop.com ship in two days, see // and myshop.com for more. ", 2000))
	if n := testing.AllocsPerRun(10, func() { m.Rewrite(prose) }); n != 0 {
		t.Errorf("Rewrite with no match: %v allocations, want 0", n)
	}
	if n := testing.AllocsPerRun(10, func() { m.Find(prose) }); n != 0 {
		t.Errorf("Find with no match: %v allocations, want 0", n)
	}
	one := []byte("see https://shop.com/x now")
	if n := testing.AllocsPerRun(10, func() { m.Rewrite(one) }); n > 4 {
		t.Errorf("Rewrite of one URL: %v allocations, want at most 4", n)
	}
}
