// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/RhythmShahriar/roadie/core"
)

// UncoveredSample is the most values per column Uncovered reads (PRD 11.3).
const UncoveredSample = 1000

// UncoveredRate is the share of sampled values, in percent, that makes a
// column personal by its content.
const UncoveredRate = 20

// personalWords are the name parts of PRD 11.3, as single parts or two
// adjacent parts joined with or without "_".
var personalWords = map[string]bool{
	"email": true, "e_mail": true, "mail": true, "phone": true, "tel": true, "mobile": true, "fax": true,
	"first_name": true, "firstname": true, "last_name": true, "lastname": true, "surname": true,
	"full_name": true, "fullname": true, "address": true, "street": true, "city": true, "postcode": true,
	"zip": true, "ip": true, "ip_address": true, "ipaddress": true, "dob": true, "birth": true, "ssn": true,
	"iban": true, "passport": true,
}

// aboutWords end a column name that describes a personal value without
// holding it: its type, state, time, count or the mail settings.
var aboutWords = map[string]bool{
	"type": true, "kind": true, "status": true, "format": true, "verified": true, "at": true,
	"id": true, "ids": true, "count": true, "enabled": true, "sent": true, "opened": true,
	"confirmed": true, "optin": true, "optout": true, "preference": true, "preferences": true,
	"flag": true, "template": true, "subject": true, "provider": true, "mailer": true,
	"driver": true, "host": true, "port": true, "encryption": true,
}

// PersonalName reports whether a column name matches the PRD 11.3 list:
// email, e_mail, mail, phone, tel, mobile, fax, first_name, last_name,
// surname, full_name, address, street, city, postcode, zip, ip,
// ip_address, dob, birth, ssn, iban or passport. The name is split at _,
// -, . and camelCase so "description" holds no ip; a part that contains
// email, phone or birth (billingphone, dateofbirth) counts too. A name
// that ends in a word about the value rather than the person
// (address_type, email_verified_at, phone_status, mail_driver) holds no
// personal data and doesn't count; its content is still sampled.
func PersonalName(column string) bool {
	parts := columnParts(column)
	if len(parts) > 1 && aboutWords[parts[len(parts)-1]] {
		return false
	}
	for i, p := range parts {
		if personalWords[p] || strings.Contains(p, "email") || strings.Contains(p, "phone") || strings.Contains(p, "birth") {
			return true
		}
		if i+1 < len(parts) && (personalWords[p+"_"+parts[i+1]] || personalWords[p+parts[i+1]]) {
			return true
		}
	}
	return false
}

// columnParts splits a column name at punctuation and camelCase into
// lower-case parts.
func columnParts(name string) []string {
	var parts []string
	var cur []rune
	var prev rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
		prev = r
	}
	flush()
	return parts
}

// textColumn reports whether a declared type holds text. An unknown type
// counts as text, so a dump without a schema is still checked.
func textColumn(c core.Column) bool {
	t := strings.ToLower(c.Type)
	if t == "" {
		return true
	}
	for _, w := range []string{"char", "text", "json", "clob", "enum", "set(", "string", "citext", "blob", "binary"} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// sampleCounts returns how many of the samples hold a real-looking email
// and how many a strong-format phone (or a phone in a phone column).
func sampleCounts(column string, samples [][]byte) (n, emails, phones int) {
	phoneCol := PersonalName(column) && (strings.Contains(strings.ToLower(column), "phone") || strings.Contains(strings.ToLower(column), "mobile") ||
		strings.Contains(strings.ToLower(column), "tel") || strings.Contains(strings.ToLower(column), "fax"))
	for _, s := range samples {
		if len(s) == 0 {
			continue
		}
		if n++; n > UncoveredSample {
			n = UncoveredSample
			break
		}
		email := false
		scanEmails(s, func(m emailMatch) {
			if !email && classifyEmail(m) == emailPersonal {
				email = true
			}
		})
		if email {
			emails++
			continue
		}
		phone := false
		scanPhones(s, phoneCol, func(phoneMatch) { phone = true })
		if phone {
			phones++
		}
	}
	return n, emails, phones
}

// uncovered is Uncovered.
func uncovered(t *core.Table, covered map[string]bool, samples map[string][][]byte) []core.Uncovered {
	if t == nil {
		return nil
	}
	var out []core.Uncovered
	for _, c := range t.Columns {
		if covered[c.Name] || covered[strings.ToLower(c.Name)] || c.Generated || !textColumn(c) {
			continue
		}
		n, emails, phones := sampleCounts(c.Name, samples[c.Name])
		rate := 0
		if n > 0 {
			rate = (emails + phones) * 100 / n
		}
		if rate >= UncoveredRate || PersonalName(c.Name) {
			out = append(out, core.Uncovered{Table: t.Name, Column: c.Name, Rate: rate})
		}
	}
	return out
}

// UncoveredTitle is the PRD 11.3 sentence for an uncovered column:
// "wp_loyalty_members.email looks personal (98% of 1,000 sampled values
// are emails) and no rule covers it". A column listed by its name alone
// says "(by its name)" in place of the rate.
func UncoveredTitle(u core.Uncovered, samples [][]byte) string {
	n, emails, phones := sampleCounts(u.Column, samples)
	why := "by its name"
	if n > 0 && (emails+phones)*100/n >= UncoveredRate {
		what := "emails"
		switch {
		case phones > 0 && emails == 0:
			what = "phone numbers"
		case phones > 0:
			what = "emails or phone numbers"
		}
		why = fmt.Sprintf("%d%% of %s sampled values are %s", u.Rate, thousands(n), what)
	}
	return fmt.Sprintf("%s.%s looks personal (%s) and no rule covers it", u.Table, u.Column, why)
}

// thousands writes n with commas: 1000 gives "1,000".
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
