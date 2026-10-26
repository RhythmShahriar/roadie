// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
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

// plainName is a table or column name YAML reads as written.
var plainName = regexp.MustCompile(`^[A-Za-z0-9_$][A-Za-z0-9_$.-]*$`)

// yamlName writes a table or column name for a suggestion line. The
// names come from the source's schema, which a dump can set to anything:
// a newline in one would end the comment and turn the rest of the name
// into an active setting. Anything but a plain name is written as a
// double-quoted YAML string, whose escapes Go's quoting shares.
func yamlName(s string) string {
	if plainName.MatchString(s) {
		return s
	}
	return strconv.Quote(s)
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
		fmt.Fprintf(&b, "#  - table: %s\n#    column: %s\n", yamlName(u.Table), yamlName(u.Column))
		rule := "rule: " + RuleForColumn(u.Column)
		why := fmt.Sprintf("%d%% of sampled values look personal", u.Rate)
		if u.Rate == 0 {
			why = "the name looks personal"
		}
		fmt.Fprintf(&b, "#    %-29s # %s\n", rule, why)
	}
	return b.Bytes()
}
