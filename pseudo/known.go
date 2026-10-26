// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- md5 hex is an encoding a copy may hold, not a security use
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"hash/maphash"
	"net/netip"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// The encodings Known.Contains reports.
const (
	EncodingRaw    = "raw"
	EncodingMD5    = "md5"
	EncodingSHA256 = "sha256"
)

// encodings names each table, in table order.
var encodings = [...]string{EncodingRaw, EncodingMD5, EncodingSHA256}

// minKnownLen is the shortest value Known keeps: shorter ones match too
// much innocent text to be worth reporting.
const minKnownLen = 4

// minWordLetters is the shortest name word Known keeps.
const minWordLetters = 3

// chunkLen is how many hashes one storage chunk holds before Seal: the
// tables grow a chunk at a time, so memory never doubles on a resize.
const chunkLen = 1 << 16

// wholeValue marks, in a kind byte, a token that is a whole value and
// not a word of one; Len counts those.
const wholeValue = 0x80

// known is the core.Known NewKnown returns. Add is safe for concurrent
// use; Contains is safe for concurrent use after Seal.
type known struct {
	key   []byte    // HMAC(key, "known"): the tokens' own key
	macs  sync.Pool // of *scratch
	mu    sync.Mutex
	adds  int
	tabs  [3]table        // raw, md5, sha256
	seal  bool            // under mu, for Add and Seal
	ready atomic.Bool     // set after Seal, read by Contains without the lock
	kinds []core.FakeKind // kind byte - 1 to kind
	// seed and filters make a Bloom filter over the raw tokens, so a
	// lookup of a token never added mostly skips the HMAC. The seed is
	// random per set, which keeps the filter hash as unlinkable as the
	// tokens' own keyed hashes. filters grows under mu until Seal.
	seed    maphash.Seed
	filters []*filter
}

// table is one encoding's tokens: 8-byte keyed hashes and kind bytes,
// appended in chunks, then flattened and sorted by Seal.
type table struct {
	hchunks [][]uint64
	kchunks [][]byte
	h       []uint64
	k       []byte
}

func newKnown(k Key) core.Known {
	m := hmac.New(sha256.New, k[:])
	m.Write([]byte("known"))
	kn := &known{key: m.Sum(nil), kinds: core.FakeKinds(), seed: maphash.MakeSeed()}
	kn.macs.New = func() any {
		return &scratch{m: hmac.New(sha256.New, kn.key), sum: make([]byte, 0, sha256.Size)}
	}
	return kn
}

// kindByte returns the stored byte for kind: its index in core.FakeKinds
// plus one.
func (kn *known) kindByte(kind core.FakeKind) byte {
	for i, k := range kn.kinds {
		if k == kind {
			return byte(i + 1) // #nosec G115 -- fewer than 127 kinds
		}
	}
	return 0
}

// scratch is one goroutine's working memory for hashing and looking up
// tokens. It is pooled so Contains, which the scrubber calls for every
// word of a text, allocates nothing for ASCII tokens.
type scratch struct {
	m     hash.Hash
	sum   []byte      // room for one HMAC-SHA256
	buf   []byte      // the candidates' bytes, back to back
	cands []candidate // offsets into buf
}

// hash is the keyed 8-byte prefix of a token.
func (kn *known) hash(token []byte) uint64 {
	sc := kn.macs.Get().(*scratch)
	h := sc.hash(token)
	kn.macs.Put(sc)
	return h
}

func (sc *scratch) hash(token []byte) uint64 {
	sc.m.Reset()
	sc.m.Write(token)
	sc.sum = sc.m.Sum(sc.sum[:0])
	return binary.BigEndian.Uint64(sc.sum[:8])
}

func (t *table) add(h uint64, k byte) {
	n := len(t.hchunks)
	if n == 0 || len(t.hchunks[n-1]) == chunkLen {
		t.hchunks = append(t.hchunks, make([]uint64, 0, chunkLen))
		t.kchunks = append(t.kchunks, make([]byte, 0, chunkLen))
		n++
	}
	t.hchunks[n-1] = append(t.hchunks[n-1], h)
	t.kchunks[n-1] = append(t.kchunks[n-1], k)
}

