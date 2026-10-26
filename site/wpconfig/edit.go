// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// set writes name's value as the PHP code code(q), where q is the quote
// to use for a string, then checks the result reads back as want.
func (f *File) set(name string, code func(q byte) string, want string, quoted bool) error {
	if !validName(name) {
		return fmt.Errorf("wp-config.php: %q is not a PHP constant name", name)
	}
	p := f.p
	var edits []edit
	var first *def
	for i := range p.defs {
		d := &p.defs[i]
		if d.Name != name || !d.BeforeSettings {
			continue
		}
		if !d.Conditional {
			first = d
			break
		}
	}
	if first != nil && first.valStart >= 0 {
		// Every define of name up to and with the first unconditional
		// one gets the value: a conditional one before it would win
		// when its branch runs, since PHP keeps the first definition.
		for i := range p.defs {
			d := &p.defs[i]
			if d.Name == name && d.valStart >= 0 && d.callStart <= first.callStart {
				edits = append(edits, edit{d.valStart, d.valEnd, code(quoteOf(d))})
			}
		}
	} else {
		e, ok := f.insertion(name, "define( '"+name+"', "+code('\'')+" );")
		if !ok {
			return fmt.Errorf("wp-config.php: found no place before wp-settings.php to add %s; add define( '%s', ... ); there by hand", name, name)
		}
		edits = append(edits, e)
	}
	out := apply(f.src, edits)
	np, err := parse(out)
	if err != nil {
		return fmt.Errorf("wp-config.php: setting %s would break the file: %w", name, err)
	}
	d := np.effective(name)
	if d == nil || d.Conditional || !d.Literal || !d.BeforeSettings || d.Value != want || d.Quoted != quoted {
		return fmt.Errorf("wp-config.php: %s can't be set safely in this file; set it by hand before wp-settings.php", name)
	}
	f.src, f.p = out, np
	return nil
}

// quoteOf keeps a string define's quote style; anything else becomes a
// single-quoted string.
func quoteOf(d *def) byte {
	if d.Quoted && d.quote == '"' {
		return '"'
	}
	return '\''
}

type edit struct {
	start, end int
	text       string
}

// apply makes the edits, which don't overlap, from the last to the first.
func apply(src []byte, edits []edit) []byte {
	slices.SortFunc(edits, func(a, b edit) int { return b.start - a.start })
	out := bytes.Clone(src)
	for _, e := range edits {
		out = slices.Concat(out[:e.start], []byte(e.text), out[e.end:])
	}
	return out
}

// insertion finds where a new define goes (PRD 13.1): before the line of
// the "That's all, stop editing!" comment, else before the require_once
// of wp-settings.php, else before a final ?> or at the end. When a
// conditional define of the same name comes earlier, the new one goes
// before that statement, so it is the first PHP sees.
func (f *File) insertion(name, line string) (edit, bool) {
	p := f.p
	var g *gap
	var at int
	switch {
	case p.stopEditing != nil && (p.settingsAt < 0 || p.stopAt < p.settingsAt):
		g, at = p.stopEditing, p.stopAt
	case p.settings != nil:
		g, at = p.settings, p.settingsAt
	case p.settingsAt >= 0:
		return edit{}, false // a wp-settings.php line with no safe place before it
	case p.end != nil:
		g, at = p.end, p.end.end
	default:
		return edit{}, false
	}
	for i := range p.defs {
		d := &p.defs[i]
		if d.Name == name && d.Conditional && d.stmt != nil && d.stmt.next >= 0 && p.toks[d.stmt.next].start < at {
			g, at = d.stmt, p.toks[d.stmt.next].start
			break
		}
	}
	eol := lineEnding(f.src)
	ls := p.lineStart(at)
	if ls >= g.start && !insideComment(g, ls) {
		if ls == len(f.src) && ls > 0 && !endsWithEOL(f.src) {
			return edit{ls, ls, eol + line}, true
		}
		return edit{ls, ls, line + eol}, true
	}
	if at == len(f.src) {
		return edit{at, at, eol + line}, true
	}
	return edit{at, at, line + eol}, true
}

func insideComment(g *gap, off int) bool {
	for _, c := range g.comments {
		if off > c.start && off < c.end {
			return true
		}
	}
	return false
}

// lineEnding is the file's first line ending: "\r\n", "\r" or "\n".
func lineEnding(src []byte) string {
	i := bytes.IndexAny(src, "\r\n")
	switch {
	case i < 0:
		return "\n"
	case src[i] == '\n':
		return "\n"
	case i+1 < len(src) && src[i+1] == '\n':
		return "\r\n"
	}
	return "\r"
}

func endsWithEOL(src []byte) bool {
	return len(src) > 0 && (src[len(src)-1] == '\n' || src[len(src)-1] == '\r')
}

func validName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// Quote is the one function that writes a PHP string literal: single
// quotes unless q is '"' or the value holds a control character, which
// only a double-quoted escape can carry on one line.
func Quote(v string, q byte) string {
	control := strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f })
	if q != '"' && !control {
		return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case 0x1b:
			b.WriteString(`\e`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
