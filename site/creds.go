// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// dbConfig is what a site's wp-config.php or .env says about its
// database. Password is a secret: it goes only into an Endpoint's
// Password field or onto a remote command's stdin.
type dbConfig struct {
	driver   core.Dialect
	host     string // as the site's own machine reaches it
	port     int    // 0: the driver's default
	socket   string // a MySQL socket from DB_HOST "localhost:/run/mysqld.sock"
	name     string // database name, or the SQLite file
	user     string
	password string
	prefix   string // WordPress table prefix, "" when unknown
}

// fileReader is the part of core.Site the config readers need.
type fileReader interface {
	ReadFile(ctx context.Context, rel string) ([]byte, error)
}

// defaultPort is the port a driver uses when none is set.
func defaultPort(d core.Dialect) int {
	switch d {
	case core.Postgres:
		return 5432
	case core.MySQL, core.MariaDB:
		return 3306
	}
	return 0
}

// siteConfigError is the PRD 17 site-config error for a database setting
// Roadie can't read without running the site's code.
func siteConfigError(file, name, from string, readOnly bool) error {
	side, flagEnv, alias := "target", "ROADIE_TO_PASSWORD", "@staging"
	if readOnly {
		side, flagEnv, alias = "source", "ROADIE_FROM_PASSWORD", "@prod"
	}
	return core.Errorf(core.ErrSiteConfig, 3,
		"site-config: %s sets %s from %s, which Roadie can't run. Pass the %s as a DSN with %s, or use %s through WP-CLI.",
		file, name, from, side, flagEnv, alias)
}

// unreadableConfig is site-config for a config file Roadie can't read,
// so a stop names its code. A canceled run stays canceled.
func unreadableConfig(ctx context.Context, file string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	why := readReason(err)
	if errors.Is(err, fs.ErrNotExist) {
		why = "it isn't there"
	}
	return core.Errorf(core.ErrSiteConfig, 3,
		"site-config: %s can't be read (%s). Make it readable by the user Roadie runs as, or pass the database as a DSN.", file, why)
}

// missingSetting is site-config for a setting the file doesn't hold.
func missingSetting(file, name string, readOnly bool) error {
	side, flagEnv := "target", "ROADIE_TO_PASSWORD"
	if readOnly {
		side, flagEnv = "source", "ROADIE_FROM_PASSWORD"
	}
	return core.Errorf(core.ErrSiteConfig, 3,
		"site-config: %s doesn't set %s. Add it there, or pass the %s as a DSN with %s.",
		file, name, side, flagEnv)
}

// envSourceRe matches a define value that is one plain environment
// lookup, such as getenv('DB_PASSWORD') or $_ENV["DB_PASSWORD"]. Only
// that exact shape is quoted back: a fallback such as
// getenv('X') ?: 'literal' or a concatenation can carry the secret itself.
var envSourceRe = regexp.MustCompile(`^(?:(?:getenv|env)\(\s*['"][A-Za-z0-9_]+['"]\s*\)|\$_(?:ENV|SERVER)\[\s*['"][A-Za-z0-9_]+['"]\s*\])$`)

// exprSource describes where a non-literal define gets its value without
// echoing an arbitrary expression, which could hold the secret itself.
func exprSource(v string) string {
	t := strings.TrimSpace(v)
	if envSourceRe.MatchString(t) {
		return t
	}
	return "a PHP expression"
}

// readDBConfig reads the database settings of a WordPress or Laravel
// site. display names the site folder in messages.
func readDBConfig(ctx context.Context, s fileReader, stack core.Stack, display string, readOnly bool) (dbConfig, error) {
	switch stack {
	case core.StackWordPress:
		return readWPConfig(ctx, s, display, readOnly)
	case core.StackLaravel:
		return readEnvConfig(ctx, s, display, readOnly)
	}
	return dbConfig{}, core.Errorf(core.ErrSiteConfig, 3,
		"site-config: %s has no wp-config.php or Laravel .env to read the database from. Pass the database as a DSN instead.", display)
}

// joinDisplay names a file inside a site for messages.
func joinDisplay(display, rel string) string {
	if strings.Contains(display, "\\") && !strings.Contains(display, "/") {
		return display + "\\" + rel
	}
	return strings.TrimSuffix(display, "/") + "/" + rel
}

func readWPConfig(ctx context.Context, s fileReader, display string, readOnly bool) (dbConfig, error) {
	file := joinDisplay(display, "wp-config.php")
	b, err := s.ReadFile(ctx, "wp-config.php")
	if err != nil {
		return dbConfig{}, unreadableConfig(ctx, file, err)
	}
	f, err := wpconfig.Parse(b)
	if err != nil {
		return dbConfig{}, core.Errorf(core.ErrSiteConfig, 3, "site-config: %s can't be read: %v", file, err)
	}
	vals := map[string]string{}
	for _, n := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"} {
		d, ok := f.Lookup(n)
		if !ok {
			return dbConfig{}, missingSetting(file, n, readOnly)
		}
		if !d.Literal {
			return dbConfig{}, siteConfigError(file, n, exprSource(d.Value), readOnly)
		}
		vals[n] = d.Value
	}
	c := dbConfig{driver: core.MySQL, name: vals["DB_NAME"], user: vals["DB_USER"], password: vals["DB_PASSWORD"], prefix: f.TablePrefix()}
	c.host, c.port, c.socket = splitWPHost(vals["DB_HOST"])
	return c, nil
}