// Add stores the keyed hashes of the normalised value, its md5 hex and
// its sha256 hex; for names also of each word of 3 or more letters and
// each pair of adjacent words. Values under 4 bytes are ignored. Adding
// after Seal does nothing.
func (kn *known) Add(kind core.FakeKind, original []byte) {
	n := normalise(kind, original)
	if len(n) < minKnownLen {
		return
	}
	kb := kn.kindByte(kind)
	type tok struct {
		tab int
		h   uint64
		f   uint64 // the filter hash, for raw tokens
		k   byte
	}
	raw := func(b []byte, k byte) tok { return tok{0, kn.hash(b), maphash.Bytes(kn.seed, b), k} }
	m, s := md5.Sum(n), sha256.Sum256(n) // #nosec G401 -- an encoding to look for
	toks := []tok{
		raw(n, kb|wholeValue),
		{1, kn.hash(hex.AppendEncode(nil, m[:])), 0, kb | wholeValue},
		{2, kn.hash(hex.AppendEncode(nil, s[:])), 0, kb | wholeValue},
	}
	if isNameKind(kind) {
		words := nameWords(n)
		for i, w := range words {
			word := n[w[0]:w[1]]
			if utf8.RuneCount(word) >= minWordLetters {
				toks = append(toks, raw(word, kb))
			}
			if i+1 < len(words) {
				next := n[words[i+1][0]:words[i+1][1]]
				toks = append(toks, raw(pairToken(word, next), kb))
			}
		}
	}
	kn.mu.Lock()
	defer kn.mu.Unlock()
	if kn.seal {
		return
	}
	kn.adds++
	for _, t := range toks {
		kn.tabs[t.tab].add(t.h, t.k)
		if t.tab == 0 {
			kn.filterAdd(t.f)
		}
	}
}

// Filter sizing: each filter holds up to its capacity of raw tokens in
// filterBits bits per token with filterK probes, about 1% false hits
// when full. The first holds firstFilter tokens; each next one twice the
// one before, so the set grows without knowing its size up front.
const (
	filterBits  = 12
	filterK     = 3
	firstFilter = 1 << 12
)

// filter is one Bloom filter over raw tokens.
type filter struct {
	bits []uint64 // a power of two of words
	n    int
	max  int
}

// probes calls fn with the filterK bit numbers of h.
func (f *filter) probes(h uint64, fn func(word int, bit uint64) bool) bool {
	mask := uint64(len(f.bits))*64 - 1
	h2 := h>>32 | 1
	for i := range uint64(filterK) {
		b := (h + i*h2) & mask
		if !fn(int(b>>6), 1<<(b&63)) { // #nosec G115 -- b is under len(bits)*64
			return false
		}
	}
	return true
}

func (f *filter) add(h uint64) {
	f.probes(h, func(w int, bit uint64) bool { f.bits[w] |= bit; return true })
	f.n++
}

func (f *filter) has(h uint64) bool {
	return f.probes(h, func(w int, bit uint64) bool { return f.bits[w]&bit != 0 })
}

// filterAdd puts a raw token's filter hash in the newest filter, opening
// a filter twice as big when it is full. The caller holds mu.
func (kn *known) filterAdd(h uint64) {
	n := len(kn.filters)
	if n == 0 || kn.filters[n-1].n == kn.filters[n-1].max {
		capacity := firstFilter
		if n > 0 {
			capacity = 2 * kn.filters[n-1].max
		}
		words := 1
		for words*64 < capacity*filterBits {
			words *= 2
		}
		kn.filters = append(kn.filters, &filter{bits: make([]uint64, words), max: capacity})
		n++
	}
	kn.filters[n-1].add(h)
}

// mayHave reports whether a raw token with filter hash h may have been
// added. False is certain; true needs the HMAC lookup to confirm.
func (kn *known) mayHave(h uint64) bool {
	for _, f := range kn.filters {
		if f.has(h) {
			return true
		}
	}
	return false
}

