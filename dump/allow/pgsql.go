// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"bytes"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// pgVars are the settings pg_dump writes at the top of a plain dump and
// a few more of the same harmless kind.
var pgVars = map[string]bool{
	"statement_timeout": true, "lock_timeout": true, "idle_in_transaction_session_timeout": true,
	"transaction_timeout": true, "client_encoding": true, "standard_conforming_strings": true,
	"check_function_bodies": true, "xmloption": true, "client_min_messages": true, "row_security": true,
	"default_tablespace": true, "default_table_access_method": true, "search_path": true,
	"default_with_oids": true, "default_toast_compression": true, "synchronous_commit": true,
	"escape_string_warning": true, "extra_float_digits": true, "datestyle": true, "intervalstyle": true,
	"timezone": true,
}

// pgExtensions are the contrib extensions a kept CREATE EXTENSION may
// name. Each is trusted or ships with PostgreSQL and adds functions and
// types only; an extension that reaches files or other servers (adminpack,
// dblink, postgres_fdw, file_fdw) is refused.
var pgExtensions = map[string]bool{
	"pgcrypto": true, "uuid-ossp": true, "citext": true, "pg_trgm": true, "hstore": true,
	"btree_gin": true, "btree_gist": true, "unaccent": true, "fuzzystrmatch": true, "intarray": true,
	"ltree": true, "cube": true, "earthdistance": true, "tablefunc": true, "pg_stat_statements": true,
	"plpgsql": true, "isn": true, "seg": true, "tcn": true, "tsm_system_rows": true,
	"tsm_system_time": true, "pgrowlocks": true, "bloom": true, "postgis": true, "vector": true,
}

// pgTrusted are the procedural languages a kept function may use: none
// of them can reach the server's files or run commands.
var pgTrusted = map[string]bool{"sql": true, "plpgsql": true, "plperl": true, "pltcl": true, "plv8": true}

// pgRoutines are the object kinds kept only with --keep-routines.
var pgRoutines = map[string]bool{
	"function": true, "procedure": true, "trigger": true, "view": true, "rule": true, "aggregate": true,
	"operator": true, "cast": true, "policy": true, "event": true, "materialized": true,
	"conversion": true, "transform": true,
}

// whyPGRoutine is the refusal of a PostgreSQL routine without --keep-routines.
var whyPGRoutine = why{whyRunsCode.danger, "Pass --keep-routines to keep it, or leave it out of the export."}

