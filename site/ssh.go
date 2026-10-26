// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/site/remote"
)

// sshSite is a site on another host, reached only through the remote
// allowlist (PRD 6.13). A WP-CLI alias with ssh is one too.
type sshSite struct {
	ep    core.Endpoint
	r     *remote.Runner
	ro    bool
	stack core.Stack
	o     Options

	mu  sync.Mutex
	cfg *dbConfig
}

func openSSH(ctx context.Context, e core.Endpoint, o Options) (core.Site, error) {
	if err := remote.CheckHost(e.Host); err != nil {
		return nil, err
	}
	if err := remote.CheckUser(e.User); err != nil {
		return nil, err
	}
	if e.Path == "" || !strings.HasPrefix(e.Path, "/") && !strings.HasPrefix(e.Path, "~") {
		return nil, fmt.Errorf("site: %s needs an absolute site folder on the host", e.Display)
	}
	r := &remote.Runner{User: e.User, Host: e.Host, Port: e.Port, Root: e.Path, WP: o.WP, BatchMode: o.BatchMode, ReadOnly: o.ReadOnly}
	s := &sshSite{ep: e, r: r, ro: o.ReadOnly, o: o}
	st, err := detectStack(ctx, s)
	if err != nil {
		return nil, err
	}
	s.stack = st
	return s, nil
}

func (s *sshSite) Endpoint() core.Endpoint { return s.ep }
func (s *sshSite) Stack() core.Stack       { return s.stack }
func (s *sshSite) ReadOnly() bool          { return s.ro }

// remotePath is rel inside the site folder on the host.
func (s *sshSite) remotePath(rel string) (string, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	if r == "." {
		return s.ep.Path, nil
	}
	return path.Join(s.ep.Path, r), nil
}

// missing reports a remote cat or find that failed because the path
// isn't there.
func missing(err error) bool {
	var re *remote.RunError
	return errors.As(err, &re) && bytes.Contains(re.Stderr, []byte("No such file or directory"))
}

func (s *sshSite) ReadFile(ctx context.Context, rel string) ([]byte, error) {
	p, err := s.remotePath(rel)
	if err != nil {
		return nil, err
	}
	b, err := s.r.Output(ctx, nil, remote.Cat, p)
	if missing(err) {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: fs.ErrNotExist}
	}
	return b, err
}

// WriteFile streams b into a temp file next to rel with umask 077, sets
// its mode and renames it into place.
func (s *sshSite) WriteFile(ctx context.Context, rel string, b []byte, perm fs.FileMode) error {
	if s.ro {
		return core.ErrReadOnly
	}
	p, err := s.remotePath(rel)
	if err != nil {
		return err
	}
	if err := s.r.Run(ctx, nil, io.Discard, remote.Mkdir, path.Dir(p)); err != nil {
		return err
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(p), "."+path.Base(p)+".roadie-"+hex.EncodeToString(rnd[:]))
	mode := fmt.Sprintf("%04o", perm.Perm())
	return s.r.Run(ctx, bytes.NewReader(b), io.Discard, remote.WriteMode, tmp, mode, tmp, tmp, p)
}

func (s *sshSite) Remove(ctx context.Context, rel string) error {
	if s.ro {
		return core.ErrReadOnly
	}
	p, err := s.remotePath(rel)
	if err != nil {
		return err
	}
	return s.r.Run(ctx, nil, io.Discard, remote.Remove, p)
}

// Exists uses test -f where the allowlist lets it (always on a target;
// wp-config.php and .env on a source) and find -maxdepth 0 otherwise,
// which also finds folders.
func (s *sshSite) Exists(ctx context.Context, rel string) (bool, error) {
	p, err := s.remotePath(rel)
	if err != nil {
		return false, err
	}
	base := path.Base(p)
	if !s.ro || base == "wp-config.php" || base == ".env" {
		err := s.r.Run(ctx, nil, io.Discard, remote.TestFile, p)
		var re *remote.RunError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &re) && re.Exit == 1:
			if s.ro {
				return false, nil
			}
		default:
			return false, err
		}
	}
	out, err := s.r.Output(ctx, nil, remote.Find, p, "0")
	if missing(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// List runs find -printf '%y %s %P\n': names and sizes, never contents.
func (s *sshSite) List(ctx context.Context, rel string, depth int) ([]core.FileInfo, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return nil, err
	}
	p, err := s.remotePath(rel)
	if err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = 65535
	}
	out, err := s.r.Output(ctx, nil, remote.Find, p, strconv.Itoa(depth))
	if missing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseFind(out, r), nil
}

