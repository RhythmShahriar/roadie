// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/maphash"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func sealedKnown(t testing.TB, add map[core.FakeKind][]string) core.Known {
	t.Helper()
	k := NewKnown(testKey(4))
	for kind, vals := range add {
		for _, v := range vals {
			k.Add(kind, []byte(v))
		}
	}
	k.Seal()
	return k
}

func hexOf(v string) (string, string) {
	m, s := md5.Sum([]byte(v)), sha256.Sum256([]byte(v)) // #nosec G401 -- the encodings a copy may hold
	return hex.EncodeToString(m[:]), hex.EncodeToString(s[:])
}

func TestKnownEncodings(t *testing.T) {
	c := testutil.Canaries(21)
	k := sealedKnown(t, map[core.FakeKind][]string{
		core.KEmail: c[core.KEmail], core.KPhone: c[core.KPhone], core.KIP: c[core.KIP],
	})
	for _, e := range c[core.KEmail] {
		m, s := hexOf(strings.ToLower(e))
		for token, wantEnc := range map[string]string{
			e: EncodingRaw, strings.ToUpper(e): EncodingRaw, " " + e + "\n": EncodingRaw,
			m: EncodingMD5, strings.ToUpper(m): EncodingMD5, s: EncodingSHA256, strings.ToUpper(s): EncodingSHA256,
		} {
			kind, enc, ok := k.Contains([]byte(token))
			if !ok || kind != core.KEmail || enc != wantEnc {
				t.Errorf("Contains(%q) = %s %s %v, want email %s", token, kind, enc, ok, wantEnc)
			}
		}
	}
	p := c[core.KPhone][0] // +44 7911 dddddd
	for _, token := range []string{p, strings.ReplaceAll(p, " ", ""), strings.ReplaceAll(p, " ", "-")} {
		if kind, _, ok := k.Contains([]byte(token)); !ok || kind != core.KPhone {
			t.Errorf("phone form %q not found", token)
		}
	}
	ip := c[core.KIP][0]
	for _, token := range []string{ip, "::ffff:" + ip} {
		if kind, _, ok := k.Contains([]byte(token)); !ok || kind != core.KIP {
			t.Errorf("ip form %q not found", token)
		}
	}
	if k.Len() != len(c[core.KEmail])+len(c[core.KPhone])+len(c[core.KIP]) {
		t.Errorf("Len = %d", k.Len())
	}
}

func TestKnownMisses(t *testing.T) {
	k := sealedKnown(t, map[core.FakeKind][]string{core.KEmail: {"jane.doe@shop.com"}, core.KFullName: {"Bo Li"}})
	m, s := hexOf("someone.else@shop.com")
	for _, token := range []string{"someone.else@shop.com", "jane.doe@shop.co", "jane.doe", m, s, "", "   ",
		// "Bo Li" is kept whole (5 bytes); its words are under 3 letters.
		"bo", "li"} {
		if kind, enc, ok := k.Contains([]byte(token)); ok {
			t.Errorf("Contains(%q) = %s %s, want not found", token, kind, enc)
		}
	}
	if _, _, ok := k.Contains([]byte("BO LI")); !ok {
		t.Error("the whole short name is a value of its own")
	}
}

func TestKnownShortValuesIgnored(t *testing.T) {
	k := NewKnown(testKey(4))
	for _, v := range []string{"Ann", "a@b", "  12 ", "", "+1"} {
		k.Add(core.KFirstName, []byte(v))
	}
	k.Seal()
	if k.Len() != 0 {
		t.Errorf("Len = %d after only short values", k.Len())
	}
	if _, _, ok := k.Contains([]byte("ann")); ok {
		t.Error("a 3-byte value was kept")
	}
}

func TestKnownNames(t *testing.T) {
	nfd := norm.NFD.String("María José López")
	k := sealedKnown(t, map[core.FakeKind][]string{
		core.KFullName:  {nfd, "Mary-Jane O'Neill"},
		core.KFirstName: {"Józef"},
	})
	for token, want := range map[string]core.FakeKind{
		"maría": core.KFullName, "MARÍA": core.KFullName, norm.NFC.String("José"): core.KFullName,
		"López": core.KFullName, "maría josé": core.KFullName, "josé lópez": core.KFullName,
		"María José López": core.KFullName, "mary": core.KFullName, "jane": core.KFullName,
		"neill": core.KFullName, "mary jane": core.KFullName, "o neill": core.KFullName, "józef": core.KFirstName,
	} {
		kind, enc, ok := k.Contains([]byte(token))
		if !ok || kind != want || enc != EncodingRaw {
			t.Errorf("Contains(%q) = %s %s %v, want %s raw", token, kind, enc, ok, want)
		}
	}
	for _, token := range []string{"maría lópez", "o", "lópez maría", "jose"} {
		if _, _, ok := k.Contains([]byte(token)); ok {
			t.Errorf("Contains(%q) found a token never added", token)
		}
	}
}

