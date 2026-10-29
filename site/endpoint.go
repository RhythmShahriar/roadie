// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/remote"
)

// masked stands in for a password in Display.
const masked = "•••"

// dumpExts are the names that make a path a dump file even before it
// exists (a target), longest first so ".sql.gz" wins over ".gz".
var dumpExts = []struct{ ext, compression string }{
	{".sql.gz", "gzip"},
	{".sql.zst", "zstd"},
	{".sql.bz2", "bzip2"},
	{".sql", ""},
	{".dump", ""},
}

// dsnDrivers maps a DSN scheme to its dialect.
var dsnDrivers = map[string]core.Dialect{
	"mysql":      core.MySQL,
	"mariadb":    core.MariaDB,
	"postgres":   core.Postgres,
	"postgresql": core.Postgres,
	"sqlite":     core.SQLite,
}

// passwordParams are query parameters that carry a password; they leave
// the DSN like the user-info password does.
var passwordParams = []string{"password", "pass", "pwd", "sslpassword"}

// statFunc is os.Stat, replaced in tests.
var statFunc = os.Stat

func parseEndpoint(s string) (core.Endpoint, error) {
	switch {
	case s == "":
		return core.Endpoint{}, cli.Usagef("endpoint: give a dump file, site folder, ssh://, @alias or DSN")
	case s == "-":
		return core.Endpoint{Kind: core.Stdio, Raw: s, Display: "-"}, nil
	case strings.HasPrefix(s, "@"):
		return parseAlias(s)
	}
	if scheme, _, ok := strings.Cut(s, "://"); ok && !isWindowsPath(s) {
		lower := strings.ToLower(scheme)
		if _, ok := dsnDrivers[lower]; ok {
			return parseDSN(s, lower)
		}
		if lower == "ssh" {
			return parseSSHURL(s)
		}
		return core.Endpoint{}, notAnEndpoint(s)
	}
	// An existing local file or folder wins over the scp-like form, so a
	// local name holding '@' and ':' still reads as local.
	if user, host, path, ok := splitSCP(s); ok && !exists(s) {
		// Mask the password before any message can print the user part.
		if name, _, has := strings.Cut(user, ":"); has {
			shown := name + ":" + masked + s[len(user):]
			return core.Endpoint{}, cli.Usagef("endpoint: %s holds a password; Roadie uses your ssh keys and agent, never an ssh password", shown)
		}
		return sshEndpoint(s, user, host, 0, path)
	}
	return parseLocal(s)
}

// notAnEndpoint is the exit 2 error for a value that has no known shape.
func notAnEndpoint(s string) error {
	return cli.Usagef("endpoint: %s is not a dump file, site folder, ssh://, @alias or DSN", redact(s))
}

// redact hides a password in a URL-shaped value before it is printed:
// the user-info password and every passwordParams query value.
func redact(s string) string {
	s = maskQueryPasswords(s)
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		if strings.Contains(s, "://") && strings.Contains(s, "@") {
			// A value too broken for url.Parse may still hold user:pass@.
			scheme, rest, _ := strings.Cut(s, "://")
			if i := strings.LastIndexByte(rest, '@'); i >= 0 {
				return scheme + "://" + masked + rest[i:]
			}
		}
		return s
	}
	if _, has := u.User.Password(); !has {
		return s
	}
	return withMaskedPassword(u)
}

// maskQueryPasswords replaces the value of every passwordParams query
// parameter with "•••". It works on the raw text so that it still holds
// when url.Parse fails. Each key is decoded and lowercased first, so a key
// parseDSN would read as a password (pass%77ord, PASSWORD) is masked too.
func maskQueryPasswords(s string) string {
	before, query, ok := strings.Cut(s, "?")
	if !ok {
		return s
	}
	query, frag, hasFrag := strings.Cut(query, "#")
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		key, _, has := strings.Cut(pair, "=")
		if !has {
			continue
		}
		name, err := url.QueryUnescape(key)
		if err != nil {
			name = key
		}
		name = strings.ToLower(name)
		for _, k := range passwordParams {
			if name == k {
				pairs[i] = key + "=" + masked
				break
			}
		}
	}
	out := before + "?" + strings.Join(pairs, "&")
	if hasFrag {
		out += "#" + frag
	}
	return out
}

// withMaskedPassword prints u with its password, if any, as "•••".
func withMaskedPassword(u *url.URL) string {
	v := *u
	_, has := u.User.Password()
	v.User = url.User(u.User.Username())
	out := v.String()
	if has {
		// The escaped user name can't hold '@', so the first '@' after
		// "://" ends the user info.
		scheme, rest, _ := strings.Cut(out, "://")
		if i := strings.IndexByte(rest, '@'); i >= 0 {
			out = scheme + "://" + rest[:i] + ":" + masked + rest[i:]
		}
	}
	return out
}

