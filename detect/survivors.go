// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// Exact mode knows every value Roadie replaced, so it can look for them
// by value instead of by shape. The survivors detector cuts each leaf
// into the tokens a replaced value could hide in and asks the known set
// about each one: emails, the whole value, words and word pairs (names),
// phone digit runs, md5 and sha256 hex and decoded base64. It reads the
// leaf as written, then with HTML entities decoded, then with percent
// escapes decoded; nested.Leaves has already undone JSON escapes.

const (
	minToken      = 4   // values under 4 bytes are never looked up
	maxWholeValue = 512 // longer values are looked up by their parts only
	maxWord       = 64  // bytes; a longer run of letters is not a name
	minBase64     = 12  // shortest base64 run decoded
	maxBase64     = 2 << 20
	base64Depth   = 2 // base64 inside base64 inside the leaf, no deeper
)

// nameKinds are the kinds whose originals are written capitalised, so
// finding one in lower case says the copy lower-cased it.
var nameKinds = []core.FakeKind{core.KFirstName, core.KLastName, core.KFullName, core.KCity, core.KStreet, core.KCompany}

// survivorsDetector finds known production values (exact.survivors).
type survivorsDetector struct{ k core.Known }

// ID returns exact.survivors.
func (survivorsDetector) ID() string { return core.FindingExactSurvivors }

// Scan emits one hit per surviving value, never two over the same bytes.
func (d survivorsDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	if d.k == nil || len(v) < minToken {
		return
	}
	if wholeFake(bytes.TrimSpace(v)) {
		return
	}
	s := &survivorScan{k: d.k, enum: enumColumn(sc.Column)}
	s.ws = len(v) - len(bytes.TrimLeftFunc(v, unicode.IsSpace))
	s.we = len(bytes.TrimRightFunc(v, unicode.IsSpace))
	for _, singles := range []bool{false, true} {
		s.singles = singles
		s.pass(v, func(a, b int) (int, int) { return a, b }, "raw", 0)
		if bytes.IndexByte(v, '&') >= 0 {
			if t, offs, ok := decodeEntities(v); ok {
				s.pass(t, offsetMapper(offs), "html-entity", 0)
			}
		}
		if bytes.IndexByte(v, '%') >= 0 {
			if t, offs, ok := decodePercent(v); ok {
				s.pass(t, offsetMapper(offs), "url", 0)
			}
		}
	}
	for _, h := range s.hits {
		emit(h)
	}
}

// mapper turns a range of the text a pass reads into a range of the leaf.
type mapper func(start, end int) (int, int)

// offsetMapper maps through offs, where offs[i] is the leaf offset of
// byte i of a decoded text and offs[len] its end.
func offsetMapper(offs []int) mapper {
	return func(a, b int) (int, int) { return offs[a], offs[b] }
}

// survivorScan collects the hits of one leaf.
type survivorScan struct {
	k    core.Known
	hits []core.Hit // sorted by Start, never overlapping
	// ws and we are where the leaf starts and ends without its white
	// space: a city counts only as the whole leaf.
	ws, we int
	// singles is the second round: only single words are looked up. The
	// known set holds a full name and each of its words, so every
	// encoding gets the chance to report "Jane Doe" whole before any
	// encoding reports "Jane" and "Doe" on their own.
	singles bool
	// enum says the column holds a schema's own codes (type, status),
	// where a word that is also a customer's name is the code.
	enum bool
	// fakes are the leaf ranges of Roadie's own fake emails: a word of
	// one (example of u1f2@example.invalid) is the fake's, never a
	// customer's name that survived (PRD 15.2).
	fakes [][2]int
}

// enumColumn reports whether a column holds a schema's codes rather
// than anything a person typed: type, status and their prefixed forms
// (post_type, comment_status).
func enumColumn(col string) bool {
	c := strings.ToLower(col)
	return c == "type" || c == "status" || strings.HasSuffix(c, "_type") || strings.HasSuffix(c, "_status")
}

// commonWords are words a site's own text, slugs and codes use all
// over: a customer named Shop or Order (the shop owner's account,
// "Shop Manager") would make every shop_order and every order page a
// survivor. A full name holding one is still looked up as a pair.
var commonWords = map[string]bool{
	"shop": true, "store": true, "order": true, "orders": true, "product": true, "customer": true, "admin": true,
	"administrator": true, "owner": true, "manager": true, "editor": true, "author": true, "subscriber": true,
	"contributor": true, "guest": true, "test": true, "cart": true, "checkout": true, "account": true,
	"billing": true, "shipping": true, "support": true, "info": true, "sales": true, "team": true, "staff": true,
	"office": true, "system": true, "page": true, "post": true, "user": true, "member": true, "default": true,
}

