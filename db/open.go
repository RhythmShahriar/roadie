// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/RhythmShahriar/roadie/core"
)

// dialTimeout bounds the connect, so a firewalled host fails in seconds
// instead of the system's TCP timeout.
const dialTimeout = 15 * time.Second

func open(ctx context.Context, e core.Endpoint, m Mode) (*Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		sdb *sql.DB
		err error
	)
	switch e.Driver {
	case core.MySQL, core.MariaDB:
		var cfg *mysql.Config
		if cfg, err = mysqlConfig(e); err == nil {
			connector, cerr := mysql.NewConnector(cfg)
			if cerr != nil {
				return nil, cerr
			}
			sdb = sql.OpenDB(connector)
		}
	case core.Postgres:
		var cfg *pgx.ConnConfig
		if cfg, err = pgConfig(e, m); err == nil {
			sdb = stdlib.OpenDB(*cfg)
		}
	case core.SQLite:
		var dsn string
		if dsn, err = sqliteDSN(e, m); err == nil {
			sdb, err = sql.Open("sqlite", dsn)
		}
	default:
		return nil, fmt.Errorf("db: %s is not a mysql://, mariadb://, postgres:// or sqlite:/// DSN", e.Display)
	}
	if err != nil {
		return nil, err
	}
	// One session: the read-only snapshot, SET statements and the
	// per-table transactions all live on it.
	sdb.SetMaxOpenConns(1)
	conn, err := sdb.Conn(ctx)
	if err != nil {
		_ = sdb.Close()
		return nil, &ConnectError{Display: e.Display, Err: err}
	}
	c := &Conn{ep: e, mode: m, engine: e.Driver, db: sdb, conn: conn, dbName: e.Path, user: e.User}
	if err := c.hello(ctx); err != nil {
		_ = c.close()
		return nil, err
	}
	if m == ReadOnly {
		if err := c.beginReadOnly(ctx); err != nil {
			_ = c.close()
			return nil, err
		}
	}
	return c, nil
}

// mysqlConfig is the driver configuration for a MySQL or MariaDB
// endpoint. LOCAL INFILE, multi-statements and client-side interpolation
// stay off whatever the DSN says: a dump's text never reaches the server
// as more than one statement and a value is never spliced into SQL.
func mysqlConfig(e core.Endpoint) (*mysql.Config, error) {
	cfg := mysql.NewConfig()
	cfg.User = e.User
	cfg.Passwd = e.Password
	cfg.DBName = e.Path
	cfg.Net = "tcp"
	host := e.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := e.Port
	if port == 0 {
		port = 3306
	}
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	q := dsnQuery(e)
	if s := q.Get("socket"); s != "" {
		cfg.Net, cfg.Addr = "unix", s
	}
	switch v := q.Get("tls"); v {
	case "", "false":
	case "true", "skip-verify", "preferred":
		cfg.TLSConfig = v
	default:
		return nil, fmt.Errorf("db: %s: tls must be true, false, skip-verify or preferred", e.Display)
	}
	cfg.AllowAllFiles = false
	cfg.MultiStatements = false
	cfg.ParseTime = false
	cfg.InterpolateParams = false
	cfg.Timeout = dialTimeout
	// The driver logs retries to stderr on its own, which would break
	// into Roadie's output; every error reaches the caller anyway.
	cfg.Logger = mysql.Logger(log.New(io.Discard, "", 0))
	if err := cfg.Apply(mysql.Charset("utf8mb4", "")); err != nil {
		return nil, err
	}
	return cfg, nil
}

// pgConfig is the pgx configuration for a PostgreSQL endpoint. The
// password is set from the endpoint after parsing, so neither PGPASSWORD
// nor a .pgpass file can supply one. A read-only connection also asks the
// server to make every transaction read-only.
func pgConfig(e core.Endpoint, m Mode) (*pgx.ConnConfig, error) {
	u := url.URL{Scheme: "postgres", Path: "/" + e.Path}
	if e.User != "" {
		u.User = url.User(e.User)
	}
	host := e.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := e.Port
	if port == 0 {
		port = 5432
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	q := url.Values{}
	src := dsnQuery(e)
	for _, k := range []string{"sslmode", "sslrootcert", "application_name", "search_path"} {
		if v := src.Get(k); v != "" {
			q.Set(k, v)
		}
	}
	q.Set("connect_timeout", strconv.Itoa(int(dialTimeout/time.Second)))
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("db: %s: %w", e.Display, err)
	}
	cfg.Password = e.Password
	if m == ReadOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	return cfg, nil
}

// sqliteDSN is the modernc.org/sqlite name for a SQLite endpoint: a
// file: URI, opened read-only (mode=ro, query_only) for a source.
func sqliteDSN(e core.Endpoint, m Mode) (string, error) {
	if e.Path == "" {
		return "", fmt.Errorf("db: %s names no SQLite file", e.Display)
	}
	p, err := filepath.Abs(e.Path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		// The driver would say only "unable to open database file".
		return "", core.Errorf(core.ErrNotFound, 2,
			"not-found: the SQLite file %s doesn't exist. Roadie never creates one: create it (touch %s) or check the path.", p, p)
	}
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x is the URI path /C:/x
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	if m == ReadOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("mode", "rw") // never create a missing file
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: q.Encode()}).String(), nil
}

