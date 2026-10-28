// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"crypto/md5" // #nosec G501 -- an encoding exact mode looks for
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// newKnown returns the real known set once the values lane has built it
// and the test fake until then.
func newKnown(t *testing.T) core.Known {
	t.Helper()
	key, err := pseudo.NewKey()
	if errors.Is(err, core.ErrNotBuilt) {
		return testutil.FakeKnown()
	}
	if err != nil {
		t.Fatal(err)
	}
	if k := pseudo.NewKnown(key); k != nil {
		return k
	}
	return testutil.FakeKnown()
}

func hexOf(b [16]byte) string     { return hex.EncodeToString(b[:]) }
func hex256(b [32]byte) string    { return hex.EncodeToString(b[:]) }
func lowerB(s string) []byte      { return []byte(strings.ToLower(s)) }
func entityFirst(s string) string { return fmt.Sprintf("&#%d;", s[0]) + s[1:] }

func TestSurvivorsEveryEncoding(t *testing.T) {
	c := testutil.Canaries(11)
	email, name, phone := c[core.KEmail][0], c[core.KFullName][1], c[core.KPhone][2]
	street := c[core.KStreet][3]
	k := newKnown(t)
	k.Add(core.KEmail, []byte(email))
	k.Add(core.KFullName, []byte(name))
	k.Add(core.KPhone, []byte(strings.ReplaceAll(phone, " ", "")))
	k.Add(core.KStreet, []byte(street))
	k.Seal()
	local, domain, _ := strings.Cut(email, "@")
	cases := []struct{ leaf, enc string }{
		{"Contact " + email + " today", "raw"},
		{"order for " + strings.ToLower(name) + " shipped", "lower"},
		{name, "raw"},
		{"phone " + phone, "raw"},
		{street, "raw"},
		{"hash=" + hexOf(md5.Sum(lowerB(email))) + ";", "md5"}, // #nosec G401 -- test of the md5 encoding
		{"em=" + hex256(sha256.Sum256(lowerB(email))), "sha256"},
		{"payload " + base64.StdEncoding.EncodeToString([]byte(`{"email":"`+email+`"}`)), "base64"},
		{"t=" + base64.RawURLEncoding.EncodeToString([]byte("to: "+name)), "base64"},
		{"https://shop.example/track?n=" + url.PathEscape(name), "url"},
		{local + "%40" + domain, "url"},
		{local + "&#64;" + domain, "html-entity"},
		{"<p>" + entityFirst(name) + "</p>", "html-entity"},
	}
	d := Survivors(k)
	if d.ID() != core.FindingExactSurvivors {
		t.Fatalf("ID %q", d.ID())
	}
	for _, tc := range cases {
		hits := scanAll(d, tc.leaf, core.ScanContext{})
		if len(hits) != 1 {
			t.Errorf("%q: %d hits %v, want 1", tc.leaf, len(hits), hits)
			continue
		}
		h := hits[0]
		if h.Encoding != tc.enc || h.Severity != core.SeverityCritical {
			t.Errorf("%q: encoding %q severity %v, want %q critical", tc.leaf, h.Encoding, h.Severity, tc.enc)
		}
		for _, secret := range []string{email, name, local, phone} {
			if strings.Contains(h.Masked, secret) {
				t.Errorf("%q: masked %q shows %q", tc.leaf, h.Masked, secret)
			}
		}
	}
}

