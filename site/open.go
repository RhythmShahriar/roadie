// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/site/remote"
)

// dbConn is the part of *db.Conn a site uses, so tests can stand in for
// the io lane's driver.
type dbConn interface {
	Identity(ctx context.Context) (core.DBIdentity, error)
	Source(ctx context.Context, tables []string) (core.Source, error)
	Sink(ctx context.Context, o db.SinkOptions) (core.Sink, error)
	Close() error
}

// The io lane's entry points, replaced in tests.
var (
	openDB = func(ctx context.Context, e core.Endpoint, m db.Mode) (dbConn, error) {
		c, err := db.Open(ctx, e, m)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	openDump      = dump.Open
	newStreamSink = dump.NewStreamSink
	newFileSink   = dump.NewFileSink
	// now gives backup timestamps.
	now = time.Now
	// getwd is where WP-CLI aliases and .roadie/backups are found from.
	getwd = os.Getwd
)

// errNotSite is returned by Open for an endpoint that names no site.
var errNotSite = errors.New("not a site endpoint")

func open(ctx context.Context, e core.Endpoint, o Options) (core.Site, error) {
	switch e.Kind {
	case core.LocalSite:
		return openLocal(ctx, e, e.Path, o)
	case core.SSHSite:
		return openSSH(ctx, e, o)
	case core.WPAlias:
		return openAlias(ctx, e, o)
	case core.DSN:
		return openDSN(e, o), nil
	}
	return nil, fmt.Errorf("site: %s is a %s: %w", e.Display, e.Kind, errNotSite)
}

// openAlias resolves a WP-CLI alias from the working folder up and opens
// the ssh or local site it names. The site keeps the alias endpoint, with
// the host and path filled in.
func openAlias(ctx context.Context, e core.Endpoint, o Options) (core.Site, error) {
	dir, err := getwd()
	if err != nil {
		return nil, err
	}
	r, err := remote.ResolveAlias(dir, e.Alias)
	if err != nil {
		return nil, err
	}
	merged := e
	merged.User, merged.Host, merged.Port, merged.Path = r.User, r.Host, r.Port, r.Path
	if r.Kind == core.LocalSite {
		return openLocal(ctx, merged, r.Path, o)
	}
	return openSSH(ctx, merged, o)
}

func detectStack(ctx context.Context, s core.Site) (core.Stack, error) {
	if s == nil {
		return "", errors.New("site: no site to detect")
	}
	if s.Endpoint().Kind == core.DSN {
		return core.StackSQL, nil
	}
	for _, rel := range []string{"wp-config.php", "wp-includes/version.php"} {
		ok, err := s.Exists(ctx, rel)
		if err != nil {
			return "", err
		}
		if ok {
			return core.StackWordPress, nil
		}
	}
	artisan, err := s.Exists(ctx, "artisan")
	if err != nil {
		return "", err
	}
	if artisan {
		env, err := s.Exists(ctx, ".env")
		if err != nil {
			return "", err
		}
		if env {
			return core.StackLaravel, nil
		}
	}
	return core.StackSQL, nil
}

// cleanRel checks a site-relative path: forward slashes, inside the site.
func cleanRel(rel string) (string, error) {
	r := path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	if r == "." || r == "" {
		return ".", nil
	}
	if path.IsAbs(r) || r == ".." || strings.HasPrefix(r, "../") || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("site: %q is outside the site folder", rel)
	}
	return r, nil
}

// readWP reports a WP-CLI call that only reads, the ones a source may run.
func readWP(args []string) bool {
	switch {
	case len(args) == 2 && args[0] == "core" && args[1] == "version":
		return true
	case len(args) == 3 && args[0] == "option" && args[1] == "get" && (args[2] == "siteurl" || args[2] == "home"):
		return true
	}
	return false
}

// connSource closes the database connection with the source.
type connSource struct {
	core.Source
	conn dbConn
}

func (s *connSource) Close() error {
	return errors.Join(s.Source.Close(), s.conn.Close())
}

// connSink closes the database connection when the import ends.
type connSink struct {
	core.Sink
	conn dbConn
}

func (s *connSink) Commit(ctx context.Context) error {
	return errors.Join(s.Sink.Commit(ctx), s.conn.Close())
}

func (s *connSink) Abort(ctx context.Context) error {
	return errors.Join(s.Sink.Abort(ctx), s.conn.Close())
}

// dbSource opens a read-only export of the database e names.
func dbSource(ctx context.Context, e core.Endpoint, tables []string) (core.Source, error) {
	c, err := openDB(ctx, e, db.ReadOnly)
	if err != nil {
		return nil, err
	}
	src, err := c.Source(ctx, tables)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return &connSource{Source: src, conn: c}, nil
}

// dbSink opens an import into the database e names.
func dbSink(ctx context.Context, e core.Endpoint, fix bool) (core.Sink, error) {
	c, err := openDB(ctx, e, db.ReadWrite)
	if err != nil {
		return nil, err
	}
	sk, err := c.Sink(ctx, db.SinkOptions{FixCollation: fix})
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return &connSink{Sink: sk, conn: c}, nil
}

// copyAll streams every batch of src into sink and commits it; on any
// error the sink is aborted.
func copyAll(ctx context.Context, src core.Source, sink core.Sink) error {
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil {
			err = sink.Write(ctx, b)
		}
		if err != nil {
			return errors.Join(err, sink.Abort(ctx))
		}
	}
	return sink.Commit(ctx)
}

// backupStamp is the UTC timestamp a backup file is named after.
func backupStamp() string { return now().UTC().Format("2006-01-02T15-04-05") }

// localBackup writes src to <dir>/<UTC ts>.sql.gz, the folder 0700 and
// the file 0600 (dump.NewFileSink writes 0600).
func localBackup(ctx context.Context, dir string, dialect core.Dialect, src func(context.Context) (core.Source, error)) (string, error) {
	if dir == "" {
		wd, err := getwd()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(wd, ".roadie", "backups")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a folder needs x; 0700 keeps it private
		return "", fmt.Errorf("backup: %w", err)
	}
	p := filepath.Join(dir, backupStamp()+".sql.gz")
	s, err := src(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	sink, err := newFileSink(p, dump.WriterOptions{Target: dialect, Compression: "gzip"})
	if err != nil {
		return "", err
	}
	if err := copyAll(ctx, s, sink); err != nil {
		return "", err
	}
	return p, nil
}

// shellWord writes s for a printed command line: bare when it is safe,
// else single-quoted.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=-~") == "" {
		return s
	}
	return remote.Quote(s)
}

// clientRestore is the restore command for a database backup through the
// database's own client. It never holds a password: mysql -p asks for it.
func clientRestore(p string, e core.Endpoint) string {
	in := "gunzip -c " + shellWord(p) + " | "
	switch e.Driver {
	case core.Postgres:
		cmd := in + "psql"
		if e.Host != "" {
			cmd += " -h " + shellWord(e.Host)
		}
		if e.Port != 0 {
			cmd += fmt.Sprintf(" -p %d", e.Port)
		}
		if e.User != "" {
			cmd += " -U " + shellWord(e.User)
		}
		return cmd + " -d " + shellWord(e.Path)
	case core.SQLite:
		return in + "sqlite3 " + shellWord(e.Path)
	}
	cmd := in + "mysql"
	if e.Host != "" {
		cmd += " -h " + shellWord(e.Host)
	}
	if e.Port != 0 {
		cmd += fmt.Sprintf(" -P %d", e.Port)
	}
	if e.User != "" {
		cmd += " -u " + shellWord(e.User) + " -p"
	}
	return cmd + " " + shellWord(e.Path)
}