// dsnQuery is the query of the endpoint's DSN (socket, tls, sslmode).
func dsnQuery(e core.Endpoint) url.Values {
	if e.DSN == "" {
		return url.Values{}
	}
	u, err := url.Parse(e.DSN)
	if err != nil {
		return url.Values{}
	}
	return u.Query()
}

// hello reads the server version, the engine and the database name.
func (c *Conn) hello(ctx context.Context) error {
	var v string
	switch c.engine {
	case core.MySQL, core.MariaDB:
		var name sql.NullString
		if err := c.conn.QueryRowContext(ctx, "SELECT VERSION(), DATABASE()").Scan(&v, &name); err != nil {
			return fmt.Errorf("db: %s: %w", c.ep.Display, err)
		}
		if strings.Contains(strings.ToLower(v), "mariadb") {
			c.engine = core.MariaDB
		} else {
			c.engine = core.MySQL
		}
		if c.dbName == "" {
			c.dbName = name.String
		}
		if c.dbName == "" {
			return fmt.Errorf("db: %s names no database", c.ep.Display)
		}
	case core.Postgres:
		var name, user string
		if err := c.conn.QueryRowContext(ctx, "SELECT current_setting('server_version'), current_database(), current_user").Scan(&v, &name, &user); err != nil {
			return fmt.Errorf("db: %s: %w", c.ep.Display, err)
		}
		c.dbName = name
		if c.user == "" {
			c.user = user
		}
	case core.SQLite:
		if err := c.conn.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
			return fmt.Errorf("db: %s: %w", c.ep.Display, err)
		}
		if abs, err := filepath.Abs(c.ep.Path); err == nil {
			c.dbName = abs
		}
	}
	c.server = serverVersion(v)
	return nil
}

// serverVersion is the plain version number of a server's version text:
// "8.4.3" from "8.4.3", "10.6.12" from "5.5.5-10.6.12-MariaDB-log",
// "16.4" from "16.4 (Debian 16.4-1.pgdg120+2)".
func serverVersion(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(strings.ToLower(v), "mariadb") {
		v = strings.TrimPrefix(v, "5.5.5-") // the old replication prefix
	}
	if i := strings.IndexAny(v, " -+"); i >= 0 {
		v = v[:i]
	}
	return v
}

// beginReadOnly opens the read-only transaction every read of a source
// runs in: one consistent snapshot of the whole database.
func (c *Conn) beginReadOnly(ctx context.Context) error {
	var stmts []string
	switch c.engine {
	case core.MySQL, core.MariaDB:
		stmts = []string{"SET SESSION TRANSACTION READ ONLY", "START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY"}
	case core.Postgres:
		stmts = []string{"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"}
	case core.SQLite:
		stmts = []string{"BEGIN"}
	}
	for _, s := range stmts {
		if _, err := c.conn.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("db: %s: start the read-only transaction: %w", c.ep.Display, err)
		}
	}
	c.inTx = true
	return nil
}

func (c *Conn) identity(ctx context.Context) (core.DBIdentity, error) {
	if c.closed {
		return core.DBIdentity{}, errClosed
	}
	id := core.DBIdentity{Engine: c.engine, Server: c.server, Host: strings.ToLower(c.ep.Host), Port: c.ep.Port, Name: c.dbName, User: c.user}
	if id.Port == 0 {
		switch c.engine {
		case core.MySQL, core.MariaDB:
			id.Port = 3306
		case core.Postgres:
			id.Port = 5432
		}
	}
	if c.engine != core.SQLite && id.Host == "" {
		id.Host = "127.0.0.1"
	}
	if c.engine == core.SQLite {
		id.Host, id.Port = "", 0
	}
	s, err := c.readSchema(ctx)
	if err != nil {
		return core.DBIdentity{}, err
	}
	id.Prefix = s.Prefix
	return id, nil
}

// errClosed is a call on a closed connection.
var errClosed = errors.New("db: the connection is closed")

// errBusy is a call while a Source or Sink holds the session.
var errBusy = errors.New("db: a source or sink is using this connection")

func (c *Conn) close() error {
	if c == nil || c.closed {
		return nil
	}
	var errs []error
	if c.active != nil {
		// An unfinished Source holds open rows, which would block the
		// rollback below.
		errs = append(errs, c.active.Close())
	}
	c.closed = true
	if c.inTx {
		// A read-only transaction has nothing to keep.
		_, _ = c.conn.ExecContext(context.Background(), "ROLLBACK")
		c.inTx = false
	}
	if c.conn != nil {
		errs = append(errs, c.conn.Close())
	}
	if c.db != nil {
		errs = append(errs, c.db.Close())
	}
	return errors.Join(errs...)
}

// ConnectError is a connection to a database that failed: the server
// didn't answer, or it answered with a refusal (an unknown database, a
// wrong password, a user without rights).
type ConnectError struct {
	Display string // the DSN without its password
	Err     error
}

func (e *ConnectError) Error() string { return "db: connect to " + e.Display + ": " + e.Err.Error() }

func (e *ConnectError) Unwrap() error { return e.Err }

// Reason is the server's answer with every quoted part masked, as the
// import errors word it.
func (e *ConnectError) Reason() string { return describe(e.Err) }