func parseAlias(s string) (core.Endpoint, error) {
	name := s[1:]
	if name == "" {
		return core.Endpoint{}, cli.Usagef("endpoint: @ needs an alias name from wp-cli.yml, such as @staging")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return core.Endpoint{}, cli.Usagef("endpoint: %q is not a WP-CLI alias name (letters, digits, '.', '_' and '-')", s)
		}
	}
	return core.Endpoint{Kind: core.WPAlias, Raw: s, Display: s, Alias: s}, nil
}

func parseDSN(s, scheme string) (core.Endpoint, error) {
	driver := dsnDrivers[scheme]
	u, err := url.Parse(s)
	if err != nil {
		return core.Endpoint{}, cli.Usagef("endpoint: %s is not a valid %s DSN", redact(s), scheme)
	}
	e := core.Endpoint{Kind: core.DSN, Raw: s, Driver: driver}
	if u.User != nil {
		e.User = u.User.Username()
		if pw, has := u.User.Password(); has {
			e.Password = pw
		}
	}
	q := u.Query()
	// Keys match in any case (PASSWORD, Password), as maskQueryPasswords
	// does, so no spelling of a password key stays in DSN or Display.
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, p := range passwordParams {
		for _, k := range keys {
			if strings.ToLower(k) != p {
				continue
			}
			if e.Password == "" {
				e.Password = q.Get(k)
			}
			q.Del(k)
		}
	}
	clean := *u
	if u.User != nil {
		clean.User = url.User(u.User.Username())
		if e.User == "" {
			clean.User = nil
		}
	}
	if u.RawQuery != "" {
		clean.RawQuery = q.Encode()
	}
	e.DSN = clean.String()
	if driver == core.SQLite {
		// sqlite:///abs/path: no host, an absolute path; on Windows the
		// path is /C:/x, which names C:/x.
		if u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") {
			return core.Endpoint{}, cli.Usagef("endpoint: %s: a SQLite DSN is sqlite:///absolute/path.sqlite", redact(s))
		}
		e.Path = u.Path
		if len(e.Path) >= 3 && e.Path[2] == ':' && isLetter(e.Path[1]) {
			e.Path = e.Path[1:]
		}
		e.Path = filepath.FromSlash(e.Path)
		e.Display = e.DSN
		return e, nil
	}
	e.Host = u.Hostname()
	if e.Host == "" {
		return core.Endpoint{}, cli.Usagef("endpoint: %s has no host", redact(s))
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return core.Endpoint{}, cli.Usagef("endpoint: %s has a bad port", redact(s))
		}
		e.Port = n
	}
	e.Path = strings.TrimPrefix(u.Path, "/")
	e.Display = clean.String()
	if e.Password != "" {
		e.Display = withMaskedPassword(&url.URL{Scheme: clean.Scheme, User: url.UserPassword(e.User, "x"), Host: clean.Host, Path: clean.Path, RawPath: clean.RawPath, RawQuery: clean.RawQuery})
	}
	return e, nil
}

// parseSSHURL reads ssh://[user@]host[:port]/path by hand, not with
// url.Parse, so a host holding spaces or shell characters reaches the
// host check and is refused with that reason.
func parseSSHURL(s string) (core.Endpoint, error) {
	_, rest, _ := strings.Cut(s, "://")
	if strings.ContainsAny(rest, "?#") {
		return core.Endpoint{}, cli.Usagef("endpoint: %s: an ssh:// address takes no ?query or #fragment", redact(s))
	}
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	user, hostport := "", authority
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		user, hostport = authority[:i], authority[i+1:]
		if strings.Contains(user, ":") {
			return core.Endpoint{}, cli.Usagef("endpoint: %s holds a password; Roadie uses your ssh keys and agent, never an ssh password", redact(s))
		}
		if u, err := url.PathUnescape(user); err == nil {
			user = u
		}
	}
	host, port := hostport, 0
	portText := ""
	if strings.HasPrefix(hostport, "[") {
		if end := strings.IndexByte(hostport, ']'); end >= 0 {
			host, portText = hostport[:end+1], strings.TrimPrefix(hostport[end+1:], ":")
			if hostport[end+1:] != "" && !strings.HasPrefix(hostport[end+1:], ":") {
				return core.Endpoint{}, cli.Usagef("endpoint: %s has a bad port", s)
			}
		}
	} else if i := strings.LastIndexByte(hostport, ':'); i >= 0 {
		host, portText = hostport[:i], hostport[i+1:]
	}
	if portText != "" || strings.HasSuffix(hostport, ":") {
		n, err := strconv.Atoi(portText)
		if err != nil || n < 1 || n > 65535 {
			return core.Endpoint{}, cli.Usagef("endpoint: %s has a bad port", s)
		}
		port = n
	}
	return sshEndpoint(s, user, host, port, path)
}

