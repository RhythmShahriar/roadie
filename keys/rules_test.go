// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// ruleCase is one case of testdata/rules.yaml.
type ruleCase struct {
	Provider string            `yaml:"provider"`
	Env      string            `yaml:"env"`
	Value    string            `yaml:"value"`
	Row      string            `yaml:"row"`
	Path     string            `yaml:"path"`
	Siblings map[string]string `yaml:"siblings"`
	Pairs    map[string]string `yaml:"pairs"`
	Target   string            `yaml:"target"`
	Note     string            `yaml:"note"`
}

var templ = regexp.MustCompile(`\{([aAlhdt]):(\d+)\}|\{uuid\}`)

// expander fills {x:N} templates with characters from a seeded source and
// joins the parts with testutil.J, so no key is ever in the source.
type expander struct{ r *rand.Rand }

// keyBody returns n key characters starting with seed. A real key body
// is random, so it has more than three different characters; a body of
// fewer reads as a placeholder (xxxx, 0000).
func keyBody(seed string, n int) string {
	return strings.Repeat(seed+"Zk7Wm", n)[:n]
}

func newExpander(seed string) *expander {
	h := fnv.New64a()
	h.Write([]byte(seed))
	return &expander{r: rand.New(rand.NewPCG(h.Sum64(), 7))}
}

func (e *expander) chars(set string, n int) string {
	var b strings.Builder
	for range n {
		b.WriteByte(set[e.r.IntN(len(set))])
	}
	return b.String()
}

func (e *expander) expand(s string) string {
	var parts []string
	last := 0
	for _, m := range templ.FindAllStringSubmatchIndex(s, -1) {
		parts = append(parts, s[last:m[0]])
		last = m[1]
		if m[2] < 0 {
			parts = append(parts, e.chars("0123456789abcdef", 8), "-", e.chars("0123456789abcdef", 4), "-4",
				e.chars("0123456789abcdef", 3), "-a", e.chars("0123456789abcdef", 3), "-", e.chars("0123456789abcdef", 12))
			continue
		}
		n, _ := strconv.Atoi(s[m[4]:m[5]])
		set := map[string]string{
			"a": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
			"A": "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			"l": "abcdefghijklmnopqrstuvwxyz0123456789",
			"h": "0123456789abcdef",
			"d": "0123456789",
			"t": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-",
		}[s[m[2]:m[3]]]
		parts = append(parts, e.chars(set, n))
	}
	parts = append(parts, s[last:])
	return testutil.J(parts...)
}

