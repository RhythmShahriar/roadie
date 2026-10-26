// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// charsetBytes is the most bytes one character takes in a charset, for
// Column.MaxLen. A charset not listed counts as utf8mb4 (4), the
// WordPress default.
var charsetBytes = map[string]int{
	"utf8mb4": 4, "utf8": 3, "utf8mb3": 3, "latin1": 1, "ascii": 1, "binary": 1,
	"latin2": 1, "latin5": 1, "latin7": 1, "cp1250": 1, "cp1251": 1, "cp1256": 1,
	"cp1257": 1, "cp850": 1, "cp852": 1, "cp866": 1, "greek": 1, "hebrew": 1,
	"koi8r": 1, "koi8u": 1, "swe7": 1, "armscii8": 1, "dec8": 1, "hp8": 1,
	"keybcs2": 1, "macce": 1, "macroman": 1, "geostd8": 1, "tis620": 1,
	"ucs2": 2, "utf16": 4, "utf16le": 4, "utf32": 4, "big5": 2, "gbk": 2,
	"gb2312": 2, "gb18030": 4, "sjis": 2, "cp932": 2, "ujis": 3, "eucjpms": 3, "euckr": 2,
}

// byteTypes are the text and blob types with a fixed byte limit.
var byteTypes = map[string]int{
	"tinytext": 255, "text": 65535, "mediumtext": 16777215, "longtext": 0,
	"tinyblob": 255, "blob": 65535, "mediumblob": 16777215, "longblob": 0,
}

// columnAttrs end the type of a column definition.
var columnAttrs = map[string]bool{
	"not": true, "null": true, "default": true, "collate": true, "character": true,
	"charset": true, "auto_increment": true, "comment": true, "generated": true,
	"as": true, "primary": true, "unique": true, "key": true, "on": true, "check": true,
	"references": true, "visible": true, "invisible": true, "srid": true, "storage": true,
	"column_format": true, "constraint": true, "engine_attribute": true,
	"secondary_engine_attribute": true, "with": true, "compressed": true, "serial": true,
}

// charsetOf reads the multiplier of a charset name or of a collation
// ("utf8mb4_unicode_520_ci" is utf8mb4).
func charsetOf(name string) (int, bool) {
	name = strings.ToLower(strings.Trim(name, "'\""))
	if n, ok := charsetBytes[name]; ok {
		return n, true
	}
	if i := strings.IndexByte(name, '_'); i > 0 {
		n, ok := charsetBytes[name[:i]]
		return n, ok
	}
	return 0, false
}

