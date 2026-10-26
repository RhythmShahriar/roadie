// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package detect holds the personal-data detectors verify runs over every
// value of a copy (PRD 15.2), the free-text spanner the scrubber uses and
// the uncovered-column scan.
//
// The exported names in this file are the frozen cross-lane API; the
// detect lane replaces the bodies, which return zero values until then.
package detect

import "github.com/RhythmShahriar/roadie/core"

// All returns every PII detector of PRD 15.2.
func All() []core.Detector { return nil }

// Email finds real email addresses (pii.email, pii.email-role).
func Email() core.Detector { return nil }

// Phone finds phone numbers (pii.phone).
func Phone() core.Detector { return nil }

// IP finds public IP addresses (pii.ip).
func IP() core.Detector { return nil }

// Card finds payment card numbers (pii.card).
func Card() core.Detector { return nil }

// IBAN finds IBANs (pii.iban).
func IBAN() core.Detector { return nil }

// Name finds real names in name columns (pii.name).
func Name() core.Detector { return nil }

// Address finds real street addresses (pii.address).
func Address() core.Detector { return nil }

// TextSpans finds emails, strong phones, cards and IPs in free text, for pseudo.Scrub.
func TextSpans() core.Spanner { return nil }

// Survivors finds production values that survived in the copy, in any
// encoding (exact.survivors).
func Survivors(k core.Known) core.Detector { return nil }

// Allow lists values a detector must not flag.
type Allow struct {
	Emails     []string // allow_emails globs
	Phones     []string // allow_phones globs
	KeptEmails []string // emails of kept users
}

// WithAllow wraps d so it skips allowed values.
func WithAllow(d core.Detector, a Allow) core.Detector { return nil }

// Uncovered applies PRD 11.3 to one table: covered names the columns a rule covers, samples holds up to
// 1,000 non-empty values per text column. Used by engine (pass 1, copy stops) and verify (findings).
func Uncovered(t *core.Table, covered map[string]bool, samples map[string][][]byte) []core.Uncovered {
	return nil
}
