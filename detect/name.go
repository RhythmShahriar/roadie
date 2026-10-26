// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// Names and street addresses can't be told from other words by their
// shape, so these two detectors work only in the columns that hold them
// (heuristic mode) and flag a value that is not one of Roadie's fakes.
// Free-text names are found only in exact mode, by the survivors detector.

// usersTables are the name endings of tables whose "name" column is a
// person's name.
var usersTables = []string{"users", "customers", "customer_lookup", "members", "contacts", "subscribers", "people", "persons"}

// nameKind returns the name kind of the column the value sits in, or "".
func nameKind(sc core.ScanContext) core.FakeKind {
	switch sc.ColumnKind {
	case core.KFirstName, core.KLastName, core.KFullName:
		return sc.ColumnKind
	}
	var kind core.FakeKind
	anyName(sc, func(name string, _ []string) bool {
		switch {
		case name == "first_name" || strings.HasSuffix(name, "_first_name"):
			kind = core.KFirstName
		case name == "last_name" || strings.HasSuffix(name, "_last_name"):
			kind = core.KLastName
		case name == "display_name" || name == "comment_author" || name == "author" || name == "full_name":
			kind = core.KFullName
		case name == "name" && usersTable(sc.Table):
			kind = core.KFullName
		}
		return kind != ""
	})
	return kind
}

// usersTable reports whether table holds people, by its name.
func usersTable(table string) bool {
	t := strings.ToLower(table)
	for _, s := range usersTables {
		if t == s || strings.HasSuffix(t, "_"+s) {
			return true
		}
	}
	return false
}

// addressColumns are the street address columns and meta keys.
var addressColumns = []string{"address_1", "address_2", "street", "address", "billing_address_1", "shipping_address_1"}

// notStreetTokens mark *_address columns that hold no street: ip_address,
// email_address, mac_address and the like.
var notStreetTokens = []string{"ip", "email", "mail", "mac", "url", "web", "wallet", "server", "remote"}

// addressColumn reports whether the value sits in a street address column.
func addressColumn(sc core.ScanContext) bool {
	switch sc.ColumnKind {
	case core.KStreet, core.KAddress:
		return true
	}
	return anyName(sc, func(name string, tokens []string) bool {
		if hasToken(tokens, notStreetTokens...) {
			return false
		}
		for _, c := range addressColumns {
			if name == c || strings.HasSuffix(name, "_"+c) {
				return true
			}
		}
		return false
	})
}

// sampleRow reports whether the row's email is reserved or a Roadie fake,
// which marks sample or test data (WooCommerce's sample customers, a copy
// Roadie already made).
func sampleRow(sc core.ScanContext) bool {
	for _, e := range rowEmails(sc) {
		_, domain, _ := strings.Cut(e, "@")
		if core.ReservedEmailDomain(domain) || pseudo.IsFake(core.KEmail, []byte(e)) {
			return true
		}
	}
	return false
}

// personValue trims v and reports where the value is and whether it could
// be a name or a street: two or more runes, a letter, no @ and no URL.
func personValue(v []byte) (int, int, bool) {
	start := len(v) - len(bytes.TrimLeftFunc(v, unicode.IsSpace))
	end := len(bytes.TrimRightFunc(v, unicode.IsSpace))
	if start >= end {
		return 0, 0, false
	}
	t := v[start:end]
	if utf8.RuneCount(t) < 2 || bytes.IndexByte(t, '@') >= 0 || bytes.Contains(t, []byte("://")) {
		return 0, 0, false
	}
	return start, end, bytes.IndexFunc(t, unicode.IsLetter) >= 0
}

// nameDetector finds real names in name columns (pii.name).
type nameDetector struct{}

// ID returns pii.name.
func (nameDetector) ID() string { return core.FindingPIIName }

// Scan emits one hit for a name column value that is not a Roadie fake.
func (nameDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	kind := nameKind(sc)
	if kind == "" {
		return
	}
	start, end, ok := personValue(v)
	if !ok || sampleRow(sc) || pseudo.IsFake(kind, v[start:end]) {
		return
	}
	emit(core.Hit{ID: core.FindingPIIName, Severity: core.SeverityMedium, Start: start, End: end,
		Masked: core.MaskName(string(v[start:end])), Encoding: "raw"})
}

// addressDetector finds real street addresses in address columns (pii.address).
type addressDetector struct{}

// ID returns pii.address.
func (addressDetector) ID() string { return core.FindingPIIAddress }

// Scan emits one hit for an address column value that is not a Roadie fake.
func (addressDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	if !addressColumn(sc) {
		return
	}
	start, end, ok := personValue(v)
	if !ok || sampleRow(sc) {
		return
	}
	if t := v[start:end]; pseudo.IsFake(core.KStreet, t) || pseudo.IsFake(core.KAddress, t) {
		return
	}
	emit(core.Hit{ID: core.FindingPIIAddress, Severity: core.SeverityMedium, Start: start, End: end,
		Masked: core.MaskName(string(v[start:end])), Encoding: "raw"})
}
