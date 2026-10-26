// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// The email scanner is hand-written so it runs in linear time on any
// input: each at sign looks at most maxLocal bytes back and maxDomain
// bytes forward; a left scan never crosses the end of the previous
// match.

const (
	maxLocal  = 64  // RFC 5321 4.5.3.1.1
	maxDomain = 253 // RFC 1035 2.3.4 less the root dot
)

// emailMatch is one address the scanner found.
type emailMatch struct {
	start, end    int    // the address in the input, at sign form included
	local, domain string // decoded parts
	encoding      string // "raw", "html-entity" or "url": how the at sign was written
	userinfo      bool   // the local part follows "//", as in a URL's user part
	next          rune   // the rune after the address
}

// addr returns the address with a plain at sign.
func (m emailMatch) addr() string { return m.local + "@" + m.domain }

// atSign returns the length of the at sign written at v[i] and its
// encoding: "@", "&#64;", "&#x40;" or "%40", or 0 when there is none.
func atSign(v []byte, i int) (int, string) {
	switch v[i] {
	case '@':
		return 1, "raw"
	case '&':
		r := v[i:]
		if bytes.HasPrefix(r, []byte("&#64;")) {
			return 5, "html-entity"
		}
		if len(r) >= 6 && r[1] == '#' && (r[2] == 'x' || r[2] == 'X') && bytes.HasPrefix(r[3:], []byte("40;")) {
			return 6, "html-entity"
		}
	case '%':
		if bytes.HasPrefix(v[i:], []byte("%40")) {
			return 3, "url"
		}
	}
	return 0, ""
}

// scanEmails calls fn for every well-formed address in v, in order and
// without overlap.
func scanEmails(v []byte, fn func(m emailMatch)) {
	last := 0
	for i := 0; i < len(v); {
		if !atStart[v[i]] {
			i++
			continue
		}
		// Most at signs in hostile or binary-ish text have no local part
		// next to them; skip those before reading the sign's form.
		if i == 0 || asciiNotLocal[v[i-1]] {
			i++
			continue
		}
		n, enc := atSign(v, i)
		if n == 0 || i+n >= len(v) || v[i+n] < utf8.RuneSelf && !isDomainRune(rune(v[i+n])) ||
			enc == "url" && v[i-1] == '%' {
			i = max(i+n, i+1)
			continue
		}
		if m, ok := matchEmail(v, i, n, last, enc); ok {
			fn(m)
			last, i = m.end, m.end
			continue
		}
		i += n
	}
}

// atStart marks the bytes an at sign can start with: @, & and %.
var atStart = [256]bool{'@': true, '&': true, '%': true}

// asciiNotLocal marks the ASCII bytes that can't end a local part.
var asciiNotLocal = func() (t [256]bool) {
	for c := range utf8.RuneSelf {
		t[c] = !isLocalRune(rune(c), false)
	}
	return t
}()

// isLocalRune reports whether r may be part of a local part: ASCII
// letters, digits and . _ % + - (no % when the at sign is itself
// percent-encoded, since a literal % would then be written %25) and any
// letter, mark or digit outside ASCII (RFC 6531).
func isLocalRune(r rune, pct bool) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '.', r == '_', r == '+', r == '-':
		return true
	case r == '%':
		return !pct
	case r < utf8.RuneSelf, r == utf8.RuneError:
		return false
	}
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)
}

// isDomainRune reports whether r may be part of a domain label.
func isDomainRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		return true
	case r < utf8.RuneSelf, r == utf8.RuneError:
		return false
	}
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)
}

