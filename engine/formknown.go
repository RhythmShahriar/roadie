// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// The field-type row fixes (Gravity Forms, WPForms, Ninja Forms) make
// their rules only after pass 1 has read every form definition; a
// dump can hold the entries first: mysqldump writes gf_entry_meta before
// gf_form_meta. So pass 1 keeps the entry values those rules may cover
// and adds the ones they do cover to Known once the rules are built,
// before Known is sealed. Without this a name typed into a form would
// be faked in its own row but never looked for in free text or by
// verify's exact mode.
const (
	// entryBudget bounds the bytes kept for all entry values of a run;
	// past it pass 1 keeps no more and those values miss Known (the
	// detectors still find their emails and phones).
	entryBudget = 32 << 20
	// entryMaxValue is the longest value kept; a longer one is a
	// textarea or a whole entry, which the scrubber handles.
	entryMaxValue = 64 << 10
	// entryOverhead is what one kept value costs beside its bytes.
	entryOverhead = 96
)

// entryTarget is a table a field-type rule changes: the where columns
// the rules name (in the order the key joins them) plus the value column.
type entryTarget struct {
	suffix string
	where  []string
	column string
	// want picks the rows a field-type rule could cover; nil keeps all.
	want func(get func(string) ([]byte, bool)) bool
}

// entryTargets are the tables of profile.FieldTypes' rules.
var entryTargets = []entryTarget{
	{suffix: "gf_entry_meta", where: []string{"form_id", "meta_key"}, column: "meta_value",
		want: func(get func(string) ([]byte, bool)) bool { k, _ := get("meta_key"); return fieldNumber(k, "") }},
	{suffix: "wpforms_entry_fields", where: []string{"form_id", "field_id"}, column: "value"},
	{suffix: "wpforms_entries", where: []string{"form_id"}, column: "fields"},
	{suffix: "postmeta", where: []string{"meta_key"}, column: "meta_value",
		want: func(get func(string) ([]byte, bool)) bool { k, _ := get("meta_key"); return fieldNumber(k, "_field_") }},
}

// fieldNumber reports whether k is prefix followed by a field number:
// "3", or for Gravity Forms an input of one, "3.6".
func fieldNumber(k []byte, prefix string) bool {
	rest, ok := bytes.CutPrefix(k, []byte(prefix))
	if !ok || len(rest) == 0 {
		return false
	}
	field, sub, dotted := bytes.Cut(rest, []byte("."))
	if prefix != "" && dotted {
		return false
	}
	return digitsOnly(field) && (!dotted || digitsOnly(sub))
}

func digitsOnly(b []byte) bool {
	return len(b) > 0 && len(bytes.Trim(b, "0123456789")) == 0
}

// entry is one kept value with its where values.
type entry struct {
	target *entryTarget
	table  string
	key    string // the where values joined by NUL, in target.where order
	value  []byte
}

// formEntries holds the entry values pass 1 keeps. Pass 1 is one
// goroutine; formEntries isn't safe for concurrent use.
type formEntries struct {
	list  []entry
	bytes int64
	// Skipped counts values not kept once the budget ran out.
	skipped int64
}

// observe keeps one row of table when it is an entry a field-type rule
// of fixes could cover; blog finds a table's blog part under the prefix.
func (e *formEntries) observe(table string, fixes []string, blog func(table, suffix string) bool, get func(string) ([]byte, bool)) {
	if e == nil || len(fixes) == 0 {
		return
	}
	for i := range entryTargets {
		t := &entryTargets[i]
		if !blog(table, t.suffix) || !fixFor(t.suffix, fixes) {
			continue
		}
		if t.want != nil && !t.want(get) {
			return
		}
		v, ok := get(t.column)
		if !ok || len(bytes.TrimSpace(v)) == 0 || len(v) > entryMaxValue {
			return
		}
		parts := make([]string, len(t.where))
		for j, w := range t.where {
			wv, ok := get(w)
			if !ok {
				return
			}
			parts[j] = string(wv)
		}
		key := strings.Join(parts, "\x00")
		cost := int64(len(v)+len(key)) + entryOverhead
		if e.bytes+cost > entryBudget {
			e.skipped++
			return
		}
		e.bytes += cost
		e.list = append(e.list, entry{target: t, table: table, key: key, value: bytes.Clone(v)})
		return
	}
}

// fixFor reports whether one of fixes makes rules on a table ending in
// suffix.
func fixFor(suffix string, fixes []string) bool {
	for _, f := range fixes {
		switch {
		case f == profile.FixGravityForms && suffix == "gf_entry_meta",
			f == profile.FixWPForms && strings.HasPrefix(suffix, "wpforms_entr"),
			f == profile.FixNinjaForms && suffix == "postmeta":
			return true
		}
	}
	return false
}

// addKnown adds every kept value a structured personal rule of rules
// covers to known: the whole value, or the leaves a path rule names.
func (e *formEntries) addKnown(known core.Known, rules []core.Rule) {
	if e == nil || known == nil || len(e.list) == 0 {
		return
	}
	byKey := map[string][]core.Rule{}
	for _, r := range rules {
		if r.Action != core.Fake || !personal(r.Fake) {
			continue
		}
		for i := range entryTargets {
			t := &entryTargets[i]
			if !strings.HasSuffix(r.Table, t.suffix) || !strings.EqualFold(r.Column, t.column) || len(r.Where) != len(t.where) {
				continue
			}
			parts := make([]string, len(t.where))
			ok := true
			for j, w := range t.where {
				v, has := r.Where[w]
				ok = ok && has
				parts[j] = v
			}
			if ok {
				k := r.Table + "\x00" + strings.Join(parts, "\x00")
				byKey[k] = append(byKey[k], r)
			}
		}
	}
	for _, en := range e.list {
		for _, r := range byKey[en.table+"\x00"+en.key] {
			if r.Path == "" {
				known.Add(r.Fake, en.value)
				continue
			}
			_ = nestedLeaves(en.value, func(p string, leaf []byte) {
				if leafMatch(r.Path, p) && len(bytes.TrimSpace(leaf)) > 0 {
					known.Add(r.Fake, leaf)
				}
			}, nested.Options{})
		}
	}
}
