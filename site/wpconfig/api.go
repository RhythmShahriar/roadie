// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wpconfig reads and edits wp-config.php without running it: every
// define() is found by a PHP tokenizer and an edit changes only its bytes.
//
// The tokenizer knows PHP open and close tags, comments, single and
// double quoted strings, heredoc and nowdoc plus enough nesting (blocks,
// brace-less and alternative-syntax control structures) to tell a define
// PHP always runs from a conditional one.
package wpconfig

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/RhythmShahriar/roadie/core"
)

// File is one parsed file.
type File struct {
	src []byte
	p   *parsed
}

// Parse reads b. It fails only when a comment, string or heredoc runs to
// the end of the file, since nothing in such a file can be edited safely.
func Parse(b []byte) (*File, error) {
	src := bytes.Clone(b)
	p, err := parse(src)
	if err != nil {
		return nil, err
	}
	return &File{src: src, p: p}, nil
}

// Pairs returns every define with a literal value (a string, true, false
// or a number) in file order, conditional ones included. Defines built
// from expressions are in Unresolved instead.
func (f *File) Pairs() []core.Pair {
	var out []core.Pair
	for _, d := range f.p.defs {
		if d.Literal {
			out = append(out, core.Pair{Name: d.Name, Value: d.Value, Line: d.Line, Quoted: d.Quoted})
		}
	}
	return out
}

// MemberPairs returns the string literals inside defines built from an
// expression, such as define( 'AS3CF_SETTINGS', serialize( array(
// 'secret-access-key' => '...' ) ) ), so a key there is found like one
// in a plain define. Each is named NAME.key (NAME alone for a literal
// with no array key) and carries the literal's own line.
func (f *File) MemberPairs() []core.Pair {
	var out []core.Pair
	for _, d := range f.p.defs {
		for _, m := range d.members {
			out = append(out, core.Pair{Name: memberName(d.Name, m.key), Value: m.value, Line: m.line, Quoted: true})
		}
	}
	return out
}

func memberName(define, key string) string {
	if key == "" {
		return define
	}
	return define + "." + key
}

// SetMember replaces the string literal MemberPairs names name at line
// with value, keeping its quote style; nothing else in the file changes.
func (f *File) SetMember(name string, line int, value string) error {
	for _, d := range f.p.defs {
		for _, m := range d.members {
			if m.line != line || memberName(d.Name, m.key) != name {
				continue
			}
			out := apply(f.src, []edit{{m.start, m.end, Quote(value, m.quote)}})
			np, err := parse(out)
			if err != nil {
				return fmt.Errorf("wp-config.php: setting %s would break the file: %w", name, err)
			}
			f.src, f.p = out, np
			return nil
		}
	}
	return fmt.Errorf("wp-config.php: no string %s at line %d", name, line)
}

// Get returns the define PHP keeps for name (the first unconditional one,
// else the first conditional one), when its value is a literal.
func (f *File) Get(name string) (core.Pair, bool) {
	d := f.p.effective(name)
	if d == nil || !d.Literal {
		return core.Pair{}, false
	}
	return core.Pair{Name: d.Name, Value: d.Value, Line: d.Line, Quoted: d.Quoted}, true
}

// Lookup returns the define PHP keeps for name, literal or not.
func (f *File) Lookup(name string) (Define, bool) {
	d := f.p.effective(name)
	if d == nil {
		return Define{}, false
	}
	return d.Define, true
}

// Defines returns every define() call in file order.
func (f *File) Defines() []Define {
	out := make([]Define, len(f.p.defs))
	for i, d := range f.p.defs {
		out[i] = d.Define
	}
	return out
}

// Set makes name the string value. The first unconditional define before
// wp-settings.php is changed in place, keeping its quote style and
// spacing, along with any conditional define of name before it; with
// none, define( 'NAME', 'value' ); goes on its own line before the
// "That's all, stop editing!" comment, else before the require_once of
// wp-settings.php, else before a closing ?> or at the end, never after
// wp-settings.php. Line endings, a BOM and the trailing newline are kept.
func (f *File) Set(name, value string) error {
	return f.set(name, func(q byte) string { return Quote(value, q) }, value, true)
}

// SetBool is Set for a true or false constant such as DISABLE_WP_CRON.
func (f *File) SetBool(name string, v bool) error {
	code := "false"
	if v {
		code = "true"
	}
	return f.set(name, func(byte) string { return code }, code, false)
}

// Bytes returns the file, byte-identical to the input when nothing changed.
func (f *File) Bytes() []byte {
	return bytes.Clone(f.src)
}

// Unresolved names the defines set from getenv(), $_ENV, $_SERVER or any
// other expression, in file order: the ones PHP keeps for their name
// whose value Roadie can't know without running PHP.
func (f *File) Unresolved() []string {
	var out []string
	for _, d := range f.p.defs {
		if e := f.p.effective(d.Name); e != nil && !e.Literal && !slices.Contains(out, d.Name) {
			out = append(out, d.Name)
		}
	}
	return out
}

// TablePrefix returns $table_prefix when it is set to a literal string,
// else "".
func (f *File) TablePrefix() string {
	return f.p.prefix
}

// DB returns the database settings when every one is a literal.
func (f *File) DB() (name, user, password, host string, ok bool) {
	var vals [4]string
	for i, n := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"} {
		p, found := f.Get(n)
		if !found {
			return "", "", "", "", false
		}
		vals[i] = p.Value
	}
	return vals[0], vals[1], vals[2], vals[3], true
}
