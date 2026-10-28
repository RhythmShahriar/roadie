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

// chunkShift sets chunkLen, how many entries one storage chunk holds:
// the tables grow a chunk at a time, so memory never doubles on a
// resize. They are sorted where they are, so Seal copies nothing.
const (
	chunkShift = 16
	chunkLen   = 1 << chunkShift
)

// compactLen is how many entries a table takes in before Add sorts them
// and drops the ones it already holds. A name word repeated by many
// customers then costs room once, not once per customer. The most a set
// holds beyond its sealed size is one such batch per table.
const compactLen = 8 * chunkLen

// kindMask is the low byte of an entry, which holds the kind byte; the
// other 56 bits are the token's keyed hash. One 8-byte word per token
// keeps 2 million values under 64 MB; at 6 million entries a random
// token matches one with probability about 6e6 / 2^56, under 1e-10.
const kindMask = 0xff

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
	tabs  [3]table    // raw, md5, sha256
	seal  bool        // under mu, for Add and Seal
	ready atomic.Bool // set after Seal, read by Contains without the lock
	// size is Len once sealed: the transform asks for it on every leaf,
	// so it is counted once at Seal and read without the lock.
	size  atomic.Int64
	kinds []core.FakeKind // kind byte - 1 to kind
	// seed and filters make a Bloom filter over the raw tokens, so a
	// lookup of a token never added mostly skips the HMAC. The seed is
	// random per set, which keeps the filter hash as unlinkable as the
	// tokens' own keyed hashes. filters grows under mu until Seal.
	seed    maphash.Seed
	filters []*filter
}

// table is one encoding's tokens: entries of a 56-bit keyed hash and a
// kind byte, appended in chunks. Before Seal the entries before tail are
// sorted runs with no hash in two places; Seal sorts them into one.
type table struct {
	chunks [][]uint64 // each chunkLen long; entries past n are unused
	n      int        // entries held
	runs   []int      // the start of each sorted run before tail
	tail   int        // the first entry not yet compacted
	firsts []uint64   // after Seal: each chunk's first entry
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
	if t.n == len(t.chunks)*chunkLen {
		t.chunks = append(t.chunks, make([]uint64, chunkLen))
	}
	*t.at(t.n) = h&^kindMask | uint64(k)
	t.n++
	if t.n-t.tail >= compactLen {
		t.compact()
	}
}

// at returns entry i.
func (t *table) at(i int) *uint64 {
	return &t.chunks[i>>chunkShift][i&(chunkLen-1)]
}

// sameHash reports whether two entries hold the same token hash.
func sameHash(a, b uint64) bool { return a>>8 == b>>8 }

// compact sorts the tail and drops every entry whose hash is already in
// the tail or in an earlier run, keeping the whole value mark of what it
// drops. What is left becomes a new run. It works in place.
func (t *table) compact() {
	if t.tail == t.n {
		return
	}
	sort.Sort(span{t, t.tail, t.n})
	w := t.tail
	for r := t.tail; r < t.n; r++ {
		e := *t.at(r)
		if w > t.tail && sameHash(*t.at(w - 1), e) {
			*t.at(w - 1) |= e & wholeValue
			continue
		}
		if i, ok := t.inRuns(e); ok {
			*t.at(i) |= e & wholeValue
			continue
		}
		*t.at(w) = e
		w++
	}
	if w > t.tail {
		t.runs = append(t.runs, t.tail)
	}
	t.n, t.tail = w, w
	// Free the chunks the dropped entries leave empty.
	keep := (w + chunkLen - 1) >> chunkShift
	clear(t.chunks[keep:])
	t.chunks = t.chunks[:keep]
}

// inRuns returns the index of the entry with e's hash in the runs before
// tail.
func (t *table) inRuns(e uint64) (int, bool) {
	for r, lo := range t.runs {
		hi := t.tail
		if r+1 < len(t.runs) {
			hi = t.runs[r+1]
		}
		if i := t.search(lo, hi, e>>8); i < hi && *t.at(i)>>8 == e>>8 {
			return i, true
		}
	}
	return 0, false
}