func checkPG(st core.Statement, o Options) Decision {
	text := bytes.TrimRight(trimmed(st.Text), " \t\r\n")
	if bytes.HasPrefix(text, []byte{'\\'}) {
		return pgMeta(text)
	}
	t := trimDelims(lex(st.Text, syntax{pg: true}))
	if len(t) == 0 {
		return keep()
	}
	if hasBackslash(t) {
		return refuse("a psql meta-command", whyClient)
	}
	if d, ok := globalChecks(t); ok {
		return d
	}
	w0 := t.w(0)
	switch w0 {
	case "set":
		return pgSet(t)
	case "reset":
		if t.w(1) == "role" || t.is(1, "session", "authorization") {
			return drop("RESET ROLE", reasonOwner)
		}
		if pgVars[t.w(1)] || t.w(1) == "all" {
			return keep()
		}
	case "select":
		return pgSelect(t)
	case "create":
		return pgCreate(t, o)
	case "drop":
		return pgDrop(t)
	case "alter":
		return pgAlter(t, o)
	case "comment":
		if t.w(1) == "on" {
			return keep()
		}
	case "grant", "revoke":
		return drop(strings.ToUpper(w0), reasonPrivileges)
	case "security":
		if t.w(1) == "label" {
			return drop("SECURITY LABEL", reasonPrivileges)
		}
	case "copy":
		return pgCopy(t)
	case "begin", "start", "commit", "end", "rollback", "abort":
		return keep()
	case "insert", "update", "delete", "merge":
		l := label(t, 1)
		return refuse(article(l)+" "+l+" statement that isn't a list of rows", whyRows)
	case "do":
		return refuse("a DO block", whyRunsCode)
	case "refresh":
		if o.KeepRoutines {
			return keep()
		}
		return refuse("REFRESH MATERIALIZED VIEW", whyPGRoutine)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// pgMeta keeps the \restrict and \unrestrict lines pg_dump writes since
// 17.6, 16.10, 15.14, 14.19 and 13.22 (they stop psql from running any
// other meta-command in the file) and refuses every other one.
func pgMeta(text []byte) Decision {
	cmd, arg, _ := bytes.Cut(text[1:], []byte(" "))
	c := string(cmd)
	if (c == "restrict" || c == "unrestrict") && len(arg) > 0 && len(arg) <= 128 &&
		bytes.IndexFunc(arg, func(r rune) bool { return (r < '0' || r > '9') && (r|0x20 < 'a' || r|0x20 > 'z') }) < 0 {
		return keep()
	}
	what := "a psql meta-command"
	if n := safeName(c); n != "" && len(n) <= 16 {
		what += " (\\" + n + ")"
	}
	return refuse(what, whyClient)
}

func pgSet(t toks) Decision {
	i := 1
	if t.w(i) == "session" || t.w(i) == "local" {
		if t.is(i, "session", "authorization") {
			return drop("SET SESSION AUTHORIZATION", reasonOwner)
		}
		i++
	}
	switch {
	case t.w(i) == "role":
		return drop("SET ROLE", reasonOwner)
	case t.is(i, "time", "zone"), t.w(i) == "constraints", t.w(i) == "transaction":
		return keep()
	case pgVars[t.w(i)]:
		for k := i + 1; k < len(t); k++ {
			if t.p(k, '(') {
				return refuse("a SET statement that reads a query", whyQuery)
			}
		}
		return keep()
	}
	what := "a SET of an unknown setting"
	if n := safeName(t.w(i)); n != "" {
		what = "a SET " + n + " statement"
	}
	return refuse(what, whySetting)
}

// pgSelect keeps pg_dump's set_config for search_path and setval for a
// sequence, called with literals only.
func pgSelect(t toks) Decision {
	i := 1
	if t.w(i) == "pg_catalog" && t.p(i+1, '.') {
		i += 2
	}
	fn := t.w(i)
	if (fn == "set_config" || fn == "setval") && t.p(i+1, '(') && t.p(len(t)-1, ')') {
		ok := true
		for k := i + 2; k < len(t)-1; k++ {
			if t.kind(k) == punct && !t.p(k, ',') && !t.p(k, '-') && !t.p(k, '.') {
				ok = false
			}
		}
		if fn == "set_config" && (t.kind(i+2) != str || !pgVars[strings.ToLower(strings.Trim(t[i+2].w, "'"))]) {
			ok = false
		}
		if ok {
			return keep()
		}
	}
	return refuse("a SELECT statement", whyUnknown)
}

func pgCreate(t toks, o Options) Decision {
	i := 1
modifiers:
	for {
		switch {
		case t.is(i, "or", "replace"):
			i += 2
			continue
		case t.is(i, "user", "mapping"):
			return refuse("CREATE USER MAPPING", whyConnects)
		}
		switch t.w(i) {
		case "unique", "temporary", "temp", "unlogged", "global", "local", "recursive", "trusted",
			"procedural", "default", "constraint":
			i++
			continue
		}
		break modifiers
	}
	obj := t.w(i)
	switch obj {
	case "table":
		for k := i; k < len(t); k++ {
			if t.w(k) == "as" && (t.w(k+1) == "select" || t.w(k+1) == "table" || t.w(k+1) == "values" || t.w(k+1) == "execute") ||
				t.w(k) == "select" {
				return refuse("a CREATE TABLE ... AS statement", whyQuery)
			}
		}
		return pgCreates(t, i+1, "table")
	case "index":
		return keep()
	case "sequence", "type", "domain", "schema", "statistics":
		return pgCreates(t, i+1, obj)
	case "extension":
		name, _ := t.objName(afterIfExists(t, i+1), true)
		if pgExtensions[name] {
			d := keep()
			d.creates = &object{"extension", name}
			return d
		}
		what := "a CREATE EXTENSION statement"
		if n := safeName(name); n != "" {
			what = "a CREATE EXTENSION " + n + " statement"
		}
		return refuse(what, why{"could load code into the staging database", adviceLeaveOut})
	case "role", "user", "group":
		return drop("CREATE "+strings.ToUpper(obj), reasonPrivileges)
	case "publication":
		return drop("CREATE PUBLICATION", reasonReplication)
	case "subscription", "server":
		return refuse(label(t, i+1), whyConnects)
	case "foreign":
		return refuse(label(t, i+2), whyConnects)
	case "database":
		return refuse("CREATE DATABASE", whyDatabase)
	case "tablespace":
		return refuse("CREATE TABLESPACE", whyWrites)
	case "language":
		return refuse("CREATE LANGUAGE", whyCommands)
	}
	if pgRoutines[obj] {
		return pgRoutine(t, i, o)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// pgCreates keeps a CREATE and records the object it makes.
func pgCreates(t toks, i int, kind string) Decision {
	d := keep()
	name, _ := t.objName(afterIfExists(t, i), true)
	d.creates = &object{kind, name}
	return d
}

// pgRoutine keeps a function, trigger, view or other code object with
// --keep-routines unless it is written in a language that can reach the
// server (C, plpython3u and the other untrusted ones).
func pgRoutine(t toks, i int, o Options) Decision {
	obj := t.w(i)
	j := i + 1
	if obj == "materialized" || obj == "event" {
		j++ // MATERIALIZED VIEW, EVENT TRIGGER
	}
	kind := "CREATE " + strings.ToUpper(obj)
	switch obj {
	case "materialized":
		kind, obj = "CREATE MATERIALIZED VIEW", "view"
	case "event":
		kind = "CREATE EVENT TRIGGER"
	}
	for k := range t {
		if t.w(k) != "language" {
			continue
		}
		lang := ""
		if t.kind(k+1) == word || t.kind(k+1) == ident {
			lang = strings.ToLower(t[k+1].w)
		} else if t.kind(k+1) == str {
			lang = strings.ToLower(strings.Trim(t[k+1].w, "'"))
		}
		if !pgTrusted[lang] {
			what := "a function in an untrusted language"
			if n := safeName(lang); n != "" && len(n) <= 24 {
				what = "a function in the language " + n
			}
			return refuse(what, whyCommands)
		}
	}
	if !o.KeepRoutines {
		return refuse(kind, whyPGRoutine)
	}
	name, _ := t.objName(afterIfExists(t, j), true)
	d := keep()
	d.Kind, d.Routine = kind, true
	d.creates = &object{obj, name}
	what := kind
	if n := safeName(name); n != "" {
		what += " " + n
	}
	d.Note = "kept " + what + " (--keep-routines)"
	return d
}

// pgTracked are the object kinds whose DROP must be matched by a CREATE
// later in the dump (pg_dump --clean writes the DROPs first).
var pgTracked = map[string]bool{
	"table": true, "view": true, "sequence": true, "type": true, "domain": true, "schema": true,
	"extension": true, "function": true, "procedure": true,
}

func pgDrop(t toks) Decision {
	if t.seq("cascade") {
		return refuse("a DROP ... CASCADE statement", whyCascade)
	}
	i := 1
	obj := t.w(i)
	if obj == "materialized" {
		obj = "view"
		i++
	}
	switch {
	case pgTracked[obj]:
		d := keep()
		for j := afterIfExists(t, i+1); ; j++ {
			name, next := t.objName(j, true)
			if name == "" {
				break
			}
			d.drops = append(d.drops, object{obj, name})
			j = next
			if t.p(j, '(') { // a function's argument list
				for j < len(t) && !t.p(j, ')') {
					j++
				}
				j++
			}
			if !t.p(j, ',') {
				break
			}
		}
		return d
	case obj == "index", obj == "trigger", obj == "policy", obj == "rule", obj == "aggregate", obj == "operator",
		obj == "cast", obj == "collation", obj == "statistics":
		return keep()
	case obj == "role", obj == "user", obj == "group":
		return drop("DROP "+strings.ToUpper(obj), reasonPrivileges)
	case obj == "publication":
		return drop("DROP PUBLICATION", reasonReplication)
	case obj == "database":
		return refuse("DROP DATABASE", whyDatabase)
	}
	return refuse(unknownLabel(t), whyDropOther)
}

// pgAlterClauses are the ALTER TABLE clauses pg_dump writes and other
// changes that stay inside the table.
var pgAlterClauses = map[string]bool{
	"add": true, "alter": true, "drop": true, "attach": true, "detach": true, "cluster": true,
	"replica": true, "enable": true, "disable": true, "force": true, "no": true, "set": true,
	"reset": true, "inherit": true, "validate": true, "rename": true, "of": true, "not": true,
}

func pgAlter(t toks, o Options) Decision {
	if t.seq("owner", "to") {
		return drop("ALTER ... OWNER TO", reasonOwner)
	}
	if t.is(1, "default", "privileges") {
		return drop("ALTER DEFAULT PRIVILEGES", reasonPrivileges)
	}
	obj := t.w(1)
	switch obj {
	case "table":
		i := 2
		if t.w(i) == "only" {
			i++
		}
		i = afterIfExists(t, i)
		if t.w(i) == "only" {
			i++
		}
		_, j := t.objName(i, true)
		for _, c := range clauses(t[j:]) {
			if len(c) > 0 && !pgAlterClauses[c.w(0)] {
				return refuse("an ALTER TABLE ... "+label(c, 1)+" statement", whyUnknown)
			}
			if c.is(0, "set", "tablespace") {
				return refuse("an ALTER TABLE ... SET TABLESPACE statement", whyWrites)
			}
		}
		return keep()
	case "sequence", "index", "type", "domain", "schema", "statistics":
		return keep()
	case "role", "user", "group":
		return drop("ALTER "+strings.ToUpper(obj), reasonPrivileges)
	case "database":
		return drop("ALTER DATABASE", reasonDBSettings)
	case "publication":
		return drop("ALTER PUBLICATION", reasonReplication)
	}
	if pgRoutines[obj] {
		if o.KeepRoutines {
			return keep()
		}
		return refuse(unknownLabel(t), whyPGRoutine)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// pgCopy keeps COPY ... FROM stdin, the form pg_dump writes; a COPY
// that reads or writes a server file or runs a program is refused.
func pgCopy(t toks) Decision {
	depth := 0
	for i := 1; i < len(t); i++ {
		switch {
		case t.p(i, '('):
			depth++
		case t.p(i, ')'):
			depth--
		case depth > 0:
		case t.w(i) == "from":
			switch {
			case t.w(i+1) == "stdin":
				return keep()
			case t.w(i+1) == "program":
				return refuse("a COPY ... FROM PROGRAM statement", whyProgram)
			}
			return refuse("a COPY statement that reads a server file", whyServerFile)
		case t.w(i) == "to":
			if t.w(i+1) == "program" {
				return refuse("a COPY ... TO PROGRAM statement", whyProgram)
			}
			return refuse("a COPY ... TO statement", whyServerFile)
		}
	}
	return refuse("a COPY statement Roadie can't read", whyUnknown)
}