// isNameKind reports whether kind is a person's name.
func isNameKind(kind core.FakeKind) bool {
	return kind == core.KFirstName || kind == core.KLastName || kind == core.KFullName
}

// pairToken joins two words with one space, the form pairs are stored
// and looked up in.
func pairToken(a, b []byte) []byte {
	out := make([]byte, 0, len(a)+1+len(b))
	return append(append(append(out, a...), ' '), b...)
}

// isWordRune reports whether r belongs to a name word: a letter or a
// combining mark (so a decomposed é stays one word).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r)
}

// nameWords returns the start and end of every run of word runes in b.
func nameWords(b []byte) [][2]int {
	var out [][2]int
	start := -1
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if isWordRune(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			out = append(out, [2]int{start, i})
			start = -1
		}
		i += size
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(b)})
	}
	return out
}

// Seal flattens, sorts and dedups each table. Contains works after it.
func (kn *known) Seal() {
	kn.mu.Lock()
	defer kn.mu.Unlock()
	if kn.seal {
		return
	}
	for i := range kn.tabs {
		kn.tabs[i].seal()
	}
	kn.seal = true
	kn.ready.Store(true)
}

func (t *table) seal() {
	n := 0
	for _, c := range t.hchunks {
		n += len(c)
	}
	t.h, t.k = make([]uint64, 0, n), make([]byte, 0, n)
	for i := range t.hchunks {
		t.h = append(t.h, t.hchunks[i]...)
		t.k = append(t.k, t.kchunks[i]...)
		t.hchunks[i], t.kchunks[i] = nil, nil // free each chunk as soon as it is copied
	}
	t.hchunks, t.kchunks = nil, nil
	sort.Sort(byHash{t})
	// Dedup: one entry per hash, keeping the first kind and the whole
	// value mark of any duplicate.
	w := 0
	for r := range t.h {
		if w > 0 && t.h[w-1] == t.h[r] {
			t.k[w-1] |= t.k[r] & wholeValue
			continue
		}
		t.h[w], t.k[w] = t.h[r], t.k[r]
		w++
	}
	t.h, t.k = t.h[:w:w], t.k[:w:w]
}

// byHash sorts a table's two slices together.
type byHash struct{ t *table }

func (b byHash) Len() int { return len(b.t.h) }
func (b byHash) Less(i, j int) bool {
	return b.t.h[i] < b.t.h[j] || b.t.h[i] == b.t.h[j] && b.t.k[i] < b.t.k[j]
}
func (b byHash) Swap(i, j int) {
	b.t.h[i], b.t.h[j] = b.t.h[j], b.t.h[i]
	b.t.k[i], b.t.k[j] = b.t.k[j], b.t.k[i]
}

// find returns the kind byte stored for h.
func (t *table) find(h uint64) (byte, bool) {
	if i, ok := slices.BinarySearch(t.h, h); ok {
		return t.k[i], true
	}
	return 0, false
}

// Contains reports whether token is a known value, or a word or word
// pair of a known name. It also says in which encoding. The token is
// normalised as Add normalises: case-folded text (which covers emails
// and names), phone digits and canonical IPs are tried; a 32 or 64
// character hex token is also looked up as md5 or sha256 hex in any case.
//
// Before Seal it answers false; in a test binary it panics instead, so a
// caller that forgot Seal fails loudly.
func (kn *known) Contains(token []byte) (core.FakeKind, string, bool) {
	if !kn.ready.Load() {
		if testing.Testing() {
			panic("pseudo: Known.Contains before Seal")
		}
		return "", "", false
	}
	sc := kn.macs.Get().(*scratch)
	defer kn.macs.Put(sc)
	sc.candidates(token)
	for _, c := range sc.cands {
		tok := sc.buf[c.start:c.end]
		// Most words of a text were never added: the filter turns them
		// away for the price of a fast hash instead of an HMAC.
		if c.tab == 0 && !kn.mayHave(maphash.Bytes(kn.seed, tok)) {
			continue
		}
		if k, ok := kn.tabs[c.tab].find(sc.hash(tok)); ok {
			return kn.kindOf(k), encodings[c.tab], true
		}
	}
	return "", "", false
}

