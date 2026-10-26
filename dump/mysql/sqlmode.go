// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// OldestServer is the oldest server Roadie expects a dump to be imported
// into, written as a versioned comment writes it (5.7.0). A /*!NNNNN
// comment at or below it runs on every supported server; one above it
// runs on some servers and not on others.
const OldestServer = 50700

// Vars are the user variables a dump has set, so that a later
// SET sql_mode = @OLD_SQL_MODE can be followed. The zero value is a dump
// that has set none.
type Vars struct {
	m map[string]value
	// lost is set once a user variable was set in a form Roadie can't
	// read: from then on any variable may hold anything.
	lost bool
}

// get is what @name holds: NULL when the dump never set it.
func (v *Vars) get(name string) value {
	if x, ok := v.m[name]; ok {
		return x
	}
	if v.lost {
		return value{}
	}
	return value{kind: valNull}
}

func (v *Vars) set(name string, x value) {
	if v.m == nil {
		v.m = map[string]value{}
	}
	v.m[name] = x
}

// valKind is what a SET value evaluates to.
type valKind uint8

const (
	valUnknown valKind = iota // anything Roadie can't work out
	valNull
	valDefault
	valMode // a whole sql_mode, read from @@sql_mode
	valText // a plain string literal
)

// value is a SET value as far as sql_mode is concerned.
type value struct {
	kind valKind
	mode Mode
	text string
}

// modeName lists every sql_mode name a supported server knows, with
// whether every supported server (MySQL 5.7 and 8, MariaDB 10 and 11)
// takes it. A name only some servers take makes the whole SET fail on
// the others.
var modeName = map[string]bool{
	"": true, "ALLOW_INVALID_DATES": true, "ANSI_QUOTES": true, "ERROR_FOR_DIVISION_BY_ZERO": true,
	"HIGH_NOT_PRECEDENCE": true, "IGNORE_SPACE": true, "NO_AUTO_VALUE_ON_ZERO": true,
	"NO_BACKSLASH_ESCAPES": true, "NO_DIR_IN_CREATE": true, "NO_ENGINE_SUBSTITUTION": true,
	"NO_UNSIGNED_SUBTRACTION": true, "NO_ZERO_DATE": true, "NO_ZERO_IN_DATE": true,
	"ONLY_FULL_GROUP_BY": true, "PAD_CHAR_TO_FULL_LENGTH": true, "PIPES_AS_CONCAT": true,
	"REAL_AS_FLOAT": true, "STRICT_ALL_TABLES": true, "STRICT_TRANS_TABLES": true,
	"ANSI": true, "TRADITIONAL": true,

	"NO_AUTO_CREATE_USER": false, "NO_FIELD_OPTIONS": false, "NO_KEY_OPTIONS": false,
	"NO_TABLE_OPTIONS": false, "MYSQL323": false, "MYSQL40": false, "TIME_TRUNCATE_FRACTIONAL": false,
	"EMPTY_STRING_IS_NULL": false, "SIMULTANEOUS_ASSIGNMENT": false, "TIME_ROUND_FRACTIONAL": false,
}

// Parts of the SET forms Roadie follows. They are matched against whole
// clauses, so anything more (a second literal, a character set
// introducer, a function call) makes the value unknown.
const (
	reVarName = "([a-z0-9_$.]+|'[^'\\\\]*'|`[^`]*`|\"[^\"\\\\]*\")"
	reSelf    = `@@(?:(?:session|local)\.)?sql_mode`
)

var (
	reScope   = regexp.MustCompile(`(?is)^(global|session|local|persist|persist_only)\s+(.*)$`)
	reSysMode = regexp.MustCompile(`(?is)^(?:@@(?:(session|local|global|persist|persist_only)\.)?)?sql_mode\s*:?=\s*(.*)$`)
	reUserSet = regexp.MustCompile(`(?is)^@` + reVarName + `\s*:?=\s*(.*)$`)
	reUserGet = regexp.MustCompile(`(?is)^@` + reVarName + `$`)
	reSelfGet = regexp.MustCompile(`(?is)^` + reSelf + `$`)
	reSQ      = regexp.MustCompile(`^'([^'\\]*)'$`)
	reDQ      = regexp.MustCompile(`^"([^"\\]*)"$`)
	reIfNull  = regexp.MustCompile(`(?is)^(?:ifnull|coalesce)\(\s*@` + reVarName + `\s*,\s*'([^'\\]*)'\s*\)$`)
)