// covered reports whether a hit already holds part of start..end.
func (s *survivorScan) covered(start, end int) bool {
	i, _ := slices.BinarySearchFunc(s.hits, start, func(h core.Hit, t int) int { return h.Start - t })
	if i > 0 && s.hits[i-1].End > start {
		return true
	}
	return i < len(s.hits) && s.hits[i].Start < end
}

// inFake reports whether start..end lies inside one of Roadie's fake
// emails.
func (s *survivorScan) inFake(start, end int) bool {
	for _, f := range s.fakes {
		if start >= f[0] && end <= f[1] {
			return true
		}
	}
	return false
}

// check looks token up and records a hit over start..end of the text,
// mapped to the leaf by at.
func (s *survivorScan) check(token []byte, start, end int, at mapper, enc string) bool {
	if len(token) < minToken {
		return false
	}
	ls, le := at(start, end)
	if ls >= le || s.covered(ls, le) || s.inFake(ls, le) {
		return false
	}
	kind, how, ok := s.k.Contains(token)
	if !ok || roadieFake(kind, token) {
		return false
	}
	if how == "raw" && slices.Contains(nameKinds, kind) && (s.enum || commonWords[strings.ToLower(string(token))]) {
		return false
	}
	if kind == core.KCity && how == "raw" && (ls != s.ws || le != s.we) {
		// A town named in a note or a page ("Made in Dublin") points at
		// no one; the customer's city column left as it was still does.
		return false
	}
	switch {
	case how != "raw":
		enc = how // md5 and sha256 say more than where the hex sat
	case enc == "raw" && slices.Contains(nameKinds, kind) && hasLetter(token) && bytes.Equal(token, bytes.ToLower(token)):
		enc = "lower"
	}
	masked := core.MaskValue(kind, string(token))
	if how != "raw" {
		// A hash shows nothing: MaskValue of no kind is bullets only.
		masked = core.MaskValue("", "")
	}
	h := core.Hit{ID: core.FindingExactSurvivors, Severity: core.SeverityCritical, Start: ls, End: le, Masked: masked, Encoding: enc}
	i, _ := slices.BinarySearchFunc(s.hits, ls, func(h core.Hit, t int) int { return h.Start - t })
	s.hits = slices.Insert(s.hits, i, h)
	return true
}

// roadieFake reports whether token is a Roadie fake of kind or of any
// kind the faker draws from its lists. The lists hold common names;
// Known can hold the same word as another kind: a real customer living
// in Austin makes Known call Austin a city while the copy holds Austin
// as a fake first name. PRD 15.2 leaves values that are also Roadie
// fakes out, whatever kind they came as.
func roadieFake(kind core.FakeKind, token []byte) bool {
	if pseudo.IsFake(kind, token) {
		return true
	}
	for _, k := range listKinds {
		if k != kind && pseudo.IsFake(k, token) {
			return true
		}
	}
	return false
}

// listKinds are the kinds whose fakes come from the faker's word lists.
var listKinds = []core.FakeKind{core.KFirstName, core.KLastName, core.KFullName, core.KCity, core.KStreet, core.KCompany}

// wholeKinds are the fakes of several words: a leaf that is one of them
// whole is Roadie's own value, so a word in it ("Wren" of a fake "328
// Wren Street") is not a real customer's name that survived.
var wholeKinds = []core.FakeKind{core.KStreet, core.KFullName, core.KCompany, core.KCity}

// wholeFake reports whether the whole leaf is a Roadie fake of one of
// wholeKinds.
func wholeFake(leaf []byte) bool {
	if len(leaf) == 0 || len(leaf) > maxWholeValue || bytes.IndexByte(leaf, ' ') < 0 {
		return false
	}
	for _, k := range wholeKinds {
		if pseudo.IsFake(k, leaf) {
			return true
		}
	}
	return false
}

func hasLetter(b []byte) bool { return bytes.IndexFunc(b, unicode.IsLetter) >= 0 }

