// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// nameRules guesses a rule from a column name, first match wins. The
// order puts the specific names before the general ones: email_address
// is an email, not an address.
var nameRules = []struct {
	re   *regexp.Regexp
	rule string
}{
	{regexp.MustCompile(`e_?mail`), "email"},
	{regexp.MustCompile(`phone|mobile|(^|_)tel($|_)|fax`), "phone"},
	{regexp.MustCompile(`first_?name|given_?name|forename`), "first_name"},
	{regexp.MustCompile(`last_?name|surname|family_?name`), "last_name"},
	{regexp.MustCompile(`(^|_)ip($|_)|ip_?address`), "ip"},
	{regexp.MustCompile(`street`), "street"},
	{regexp.MustCompile(`address`), "address"},
	{regexp.MustCompile(`city|town`), "city"},
	{regexp.MustCompile(`post_?code|(^|_)zip`), "postcode"},
	{regexp.MustCompile(`dob|birth`), "dob"},
	{regexp.MustCompile(`ssn|iban|passport`), "blank"},
	{regexp.MustCompile(`company`), "company"},
	{regexp.MustCompile(`name`), "full_name"},
	{regexp.MustCompile(`(^|_)mail($|_)`), "email"},
}

// RuleForColumn guesses the rule a PII-looking column needs from its
// name; text (scrub) when the name says nothing.
func RuleForColumn(column string) string {
	c := strings.ToLower(column)
	for _, r := range nameRules {
		if r.re.MatchString(c) {
			return r.rule
		}
	}
	return "text"
}

// Suggest renders the commented columns: entries config init --from
// writes for the uncovered columns pass 1 found: each with a guessed rule
// and the share of sampled values that looked personal. Nothing is
// active until the user uncomments it. No columns gives nil.
func Suggest(uncovered []core.Uncovered) []byte {
	if len(uncovered) == 0 {
		return nil
	}
	var b bytes.Buffer
	b.WriteString("# config init --from found these columns that look personal and no profile\n")
	b.WriteString("# covers. Check each rule, then uncomment the entries to keep.\n")
	for _, u := range uncovered {
		fmt.Fprintf(&b, "#  - table: %s\n#    column: %s\n", u.Table, u.Column)
		rule := "rule: " + RuleForColumn(u.Column)
		why := fmt.Sprintf("%d%% of sampled values look personal", u.Rate)
		if u.Rate == 0 {
			why = "the name looks personal"
		}
		fmt.Fprintf(&b, "#    %-29s # %s\n", rule, why)
	}
	return b.Bytes()
}
