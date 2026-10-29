// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// Strings returns every literal string value of a PHP file such as
// Laravel's bootstrap/cache/config.php, which returns nested arrays. Each
// is named by the array keys above it joined with dots (services.stripe.
// secret) and carries its line. Array keys themselves are not values.
func Strings(b []byte) ([]core.Pair, error) {
	toks, err := lex(b)
	if err != nil {
		return nil, err
	}
	p := &parsed{lines: lineStarts(b)}
	var sig []int
	for i, t := range toks {
		if t.kind != tSpace && t.kind != tComment {
			sig = append(sig, i)
		}
	}
	text := func(i int) string { return string(b[toks[i].start:toks[i].end]) }
	isArrow := func(si int) bool {
		return si < len(sig) && toks[sig[si]].kind == tPunct && text(sig[si]) == "=>"
	}
	var stack []string // the key of each open bracket, "" for none
	key := ""          // the key before the current value
	name := func(last string) string {
		var parts []string
		for _, k := range append(append([]string(nil), stack...), last) {
			if k != "" {
				parts = append(parts, k)
			}
		}
		return strings.Join(parts, ".")
	}
	var out []core.Pair
	for si, ti := range sig {
		t := toks[ti]
		switch {
		case (t.kind == tString || t.kind == tNumber) && isArrow(si+1):
			key = t.value
			if t.kind == tNumber {
				key = text(ti)
			}
			continue
		case t.kind == tPunct && text(ti) == "=>":
			continue
		case t.kind == tString || t.kind == tHeredoc:
			if t.literal {
				out = append(out, core.Pair{Name: name(key), Value: t.value, Line: p.lineOf(t.start), Quoted: true})
			}
		case t.kind == tPunct && (text(ti) == "[" || text(ti) == "("):
			stack = append(stack, key)
		case t.kind == tPunct && (text(ti) == "]" || text(ti) == ")"):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case t.kind == tIdent && strings.EqualFold(text(ti), "array"):
			// array( opens with the key before it; the '(' pushes it.
			continue
		}
		key = ""
	}
	return out, nil
}
