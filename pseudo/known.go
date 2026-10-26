// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- md5 hex is an encoding a copy may hold, not a security use
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"net/netip"
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
	key   []byte // HMAC(key, "known"): the tokens' own key
	macs  sync.Pool
	mu    sync.Mutex
	adds  int
	tabs  [3]table        // raw, md5, sha256
	seal  bool            // under mu, for Add and Seal
	ready atomic.Bool     // set after Seal, read by Contains without the lock
	kinds []core.FakeKind // kind byte - 1 to kind
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
	kn := &known{key: m.Sum(nil), kinds: core.FakeKinds()}
	kn.macs.New = func() any { return hmac.New(sha256.New, kn.key) }
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

// hash is the keyed 8-byte prefix of a token.
func (kn *known) hash(token []byte) uint64 {
	m := kn.macs.Get().(hash.Hash)
	m.Reset()
	m.Write(token)
	var buf [sha256.Size]byte
	sum := m.Sum(buf[:0])
	kn.macs.Put(m)
	return binary.BigEndian.Uint64(sum[:8])
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
		k   byte
	}
	m, s := md5.Sum(n), sha256.Sum256(n) // #nosec G401 -- an encoding to look for
	toks := []tok{
		{0, kn.hash(n), kb | wholeValue},
		{1, kn.hash(hex.AppendEncode(nil, m[:])), kb | wholeValue},
		{2, kn.hash(hex.AppendEncode(nil, s[:])), kb | wholeValue},
	}
	if isNameKind(kind) {
		words := nameWords(n)
		for i, w := range words {
			word := n[w[0]:w[1]]
			if utf8.RuneCount(word) >= minWordLetters {
				toks = append(toks, tok{0, kn.hash(word), kb})
			}
			if i+1 < len(words) {
				next := n[words[i+1][0]:words[i+1][1]]
				toks = append(toks, tok{0, kn.hash(pairToken(word, next)), kb})
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
	}
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
	i := sort.Search(len(t.h), func(i int) bool { return t.h[i] >= h })
	if i < len(t.h) && t.h[i] == h {
		return t.k[i], true
	}
	return 0, false
}

// Contains reports whether token is a known value, or a word or word
// pair of a known name, and in which encoding. The token is normalised
// as Add normalises: case-folded text (which covers emails and names),
// phone digits and canonical IPs are tried; a 32 or 64 character hex
// token is also looked up as md5 or sha256 hex in any case.
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
	for _, c := range kn.candidates(token) {
		if k, ok := kn.tabs[c.tab].find(kn.hash(c.tok)); ok {
			return kn.kindOf(k), [...]string{EncodingRaw, EncodingMD5, EncodingSHA256}[c.tab], true
		}
	}
	return "", "", false
}

type candidate struct {
	tab int
	tok []byte
}

// candidates are the normalised forms token may have been added in.
func (kn *known) candidates(token []byte) []candidate {
	text := normText(trimSpace(token))
	out := []candidate{{0, text}}
	if lower := normalise(core.KEmail, token); string(lower) != string(text) {
		out = append(out, candidate{0, lower})
	}
	if phoneLike(token) {
		out = append(out, candidate{0, normPhone(trimSpace(token))})
	}
	if ip, err := netip.ParseAddr(string(trimSpace(token))); err == nil {
		out = append(out, candidate{0, []byte(ip.Unmap().WithZone("").String())})
	}
	switch hexLike(text) {
	case 2 * md5.Size:
		out = append(out, candidate{1, text})
	case 2 * sha256.Size:
		out = append(out, candidate{2, text})
	}
	return out
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

// Len returns the number of distinct values added after Seal, and the
// number of Add calls kept before it.
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