// candidate is one form a token may have been added in: a table and the
// form's place in scratch.buf.
type candidate struct {
	tab, start, end int
}

// candidates fills sc.cands with the normalised forms token may have
// been added in.
func (sc *scratch) candidates(token []byte) {
	sc.buf, sc.cands = sc.buf[:0], sc.cands[:0]
	trimmed := trimSpace(token)
	sc.buf = appendNormText(sc.buf, trimmed)
	text := len(sc.buf)
	sc.cands = append(sc.cands, candidate{0, 0, text})
	// Emails are kept trimmed and lower-cased only; that differs from
	// the text form when the token has runs of space or non-ASCII.
	sc.buf = appendLower(sc.buf, bytes.TrimSpace(token))
	if !bytes.Equal(sc.buf[text:], sc.buf[:text]) {
		sc.cands = append(sc.cands, candidate{0, text, len(sc.buf)})
	} else {
		sc.buf = sc.buf[:text]
	}
	if phoneLike(token) {
		start := len(sc.buf)
		sc.buf = appendPhone(sc.buf, trimmed)
		sc.cands = append(sc.cands, candidate{0, start, len(sc.buf)})
	}
	if ipLike(trimmed) {
		if ip, err := netip.ParseAddr(string(trimmed)); err == nil {
			start := len(sc.buf)
			sc.buf = ip.Unmap().WithZone("").AppendTo(sc.buf)
			sc.cands = append(sc.cands, candidate{0, start, len(sc.buf)})
		}
	}
	switch hexLike(sc.buf[:text]) {
	case 2 * md5.Size:
		sc.cands = append(sc.cands, candidate{1, 0, text})
	case 2 * sha256.Size:
		sc.cands = append(sc.cands, candidate{2, 0, text})
	}
}

// appendLower appends b lower-cased as bytes.ToLower does.
func appendLower(out, b []byte) []byte {
	if isASCII(b) {
		for _, c := range b {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			out = append(out, c)
		}
		return out
	}
	return append(out, bytes.ToLower(b)...)
}

// ipLike reports whether b has only the bytes an IP address is written
// with (hex digits, dots and colons, then any zone after a '%') and a
// dot or colon, so ParseAddr runs on few tokens.
func ipLike(b []byte) bool {
	sep := false
	for i, c := range b {
		switch {
		case c == '%':
			return sep && i > 0
		case c == '.' || c == ':':
			sep = true
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return sep
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] >= '\t' && b[0] <= '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] >= '\t' && b[len(b)-1] <= '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// phoneLike reports whether b holds 7 or more digits and only the
// characters a written phone number uses.
func phoneLike(b []byte) bool {
	digits := 0
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '+' || c == ' ' || c == '-' || c == '(' || c == ')' || c == '.' || c == '/':
		default:
			return false
		}
	}
	return digits >= 7
}

// hexLike returns len(b) when b is all lower-case hex, else 0.
func hexLike(b []byte) int {
	if isHex(string(b), 1, len(b)) {
		return len(b)
	}
	return 0
}

// kindOf turns a stored kind byte back into its kind.
func (kn *known) kindOf(k byte) core.FakeKind {
	i := int(k&^wholeValue) - 1
	if i < 0 || i >= len(kn.kinds) {
		return ""
	}
	return kn.kinds[i]
}

// Len returns the number of distinct values once sealed. Before Seal it
// returns the number of Add calls kept so far.
func (kn *known) Len() int {
	kn.mu.Lock()
	defer kn.mu.Unlock()
	if !kn.seal {
		return kn.adds
	}
	n := 0
	for _, k := range kn.tabs[0].k {
		if k&wholeValue != 0 {
			n++
		}
	}
	return n
}