func TestKnownKeyed(t *testing.T) {
	a := NewKnown(testKey(1))
	a.Add(core.KEmail, []byte("jane.doe@shop.com"))
	a.Seal()
	b := NewKnown(testKey(2))
	b.Add(core.KEmail, []byte("someone@shop.com"))
	b.Seal()
	ka, kb := a.(*known), b.(*known)
	if ka.tabs[0].h[0] == ka.hash([]byte("jane.doe@shop.com")) && kb.hash([]byte("jane.doe@shop.com")) == ka.tabs[0].h[0] {
		t.Error("two keys hash a token alike")
	}
}

func TestKnownLifecycle(t *testing.T) {
	k := NewKnown(testKey(4))
	k.Add(core.KEmail, []byte("jane.doe@shop.com"))
	if k.Len() != 1 {
		t.Errorf("Len before Seal = %d", k.Len())
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Contains before Seal didn't panic in a test binary")
			}
		}()
		k.Contains([]byte("jane.doe@shop.com"))
	}()
	k.Seal()
	k.Seal() // twice is fine
	k.Add(core.KEmail, []byte("late@shop.com"))
	if _, _, ok := k.Contains([]byte("late@shop.com")); ok {
		t.Error("Add after Seal took effect")
	}
	k.Add(core.KEmail, []byte("Jane.Doe@shop.com"))
	if k.Len() != 1 {
		t.Errorf("Len = %d, want 1 distinct value", k.Len())
	}
}

func TestKnownConcurrent(t *testing.T) {
	k := NewKnown(testKey(4))
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 500 {
				k.Add(core.KEmail, fmt.Appendf(nil, "w%d.p%d@shop.com", w, i))
			}
		})
	}
	wg.Wait()
	k.Seal()
	for w := range 8 {
		wg.Go(func() {
			for i := range 500 {
				if _, _, ok := k.Contains(fmt.Appendf(nil, "w%d.p%d@shop.com", w, i)); !ok {
					t.Errorf("w%d.p%d missing", w, i)
					return
				}
			}
		})
	}
	wg.Wait()
	if k.Len() != 4000 {
		t.Errorf("Len = %d", k.Len())
	}
}

// bigKnown is a sealed set of 2 million emails (a tenth under the race
// detector), built once for the tests that need the full size.
var bigKnown = sync.OnceValue(func() core.Known {
	k := NewKnown(testKey(6))
	for i := range scaled(2_000_000) {
		k.Add(core.KEmail, fmt.Appendf(nil, "customer%d@shop%d.com", i, i%101))
	}
	k.Seal()
	return k
})

// At 2 million values the tables hold 6 million 8-byte hashes, so a
// random token matches with probability about 6e6 / 2^64, far under
// 1e-9; 10 million random lookups find nothing. A sample of real tokens
// goes through Contains as well. Under the race detector the set is a
// tenth of the size.
func TestKnownFalsePositives(t *testing.T) {
	values, lookups := scaled(2_000_000), scaled(10_000_000)
	k := bigKnown()
	kn := k.(*known)
	r := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- test data
	for range lookups {
		h := r.Uint64()
		for i := range kn.tabs {
			if _, ok := kn.tabs[i].find(h); ok {
				t.Fatalf("random hash %x found in table %d", h, i)
			}
		}
	}
	var b [8]byte
	for i := range scaled(100_000) {
		binary.BigEndian.PutUint64(b[:], r.Uint64())
		token := fmt.Appendf(nil, "other%d-%x@shop.com", i, b)
		if _, _, ok := k.Contains(token); ok {
			t.Fatalf("token %s never added was found", token)
		}
	}
	if k.Len() != values {
		t.Errorf("Len = %d, want %d", k.Len(), values)
	}
}

// 2 million emails fit in under 64 MB once sealed. The tables' and the
// Bloom filters' own size is the measure: the heap of a test binary also
// holds what earlier tests left behind.
func TestKnownMemory(t *testing.T) {
	values := scaled(2_000_000)
	kn := bigKnown().(*known)
	for _, tab := range kn.tabs {
		if tab.hchunks != nil || tab.kchunks != nil {
			t.Error("Seal kept the chunks")
		}
	}
	at2M := float64(knownSize(kn)) / float64(values) * 2_000_000
	t.Logf("%d emails: %.1f MB at 2M", values, at2M/1e6)
	if at2M >= 64<<20 {
		t.Errorf("2M emails would take %.1f MB, want under 64 MB", at2M/(1<<20))
	}
}