// SQLMode returns the sql_mode after a statement, given its text as read
// (with any versioned comment around it) and the mode before it. vars
// holds the user variables the dump has set so far and is updated; nil
// starts from none.
//
// The quoting a server applies decides where its strings and statements
// end, so a mode Roadie can't follow exactly comes back with Unknown set
// and the allowlist refuses the statement. That covers any value but a
// plain string literal, DEFAULT, @@sql_mode, a user variable set from
// those or IFNULL(@var, 'literal'); a change inside a versioned comment
// that only some servers run; and a change that a failing SET (an
// assignment some servers reject) may or may not make.
//
// A SET checks every value before it assigns any, so values read the
// state from before the statement and one failing assignment cancels
// the whole SET. DEFAULT and the @@sql_mode a dump saves before its first
// change are taken as a mode without these flags, the default of every
// supported server.
func SQLMode(text []byte, cur Mode, vars *Vars) Mode {
	if vars == nil {
		vars = &Vars{}
	}
	body := Body(text, cur)
	if len(body) < 4 || !eqFold(body[:3], "set") || !isSpace(body[3]) {
		return cur
	}
	type update struct {
		name string // "" for sql_mode, else a user variable
		val  value
	}
	var ups []update
	written := map[string]bool{}
	uncertain := gated(text, cur) // the server may not run the statement
	unknown, fails, global := false, false, false
	for _, c := range setClauses(bytes.TrimRight(body[4:], "; \t\r\n"), cur) {
		if m := reScope.FindSubmatch(c); m != nil {
			// The latest GLOBAL or SESSION keyword holds for the
			// clauses after it that name none.
			s := strings.ToLower(string(m[1]))
			global = s != "session" && s != "local"
			c = m[2]
		}
		if m := reSysMode.FindSubmatch(c); m != nil {
			s := strings.ToLower(string(m[1]))
			if global && s == "" || s == "global" || s == "persist" || s == "persist_only" {
				uncertain = true // not the session's mode; the allowlist refuses it
				continue
			}
			mode, ok, certain, fail := modeOf(eval(m[2], cur, vars, written), cur)
			switch {
			case !ok:
				unknown = true
			case fail:
				fails = true
			}
			uncertain = uncertain || !certain
			ups = append(ups, update{"", value{kind: valMode, mode: mode}})
			written[""] = true
			continue
		}
		if m := reUserSet.FindSubmatch(c); m != nil {
			name := varName(m[1])
			ups = append(ups, update{name, eval(m[2], cur, vars, written)})
			written["@"+name] = true
			continue
		}
		if c[0] == '@' && (len(c) < 2 || c[1] != '@') {
			vars.m, vars.lost = nil, true // a user variable Roadie can't name
			continue
		}
		if bytes.Contains(bytes.ToLower(c), []byte("sql_mode")) {
			unknown = true
			continue
		}
		// Any other setting may be rejected by the server, which then
		// runs none of the statement.
		uncertain = true
	}
	// A NULL sql_mode makes the server reject the whole SET. Should a
	// server assign the clauses before it anyway, a change of quoting
	// among them still counts as unknown.
	next, changed := cur, false
	for _, u := range ups {
		if u.name == "" {
			next = u.val.mode
			changed = changed || quoting(next) != quoting(cur)
			continue
		}
		if uncertain || fails {
			u.val = value{}
		}
		vars.set(u.name, u.val)
	}
	if fails {
		next = cur
		uncertain = true
	}
	if unknown || uncertain && changed {
		next = cur
		next.Unknown = true
	}
	return next
}

// quoting is the part of a mode that decides where strings end.
func quoting(m Mode) Mode {
	return Mode{NoBackslashEscapes: m.NoBackslashEscapes, ANSIQuotes: m.ANSIQuotes}
}

// setClauses splits what follows SET at commas outside quotes and
// parentheses, each clause trimmed.
func setClauses(b []byte, m Mode) [][]byte {
	var out [][]byte
	depth, start := 0, 0
	for i := 0; i < len(b); {
		switch c := b[i]; {
		case c == '\'' || c == '"' || c == '`':
			i = quotedEnd(b, i, m)
			continue
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, bytes.TrimSpace(b[start:i]))
			start = i + 1
		}
		i++
	}
	out = append(out, bytes.TrimSpace(b[start:]))
	for _, c := range out {
		if len(c) == 0 {
			return [][]byte{{'?'}} // an empty clause: not a SET Roadie reads
		}
	}
	return out
}