// parseFind reads find's "%y %s %P" lines; base is the listed folder
// relative to the site root.
func parseFind(out []byte, base string) []core.FileInfo {
	var list []core.FileInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		typ, rest, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		size, name, ok := strings.Cut(rest, " ")
		if !ok || name == "" {
			continue
		}
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			continue
		}
		rel := name
		if base != "." {
			rel = path.Join(base, name)
		}
		fi := core.FileInfo{Rel: rel, Dir: typ == "d"}
		if !fi.Dir {
			fi.Size = n
		}
		list = append(list, fi)
	}
	return list
}

// wpTemplates maps the WP-CLI calls Site.WP takes to their templates.
func wpTemplate(args []string) (remote.Template, []string, bool) {
	switch {
	case len(args) == 2 && args[0] == "core" && args[1] == "version":
		return remote.CoreVersion, nil, true
	case len(args) == 3 && args[0] == "option" && args[1] == "get":
		return remote.OptionGet, args[2:], true
	}
	return "", nil, false
}

func (s *sshSite) WP(ctx context.Context, args ...string) ([]byte, error) {
	if s.ro && !readWP(args) {
		return nil, core.ErrReadOnly
	}
	t, a, ok := wpTemplate(args)
	if !ok {
		return nil, fmt.Errorf("%w: wp %s", remote.ErrNotAllowed, strings.Join(args, " "))
	}
	out, err := s.r.Output(ctx, nil, t, a...)
	return bytes.TrimRight(out, "\r\n"), err
}

// EvalFile pipes a PHP script into wp eval-file -, for the Echo
// connector's installer (killswitch).
func (s *sshSite) EvalFile(ctx context.Context, script []byte) error {
	if s.ro {
		return core.ErrReadOnly
	}
	return s.r.Run(ctx, bytes.NewReader(script), io.Discard, remote.EvalFile)
}

func (s *sshSite) config(ctx context.Context) (dbConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg != nil {
		return *s.cfg, nil
	}
	c, err := readDBConfig(ctx, s, s.stack, s.ep.Display, s.ro)
	if err != nil {
		return dbConfig{}, err
	}
	s.cfg = &c
	return c, nil
}

func (s *sshSite) DBIdentity(ctx context.Context) (core.DBIdentity, error) {
	c, err := s.config(ctx)
	if err != nil {
		return core.DBIdentity{}, err
	}
	id := c.identity(strings.ToLower(s.r.Host))
	if c.driver == core.SQLite && !path.IsAbs(c.name) {
		id.Name = path.Join(s.ep.Path, c.name)
	}
	return id, nil
}

// creds are the database credentials for a remote client, sent on stdin.
func (c dbConfig) creds() remote.Creds {
	port := c.port
	if port == 0 {
		port = defaultPort(c.driver)
	}
	return remote.Creds{User: c.user, Password: c.password, Host: c.host, Port: port}
}

