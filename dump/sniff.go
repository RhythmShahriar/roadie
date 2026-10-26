// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"os"
	"regexp"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

// HeadSize is how much of a file Sniff looks at.
const HeadSize = 64 << 10

// binaryWindow is how much decompressed text the binary check reads.
const binaryWindow = 4 << 10

// plainSQL is the second sentence of every dump-format refusal.
const plainSQL = "Roadie reads plain SQL: pg_dump -Fp, mysqldump, mariadb-dump or wp db export."

// The things a refused input can be, as the dump-format message names them.
const (
	isPGCustom = "a pg_dump custom-format archive"
	isTar      = "a tar archive (Roadie doesn't read inside archives)"
	isZip      = "a zip archive (Roadie doesn't read inside archives)"
	isFolder   = "a folder (pg_dump -Fd or mydumper output)"
	isBinary   = "an unknown binary file (.wpress, Duplicator or another backup format)"
)

// formatError is the dump-format refusal of PRD 17 for name, which is what.
func formatError(name, what string) *core.Error {
	return core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s is %s. %s", name, what, plainSQL)
}

// sniff is Sniff with the file name for messages; it also returns the
// server version the head names, for Schema.Server.
func sniff(name string, head []byte) (core.Dialect, string, string, error) {
	comp := compress.Detect(head)
	if what := archive(head); what != "" {
		return "", comp, "", formatError(name, what)
	}
	plain := head
	if comp != compress.None {
		plain = compress.Head(head, comp, HeadSize)
		if what := archive(plain); what != "" {
			return "", comp, "", formatError(name, what)
		}
	}
	if binary(plain[:min(len(plain), binaryWindow)]) {
		return "", comp, "", formatError(name, isBinary)
	}
	d, server := dialectOf(plain)
	return d, comp, server, nil
}

// CheckInput refuses a --from path that is a folder, such as pg_dump -Fd
// or mydumper output, before anything opens it as a file.
func CheckInput(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return formatError(path, isFolder)
	}
	return nil
}

// archive names an archive format from its magic bytes, or "".
func archive(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("PGDMP")):
		return isPGCustom
	case bytes.HasPrefix(b, []byte("PK\x03\x04")), bytes.HasPrefix(b, []byte("PK\x05\x06")):
		return isZip
	case len(b) >= 262 && bytes.Equal(b[257:262], []byte("ustar")):
		return isTar
	}
	return ""
}

// binary reports whether the start of a decompressed file is not text: more
// than 1% of its bytes outside quoted strings are C0 control characters
// other than tab, line feed, vertical tab, form feed and carriage return.
// Quoted strings are skipped because mysqldump without --hex-blob writes
// blobs as raw bytes inside '...'. The check also counts the other C0
// controls, not only those below 0x09: random bytes spend most of their
// length inside what looks like a quote, and the extra controls keep them
// well over the line. Text that isn't valid UTF-8 passes as latin1
// (MySQL's latin1 is cp1252, which gives every byte a meaning), so the
// control count is the whole test.
func binary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	var quote byte
	ctrl := 0
	for i := 0; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			switch {
			case c == '\\' && quote != '`':
				i++
			case c == quote:
				quote = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c < 0x20 && (c < 0x09 || c > 0x0d):
			ctrl++
		}
	}
	return ctrl*100 > len(b)
}

var (
	reServer   = regexp.MustCompile(`(?m)^-- Server version:?\s+(\S+)`)
	reAdminer  = regexp.MustCompile(`(?m)^-- Adminer \S+ (?:MySQL|MariaDB) (\S+) dump`)
	reMariaVer = regexp.MustCompile(`(?:^|-)(\d+\.\d+\.\d+)-MariaDB`)
)

// dialectOf reads the dialect and server version from the comment lines
// and SET statements a dump tool writes at the top. The first line that
// names a tool decides, so a value further down that happens to say
// "PostgreSQL" can't change it.
func dialectOf(head []byte) (core.Dialect, string) {
	server := ""
	if m := reServer.FindSubmatch(head); m != nil {
		server = string(m[1])
	} else if m := reAdminer.FindSubmatch(head); m != nil {
		server = string(m[1])
	}
	maria := bytes.Contains([]byte(server), []byte("MariaDB"))
	if m := reMariaVer.FindStringSubmatch(server); m != nil {
		// "5.5.5-10.6.12-MariaDB-log": the real version is the MariaDB one.
		server = m[1]
	}
	mysqlish := func() core.Dialect {
		if maria {
			return core.MariaDB
		}
		return core.MySQL
	}
	for line := range bytes.Lines(head) {
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case bytes.HasPrefix(line, []byte("/*M!999999")),
			bytes.HasPrefix(line, []byte("-- MariaDB dump")):
			return core.MariaDB, server
		case bytes.HasPrefix(line, []byte("-- MySQL dump")):
			if bytes.Contains(line, []byte("MariaDB")) {
				return core.MariaDB, server
			}
			return mysqlish(), server
		case bytes.HasPrefix(line, []byte("-- phpMyAdmin SQL Dump")),
			bytes.HasPrefix(line, []byte("-- Adminer")):
			if bytes.Contains(line, []byte("MariaDB")) {
				return core.MariaDB, server
			}
			return mysqlish(), server
		case bytes.HasPrefix(line, []byte("-- PostgreSQL database dump")),
			bytes.HasPrefix(line, []byte("SET standard_conforming_strings")):
			return core.Postgres, server
		case mysqlSet(line):
			return mysqlish(), server
		}
	}
	return "", server
}

// mysqlSet matches the SET lines mysqldump and wp db export write first.
func mysqlSet(line []byte) bool {
	for _, p := range []string{"/*!40101 SET ", "/*!40103 SET ", "/*!40014 SET ", "/*!40111 SET ", "SET NAMES ", "SET FOREIGN_KEY_CHECKS", "SET SQL_MODE", "SET @OLD_"} {
		if bytes.HasPrefix(line, []byte(p)) {
			return true
		}
	}
	return false
}
