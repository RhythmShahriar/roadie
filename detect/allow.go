// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"path"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// allowed wraps a detector with the allow lists.
type allowed struct {
	d core.Detector
	a Allow
}

// ID returns the wrapped detector's ID.
func (w allowed) ID() string { return w.d.ID() }

// Scan skips the rows of kept users and drops hits on allowed values.
func (w allowed) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	if w.a.keptRow(sc) {
		return
	}
	w.d.Scan(v, sc, func(h core.Hit) {
		if h.Start >= 0 && h.End <= len(v) && h.Start <= h.End && w.a.allows(h.ID, string(v[h.Start:h.End])) {
			return
		}
		emit(h)
	})
}

// empty reports whether a allows nothing.
func (a Allow) empty() bool {
	return len(a.Emails) == 0 && len(a.Phones) == 0 && len(a.KeptEmails) == 0
}

// keptRow reports whether the row belongs to a kept user, whose values
// are real on purpose in every column.
func (a Allow) keptRow(sc core.ScanContext) bool {
	if len(a.KeptEmails) == 0 {
		return false
	}
	for _, e := range rowEmails(sc) {
		if a.kept(e) {
			return true
		}
	}
	return false
}

// kept reports whether email is a kept user's.
func (a Allow) kept(email string) bool {
	return slices.ContainsFunc(a.KeptEmails, func(k string) bool { return strings.EqualFold(strings.TrimSpace(k), email) })
}

// allows reports whether the text of a hit with this ID is allow-listed.
func (a Allow) allows(id, text string) bool {
	switch id {
	case core.FindingPIIEmail, core.FindingPIIEmailRole:
		e := decodeAt(text)
		return a.kept(e) || globAny(a.Emails, e)
	case core.FindingPIIPhone:
		if globAny(a.Phones, strings.ToLower(strings.TrimSpace(text))) {
			return true
		}
		d := asciiDigits(text)
		return d != "" && (globAny(a.Phones, d) || globAny(a.Phones, "+"+d))
	}
	return false
}

// asciiDigits returns the digits of s in ASCII, any script read.
func asciiDigits(s string) string {
	var b strings.Builder
	v := []byte(s)
	for i := 0; i < len(v); {
		d, size := digitAt(v, i)
		if d >= 0 {
			b.WriteByte(asciiDigit(d))
		}
		i += size
	}
	return b.String()
}

// globAny reports whether s matches one of the globs, case-insensitively.
// A glob without wildcards must equal s; a malformed one matches nothing.
func globAny(globs []string, s string) bool {
	for _, g := range globs {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == "" {
			continue
		}
		if ok, err := path.Match(g, s); err == nil && ok {
			return true
		}
	}
	return false
}
