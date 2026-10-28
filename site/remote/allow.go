// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Template names one allowlisted remote command (PRD 6.13). Nothing but
// these runs over ssh.
type Template string

// The allowlist. Source templates are read-only; Target ones run only
// on a writable target.
const (
	CoreVersion    Template = "wp core version"
	OptionGet      Template = "wp option get"
	DBExport       Template = "wp db export"
	DBExportTables Template = "wp db export --tables"
	Cat            Template = "cat"
	TestFile       Template = "test -f"
	Find           Template = "find"
	MySQLDump      Template = "mysqldump"
	PGDump         Template = "pg_dump"

	DBImport  Template = "wp db import"
	MySQL     Template = "mysql"
	PSQL      Template = "psql"
	Backup    Template = "backup"
	Write     Template = "write"
	WriteMode Template = "write with mode"
	WriteKeep Template = "write keeping mode and owner"
	Stat      Template = "ls -lnLd"
	Mkdir     Template = "mkdir -p"
	Remove    Template = "rm -f"
	EvalFile  Template = "wp eval-file"
)

// template is the shape of one command. In format, each verb takes one
// argument from Build: %q a single-quoted word, %n a number, %m a file
// mode, %i a numeric user or group id, %t a backup timestamp, %o an option name, %v the rest of the
// arguments as single-quoted words (zero or more). %w is the WP-CLI
// binary with --path and takes no argument.
type template struct {
	format string
	target bool // only on a writable target
}

// mysqlWrapper writes the [client] defaults that come first on stdin to
// a private temp file, since mysql reads the dump from stdin too and
// --defaults-extra-file=/dev/stdin would swallow it. The file is removed
// when the shell exits.
const mysqlWrapper = `umask 077 && f=$(mktemp) && trap 'rm -f "$f"' EXIT && ` +
	`while IFS= read -r l && [ "$l" != '` + credsEnd + `' ]; do printf '%s\n' "$l"; done > "$f" && ` +
	`mysql --defaults-extra-file="$f" %q`

// pgWrapper reads the password from the first stdin line into
// PGPASSWORD, so it is never in an argv on either host.
const pgWrapper = `IFS= read -r PGPASSWORD; export PGPASSWORD; exec `

var templates = map[Template]template{
	CoreVersion:    {format: "%w core version --skip-plugins --skip-themes"},
	OptionGet:      {format: "%w option get %o --skip-plugins --skip-themes"},
	DBExport:       {format: "%w db export - --skip-plugins --skip-themes --single-transaction --skip-triggers"},
	DBExportTables: {format: "%w db export - --tables=%q --skip-plugins --skip-themes --single-transaction --skip-triggers"},
	Cat:            {format: "cat -- %q"},
	// test takes no "--" (POSIX test reads it as an operand, so sh exits
	// 2); the path is always absolute, so it never starts with a dash.
	TestFile:  {format: "test -f %q"},
	Find:      {format: `find %q -maxdepth %n -printf '%y %s %P\n'`},
	MySQLDump: {format: "mysqldump --defaults-extra-file=/dev/stdin --single-transaction --skip-triggers --no-tablespaces --hex-blob %q%v"},
	PGDump:    {format: pgWrapper + "pg_dump --format=plain --no-owner --no-privileges --host=%q --port=%n --username=%q --dbname=%q%v"},

	DBImport: {format: "%w db import -", target: true},
	MySQL:    {format: mysqlWrapper, target: true},
	PSQL:     {format: pgWrapper + "psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 --host=%q --port=%n --username=%q --dbname=%q", target: true},
	// Backup sets noclobber (set -C), so a backup that another run made
	// in the same second is never overwritten: the redirect fails instead.
	Backup:    {format: "set -C && umask 077 && mkdir -p ~/.roadie-backups && { %w db export - --skip-plugins --skip-themes --single-transaction || echo '" + backupFailed + "' >&2; } | gzip > ~/.roadie-backups/%t.sql.gz", target: true},
	Write:     {format: "umask 077 && cat > %q && mv -f -- %q %q", target: true},
	WriteMode: {format: "umask 077 && cat > %q && chmod %m -- %q && mv -f -- %q %q", target: true},
	// WriteKeep replaces an existing file: it gives the temp file the old
	// owner and group where the ssh user may (a non-root user can often
	// keep only the group, or neither), then the old mode.
	WriteKeep: {format: "umask 077 && cat > %q && { chown %i:%i -- %q 2>/dev/null || chgrp %i -- %q 2>/dev/null || :; } && chmod %m -- %q && mv -f -- %q %q", target: true},
	Stat:      {format: "ls -lnLd -- %q", target: true},
	Mkdir:     {format: "mkdir -p -- %q", target: true},
	Remove:    {format: "rm -f -- %q", target: true},
	// EvalFile skips plugins and themes like every other wp command: a
	// copied plugin that mails on init would otherwise send through the
	// host's sendmail before the connector it installs is there.
	EvalFile: {format: "%w eval-file - --skip-plugins --skip-themes", target: true},
}