// split cuts tokens at commas at depth 0 between t[from] (after an
// opening '(') and the matching ')'. It returns the parts and the index
// after the ')'.
func split(t []tok, from int) ([][]tok, int) {
	var parts [][]tok
	depth, start := 0, from
	for i := from; i < len(t); i++ {
		switch {
		case t[i].punct('('):
			depth++
		case t[i].punct(')'):
			if depth == 0 {
				if i > start {
					parts = append(parts, t[start:i])
				}
				return parts, i + 1
			}
			depth--
		case t[i].punct(',') && depth == 0:
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	if len(t) > start {
		parts = append(parts, t[start:])
	}
	return parts, len(t)
}

// tableCharset reads DEFAULT CHARSET=x, CHARACTER SET x or COLLATE=x from
// table options.
func tableCharset(t []tok) int {
	coll := 0
	for i := 0; i < len(t); i++ {
		val := func(j int) (string, bool) {
			if j < len(t) && t[j].punct('=') {
				j++
			}
			if j < len(t) {
				return string(t[j].text), true
			}
			return "", false
		}
		switch {
		case t[i].is("charset"):
			if v, ok := val(i + 1); ok {
				if n, ok := charsetOf(v); ok {
					return n
				}
			}
		case t[i].is("character") && i+1 < len(t) && t[i+1].is("set"):
			if v, ok := val(i + 2); ok {
				if n, ok := charsetOf(v); ok {
					return n
				}
			}
		case t[i].is("collate"):
			if v, ok := val(i + 1); ok {
				if n, ok := charsetOf(v); ok && coll == 0 {
					coll = n
				}
			}
		}
	}
	if coll > 0 {
		return coll
	}
	return 4
}

// ParseCreateTable reads a CREATE TABLE statement (the text as written;
// versioned comments are opened) into a table. like finds the table of
// CREATE TABLE x LIKE y; it may be nil. It returns nil when the text
// isn't a CREATE TABLE it can read.
func ParseCreateTable(text []byte, m Mode, like func(name string) *core.Table) *core.Table {
	t := tokens(Body(text, m), m, 0)
	if len(t) < 3 || !t[0].is("create") {
		return nil
	}
	i := skipCreateModifiers(t, 1)
	if i >= len(t) || !t[i].is("table") {
		return nil
	}
	i++
	if i < len(t) && t[i].is("if") {
		i += 3 // IF NOT EXISTS
	}
	name, i := qualifiedName(t, i)
	if name == "" {
		return nil
	}
	tb := &core.Table{Name: name}
	if i < len(t) && t[i].is("like") || i+1 < len(t) && t[i].punct('(') && t[i+1].is("like") {
		j := i + 1
		if t[i].punct('(') {
			j++
		}
		other, _ := qualifiedName(t, j)
		if like != nil {
			if src := like(other); src != nil {
				c := cloneTable(src)
				c.Name = name
				return c
			}
		}
		return tb
	}
	if i >= len(t) || !t[i].punct('(') {
		return tb // CREATE TABLE ... AS SELECT: no columns to read
	}
	defs, after := split(t, i+1)
	mult := tableCharset(t[after:])
	for _, d := range defs {
		if len(d) == 0 {
			continue
		}
		if indexDef(tb, d) {
			continue
		}
		if c, pk, ok := parseColumn(d, mult); ok {
			if pk {
				tb.PrimaryKey = append(tb.PrimaryKey, c.Name)
			}
			tb.Columns = append(tb.Columns, c)
		}
	}
	return tb
}

// qualifiedName reads `db`.`name` or name at t[i] and returns the last
// part and the index after it.
func qualifiedName(t []tok, i int) (string, int) {
	if i >= len(t) || (t[i].kind != tokWord && t[i].kind != tokIdent) {
		return "", i
	}
	name := t[i].name()
	i++
	for i+1 < len(t) && t[i].punct('.') && (t[i+1].kind == tokWord || t[i+1].kind == tokIdent) {
		name = t[i+1].name()
		i += 2
	}
	return name, i
}

// indexDef applies a PRIMARY KEY or UNIQUE KEY definition to tb and
// reports whether d was an index or constraint definition at all.
func indexDef(tb *core.Table, d []tok) bool {
	i := 0
	if d[0].is("constraint") {
		i++
		if i < len(d) && !d[i].is("primary") && !d[i].is("unique") && !d[i].is("foreign") && !d[i].is("check") {
			i++ // the constraint name
		}
	}
	if i >= len(d) || d[i].kind != tokWord {
		return false
	}
	switch strings.ToLower(string(d[i].text)) {
	case "primary":
		cols := keyColumns(d[i:])
		tb.PrimaryKey = cols
		if len(cols) == 1 {
			markUnique(tb, cols[0])
		}
		for _, c := range cols {
			if j := tb.Col(c); j >= 0 {
				tb.Columns[j].Nullable = false
			}
		}
		return true
	case "unique":
		if cols := keyColumns(d[i:]); len(cols) == 1 {
			markUnique(tb, cols[0])
		}
		return true
	case "key", "index", "fulltext", "spatial", "foreign", "check", "period", "vector":
		return true
	}
	return i > 0 // a CONSTRAINT of another kind
}

// keyColumns reads the column list of an index definition: the first
// parenthesised list, each part's first name ("meta_key"(191) is
// meta_key). A functional part ((expr)) gives no name.
func keyColumns(d []tok) []string {
	for i, x := range d {
		if !x.punct('(') {
			continue
		}
		parts, _ := split(d, i+1)
		var cols []string
		for _, p := range parts {
			if len(p) > 0 && (p[0].kind == tokIdent || p[0].kind == tokWord) {
				cols = append(cols, p[0].name())
			}
		}
		return cols
	}
	return nil
}

func markUnique(tb *core.Table, name string) {
	if j := tb.Col(name); j >= 0 {
		tb.Columns[j].Unique = true
	}
}

// parseColumn reads one column definition; mult is the table's charset
// multiplier. pk is set when the column itself says PRIMARY KEY.
func parseColumn(d []tok, mult int) (c core.Column, pk, ok bool) {
	if len(d) < 2 || (d[0].kind != tokIdent && d[0].kind != tokWord) {
		return core.Column{}, false, false
	}
	c = core.Column{Name: d[0].name(), Nullable: true}
	// The type runs to the first attribute word at depth 0.
	var typ bytes.Buffer
	i, depth := 1, 0
	for ; i < len(d); i++ {
		x := d[i]
		if depth == 0 && x.kind == tokWord && columnAttrs[strings.ToLower(string(x.text))] {
			break
		}
		switch {
		case x.punct('('):
			depth++
		case x.punct(')'):
			depth--
		}
		glue := x.punct('(') || x.punct(')') || x.punct(',')
		if typ.Len() > 0 && !glue {
			if last := typ.Bytes()[typ.Len()-1]; last != '(' && last != ',' {
				typ.WriteByte(' ')
			}
		}
		typ.Write(bytes.ToLower(x.text))
	}
	c.Type = typ.String()
	colMult := 0
	for ; i < len(d); i++ {
		x := d[i]
		switch {
		case x.is("not") && i+1 < len(d) && d[i+1].is("null"):
			c.Nullable = false
			i++
		case x.is("null"):
			c.Nullable = true
		case x.is("primary"), x.is("key") && !d[i-1].is("unique") && !d[i-1].is("primary"):
			c.Unique, c.Nullable, pk = true, false, true
		case x.is("unique"):
			c.Unique = true
		case x.is("generated"), x.is("as") && i+1 < len(d) && d[i+1].punct('('):
			c.Generated = true
		case x.is("character") && i+2 < len(d) && d[i+1].is("set"):
			if n, ok := charsetOf(string(d[i+2].text)); ok {
				colMult = n
			}
			if d[i+2].is("binary") {
				c.Binary = true
			}
			i += 2
		case x.is("charset") && i+1 < len(d):
			if n, ok := charsetOf(string(d[i+1].text)); ok {
				colMult = n
			}
			if d[i+1].is("binary") {
				c.Binary = true
			}
			i++
		case x.is("collate") && i+1 < len(d):
			if n, ok := charsetOf(string(d[i+1].text)); ok && colMult == 0 {
				colMult = n
			}
			i++
		case x.is("default") || x.is("comment"):
			i = skipValue(d, i+1)
		}
	}
	if colMult == 0 {
		colMult = mult
	}
	setLimits(&c, colMult)
	return c, pk, true
}

// skipValue returns the index of the last token of the value at t[i]: a
// literal, or a word with a parenthesised argument list.
func skipValue(t []tok, i int) int {
	if i+1 < len(t) && t[i+1].punct('(') {
		_, after := split(t, i+2)
		return after - 1
	}
	if i < len(t) && t[i].punct('(') {
		_, after := split(t, i+1)
		return after - 1
	}
	return i
}

// setLimits fills MaxLen and Binary from the type.
func setLimits(c *core.Column, mult int) {
	base, arg := c.Type, 0
	if i := strings.IndexByte(base, '('); i >= 0 {
		if j := strings.IndexAny(base[i:], ",)"); j > 0 {
			arg, _ = strconv.Atoi(strings.TrimSpace(base[i+1 : i+j]))
		}
		base = base[:i]
	}
	base = strings.TrimSpace(base)
	if f := strings.Fields(base); len(f) > 0 {
		base = f[0]
		if len(f) > 1 && (f[0] == "national" || f[0] == "character" || f[0] == "char" && f[1] == "varying") {
			base = strings.Join(f, " ")
		}
	}
	switch base {
	case "char", "varchar", "character", "character varying", "char varying":
		if c.Binary {
			mult = 1
		}
		c.MaxLen = arg * mult
	case "nchar", "nvarchar", "national char", "national varchar", "national character", "national character varying":
		c.MaxLen = arg * 3
	case "binary", "varbinary":
		c.MaxLen, c.Binary = arg, true
	default:
		if n, ok := byteTypes[base]; ok {
			c.MaxLen = n
			if strings.HasSuffix(base, "blob") {
				c.Binary = true
			}
		}
	}
}

// cloneTable copies a table so a change doesn't reach batches already
// handed out, whose Columns index into the old column list.
func cloneTable(t *core.Table) *core.Table {
	c := *t
	c.Columns = slices.Clone(t.Columns)
	c.PrimaryKey = slices.Clone(t.PrimaryKey)
	return &c
}

// AlterTable applies the column changes of an ALTER TABLE to a copy of
// tb and returns it: ADD [COLUMN] (with FIRST and AFTER), DROP [COLUMN],
// MODIFY, CHANGE, RENAME COLUMN, ADD PRIMARY KEY and ADD UNIQUE. It
// returns tb itself when the statement changes no column.
func AlterTable(tb *core.Table, text []byte, m Mode) *core.Table {
	t := tokens(Body(text, m), m, 0)
	if len(t) < 3 || !t[0].is("alter") {
		return tb
	}
	i := 1
	for i < len(t) && !t[i].is("table") {
		i++ // ONLINE, IGNORE
	}
	_, i = qualifiedName(t, i+1)
	if i >= len(t) {
		return tb
	}
	out := cloneTable(tb)
	changed := false
	actions, _ := split(append(slices.Clone(t[i:]), tok{tokPunct, []byte(")")}), 0)
	for _, a := range actions {
		if alterAction(out, a) {
			changed = true
		}
	}
	if !changed {
		return tb
	}
	return out
}

// alterAction applies one comma-separated action of an ALTER TABLE.
func alterAction(tb *core.Table, a []tok) bool {
	if len(a) < 2 {
		return false
	}
	at := func(i int, w string) bool { return i < len(a) && a[i].is(w) }
	switch {
	case at(0, "add"):
		i := 1
		if at(i, "column") {
			i++
		}
		if at(i, "if") {
			i += 3
		}
		if i < len(a) && a[i].punct('(') {
			defs, _ := split(a, i+1)
			for _, d := range defs {
				if c, _, ok := parseColumn(d, 4); ok {
					tb.Columns = append(tb.Columns, c)
				}
			}
			return true
		}
		if indexDef(tb, a[i:]) {
			return true
		}
		c, _, ok := parseColumn(a[i:], 4)
		if !ok {
			return false
		}
		placeColumn(tb, c, a[i:])
		return true
	case at(0, "drop"):
		i := 1
		if at(i, "column") {
			i++
		} else if at(i, "primary") || at(i, "key") || at(i, "index") || at(i, "foreign") || at(i, "constraint") || at(i, "check") || at(i, "partition") {
			return false
		}
		if at(i, "if") {
			i += 2
		}
		if i < len(a) {
			if j := tb.Col(a[i].name()); j >= 0 {
				tb.Columns = slices.Delete(tb.Columns, j, j+1)
				return true
			}
		}
	case at(0, "modify"), at(0, "change"):
		i := 1
		if at(i, "column") {
			i++
		}
		old := ""
		if at(0, "change") && i < len(a) {
			old = a[i].name()
			i++
		}
		c, _, ok := parseColumn(a[i:], 4)
		if !ok {
			return false
		}
		if old == "" {
			old = c.Name
		}
		if j := tb.Col(old); j >= 0 {
			c.Unique = c.Unique || tb.Columns[j].Unique
			tb.Columns[j] = c
			return true
		}
	case at(0, "rename") && at(1, "column") && len(a) >= 5:
		if j := tb.Col(a[2].name()); j >= 0 {
			tb.Columns[j].Name = a[4].name()
			return true
		}
	}
	return false
}

// placeColumn adds c where FIRST or AFTER x in its definition puts it,
// or at the end.
func placeColumn(tb *core.Table, c core.Column, d []tok) {
	for i := range d {
		switch {
		case d[i].is("first"):
			tb.Columns = slices.Insert(tb.Columns, 0, c)
			return
		case d[i].is("after") && i+1 < len(d):
			if j := tb.Col(d[i+1].name()); j >= 0 {
				tb.Columns = slices.Insert(tb.Columns, j+1, c)
				return
			}
		}
	}
	tb.Columns = append(tb.Columns, c)
}