func loadRuleCases(t *testing.T) []ruleCase {
	t.Helper()
	b, err := os.ReadFile("testdata/rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []ruleCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

// run expands a case and returns its hits and the expanded value.
func (c ruleCase) run(i int) ([]Hit, string) {
	e := newExpander(fmt.Sprint(i, c.Provider, c.Value, c.Path))
	if c.Pairs != nil {
		names := make([]string, 0, len(c.Pairs))
		for n := range c.Pairs {
			names = append(names, n)
		}
		slices.Sort(names)
		var pairs []core.Pair
		target := ""
		for li, n := range names {
			v := e.expand(c.Pairs[n])
			if n == c.Target {
				target = v
			}
			pairs = append(pairs, core.Pair{Name: n, Value: v, Line: li + 1})
		}
		var out []Hit
		for _, h := range ScanPairs(".env", pairs) {
			if h.At.Line == slices.Index(names, c.Target)+1 {
				out = append(out, h)
			}
		}
		return out, target
	}
	sib := map[string]string{}
	for k, v := range c.Siblings {
		sib[k] = e.expand(v)
	}
	v := e.expand(c.Value)
	at := core.Location{Table: "wp_options", Column: "option_value", Row: c.Row, Path: c.Path}
	return ScanValue([]byte(v), at, sib), v
}

func TestRuleTable(t *testing.T) {
	for i, c := range loadRuleCases(t) {
		name := fmt.Sprintf("%d_%s_%s", i, c.Provider, c.Env)
		t.Run(name, func(t *testing.T) {
			hits, v := c.run(i)
			if c.Env == "none" {
				if len(hits) != 0 {
					t.Fatalf("%s: got %+v, want no hit", c.Note, hits)
				}
				return
			}
			if len(hits) != 1 {
				t.Fatalf("%s: got %d hits %+v, want one", c.Note, len(hits), hits)
			}
			h := hits[0]
			if h.Provider != c.Provider || h.Env != c.Env {
				t.Fatalf("%s: got %s/%s, want %s/%s", c.Note, h.Provider, h.Env, c.Provider, c.Env)
			}
			if !core.KnownID(h.ID) {
				t.Errorf("ID %q is not a known finding ID", h.ID)
			}
			if strings.Contains(h.Masked, v) || len(v) > 8 && strings.Contains(h.Masked, v[len(v)-6:]) {
				t.Errorf("masked %q shows the key", h.Masked)
			}
			if !strings.Contains(h.Masked, "•") {
				t.Errorf("masked %q has no bullets", h.Masked)
			}
			switch c.Env {
			case EnvTest:
				if h.Severity != core.SeverityNone {
					t.Errorf("a test key has severity %v", h.Severity)
				}
			case EnvUnknown:
				if h.ID != core.KeyLiveID(c.Provider) || h.Severity != core.SeverityMedium {
					t.Errorf("unknown env: %s %v", h.ID, h.Severity)
				}
			case EnvLive:
				if h.Severity < core.SeverityHigh {
					t.Errorf("a live key has severity %v", h.Severity)
				}
			}
		})
	}
}

// Every provider of the table has a live positive and a negative; those
// with a test form also a test negative.
func TestEveryProviderHasFixtures(t *testing.T) {
	type seen struct{ live, test, none bool }
	got := map[string]*seen{}
	for _, c := range loadRuleCases(t) {
		s := got[c.Provider]
		if s == nil {
			s = &seen{}
			got[c.Provider] = s
		}
		switch c.Env {
		case EnvLive:
			s.live = true
		case EnvTest:
			s.test = true
		case "none":
			s.none = true
		}
	}
	providers := []string{ProviderGeneric}
	for _, r := range tokenRules {
		providers = append(providers, r.provider)
	}
	for _, r := range nameRules {
		if r.provider != "mail" {
			providers = append(providers, r.provider)
		}
	}
	for _, p := range mailProviders {
		providers = append(providers, p.provider)
	}
	noTest := []string{"woocommerce-rest", "aws", "jetpack", "sendgrid", "mailgun", "brevo", "mailersend",
		"resend", "mailchimp", "postmark", "sparkpost", "mandrill", "ses", ProviderGeneric}
	for _, p := range providers {
		s := got[p]
		if s == nil || !s.live || !s.none {
			t.Errorf("provider %s needs a live positive and a negative in testdata/rules.yaml", p)
			continue
		}
		if !slices.Contains(noTest, p) && !s.test {
			t.Errorf("provider %s needs a test negative", p)
		}
		if p != ProviderGeneric && !slices.Contains(core.KeyProviders(), p) {
			t.Errorf("provider %s has no key.<provider>-live ID in core", p)
		}
	}
}

// A test key is reported with Env test and no severity, so it never
// becomes a finding.
func TestTestKeysPassThrough(t *testing.T) {
	v := testutil.J("sk_", "test_", keyBody("Ab3", 24))
	hits := ScanValue([]byte(v), core.Location{Table: "wp_options"}, nil)
	if len(hits) != 1 || hits[0].Env != EnvTest || hits[0].Severity != core.SeverityNone {
		t.Fatalf("got %+v", hits)
	}
}

func TestClassify(t *testing.T) {
	live := testutil.J("sk_", "live_", keyBody("x9Z", 24))
	tests := []struct {
		provider, value string
		sib             map[string]string
		want            string
	}{
		{"stripe", live, nil, EnvLive},
		{"stripe", testutil.J("sk_", "test_", keyBody("x9Z", 24)), nil, EnvTest},
		{"stripe_publishable", testutil.J("pk_", "test_", keyBody("x9Z", 24)), nil, EnvTest},
		{"stripe-webhook", testutil.J("whsec_", keyBody("x9Z", 24)), map[string]string{"testmode": "no"}, EnvLive},
		{"paypal_client_secret", keyBody("Ab", 40), map[string]string{"sandbox_on": "1"}, EnvTest},
		{"paypal_client_secret", keyBody("Ab", 40), map[string]string{"name": "client_secret_production"}, EnvLive},
		{"paypal", keyBody("Ab", 40), nil, EnvUnknown},
		{"razorpay_key_id", testutil.J("rzp_", "live_", keyBody("a1", 14)), nil, EnvLive},
		{"square", testutil.J("sandbox-", "sq0idb-", keyBody("a1", 22)), nil, EnvTest},
		{"square", testutil.J("EAAA", keyBody("a1", 60)), map[string]string{"environment": "production"}, EnvLive},
		{"mollie", testutil.J("test_", keyBody("a1B", 30)), nil, EnvTest},
		{"stripe", "", nil, EnvUnknown},
	}
	for _, tt := range tests {
		if got := Classify(tt.provider, tt.value, tt.sib); got != tt.want {
			t.Errorf("Classify(%s, %s) = %s, want %s", tt.provider, core.MaskKey(tt.value), got, tt.want)
		}
	}
}

func TestDisabledGatewayIsLow(t *testing.T) {
	v := testutil.J("sk_", "live_", keyBody("Ab3", 24))
	hits := ScanValue([]byte(v), core.Location{Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"},
		map[string]string{"enabled": "no", "testmode": "no"})
	if len(hits) != 1 || hits[0].Severity != core.SeverityLow || hits[0].Env != EnvLive {
		t.Fatalf("got %+v", hits)
	}
}

func TestPlaceholders(t *testing.T) {
	for _, v := range []string{"", "  ", "xxx", "XXXXXXXX", "changeme", "${STRIPE_SECRET}", "{{ key }}", "<your-key>",
		"%STRIPE%", "sk_l•••••", "********", "your_api_key_here", "null", "false"} {
		if !placeholder(v) {
			t.Errorf("placeholder(%q) = false", v)
		}
	}
	for _, v := range []string{"Ab3dE5gH7jK9", "hunter2hunter2"} {
		if placeholder(v) {
			t.Errorf("placeholder(%q) = true", v)
		}
	}
}

// A key body that only stands in for one is no key, inside any text.
func TestPlaceholderBody(t *testing.T) {
	for _, v := range []string{"xxxxxxxxxxxxxxxxxxxxxxxx", "XXXXXXXXXXXXXXXX", "000000000000000000", "x0x0x0x0x0x0x0x0",
		"YOUR_SECRET_KEY_HERE_123", "your_key_goes_here_abc9", "Ab3dEXAMPLE7kLm9nPq", "Ab3dREDACTED9QwE"} {
		if !placeholderBody([]byte(v)) {
			t.Errorf("placeholderBody(%q) = false", v)
		}
	}
	for _, v := range []string{"Ab3dE5gH7jK9mN2pQ4rS6tU8", keyBody("Ab3", 24), "51NqZx8Kp2Zx8Kp2Zx8Kp2Zx8Kp2", "YoUr3dE5gH7jK9mN2pQ4"} {
		if placeholderBody([]byte(v)) {
			t.Errorf("placeholderBody(%q) = true", v)
		}
	}
}

// Two keys in one value are two hits; a key glued to a word is none.
func TestTokensInText(t *testing.T) {
	a := testutil.J("sk_", "live_", keyBody("Ab3", 24))
	b := testutil.J("pk_", "live_", keyBody("Cd4", 24))
	hits := ScanValue([]byte("secret="+a+"\npublishable="+b), core.Location{}, nil)
	if len(hits) != 2 || hits[0].Provider != "stripe" || hits[1].Provider != "stripe-publishable" {
		t.Fatalf("got %+v", hits)
	}
	if hits := ScanValue([]byte("x"+a), core.Location{}, nil); len(hits) != 0 {
		t.Fatalf("glued: got %+v", hits)
	}
}

func TestLeafKey(t *testing.T) {
	tests := []struct{ in, path, key string }{
		{"serialized:smtp.pass", "smtp.pass", "pass"},
		{"serialized:secret_key", "secret_key", "secret_key"},
		{"serialized:data.json:email", "email", "email"},
		{"json:items[2]", "items[2]", "2"},
		{`serialized:a\.b`, `a\.b`, "a.b"},
		{"", "", ""},
	}
	for _, tt := range tests {
		p, k := LeafKey(tt.in)
		if p != tt.path || k != tt.key {
			t.Errorf("LeafKey(%q) = %q, %q; want %q, %q", tt.in, p, k, tt.path, tt.key)
		}
	}
}

func TestLoopbackHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "localhost", "LOCALHOST:1025", "[::1]", "mailpit", "x.localhost"} {
		if !loopbackHost(h) {
			t.Errorf("%s is loopback", h)
		}
	}
	for _, h := range []string{"smtp.gmail.com", "10.0.0.5", ""} {
		if loopbackHost(h) {
			t.Errorf("%s is not loopback", h)
		}
	}
}

// The scan is linear: a megabyte of near-misses for every rule takes
// well under a second.
func TestScanLinear(t *testing.T) {
	for _, unit := range []string{"sk_live_", "live_", "AQE", "a", "access_token$production$", "re_", "0123456789abcdef"} {
		v := bytes.Repeat([]byte(unit), (1<<20)/len(unit))
		bound := time.Second
		if raceEnabled {
			bound *= 15
		}
		// Best of three, so one stall on a busy runner doesn't fail it.
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			ScanValue(v, core.Location{Row: "option_name=adyen"}, nil)
			if best = min(best, time.Since(start)); best < bound {
				break
			}
		}
		if best >= bound {
			t.Errorf("1 MiB of %q took %v, want under %v", unit, best, bound)
		}
	}
}