// pass tokenises text and looks every token up. Emails go first, so the
// words of an address that survived whole are not reported again.
func (s *survivorScan) pass(t []byte, at mapper, enc string, depth int) {
	if s.singles {
		words(t, func(a, b int) { s.check(t[a:b], a, b, at, enc) }, func(int, int) {})
		s.base64(t, at, depth)
		return
	}
	if whole := bytes.TrimSpace(t); len(whole) <= maxWholeValue {
		start := len(t) - len(bytes.TrimLeftFunc(t, unicode.IsSpace))
		s.check(whole, start, start+len(whole), at, enc)
	}
	scanEmails(t, func(m emailMatch) {
		if addr := []byte(m.addr()); pseudo.IsFake(core.KEmail, addr) {
			ls, le := at(m.start, m.end)
			s.fakes = append(s.fakes, [2]int{ls, le})
			return
		}
		e := enc
		if m.encoding != "raw" {
			e = m.encoding
		}
		s.check([]byte(m.addr()), m.start, m.end, at, e)
	})
	hexRuns(t, func(a, b int) { s.check(t[a:b], a, b, at, enc) })
	phoneRuns(t, func(a, b int, norm []byte) {
		if !s.check(norm, a, b, at, enc) && norm[0] == '+' {
			s.check(norm[1:], a, b, at, enc)
		}
	})
	words(t, func(int, int) {}, func(a, b int) { s.check(t[a:b], a, b, at, enc) })
	s.base64(t, at, depth)
}

// base64 runs pass over every base64 run of t that decodes to text.
func (s *survivorScan) base64(t []byte, at mapper, depth int) {
	if depth < base64Depth {
		base64Runs(t, func(a, b int, decoded []byte) {
			ra, rb := at(a, b)
			s.pass(decoded, func(int, int) (int, int) { return ra, rb }, "base64", depth+1)
		})
	}
}

// hexRuns calls fn for each run of exactly 32 or 64 hex digits, the
// length of an md5 or a sha256 in hex.
func hexRuns(t []byte, fn func(start, end int)) {
	for i := 0; i < len(t); {
		if !isHex(t[i]) {
			i++
			continue
		}
		j := i
		for j < len(t) && isHex(t[j]) {
			j++
		}
		if n := j - i; (n == 32 || n == 64) && !isWordRune(runeBefore(t, i)) && !isWordRune(runeAt(t, j)) {
			fn(i, j)
		}
		i = j
	}
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// phoneRuns calls fn for each run of 7 to 15 digits with phone
// separators, with its digits normalised as the known set keeps phones:
// ASCII digits with a leading + when there was one.
func phoneRuns(t []byte, fn func(start, end int, norm []byte)) {
	if !hasDigit(t) {
		return
	}
	for i := 0; i < len(t); {
		d, size := digitAt(t, i)
		if (d < 0 && t[i] != '+') || isWordRune(runeBefore(t, i)) {
			i += size
			continue
		}
		plus := t[i] == '+'
		j := i
		if plus {
			j++
		}
		end, digits, _ := readNumber(t, j, 15)
		if n := len(digits); n >= 7 && n <= 15 && !isWordRune(runeAt(t, end)) {
			if plus {
				digits = append([]byte{'+'}, digits...)
			}
			fn(i, end, digits)
		}
		i = max(end, i+size)
	}
}

// isNameRune reports whether r is part of a word of a name.
func isNameRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) }

// identRune reports whether r joins letters into an identifier.
func identRune(r rune) bool { return r == '_' || r >= '0' && r <= '9' }

// words calls word for every run of letters (an apostrophe or hyphen
// inside one kept, as in O'Brien and Smith-Jones) and pair for every two
// words joined by one space. A run joined to an underscore or a digit
// is part of an identifier (shop_order, user2), not a word.
func words(t []byte, word, pair func(start, end int)) {
	prevStart, prevEnd := -1, -1
	for i := 0; i < len(t); {
		r, size := utf8.DecodeRune(t[i:])
		if !isNameRune(r) {
			i += size
			continue
		}
		j := i
		for j < len(t) {
			r, size := utf8.DecodeRune(t[j:])
			if isNameRune(r) || (r == '\'' || r == '-') && isNameRune(runeAt(t, j+1)) {
				j += size
				continue
			}
			break
		}
		if identRune(runeBefore(t, i)) || identRune(runeAt(t, j)) {
			prevEnd = -1
		} else if j-i <= maxWord {
			word(i, j)
			if prevEnd >= 0 && i == prevEnd+1 && t[prevEnd] == ' ' {
				pair(prevStart, j)
			}
			prevStart, prevEnd = i, j
		} else {
			prevEnd = -1
		}
		i = j
	}
}

