// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"net/netip"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/RhythmShahriar/roadie/core"
)

// folders pools case folders: a cases.Caser keeps state, so each
// goroutine takes its own.
var folders = sync.Pool{New: func() any { c := cases.Fold(); return &c }}

// normalise is Normalise.
func normalise(kind core.FakeKind, b []byte) []byte {
	b = bytes.TrimSpace(b)
	switch kind {
	case core.KEmail:
		return bytes.ToLower(b)
	case core.KPhone:
		return normPhone(b)
	case core.KFirstName, core.KLastName, core.KFullName, core.KStreet, core.KAddress,
		core.KCity, core.KCompany:
		return normText(b)
	case core.KIP:
		if ip, err := netip.ParseAddr(string(b)); err == nil {
			return []byte(ip.Unmap().WithZone("").String())
		}
	}
	return bytes.Clone(b)
}

// normPhone keeps the digits of b and a leading '+'.
func normPhone(b []byte) []byte {
	out := make([]byte, 0, len(b))
	if len(b) > 0 && b[0] == '+' {
		out = append(out, '+')
	}
	for _, c := range b {
		if c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	return out
}

// normText makes b NFC, case-folds it and makes every run of white space
// one space. b is already trimmed.
func normText(b []byte) []byte {
	if isASCII(b) {
		// NFC and case folding of ASCII is lower case: skip the tables.
		out := make([]byte, 0, len(b))
		space := false
		for _, c := range b {
			switch {
			case c == ' ' || c >= '\t' && c <= '\r':
				space = true
				continue
			case c >= 'A' && c <= 'Z':
				c += 'a' - 'A'
			}
			if space {
				out = append(out, ' ')
				space = false
			}
			out = append(out, c)
		}
		return out
	}
	c := folders.Get().(*cases.Caser)
	folded := c.Bytes(norm.NFC.Bytes(b))
	folders.Put(c)
	// Folding can undo NFC for a few letters; compose once more.
	folded = norm.NFC.Bytes(folded)
	out := make([]byte, 0, len(folded))
	space := false
	for i := 0; i < len(folded); {
		r, size := utf8.DecodeRune(folded[i:])
		if unicode.IsSpace(r) {
			space = true
			i += size
			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, folded[i:i+size]...)
		i += size
	}
	return out
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
