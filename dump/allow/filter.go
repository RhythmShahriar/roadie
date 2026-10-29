// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// Filter applies the allowlist to a dump's batches in input order. It
// keeps what one statement can't know alone: the objects the dump
// creates (a DROP must be matched by a CREATE somewhere in the dump),
// the prepared statements it dropped and the plan lines.
type Filter struct {
	o       Options
	mode    mysql.Mode // the sql_mode before the statement being judged
	created map[object]bool
	drops   []pendingDrop
	// emptied are the DROPs of an object the dump had already created,
	// each waiting for a CREATE that makes it again.
	emptied []pendingDrop
	notes   []*planNote
	byNote  map[string]*planNote
	verb    string // INSERT or REPLACE, for the row statement being read
}

// pendingDrop is a kept DROP waiting for its CREATE.
type pendingDrop struct {
	obj  object
	line int64
}

// planNote is one plan line and how often it came up.
type planNote struct {
	text  string // "GRANT left out of the copy: <reason>" or "kept CREATE TRIGGER x ..."
	kind  string
	why   string
	first int64
	count int
	each  bool // listed once per statement (kept routines)
}

// NewFilter returns a filter for one dump.
func NewFilter(o Options) *Filter {
	if o.Prepared == nil {
		o.Prepared = map[string]bool{}
	}
	if o.Charsets == nil {
		o.Charsets = map[string]bool{}
	}
	return &Filter{o: o, created: map[object]bool{}, byNote: map[string]*planNote{}}
}

// Add judges a batch. Row batches pass unchanged here: Rows judges the
// text around MySQL rows and Head a PostgreSQL COPY head. A passthrough
// statement comes back kept (its text replaced when a DEFINER was
// rewritten), nil when dropped, or with the dump-refused error. m is the
// MySQL sql_mode after the statement; the statement is judged under the
// mode before it and the mode after it and the stricter verdict wins,
// since a SET sql_mode changes how its own quotes read.
func (f *Filter) Add(b *core.Batch, m mysql.Mode) (*core.Batch, error) {
	if b.Pass == nil {
		return b, nil
	}
	o := f.o
	o.Mode = f.mode
	d, err := Check(*b.Pass, o)
	if m != f.mode && o.Dialect != core.Postgres {
		o.Mode = m
		d2, err2 := Check(*b.Pass, o)
		if d2.Verdict > d.Verdict {
			d, err = d2, err2
		}
		f.mode = m
	}
	if err != nil {
		return nil, err
	}
	for _, x := range d.drops {
		if f.made(x) {
			f.emptied = append(f.emptied, pendingDrop{x, b.Pass.Line})
			continue
		}
		f.drops = append(f.drops, pendingDrop{x, b.Pass.Line})
	}
	if d.creates != nil {
		f.created[*d.creates] = true
		f.emptied = slices.DeleteFunc(f.emptied, func(p pendingDrop) bool { return sameName(p.obj, *d.creates) })
	}
	switch d.txn {
	case 1:
		f.o.InTransaction = true
	case -1:
		f.o.InTransaction = false
	}
	switch d.Verdict {
	case Drop:
		if d.prepared != "" {
			f.o.Prepared[d.prepared] = true
		}
		f.note(d, b.Pass.Line, false)
		return nil, nil
	case Keep:
		f.charsets(d)
		if d.Routine {
			f.note(d, b.Pass.Line, true)
		}
		if d.Text != nil {
			c := *b
			p := *b.Pass
			p.Text = d.Text
			c.Pass = &p
			return &c, nil
		}
	}
	return b, nil
}

// Head judges the head of a row batch that a dump reader read as rows,
// such as PostgreSQL's COPY ... FROM stdin, which the reader only takes
// as rows in its safe form. Anything but Keep is refused: a head can't
// be dropped without its rows.
func (f *Filter) Head(st core.Statement) error {
	o := f.o
	o.Mode = f.mode
	d, err := Check(st, o)
	if err != nil {
		return err
	}
	if d.Verdict != Keep {
		return refusal(st, o, refuse(d.Kind, whyUnknown))
	}
	return nil
}

// charsets follows the user variables a kept SET assigns.
func (f *Filter) charsets(d Decision) {
	if d.lostVars {
		clear(f.o.Charsets)
		f.o.CharsetsLost = true
	}
	for k, ok := range d.vars {
		f.o.Charsets[k] = ok
	}
}

// note counts a plan line.
func (f *Filter) note(d Decision, line int64, each bool) {
	key := d.Note
	if each {
		key = fmt.Sprintf("%d\x00%s", line, d.Note)
	}
	if n := f.byNote[key]; n != nil {
		n.count++
		return
	}
	n := &planNote{text: d.Note, kind: d.Kind, first: line, count: 1, each: each}
	if _, r, ok := strings.Cut(d.Note, ": "); ok && !each {
		n.why = r
	}
	f.byNote[key] = n
	f.notes = append(f.notes, n)
}

// made reports whether the dump has created x already: a table and a
// view share one namespace.
func (f *Filter) made(x object) bool {
	for o := range f.created {
		if sameName(o, x) {
			return true
		}
	}
	return false
}

// sameName reports whether a and b name one object: the same kind and
// name, or a table and a view of the same name.
func sameName(a, b object) bool {
	if a.name != b.name {
		return false
	}
	tv := func(k string) bool { return k == "table" || k == "view" }
	return a.kind == b.kind || tv(a.kind) && tv(b.kind)
}

// Finish ends the dump: a DROP whose object no CREATE in the dump makes,
// or that removes what the dump created and filled without making it
// again, is refused now, before the sink commits anything (PRD 10.5:
// DROP TABLE IF EXISTS only before the CREATE of the same table).
func (f *Filter) Finish() error {
	for _, d := range f.emptied {
		kind := "DROP " + strings.ToUpper(d.obj.kind)
		return refusal(core.Statement{Line: d.line}, f.o, refuse(kind, whyDropFilled))
	}
	for _, d := range f.drops {
		if f.created[d.obj] {
			continue
		}
		if d.obj.kind == "table" && f.created[object{"view", d.obj.name}] {
			continue
		}
		kind := "DROP " + strings.ToUpper(d.obj.kind)
		return refusal(core.Statement{Line: d.line}, f.o, refuse(kind, whyDropOther))
	}
	return nil
}

// Notes are the plan lines: each kept routine and each kind of dropped
// statement with its count and first line.
func (f *Filter) Notes() []string {
	out := make([]string, 0, len(f.notes))
	for _, n := range f.notes {
		switch {
		case n.each, n.count == 1:
			out = append(out, fmt.Sprintf("line %s: %s", size.Count(n.first), n.text))
		default:
			out = append(out, fmt.Sprintf("%d %s statements left out of the copy (first at line %s): %s", n.count, n.kind, size.Count(n.first), n.why))
		}
	}
	return out
}