// backupFailed is what the backup command prints when wp db export
// fails, since a pipe into gzip hides wp's exit status in POSIX sh.
const backupFailed = "roadie: wp db export failed"

// credsEnd ends the [client] defaults the mysql wrapper reads from stdin.
const credsEnd = "#roadie-end"

// Templates returns every allowlisted template, sorted.
func Templates() []Template {
	out := make([]Template, 0, len(templates))
	for t := range templates {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// TargetOnly reports a template that writes, which a source never runs.
func (t Template) TargetOnly() bool { return templates[t].target }

// ErrNotAllowed is wrapped by every refusal to build a command.
var ErrNotAllowed = errors.New("remote command not in the allowlist")

// Quote is the one POSIX single-quote function every remote argument
// goes through: the remote shell reads the result as exactly s.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// optionNames are the options OptionGet may read.
var optionNames = []string{"siteurl", "home"}

// tsRE is a backup timestamp, UTC 2006-01-02T15-04-05.
var tsRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}$`)

// wpRE is a remote.wp value that can go unquoted, so a ~ in it still expands.
var wpRE = regexp.MustCompile(`^~?[A-Za-z0-9_./-]+$`)

// build fills template t. wp is the WP-CLI binary and root the site path
// for %w. A template outside the allowlist panics under go test, so a
// test can never pass with one; otherwise it is an error.
func build(t Template, wp, root string, args []string) (string, error) {
	tm, ok := templates[t]
	if !ok {
		if testing.Testing() {
			panic(fmt.Sprintf("remote: template %q is not in the allowlist", t))
		}
		return "", fmt.Errorf("%w: %q", ErrNotAllowed, t)
	}
	var b strings.Builder
	f := tm.format
	next := 0
	arg := func() (string, error) {
		if next >= len(args) {
			return "", fmt.Errorf("%w: %s needs more arguments", ErrNotAllowed, t)
		}
		next++
		return args[next-1], nil
	}
	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i+1 >= len(f) || !strings.ContainsRune("qnmitovw", rune(f[i+1])) {
			b.WriteByte(f[i])
			continue
		}
		verb := f[i+1]
		i++
		if verb == 'w' {
			b.WriteString(wpCommand(wp, root))
			continue
		}
		if verb == 'v' {
			for _, a := range args[next:] {
				if err := checkArg(a); err != nil {
					return "", fmt.Errorf("%s: %w", t, err)
				}
				b.WriteString(" " + Quote(a))
			}
			next = len(args)
			continue
		}
		a, err := arg()
		if err != nil {
			return "", err
		}
		switch verb {
		case 'q':
			if err := checkArg(a); err != nil {
				return "", fmt.Errorf("%s: %w", t, err)
			}
			b.WriteString(Quote(a))
		case 'n':
			n, err := strconv.Atoi(a)
			if err != nil || n < 0 || n > 65535 {
				return "", fmt.Errorf("%w: %s takes a number, not %q", ErrNotAllowed, t, a)
			}
			b.WriteString(strconv.Itoa(n))
		case 'm':
			if len(a) != 4 || a[0] != '0' || strings.Trim(a, "01234567") != "" {
				return "", fmt.Errorf("%w: %s takes a mode like 0644, not %q", ErrNotAllowed, t, a)
			}
			b.WriteString(a)
		case 'i':
			if _, err := strconv.ParseUint(a, 10, 32); err != nil {
				return "", fmt.Errorf("%w: %s takes a numeric id, not %q", ErrNotAllowed, t, a)
			}
			b.WriteString(a)
		case 't':
			if !tsRE.MatchString(a) {
				return "", fmt.Errorf("%w: %s takes a UTC timestamp, not %q", ErrNotAllowed, t, a)
			}
			b.WriteString(a)
		case 'o':
			if !slices.Contains(optionNames, a) {
				return "", fmt.Errorf("%w: %s reads only siteurl and home, not %q", ErrNotAllowed, t, a)
			}
			b.WriteString(a)
		}
	}
	if next != len(args) {
		return "", fmt.Errorf("%w: %s takes %d arguments, got %d", ErrNotAllowed, t, next, len(args))
	}
	return b.String(), nil
}

// checkArg refuses bytes no shell word may carry: NUL and line breaks
// (a quoted line break is still a new line in some remote logs).
func checkArg(a string) error {
	if strings.ContainsAny(a, "\x00\n\r") {
		return fmt.Errorf("%w: an argument holds a NUL or a line break", ErrNotAllowed)
	}
	return nil
}

// wpCommand is the WP-CLI binary (remote.wp, default wp) with --path.
func wpCommand(wp, root string) string {
	if wp == "" {
		wp = "wp"
	}
	if !wpRE.MatchString(wp) {
		wp = Quote(wp)
	}
	if root == "" {
		return wp
	}
	return wp + " --path=" + Quote(root)
}

// quotedRE matches one word written by Quote.
const quotedRE = `'(?:[^'\x00\n\r]|'\\'')*'`

// allowRE is the allowlist as regular expressions, one per template.
var allowRE = func() map[Template]*regexp.Regexp {
	verbs := map[byte]string{
		'q': quotedRE,
		'n': `[0-9]+`,
		'm': `0[0-7]{3}`,
		'i': `[0-9]+`,
		't': `[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}`,
		'o': `(?:siteurl|home)`,
		'v': `(?: ` + quotedRE + `)*`,
		'w': `(?:~?[A-Za-z0-9_./-]+|` + quotedRE + `)(?: --path=` + quotedRE + `)?`,
	}
	out := map[Template]*regexp.Regexp{}
	for t, tm := range templates {
		var b strings.Builder
		b.WriteString("^")
		f := tm.format
		lit := 0
		for i := 0; i < len(f); i++ {
			if f[i] == '%' && i+1 < len(f) && verbs[f[i+1]] != "" {
				b.WriteString(regexp.QuoteMeta(f[lit:i]))
				b.WriteString(verbs[f[i+1]])
				i++
				lit = i + 1
			}
		}
		b.WriteString(regexp.QuoteMeta(f[lit:]))
		b.WriteString("$")
		out[t] = regexp.MustCompile(b.String())
	}
	return out
}()

// Allowed reports the template a remote command string matches, so a
// test can check every command an ssh shim recorded (PRD 6.13).
func Allowed(cmd string) (Template, bool) {
	for _, t := range Templates() {
		if allowRE[t].MatchString(cmd) {
			return t, true
		}
	}
	return "", false
}

// List is the allowlist for plan: each template's command with
// placeholders, source ones first.
func List(target bool) []string {
	var out []string
	for _, wantTarget := range []bool{false, true} {
		if wantTarget && !target {
			break
		}
		for _, t := range Templates() {
			if templates[t].target == wantTarget {
				out = append(out, placeholder(templates[t].format))
			}
		}
	}
	return out
}

// placeholder shows a format with readable placeholders.
func placeholder(f string) string {
	return strings.NewReplacer("%w", "wp --path=<path>", "%q", "<arg>", "%n", "<n>", "%m", "<mode>", "%i", "<id>",
		"%t", "<UTC timestamp>", "%o", "<siteurl|home>", "%v", " [<table> ...]").Replace(f)
}