// splitWPHost reads DB_HOST the way WordPress does: "host", "host:port",
// "host:/path/to.sock", "[::1]:3307" or ":/path/to.sock".
func splitWPHost(h string) (host string, port int, socket string) {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			host, rest := h[1:i], h[i+1:]
			if p, err := strconv.Atoi(strings.TrimPrefix(rest, ":")); err == nil {
				return host, p, ""
			}
			return host, 0, ""
		}
	}
	i := strings.Index(h, ":")
	if i < 0 || strings.Count(h, ":") > 1 && !strings.Contains(h[i+1:], "/") {
		return h, 0, "" // a bare IPv6 address
	}
	host, rest := h[:i], h[i+1:]
	if host == "" {
		host = "localhost"
	}
	if strings.HasPrefix(rest, "/") {
		return host, 0, rest
	}
	if p, err := strconv.Atoi(rest); err == nil {
		return host, p, ""
	}
	return host, 0, ""
}

// envDrivers maps Laravel's DB_CONNECTION to a dialect.
var envDrivers = map[string]core.Dialect{
	"mysql":    core.MySQL,
	"mariadb":  core.MariaDB,
	"pgsql":    core.Postgres,
	"postgres": core.Postgres,
	"sqlite":   core.SQLite,
}

func readEnvConfig(ctx context.Context, s fileReader, display string, readOnly bool) (dbConfig, error) {
	file := joinDisplay(display, ".env")
	b, err := s.ReadFile(ctx, ".env")
	if err != nil {
		return dbConfig{}, unreadableConfig(ctx, file, err)
	}
	f, err := dotenv.Parse(b)
	if err != nil {
		return dbConfig{}, core.Errorf(core.ErrSiteConfig, 3, "site-config: %s can't be read: %v", file, err)
	}
	unresolved := f.Unresolved()
	get := func(n string) (string, bool, error) {
		p, ok := f.Get(n)
		if !ok {
			return "", false, nil
		}
		for _, u := range unresolved {
			if u == n {
				return "", true, siteConfigError(file, n, "another variable (${...})", readOnly)
			}
		}
		return p.Value, true, nil
	}
	conn, ok, err := get("DB_CONNECTION")
	if err != nil {
		return dbConfig{}, err
	}
	if !ok || conn == "" {
		return dbConfig{}, missingSetting(file, "DB_CONNECTION", readOnly)
	}
	driver, known := envDrivers[strings.ToLower(conn)]
	if !known {
		return dbConfig{}, core.Errorf(core.ErrSiteConfig, 3,
			"site-config: %s sets DB_CONNECTION=%s; Roadie reads mysql, mariadb, pgsql and sqlite. Pass the database as a DSN instead.", file, conn)
	}
	c := dbConfig{driver: driver}
	vals := map[string]string{}
	for _, n := range []string{"DB_HOST", "DB_PORT", "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD"} {
		v, _, err := get(n)
		if err != nil {
			return dbConfig{}, err
		}
		vals[n] = v
	}
	if driver == core.SQLite {
		c.name = vals["DB_DATABASE"]
		if c.name == "" {
			c.name = "database/database.sqlite"
		}
		return c, nil
	}
	if vals["DB_DATABASE"] == "" {
		return dbConfig{}, missingSetting(file, "DB_DATABASE", readOnly)
	}
	c.host = vals["DB_HOST"]
	if c.host == "" {
		c.host = "127.0.0.1"
	}
	if vals["DB_PORT"] != "" {
		p, err := strconv.Atoi(vals["DB_PORT"])
		if err != nil || p < 1 || p > 65535 {
			return dbConfig{}, core.Errorf(core.ErrSiteConfig, 3, "site-config: %s has a bad DB_PORT. Fix it, or pass the database as a DSN.", file)
		}
		c.port = p
	}
	c.name, c.user, c.password = vals["DB_DATABASE"], vals["DB_USERNAME"], vals["DB_PASSWORD"]
	return c, nil
}

// endpoint is the DSN endpoint of the database a local site reaches, for
// db.Open. root resolves a relative SQLite path.
func (c dbConfig) endpoint(root string) core.Endpoint {
	if c.driver == core.SQLite {
		p := c.name
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		u := url.URL{Scheme: "sqlite", Path: "/" + strings.TrimPrefix(filepath.ToSlash(p), "/")}
		return core.Endpoint{Kind: core.DSN, Driver: core.SQLite, Path: p, DSN: u.String(), Display: u.String()}
	}
	port := c.port
	if port == 0 {
		port = defaultPort(c.driver)
	}
	scheme := string(c.driver)
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(c.host, strconv.Itoa(port)), Path: "/" + c.name}
	if c.user != "" {
		u.User = url.User(c.user)
	}
	if c.socket != "" {
		u.RawQuery = url.Values{"socket": {c.socket}}.Encode()
	}
	e := core.Endpoint{Kind: core.DSN, Driver: c.driver, Host: c.host, Port: port, User: c.user, Path: c.name, DSN: u.String(), Password: c.password}
	// Display leaves the user out with the password: both come from the
	// site's own config, not from what the user typed. On shared hosting
	// the database user is often the account holder's name.
	shown := u
	shown.User = nil
	e.Display = shown.String()
	return e
}

// identity is the DBIdentity of the database. server is the machine the
// site runs on ("" for this one), so two loopback hosts on different
// machines never look like one database.
func (c dbConfig) identity(server string) core.DBIdentity {
	port := c.port
	if port == 0 {
		port = defaultPort(c.driver)
	}
	host := strings.ToLower(c.host)
	if c.socket != "" {
		host = "localhost"
	}
	return core.DBIdentity{Engine: c.driver, Server: server, Host: host, Port: port, Name: c.name, User: c.user, Prefix: c.prefix}
}