// TestKnownMemoryNames measures full names, which hold more tokens than
// emails: each word and each pair of a longer name as well as the whole
// value and its hashes. Words repeat across customers, so Seal's dedup
// shrinks the tables; the peak is just before Seal, when every token is
// still held. The bounds here are the ones changes/values.md states.
func TestKnownMemoryNames(t *testing.T) {
	values := scaled(2_000_000)
	k := NewKnown(testKey(7))
	// At 2M: 2,000 first names and 100,003 last names, every pair
	// distinct. The race detector's smaller set keeps the same ratios.
	firsts, lasts := values/1000, values/20+3
	for i := range values {
		first, last := letters(i%firsts), letters(i%lasts)
		k.Add(core.KFullName, []byte(first+" "+last))
	}
	kn := k.(*known)
	// The last chunk's unused room doesn't grow with the set, so it is
	// left out before scaling up the race detector's smaller set.
	spare := 0
	for _, tab := range kn.tabs {
		if n := len(tab.hchunks); n > 0 {
			spare += (chunkLen - len(tab.hchunks[n-1])) * 9
		}
	}
	peak := float64(knownSize(kn)-spare) / float64(values) * 2_000_000
	k.Seal()
	sealed := float64(knownSize(kn)) / float64(values) * 2_000_000
	t.Logf("%d full names at 2M: %.1f MB before Seal, %.1f MB after", values, peak/1e6, sealed/1e6)
	if peak >= 96<<20 || sealed >= 64<<20 {
		t.Errorf("2M full names would take %.1f MB before Seal and %.1f MB after, want under 96 MB and 64 MB",
			peak/(1<<20), sealed/(1<<20))
	}
	if _, _, ok := k.Contains([]byte(letters(firsts-1) + " " + letters(firsts-1))); !ok {
		t.Error("a full name added was not found")
	}
}

// letters writes i in base 26 with a capital first letter, a made-up
// name word for i.
func letters(i int) string {
	b := []byte{byte('A' + i%26)}
	for i /= 26; i > 0; i /= 26 {
		b = append(b, byte('a'+i%26))
	}
	if len(b) < 3 {
		b = append(b, "xx"[:3-len(b)]...)
	}
	return string(b)
}

// knownSize is the memory a set's tables and filters hold, counting the
// chunks a set holds before Seal.
func knownSize(kn *known) int {
	size := 0
	for _, f := range kn.filters {
		size += cap(f.bits) * 8
	}
	for _, tab := range kn.tabs {
		size += cap(tab.h)*8 + cap(tab.k)
		for i := range tab.hchunks {
			size += cap(tab.hchunks[i])*8 + cap(tab.kchunks[i])
		}
	}
	return size
}

// The Bloom filter in front of the raw table never turns away a token
// that was added (across several filter generations) and turns away
// most tokens that weren't.
func TestKnownFilter(t *testing.T) {
	k := NewKnown(testKey(9))
	const n = 5 * firstFilter // fills three filters
	for i := range n {
		k.Add(core.KEmail, fmt.Appendf(nil, "member%d@club.example", i))
	}
	k.Add(core.KFullName, []byte("Ada Kowalczyk"))
	k.Seal()
	kn := k.(*known)
	if len(kn.filters) < 3 {
		t.Fatalf("%d filters after %d tokens, want at least 3", len(kn.filters), n)
	}
	for i := range n {
		tok := fmt.Appendf(nil, "MEMBER%d@club.example", i)
		if _, _, ok := k.Contains(tok); !ok {
			t.Fatalf("added %s not found", tok)
		}
	}
	for _, tok := range []string{"ada", "kowalczyk", "Ada Kowalczyk"} {
		if _, _, ok := k.Contains([]byte(tok)); !ok {
			t.Fatalf("name token %q not found", tok)
		}
	}
	passed := 0
	const misses = 100_000
	for i := range misses {
		if kn.mayHave(maphash.Bytes(kn.seed, fmt.Appendf(nil, "word%d", i))) {
			passed++
		}
	}
	// Each full filter lets about 1% through; three or four of them stay
	// well under 10%.
	if passed > misses/10 {
		t.Fatalf("filter let %d of %d unknown tokens through", passed, misses)
	}
}