// Roadie's own fakes and other people's values never hit.
func TestSurvivorsLeaveFakesAlone(t *testing.T) {
	c := testutil.Canaries(12)
	k := newKnown(t)
	for _, kind := range core.FakeKinds() {
		for _, v := range c[kind] {
			k.Add(kind, []byte(v))
		}
	}
	k.Seal()
	f := testutil.FakeFaker()
	other := testutil.Canaries(13)
	d := Survivors(k)
	for _, kind := range core.FakeKinds() {
		for _, v := range c[kind] {
			fake, err := f.Fake(kind, []byte(v), core.FakeOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if h := scanAll(d, string(fake), core.ScanContext{}); len(h) != 0 {
				t.Errorf("fake %q hit: %v", fake, h)
			}
		}
		for _, v := range other[kind] {
			if h := scanAll(d, v, core.ScanContext{}); len(h) != 0 {
				t.Errorf("unknown %s %q hit: %v", kind, v, h)
			}
		}
	}
}

// A Roadie-format fake that somehow got into the known set is still not
// reported: a fake is never a survivor.
func TestSurvivorsSkipRoadieFakes(t *testing.T) {
	if !valuesLaneBuilt() {
		t.Skip("needs the values lane")
	}
	fake := "u0123456789abcdef@example.invalid"
	k := newKnown(t)
	k.Add(core.KEmail, []byte(fake))
	k.Seal()
	if h := scanAll(Survivors(k), "to "+fake, core.ScanContext{}); len(h) != 0 {
		t.Errorf("fake hit: %v", h)
	}
}

func TestSurvivorsRandomTextHasNoHits(t *testing.T) {
	k := newKnown(t)
	for kind, vals := range testutil.Canaries(14) {
		for _, v := range vals {
			k.Add(kind, []byte(v))
		}
	}
	k.Seal()
	r := rand.New(rand.NewPCG(1, 2))
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .,@%&;:+-_/=éßø"
	letters := []rune(alphabet)
	var b strings.Builder
	for b.Len() < mib {
		b.WriteRune(letters[r.IntN(len(letters))])
	}
	text := b.String()
	start := time.Now()
	if h := scanAll(Survivors(k), text, core.ScanContext{}); len(h) != 0 {
		t.Errorf("%d hits in random text: %v", len(h), h[:min(3, len(h))])
	}
	if d := time.Since(start); d > timeBound(5*time.Second) {
		t.Errorf("1 MiB took %v", d)
	}
}

func TestSurvivorsNilAndShort(t *testing.T) {
	if h := scanAll(Survivors(nil), "anything at all", core.ScanContext{}); len(h) != 0 {
		t.Errorf("nil known hit: %v", h)
	}
	k := newKnown(t)
	k.Add(core.KFirstName, []byte("Ada"))
	k.Seal()
	if h := scanAll(Survivors(k), "Ada", core.ScanContext{}); len(h) != 0 {
		t.Errorf("a value under 4 bytes hit: %v", h)
	}
}

// The words of an address that survived whole are not reported twice.
func TestSurvivorsNoOverlap(t *testing.T) {
	k := newKnown(t)
	k.Add(core.KEmail, []byte("kowalczyk@mail.example.org"))
	k.Add(core.KLastName, []byte("Kowalczyk"))
	k.Seal()
	hits := scanAll(Survivors(k), "kowalczyk@mail.example.org and Kowalczyk", core.ScanContext{})
	if len(hits) != 2 || hits[0].End > hits[1].Start {
		t.Errorf("hits %v", hits)
	}
}

func TestDecoders(t *testing.T) {
	out, offs, ok := decodeEntities([]byte("a&#64;b&amp;c&#xE9;&bogus;"))
	if !ok || string(out) != "a@b&cé&bogus;" || len(offs) != len(out)+1 {
		t.Errorf("entities %q %v", out, offs)
	}
	out, offs, ok = decodePercent([]byte("a%40b%2"))
	if !ok || string(out) != "a@b%2" || offs[1] != 1 || offs[2] != 4 {
		t.Errorf("percent %q %v", out, offs)
	}
	if _, _, ok := decodePercent([]byte("plain")); ok {
		t.Error("nothing to decode reported a change")
	}
}

// FuzzSurvivors feeds untrusted leaves through the base64, entity and
// percent decoders: no panic, hits inside the leaf and a linear bound.
func FuzzSurvivors(f *testing.F) {
	c := testutil.Canaries(14)
	email := c[core.KEmail][0]
	for _, s := range []string{
		email, base64.StdEncoding.EncodeToString([]byte(email)),
		base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte(email)))),
		strings.ReplaceAll(email, "@", "&#64;"), strings.ReplaceAll(email, "@", "&#x40;"), url.QueryEscape(email),
		"&#", "&#x;", "&#99999999999;", "&amp;&lt;&", "%", "%zz%4", "====", strings.Repeat("A", 4096), "\xff\xfe&#64;",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, v []byte) {
		k := newKnown(t)
		for kind, vals := range c {
			for _, s := range vals {
				k.Add(kind, []byte(s))
			}
		}
		k.Seal()
		start := time.Now()
		var hits []core.Hit
		Survivors(k).Scan(v, core.ScanContext{}, func(h core.Hit) { hits = append(hits, h) })
		if d := time.Since(start); d > timeBound(time.Second) {
			t.Fatalf("%d bytes took %v", len(v), d)
		}
		for _, h := range hits {
			if h.Start < 0 || h.End > len(v) || h.Start >= h.End {
				t.Fatalf("hit %d..%d outside %d bytes", h.Start, h.End, len(v))
			}
		}
	})
}

// A known city is a survivor as the whole leaf only: a town named in a
// note points at no one.
func TestSurvivorsCityOnlyWhole(t *testing.T) {
	k := newKnown(t)
	k.Add(core.KCity, []byte("Kilmacthomas"))
	k.Seal()
	d := Survivors(k)
	for leaf, want := range map[string]int{
		"Kilmacthomas":                    1,
		"  Kilmacthomas\n":                1,
		"kilmacthomas":                    1,
		"Made in Kilmacthomas":            0,
		"12 Main Street, Kilmacthomas":    0,
		"Kilmacthomasville is a new town": 0,
	} {
		if hits := scanAll(d, leaf, core.ScanContext{}); len(hits) != want {
			t.Errorf("%q: %d hits %v, want %d", leaf, len(hits), hits, want)
		}
	}
}