func BenchmarkScanValue(b *testing.B) {
	leaves := [][]byte{
		[]byte("ada.kowalczyk@example.com"),
		[]byte("<p>Thanks for your order. We ship within two days.</p>"),
		[]byte("2026-10-26 10:00:00"),
		[]byte("a:2:{s:4:\"name\";s:5:\"Shirt\";s:3:\"sku\";s:8:\"SKU-1234\";}"),
		[]byte("https://shop.example/wp-content/uploads/2026/10/shirt.jpg"),
		[]byte("_billing_address_index"),
		[]byte("1234"),
		[]byte(strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 20)),
	}
	var n int64
	for _, l := range leaves {
		n += int64(len(l))
	}
	b.SetBytes(n)
	at := core.Location{Table: "wp_postmeta", Column: "meta_value", Row: "meta_key=_product_attributes"}
	for b.Loop() {
		for _, l := range leaves {
			ScanValue(l, at, nil)
		}
	}
}

// mayHoldToken lets through a run of 20 token characters with a letter
// and a digit, a PEM key or a URL with a password; nothing else.
func TestMayHoldToken(t *testing.T) {
	for v, want := range map[string]bool{
		"abcdefghij0123456789":                     true,
		"x=abc_def.ghi-jkl+mn~o$p1":                true,
		"-----BEGIN RSA PRIVATE KEY-----":          true,
		"mysql://shop:" + "hunter2" + "@db/shop":   true,
		"abcdefghij012345678":                      false,
		"abcdefghijklmnopqrstuvwxyz":               false,
		"01234567890123456789012":                  false,
		"Thanks for your order, 2026-10-26 10:00.": false,
		"abcdefghij 0123456789":                    false,
		"https://shop.example/a@2x.png":            true,
		"":                                         false,
	} {
		if got := mayHoldToken([]byte(v)); got != want {
			t.Errorf("mayHoldToken(%q) = %v, want %v", v, got, want)
		}
	}
}