// isBase64 reports whether c is in the standard or URL-safe alphabet.
func isBase64(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '+' || c == '/' || c == '-' || c == '_'
}

// base64Runs calls fn for every run of 12 or more base64 characters that
// decodes to valid UTF-8 text. Ordinary words are in the alphabet too;
// they decode to bytes that are not text and are passed over.
func base64Runs(t []byte, fn func(start, end int, decoded []byte)) {
	for i := 0; i < len(t); {
		if !isBase64(t[i]) {
			i++
			continue
		}
		j := i
		for j < len(t) && isBase64(t[j]) {
			j++
		}
		end := j
		for end < len(t) && t[end] == '=' && end-j < 2 {
			end++
		}
		if n := j - i; n >= minBase64 && n <= maxBase64 {
			if d, ok := decodeBase64(t[i:j]); ok {
				fn(i, end, d)
			}
		}
		i = end
	}
}

// decodeBase64 decodes run in any of the four alphabets and paddings and
// keeps it only when it is printable UTF-8.
func decodeBase64(run []byte) ([]byte, bool) {
	enc := base64.RawStdEncoding
	if bytes.ContainsAny(run, "-_") {
		enc = base64.RawURLEncoding
	}
	if len(run)%4 == 1 {
		return nil, false
	}
	out := make([]byte, enc.DecodedLen(len(run)))
	n, err := enc.Decode(out, run)
	if err != nil {
		return nil, false
	}
	out = out[:n]
	if !utf8.Valid(out) {
		return nil, false
	}
	for _, r := range string(out) {
		if !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return nil, false
		}
	}
	return out, true
}

// decodeEntities decodes numeric HTML entities and the named ones that
// show up around personal data; it returns the text with the leaf
// offset of each byte. It reports false when there was nothing to decode.
func decodeEntities(v []byte) ([]byte, []int, bool) {
	out := make([]byte, 0, len(v))
	offs := make([]int, 0, len(v)+1)
	changed := false
	for i := 0; i < len(v); {
		if v[i] == '&' {
			if r, n := entityAt(v[i:]); n > 0 {
				var buf [utf8.UTFMax]byte
				w := utf8.EncodeRune(buf[:], r)
				for k := range w {
					out = append(out, buf[k])
					offs = append(offs, i)
				}
				i += n
				changed = true
				continue
			}
		}
		out = append(out, v[i])
		offs = append(offs, i)
		i++
	}
	return out, append(offs, len(v)), changed
}

// namedEntities are the named references decodeEntities knows.
var namedEntities = map[string]rune{
	"amp": '&', "lt": '<', "gt": '>', "quot": '"', "apos": '\'', "nbsp": ' ', "commat": '@',
	"period": '.', "plus": '+', "hyphen": '-', "lowbar": '_',
}

// entityAt reads the entity at the start of b and returns its rune and
// length, or 0 when b doesn't start with one.
func entityAt(b []byte) (rune, int) {
	end := bytes.IndexByte(b[:min(len(b), 12)], ';')
	if end < 2 {
		return 0, 0
	}
	name := string(b[1:end])
	if r, ok := namedEntities[name]; ok {
		return r, end + 1
	}
	if name[0] != '#' {
		return 0, 0
	}
	base, digits := 10, name[1:]
	if strings.HasPrefix(digits, "x") || strings.HasPrefix(digits, "X") {
		base, digits = 16, digits[1:]
	}
	n, err := strconv.ParseUint(digits, base, 32)
	if err != nil || n == 0 || n > unicode.MaxRune {
		return 0, 0
	}
	return rune(n), end + 1
}

// decodePercent decodes %HH escapes and returns the text with the leaf
// offset of each byte. It reports false when there was nothing to decode.
func decodePercent(v []byte) ([]byte, []int, bool) {
	out := make([]byte, 0, len(v))
	offs := make([]int, 0, len(v)+1)
	changed := false
	for i := 0; i < len(v); {
		if v[i] == '%' && i+2 < len(v) && isHex(v[i+1]) && isHex(v[i+2]) {
			n, _ := strconv.ParseUint(string(v[i+1:i+3]), 16, 8)
			out = append(out, byte(n))
			offs = append(offs, i)
			i += 3
			changed = true
			continue
		}
		out = append(out, v[i])
		offs = append(offs, i)
		i++
	}
	return out, append(offs, len(v)), changed
}