// splitSCP reads the scp-like user@host:/path form. A Windows path
// (C:\x, C:/x, \\server\share) is never one.
func splitSCP(s string) (user, host, path string, ok bool) {
	if isWindowsPath(s) {
		return "", "", "", false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "", "", "", false
	}
	user, rest := s[:at], s[at+1:]
	if strings.ContainsAny(user, "/\\") {
		return "", "", "", false
	}
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]:")
		if end < 0 {
			return "", "", "", false
		}
		host, path = rest[:end+1], rest[end+2:]
	} else {
		var found bool
		host, path, found = strings.Cut(rest, ":")
		if !found {
			return "", "", "", false
		}
	}
	return user, host, path, path != ""
}

func sshEndpoint(raw, user, host string, port int, path string) (core.Endpoint, error) {
	if err := remote.CheckHost(host); err != nil {
		return core.Endpoint{}, cli.Usagef("endpoint: ssh host %q is refused: %v", host, unwrapBad(err))
	}
	if err := remote.CheckUser(user); err != nil {
		return core.Endpoint{}, cli.Usagef("endpoint: ssh user %q is refused: %v", user, unwrapBad(err))
	}
	if path == "" {
		return core.Endpoint{}, cli.Usagef("endpoint: %s needs the site's folder on the host, such as ssh://deploy@shop.com/var/www/shop", raw)
	}
	// Every remote command single-quotes the site folder, so a leading ~
	// would never expand: refuse it rather than look in a folder named ~.
	if strings.HasPrefix(path, "~") || strings.HasPrefix(path, "/~") && strings.HasPrefix(raw, "ssh://") {
		return core.Endpoint{}, cli.Usagef("endpoint: %s: give the absolute folder on the host, such as /home/deploy/www; Roadie doesn't expand '~'", raw)
	}
	for _, c := range path {
		if c < ' ' || c == 0x7f {
			return core.Endpoint{}, cli.Usagef("endpoint: the remote path in %q holds a control character", raw)
		}
	}
	return core.Endpoint{Kind: core.SSHSite, Raw: raw, Display: raw, User: user, Host: host, Port: port, Path: path}, nil
}

// unwrapBad drops the ErrBadHost prefix, which the endpoint message says
// in its own words.
func unwrapBad(err error) string {
	msg := err.Error()
	return strings.TrimPrefix(msg, remote.ErrBadHost.Error()+": ")
}

func parseLocal(s string) (core.Endpoint, error) {
	lower := strings.ToLower(s)
	for _, d := range dumpExts {
		if strings.HasSuffix(lower, d.ext) {
			return core.Endpoint{Kind: core.DumpFile, Raw: s, Display: s, Path: s, Compression: d.compression}, nil
		}
	}
	fi, err := statFunc(s)
	switch {
	case err == nil && fi.Mode().IsRegular():
		return core.Endpoint{Kind: core.DumpFile, Raw: s, Display: s, Path: s}, nil
	case err == nil && fi.IsDir():
		return core.Endpoint{Kind: core.LocalSite, Raw: s, Display: s, Path: s}, nil
	case err != nil && !errors.Is(err, os.ErrNotExist) && !unnamable(err):
		return core.Endpoint{}, cli.Usagef("endpoint: %s can't be read: %v", s, pathErr(err))
	}
	return core.Endpoint{}, notAnEndpoint(s)
}

func exists(s string) bool {
	_, err := statFunc(s)
	return err == nil
}

// pathErr gives the cause of a stat error without repeating the path.
func pathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// isWindowsPath reports a drive path (C:\x, C:/x) or a UNC path
// (\\server\share), which is local even on a Unix machine.
func isWindowsPath(s string) bool {
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	return len(s) >= 3 && isLetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// InlinePassword reports a DSN whose password was typed in the value
// itself, so the caller prints InlinePasswordWarning (PRD 6.2). Call it
// before filling Password from ROADIE_FROM_PASSWORD or ROADIE_TO_PASSWORD.
func InlinePassword(e core.Endpoint) bool { return e.Kind == core.DSN && e.Password != "" }

// InlinePasswordWarning is the PRD 6.2 warning for --from or --to (flag
// is "from" or "to").
func InlinePasswordWarning(flag string) string {
	return fmt.Sprintf("the password in --%s is visible to other users of this machine; use ROADIE_%s_PASSWORD", flag, strings.ToUpper(flag))
}