// A word of a whole Roadie fake (a fake street named after a real
// customer's first name) is not a survivor; the same word in other text
// is.
func TestSurvivorsWordOfAFake(t *testing.T) {
	f, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{})
	if errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	if err != nil {
		t.Fatal(err)
	}
	var street []byte
	for i := 0; street == nil || !strings.Contains(string(street), " "); i++ {
		if street, err = f.Fake(core.KStreet, fmt.Appendf(nil, "%d Real Road", i), core.FakeOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	word := strings.Fields(string(street))[1]
	k := newKnown(t)
	k.Add(core.KFirstName, []byte(word))
	k.Seal()
	d := Survivors(k)
	if hits := scanAll(d, string(street), core.ScanContext{}); len(hits) != 0 && pseudo.IsFake(core.KStreet, street) {
		t.Errorf("fake street %q: hits %v", street, hits)
	}
	if hits := scanAll(d, "Ask "+word+" Okonkwo-Baptiste about it", core.ScanContext{}); len(hits) != 1 && !pseudo.IsFake(core.KFirstName, []byte(word)) {
		t.Errorf("%q in a note: %d hits, want 1", word, len(hits))
	}
}

// A real surname that is also a word of Roadie's fake email domain
// (Ada Example, u1f2@example.invalid) is no survivor inside the fake
// email; the same name in a person's text, or in a real email, is.
func TestSurvivorsWordOfAFakeEmail(t *testing.T) {
	f, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{})
	if errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	if err != nil {
		t.Fatal(err)
	}
	fake, err := f.Fake(core.KEmail, []byte("ada@shop.test"), core.FakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !pseudo.IsFake(core.KEmail, fake) || !strings.HasSuffix(string(fake), "@example.invalid") {
		t.Fatalf("fake email %q", fake)
	}
	k := newKnown(t)
	k.Add(core.KFullName, []byte("Ada Example"))
	k.Add(core.KLastName, []byte("Example"))
	k.Seal()
	d := Survivors(k)
	for _, leaf := range []string{string(fake), "Mail " + string(fake) + " today", base64.StdEncoding.EncodeToString(fake)} {
		if hits := scanAll(d, leaf, core.ScanContext{Column: "email"}); len(hits) != 0 {
			t.Errorf("%q: %d hits, want none", leaf, len(hits))
		}
	}
	// Negative: the surname in a note and in a real email still is.
	for _, leaf := range []string{"Ask Ada Example about it", "ada@example.co.uk"} {
		if hits := scanAll(d, leaf, core.ScanContext{}); len(hits) == 0 {
			t.Errorf("%q: no hit", leaf)
		}
	}
}

// A fake first name that Known holds as another customer's city is a
// Roadie fake all the same; a real word no list has is still reported.
func TestSurvivorsFakeOfAnotherKind(t *testing.T) {
	if !pseudo.IsFake(core.KFirstName, []byte("Austin")) {
		t.Skip("needs the values lane's name lists")
	}
	k := newKnown(t)
	k.Add(core.KCity, []byte("Austin"))
	k.Add(core.KCity, []byte("Kilmacthomas"))
	k.Seal()
	d := Survivors(k)
	if hits := scanAll(d, "Austin", core.ScanContext{}); len(hits) != 0 {
		t.Errorf("a fake first name equal to a known city: %v", hits)
	}
	if hits := scanAll(d, "Kilmacthomas", core.ScanContext{}); len(hits) != 1 {
		t.Errorf("a real city: %d hits, want 1", len(hits))
	}
}

// A name word joined to an underscore or a digit is part of an
// identifier; a schema's code columns and the site's common words are
// no survivors; the same name in a person's text still is.
func TestSurvivorsIdentifiersAndCodes(t *testing.T) {
	k := newKnown(t)
	k.Add(core.KFullName, []byte("Shop Owner"))
	k.Add(core.KFirstName, []byte("Shop"))
	k.Add(core.KLastName, []byte("Owner"))
	k.Add(core.KFirstName, []byte("Quillon"))
	k.Seal()
	d := Survivors(k)
	cases := []struct {
		leaf string
		col  string
		want int
	}{
		{"shop_order", "type", 0},
		{"shop_order", "meta_value", 0},
		{"wc-shop_owner", "meta_key", 0},
		{"quillon_2", "meta_value", 0},
		{"quillon2", "meta_value", 0},
		{"Quillon", "type", 0},
		{"Quillon", "post_status", 0},
		{"Visit the shop page", "post_content", 0},
		{"Quillon", "meta_value", 1},
		{"Ask Quillon at the desk", "comment_content", 1},
		{"hi quillon!", "meta_value", 1},
		{"Shop Owner", "display_name", 1},
	}
	for _, c := range cases {
		if hits := scanAll(d, c.leaf, core.ScanContext{Column: c.col}); len(hits) != c.want {
			t.Errorf("%q in %s: %d hits %v, want %d", c.leaf, c.col, len(hits), hits, c.want)
		}
	}
}