// varName is a user variable name as the server compares it: unquoted
// and in lower case.
func varName(b []byte) string {
	if len(b) >= 2 && (b[0] == '\'' || b[0] == '`' || b[0] == '"') {
		b = b[1 : len(b)-1]
	}
	return strings.ToLower(string(b))
}

// eval reads a SET value against the state before the statement. A
// value that reads a variable an earlier clause of the same SET assigns
// is unknown: servers differ on which value it sees.
func eval(b []byte, cur Mode, vars *Vars, written map[string]bool) value {
	b = bytes.TrimSpace(b)
	if m := reSQ.FindSubmatch(b); m != nil {
		return value{kind: valText, text: string(m[1])}
	}
	if m := reDQ.FindSubmatch(b); m != nil && !cur.ANSIQuotes {
		return value{kind: valText, text: string(m[1])}
	}
	switch {
	case eqFold(b, "default"):
		return value{kind: valDefault}
	case eqFold(b, "null"):
		return value{kind: valNull}
	case reSelfGet.Match(b):
		if written[""] {
			return value{}
		}
		return value{kind: valMode, mode: cur}
	}
	if m := reUserGet.FindSubmatch(b); m != nil {
		name := varName(m[1])
		if written["@"+name] {
			return value{}
		}
		return vars.get(name)
	}
	if m := reIfNull.FindSubmatch(b); m != nil {
		name := varName(m[1])
		if written["@"+name] {
			return value{}
		}
		if x := vars.get(name); x.kind != valNull {
			return x
		}
		return value{kind: valText, text: string(m[2])}
	}
	return value{}
}

// modeOf is the session sql_mode a value sets. ok is false when Roadie
// can't tell; certain is false when some supported server rejects the
// value; fail is true when every server rejects it (NULL).
func modeOf(x value, cur Mode) (m Mode, ok, certain, fail bool) {
	switch x.kind {
	case valNull:
		return cur, true, true, true
	case valDefault:
		return Mode{}, true, true, false
	case valMode:
		return x.mode, true, true, false
	case valText:
		m, certain, ok = parseModes(x.text)
		return m, ok, certain, false
	}
	return cur, false, false, false
}

// parseModes reads a comma-separated list of sql_mode names. ok is false
// for a name Roadie doesn't know, which some server may read as anything,
// and for the combination modes ORACLE, MSSQL, DB2, POSTGRESQL and MAXDB,
// which change more of how MariaDB parses a statement than Roadie follows.
func parseModes(s string) (m Mode, certain, ok bool) {
	certain = true
	if s == "" {
		return m, true, true
	}
	for _, name := range strings.Split(s, ",") {
		name = strings.ToUpper(name)
		all, known := modeName[name]
		if !known || name == "" {
			return Mode{}, false, false
		}
		certain = certain && all
		switch name {
		case "NO_BACKSLASH_ESCAPES":
			m.NoBackslashEscapes = true
		case "ANSI_QUOTES", "ANSI":
			m.ANSIQuotes = true
		}
	}
	return m, certain, true
}

// gated reports whether text holds a versioned comment that only some
// supported servers run: /*!NNNNN above OldestServer, any other number of
// digits, or MariaDB's /*M!, which MySQL reads as a plain comment.
func gated(b []byte, m Mode) bool {
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = quotedEnd(b, i, m)
		case c == '/' && i+2 < n && b[i+1] == '*' && b[i+2] == '!':
			j := skipDigits(b, i+3)
			if d := j - i - 3; d != 0 {
				v, _ := strconv.Atoi(string(b[i+3 : j]))
				if d != 5 || v > OldestServer {
					return true
				}
			}
			i = j
		case c == '/' && i+3 < n && b[i+1] == '*' && b[i+2] == 'M' && b[i+3] == '!':
			return true
		case c == '/' && i+1 < n && b[i+1] == '*':
			end := bytes.Index(b[i+2:], []byte("*/"))
			if end < 0 {
				return false
			}
			i += 2 + end + 2
		case c == '-' && i+1 < n && b[i+1] == '-' && (i+2 == n || b[i+2] <= ' '), c == '#':
			end := bytes.IndexByte(b[i:], '\n')
			if end < 0 {
				return false
			}
			i += end
		default:
			i++
		}
	}
	return false
}
