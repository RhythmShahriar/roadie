// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// The end lines dump tools write. mysqldump, mariadb-dump and wp db
// export (which runs one of them) end with "-- Dump completed" and a
// date unless --skip-dump-date; pg_dump -Fp ends with "-- PostgreSQL
// database dump complete" and, since 17.6 and 16.10, a \unrestrict line
// after it.
const (
	mysqlTrailer = "Dump completed"
	pgTrailer    = "PostgreSQL database dump complete"
)

// reAdminerEnd is the date line Adminer writes last.
var reAdminerEnd = regexp.MustCompile(`(?m)^-- \d{4}-\d\d-\d\d \d\d:\d\d:\d\d`)

// endCheck decides at the end of the input whether the dump is whole.
// Tools that write an end line must have written it, with nothing but
// comments (and pg_dump's \unrestrict) after it. phpMyAdmin, Adminer and
// dumps without a tool line write none: their end is accepted only at a
// statement boundary after a COMMIT, a complete INSERT or the ALTER
// TABLE and UNLOCK TABLES lines that close a table, or after Adminer's
// closing date line. An export that opened a transaction must have
// committed it: phpMyAdmin stops between statements when PHP runs out
// of time, so a cut there leaves its START TRANSACTION open.
type endCheck struct {
	trailer  string // the end line wanted, "" for tools that write none
	seen     bool   // the end line was read and nothing but comments followed
	read     bool   // the end line was read at some point
	after    string // the kind of the first statement that counts after it
	adminer  bool
	tool     bool   // the head names the tool that wrote the dump
	dateSeen bool   // Adminer's closing date line was read last
	closed   bool   // the last statement that counts closes an export
	any      bool   // a statement that counts was read
	txOpen   bool   // a START TRANSACTION or BEGIN has no COMMIT yet
	lastKind string // the kind of that statement, for the message
}

// newEndCheck reads the tool from the decompressed head: the first line
// that names one decides, as for the dialect.
func newEndCheck(head []byte) *endCheck {
	for line := range bytes.Lines(head) {
		switch {
		case bytes.HasPrefix(line, []byte("-- MySQL dump")), bytes.HasPrefix(line, []byte("-- MariaDB dump")):
			return &endCheck{trailer: mysqlTrailer}
		case bytes.HasPrefix(line, []byte("-- PostgreSQL database dump")):
			return &endCheck{trailer: pgTrailer}
		case bytes.HasPrefix(line, []byte("-- Adminer")):
			return &endCheck{adminer: true}
		case bytes.HasPrefix(line, []byte("-- phpMyAdmin SQL Dump")):
			return &endCheck{tool: true}
		}
	}
	return &endCheck{}
}

// see follows one batch as read, before the allowlist drops anything.
func (e *endCheck) see(b *core.Batch) {
	if b.Pass == nil {
		if b.Last {
			e.count("INSERT", true)
		} else {
			e.count("INSERT", false) // an INSERT still open
		}
		return
	}
	st := b.Pass
	switch st.Kind {
	case core.StmtComment:
		if e.trailer != "" && bytes.Contains(st.Text, []byte("-- "+e.trailer)) {
			e.seen, e.read, e.after = true, true, ""
		}
		if e.adminer {
			e.dateSeen = reAdminerEnd.Match(st.Text)
		}
		return
	case mysql.KindClient:
		// pg_dump's \unrestrict comes after its end line.
		if e.trailer == pgTrailer && bytes.HasPrefix(bytes.TrimSpace(st.Text), []byte(`\unrestrict `)) {
			return
		}
	case core.StmtSet:
		// phpMyAdmin restores its settings after COMMIT.
		e.seen, e.dateSeen = false, false
		return
	case core.StmtOther:
		// A GRANT or FLUSH PRIVILEGES added after the end line holds no
		// rows; the allowlist drops it (or refuses one that writes rows),
		// so the dump is still whole.
		if e.seen {
			return
		}
	}
	if e.read && e.after == "" {
		e.after = kindWords(st.Kind)
	}
	e.seen, e.dateSeen = false, false
	switch st.Kind {
	case core.StmtTransaction:
		switch txVerb(st.Text) {
		case "start", "begin":
			e.txOpen = true
		case "commit", "end", "rollback", "abort":
			e.txOpen = false
		}
		e.count("START TRANSACTION", isCommit(st.Text))
	case mysql.KindAlterTable, core.StmtAlterKeys, core.StmtUnlock:
		e.count("ALTER TABLE", true)
	case core.StmtCreateTable:
		e.count("CREATE TABLE", false)
	default:
		e.count(string(st.Kind), false)
	}
}

func (e *endCheck) count(kind string, closes bool) {
	if e.read && e.after == "" {
		e.after = kind
	}
	e.seen, e.dateSeen = false, false
	e.any, e.closed, e.lastKind = true, closes, kind
}

// kindWords names a statement kind as SQL does: drop-table is DROP
// TABLE; anything Roadie has no name for is a statement.
func kindWords(k core.StmtKind) string {
	if k == core.StmtOther || k == "" {
		return "statement"
	}
	return strings.ToUpper(strings.ReplaceAll(string(k), "-", " "))
}

// isCommit reports whether a transaction statement is COMMIT.
func isCommit(text []byte) bool { return txVerb(text) == "commit" }

// txVerb is the first word of a transaction statement, lower-cased:
// start, begin, commit, end, rollback or abort.
func txVerb(text []byte) string {
	body := bytes.ToLower(bytes.TrimSpace(mysql.Body(text, mysql.Mode{})))
	end := bytes.IndexFunc(body, func(r rune) bool { return r < 'a' || r > 'z' })
	if end < 0 {
		end = len(body)
	}
	return string(body[:end])
}

// whole reports whether the dump ended where its tool ends one.
func (e *endCheck) whole() bool {
	switch {
	case e.trailer != "":
		return e.seen
	case e.adminer && e.dateSeen:
		return true
	case e.txOpen:
		return false
	}
	// An input with no statement at all and no tool line is an export
	// that failed or never started (an empty file, an empty pipe). A
	// phpMyAdmin export of nothing names its tool.
	return e.closed || !e.any && e.tool
}

// truncatedError is dump-truncated for a dump that ended cleanly between
// statements but not where its tool ends one.
func (e *endCheck) truncatedError(name, table string, raw int64) *core.Error {
	where := "ends early"
	if table != "" {
		where = "ends inside table " + table
	}
	what := `with no "` + e.trailer + `" line`
	switch {
	case !e.any && e.trailer == "" && !e.adminer && !e.tool:
		return core.Errorf(core.ErrDumpTruncated, 3,
			"dump-truncated: %s holds no SQL statements (%s): it is empty or ends before its first one, as an export that failed does. Export it again; the target is untouched.",
			name, size.Human(raw))
	case e.trailer != "" && e.read:
		// The end line is there, with more after it: two dumps run
		// together, the second cut short.
		return core.Errorf(core.ErrDumpTruncated, 3,
			"dump-truncated: %s has statements after its %q line (the first a %s) and ends at %s without another one. Export it again, or remove what follows that line; the target is untouched.",
			name, e.trailer, e.after, size.Human(raw))
	case e.trailer != "":
	case e.txOpen:
		what = "with no COMMIT for its START TRANSACTION"
	default:
		what = "with no closing COMMIT or complete INSERT"
	}
	return core.Errorf(core.ErrDumpTruncated, 3, "dump-truncated: %s %s at %s, %s. Export it again; the target is untouched.",
		name, where, size.Human(raw), what)
}