// matchEmail reads the address around the at sign of n bytes at v[at];
// the local part never starts before floor.
func matchEmail(v []byte, at, n, floor int, enc string) (emailMatch, bool) {
	pct := enc == "url"
	// Local part, backwards.
	start := at
	limit := max(floor, at-maxLocal)
	for start > limit {
		r, size := utf8.DecodeLastRune(v[limit:start])
		if !isLocalRune(r, pct) {
			break
		}
		start -= size
	}
	if start > floor && isLocalRune(runeBefore(v, start), pct) {
		return emailMatch{}, false // longer than any local part: never report half of one
	}
	for start < at && v[start] == '.' {
		start++
	}
	if start == at || v[at-1] == '.' {
		return emailMatch{}, false
	}
	// Domain, forwards: labels of domain runes joined by single dots.
	i, end := at+n, at+n
	labels, tld := 0, [2]int{}
	for {
		ls := i
		for i < len(v) {
			r, size := utf8.DecodeRune(v[i:])
			if !isDomainRune(r) {
				break
			}
			if i += size; i-(at+n) > maxDomain {
				return emailMatch{}, false
			}
		}
		if ls == i || v[ls] == '-' || v[i-1] == '-' {
			break // an empty or hyphen-edged label ends the domain before it
		}
		labels++
		end, tld = i, [2]int{ls, i}
		if i+1 < len(v) && v[i] == '.' && isDomainRune(runeAt(v, i+1)) {
			i++
			continue
		}
		break
	}
	if labels < 2 || !validTLD(v[tld[0]:tld[1]]) {
		return emailMatch{}, false
	}
	m := emailMatch{
		start:    start,
		end:      end,
		local:    string(v[start:at]),
		domain:   string(v[at+n : end]),
		encoding: enc,
		userinfo: start >= 2 && v[start-1] == '/' && v[start-2] == '/',
		next:     runeAt(v, end),
	}
	return m, true
}

// validTLD reports whether tld is 2 to 24 letters (any script) or an
// ASCII punycode label (xn--).
func validTLD(tld []byte) bool {
	if len(tld) > 4 && bytes.EqualFold(tld[:4], []byte("xn--")) {
		return true
	}
	if len(tld) > 24*4 {
		return false
	}
	n := 0
	for len(tld) > 0 {
		r, size := utf8.DecodeRune(tld)
		if !unicode.IsLetter(r) && !unicode.IsMark(r) {
			return false
		}
		tld = tld[size:]
		n++
	}
	return n >= 2 && n <= 24
}

// fileExtensions are the top-level labels that mean the "address" is an
// asset name such as icon@2x.png.
var fileExtensions = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "svg": true, "webp": true,
	"css": true, "js": true, "map": true, "woff": true, "woff2": true, "ttf": true,
}

// roleLocals are local parts of mailboxes that belong to a system, not a
// person; they are reported at info only.
var roleLocals = map[string]bool{
	"noreply": true, "no-reply": true, "donotreply": true, "do-not-reply": true,
	"mailer-daemon": true, "postmaster": true, "wordpress": true,
}

// emailVerdict says what the detector makes of an address.
type emailVerdict int

const (
	emailSkip     emailVerdict = iota // reserved or not an address of anyone
	emailRole                         // a system mailbox
	emailPersonal                     // a real-looking address
)

// classifyEmail applies the guards of PRD 15.2 that need no allow list,
// in order: reserved domains, wordpress@ at any domain, file extensions,
// git@host: and URL user parts, then role local parts.
func classifyEmail(m emailMatch) emailVerdict {
	local := strings.ToLower(m.local)
	tld := strings.ToLower(m.domain[strings.LastIndexByte(m.domain, '.')+1:])
	switch {
	case core.ReservedEmailDomain(m.domain):
		return emailSkip
	case local == "wordpress":
		return emailSkip
	case fileExtensions[tld]:
		return emailSkip
	case local == "git" && m.next == ':':
		return emailSkip
	case m.userinfo:
		// https://key@o1.ingest.sentry.io/2 and other credentials in URLs:
		// the keys detector looks at those.
		return emailSkip
	case roleLocals[local]:
		return emailRole
	}
	return emailPersonal
}

// emailDetector finds addresses (pii.email) and system mailboxes (pii.email-role).
type emailDetector struct{}

// ID returns pii.email.
func (emailDetector) ID() string { return core.FindingPIIEmail }

// Scan emits one hit per address that is not guarded.
func (emailDetector) Scan(v []byte, _ core.ScanContext, emit func(core.Hit)) {
	scanEmails(v, func(m emailMatch) {
		h := core.Hit{Start: m.start, End: m.end, Masked: core.MaskEmail(m.addr()), Encoding: m.encoding}
		switch classifyEmail(m) {
		case emailSkip:
			return
		case emailRole:
			h.ID, h.Severity = core.FindingPIIEmailRole, core.SeverityInfo
		default:
			h.ID, h.Severity = core.FindingPIIEmail, core.SeverityHigh
		}
		emit(h)
	})
}

// decodeAt turns an address as written (with &#64;, &#x40; or %40) into
// one with a plain at sign, lower-cased, for the allow lists.
func decodeAt(s string) string {
	s = strings.ToLower(s)
	for _, f := range []string{"&#64;", "&#x40;", "%40"} {
		s = strings.Replace(s, f, "@", 1)
	}
	return s
}