// exportCommand picks the export for the site's stack: wp db export for
// WordPress, mysqldump or pg_dump with stdin credentials for Laravel.
func (s *sshSite) exportCommand(ctx context.Context, tables []string) (remote.Template, []string, []byte, error) {
	if s.stack == core.StackWordPress {
		if len(tables) > 0 {
			return remote.DBExportTables, []string{strings.Join(tables, ",")}, nil, nil
		}
		return remote.DBExport, nil, nil, nil
	}
	c, err := s.config(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	cr := c.creds()
	switch c.driver {
	case core.MySQL, core.MariaDB:
		b, err := remote.MySQLDefaults(cr)
		return remote.MySQLDump, append([]string{c.name}, tables...), b, err
	case core.Postgres:
		b, err := remote.PGPassword(cr)
		return remote.PGDump, append([]string{cr.Host, strconv.Itoa(cr.Port), cr.User, c.name}, tables...), b, err
	}
	return "", nil, nil, fmt.Errorf("site: %s: Roadie can't export a %s database over ssh; copy the file and use it as a sqlite:/// DSN", s.ep.Display, c.driver)
}

// Source streams the remote export straight into the dump reader; the
// dump is never stored on this machine in the clear.
func (s *sshSite) Source(ctx context.Context, tables []string) (core.Source, error) {
	t, args, creds, err := s.exportCommand(ctx, tables)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var stdin io.Reader
		if creds != nil {
			stdin = remote.WithCreds(creds, nil)
		}
		err := s.r.Run(ctx, stdin, pw, t, args...)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	src, err := openDump(ctx, pr, dump.Options{Name: s.ep.Display})
	if err != nil {
		_ = pr.Close()
		cancel()
		if runErr := <-done; runErr != nil && !errors.Is(runErr, context.Canceled) {
			return nil, runErr
		}
		return nil, err
	}
	return &streamSource{Source: src, pr: pr, cancel: cancel, done: done}, nil
}

// streamSource ends the remote export when the source is closed and
// reports the export's own error (ssh, wp-cli) ahead of the reader's.
type streamSource struct {
	core.Source
	pr     *io.PipeReader
	cancel context.CancelFunc
	done   chan error

	once   sync.Once
	runErr error
}

func (s *streamSource) wait() error {
	s.once.Do(func() { s.runErr = <-s.done })
	return s.runErr
}

func (s *streamSource) Next(ctx context.Context) (*core.Batch, error) {
	b, err := s.Source.Next(ctx)
	if err != nil && !errors.Is(err, io.EOF) {
		if runErr := s.wait(); runErr != nil {
			return nil, runErr
		}
	}
	if errors.Is(err, io.EOF) {
		if runErr := s.wait(); runErr != nil {
			return nil, runErr
		}
	}
	return b, err
}

func (s *streamSource) Close() error {
	err := s.Source.Close()
	_ = s.pr.Close()
	s.cancel()
	_ = s.wait()
	return err
}

// importCommand picks the import for the site's stack.
func (s *sshSite) importCommand(ctx context.Context) (remote.Template, []string, []byte, core.Dialect, error) {
	if s.stack == core.StackWordPress {
		return remote.DBImport, nil, nil, core.MySQL, nil
	}
	c, err := s.config(ctx)
	if err != nil {
		return "", nil, nil, "", err
	}
	cr := c.creds()
	switch c.driver {
	case core.MySQL, core.MariaDB:
		b, err := remote.MySQLDefaults(cr)
		return remote.MySQL, []string{c.name}, b, c.driver, err
	case core.Postgres:
		b, err := remote.PGPassword(cr)
		return remote.PSQL, []string{cr.Host, strconv.Itoa(cr.Port), cr.User, c.name}, b, core.Postgres, err
	}
	return "", nil, nil, "", fmt.Errorf("site: %s: Roadie can't import into a %s database over ssh", s.ep.Display, c.driver)
}

// Sink streams the dump writer's output into the remote import.
func (s *sshSite) Sink(ctx context.Context) (core.Sink, error) {
	if s.ro {
		return nil, core.ErrReadOnly
	}
	t, args, creds, dialect, err := s.importCommand(ctx)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	inner := newStreamSink(pw, dump.WriterOptions{Target: dialect, FixCollation: s.o.FixCollation})
	if inner == nil {
		return nil, core.ErrNotBuilt
	}
	rs := &remoteSink{inner: inner, pw: pw, done: make(chan error, 1), dest: s.ep.Display, what: string(t), state: core.Untouched}
	go func() {
		var stdin io.Reader = pr
		if creds != nil {
			stdin = remote.WithCreds(creds, pr)
		}
		err := s.r.Run(ctx, stdin, io.Discard, t, args...)
		_ = pr.CloseWithError(errImportEnded(err))
		rs.done <- err
	}()
	return rs, nil
}

// errImportEnded is what a write into the pipe sees once the remote
// import has exited.
func errImportEnded(err error) error {
	if err == nil {
		return io.ErrClosedPipe
	}
	return err
}

func (s *sshSite) RemoteCommands() []string { return s.r.Commands() }

// Backup runs the allowlisted remote backup: wp db export into
// ~/.roadie-backups/<UTC ts>.sql.gz with umask 077. Only WordPress sites
// have one; the restore line runs over the same ssh.
func (s *sshSite) Backup(ctx context.Context) (string, string, error) {
	if s.ro {
		return "", "", core.ErrReadOnly
	}
	if s.stack != core.StackWordPress {
		return "", "", fmt.Errorf("backup: Roadie backs up an ssh target only through WP-CLI; export %s's database yourself, then pass --no-backup", s.ep.Display)
	}
	ts := backupStamp()
	if err := s.r.Run(ctx, nil, io.Discard, remote.Backup, ts); err != nil {
		return "", "", err
	}
	p := "~/.roadie-backups/" + ts + ".sql.gz"
	wp := s.o.WP
	if wp == "" {
		wp = "wp"
	}
	inner := "gunzip -c " + p + " | " + shellWord(wp) + " --path=" + shellWord(s.ep.Path) + " db import -"
	ssh := "ssh "
	if s.ep.Port != 0 {
		ssh += "-p " + strconv.Itoa(s.ep.Port) + " "
	}
	return p, ssh + s.r.Dest() + " " + remote.Quote(inner), nil
}

func (s *sshSite) Close() error { return nil }