// search returns the first index in [lo, hi), a sorted range, whose
// hash is at least p, or hi.
func (t *table) search(lo, hi int, p uint64) int {
	for lo < hi {
		m := int(uint(lo+hi) >> 1) // #nosec G115 -- lo+hi is positive
		if *t.at(m)>>8 < p {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// span sorts entries lo to hi of a table in place.
type span struct {
	t      *table
	lo, hi int
}

func (s span) Len() int           { return s.hi - s.lo }
func (s span) Less(i, j int) bool { return *s.t.at(s.lo + i) < *s.t.at(s.lo + j) }
func (s span) Swap(i, j int) {
	a, b := s.t.at(s.lo+i), s.t.at(s.lo+j)
	*a, *b = *b, *a
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
	// A kind whose normal form keeps case (a login, a URL, a postcode) is
	// stored lower-cased: Contains looks a token up in its text and its
	// lower-case forms only, so "JaneDoe99" stored as it is would never
	// be found, not even by itself.
	whole := n
	if !foldedKind(kind) {
		whole = appendLower(nil, n)
	}
	toks := []tok{
		raw(whole, kb|wholeValue),
		{1, kn.hash(hex.AppendEncode(nil, m[:])), 0, kb | wholeValue},
		{2, kn.hash(hex.AppendEncode(nil, s[:])), 0, kb | wholeValue},
	}
	if !foldedKind(kind) {
		// Contains also tries the NFC case-folded text form, which
		// differs from plain lower case for decomposed letters and full
		// folds ("STRASSE" for "Straße"): store that form too.
		if t := appendNormText(nil, n); !bytes.Equal(t, whole) {
			toks = append(toks, raw(t, kb|wholeValue))
		}
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
				// A two-word name's pair is the value itself, already
				// stored: storing it again only costs memory until Seal.
				if pair := pairToken(word, next); !bytes.Equal(pair, n) {
					toks = append(toks, raw(pair, kb))
				}
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
		// A token the filters already let through (a common first
		// name, say) would only use up filter room.
		if t.tab == 0 && !kn.mayHave(t.f) {
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

// Seal sorts and dedups each table in place. Contains works after it.
func (kn *known) Seal() {
	kn.mu.Lock()
	defer kn.mu.Unlock()
	if kn.seal {
		return
	}
	for i := range kn.tabs {
		kn.tabs[i].seal()
	}
	n := 0
	for _, c := range kn.tabs[0].chunks {
		for _, e := range c {
			if e&wholeValue != 0 {
				n++
			}
		}
	}
	kn.size.Store(int64(n))
	kn.seal = true
	kn.ready.Store(true)
}

func (t *table) seal() {
	t.compact()
	if len(t.runs) > 1 {
		// No hash is in two runs, so one sort leaves each hash once.
		sort.Sort(span{t, 0, t.n})
	}
	t.runs = nil
	if last := len(t.chunks) - 1; last >= 0 && t.n < len(t.chunks)*chunkLen {
		// Give back the last chunk's unused room.
		t.chunks[last] = slices.Clone(t.chunks[last][:t.n-last<<chunkShift])
	}
	t.firsts = make([]uint64, len(t.chunks))
	for i, c := range t.chunks {
		t.firsts[i] = c[0]
	}
}

// find returns the kind byte stored for hash h. The table is sealed.
func (t *table) find(h uint64) (byte, bool) {
	p := h >> 8
	// The last chunk whose first hash is at most p, then p within it.
	lo, hi := 0, len(t.firsts)
	for lo < hi {
		m := int(uint(lo+hi) >> 1) // #nosec G115 -- lo+hi is positive
		if t.firsts[m]>>8 <= p {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo == 0 {
		return 0, false
	}
	c := t.chunks[lo-1]
	lo, hi = 0, len(c)
	for lo < hi {
		m := int(uint(lo+hi) >> 1) // #nosec G115 -- lo+hi is positive
		if c[m]>>8 < p {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(c) && c[lo]>>8 == p {
		return byte(c[lo] & kindMask), true
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

// foldedKind reports whether normalise already case-folds kind: emails,
// names and the address and company kinds. Phones and IPs have no case.
func foldedKind(kind core.FakeKind) bool {
	switch kind {
	case core.KEmail, core.KPhone, core.KIP, core.KFirstName, core.KLastName, core.KFullName,
		core.KStreet, core.KAddress, core.KCity, core.KCompany:
		return true
	}
	return false
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
	if kn.ready.Load() {
		return int(kn.size.Load())
	}
	kn.mu.Lock()
	defer kn.mu.Unlock()
	if !kn.seal {
		return kn.adds
	}
	return int(kn.size.Load())
}
