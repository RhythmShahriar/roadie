// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// localSite is a site folder on this machine. Every file operation goes
// through an os.Root, so a symlink inside the site can't lead a write
// outside it. WP-CLI never runs here: wp would load the site's PHP, while
// Roadie runs no code from the sites it copies (PRD 18.1).
type localSite struct {
	ep    core.Endpoint
	dir   string // the site folder, absolute
	root  *os.Root
	ro    bool
	stack core.Stack
	o     Options

	mu  sync.Mutex
	cfg *dbConfig
}

func openLocal(ctx context.Context, e core.Endpoint, dir string, o Options) (core.Site, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("site: %s: %w", e.Display, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("site: %s is not a folder", e.Display)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("site: %s: %w", e.Display, err)
	}
	s := &localSite{ep: e, dir: abs, root: root, ro: o.ReadOnly, o: o}
	if s.stack, err = detectStack(ctx, s); err != nil {
		_ = root.Close()
		return nil, err
	}
	return s, nil
}

func (s *localSite) Endpoint() core.Endpoint { return s.ep }
func (s *localSite) Stack() core.Stack       { return s.stack }
func (s *localSite) ReadOnly() bool          { return s.ro }

// name maps a site-relative path to a name inside the root.
func (s *localSite) name(rel string) (string, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(r), nil
}

func (s *localSite) ReadFile(_ context.Context, rel string) ([]byte, error) {
	n, err := s.name(rel)
	if err != nil {
		return nil, err
	}
	return s.root.ReadFile(n)
}

// WriteFile writes a temp file in the same folder and renames it over
// rel, so a request never reads half a file. An existing file keeps its
// mode (and its owner when Roadie runs as root); a new one gets perm.
func (s *localSite) WriteFile(_ context.Context, rel string, b []byte, perm fs.FileMode) error {
	if s.ro {
		return core.ErrReadOnly
	}
	n, err := s.name(rel)
	if err != nil {
		return err
	}
	if n == "." {
		return fmt.Errorf("site: can't write the site folder itself")
	}
	if dir := filepath.Dir(n); dir != "." {
		if err := s.root.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- the web server must read mu-plugins
			return err
		}
	}
	mode := perm.Perm()
	orig, err := s.root.Stat(n)
	switch {
	case err == nil && !orig.Mode().IsRegular():
		return fmt.Errorf("site: %s is not a regular file", rel)
	case err == nil:
		mode = orig.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	default:
		orig = nil
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(n), "."+filepath.Base(n)+".roadie-"+hex.EncodeToString(rnd[:]))
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = s.root.Remove(tmp)
		}
	}()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := s.root.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := keepOwner(s.root, tmp, orig); err != nil {
		return err
	}
	if err := renameReplace(s.root, tmp, n, rel); err != nil {
		return err
	}
	done = true
	return nil
}

func (s *localSite) Remove(_ context.Context, rel string) error {
	if s.ro {
		return core.ErrReadOnly
	}
	n, err := s.name(rel)
	if err != nil {
		return err
	}
	if err := s.root.Remove(n); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *localSite) Exists(_ context.Context, rel string) (bool, error) {
	n, err := s.name(rel)
	if err != nil {
		return false, err
	}
	_, err = s.root.Stat(n)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// List walks rel at most depth levels down (0 or less: every level). It
// reads names and sizes only, never file contents.
func (s *localSite) List(_ context.Context, rel string, depth int) ([]core.FileInfo, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return nil, err
	}
	var out []core.FileInfo
	err = fs.WalkDir(s.root.FS(), r, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == r {
				return err
			}
			return nil // an unreadable folder is skipped, not fatal
		}
		if p == r {
			return nil
		}
		levels := strings.Count(strings.TrimPrefix(p, r+"/"), "/") + 1
		if r == "." {
			levels = strings.Count(p, "/") + 1
		}
		if depth > 0 && levels > depth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi := core.FileInfo{Rel: path.Clean(p), Dir: d.IsDir()}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				fi.Size = info.Size()
			}
		}
		out = append(out, fi)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

func (s *localSite) WP(_ context.Context, args ...string) ([]byte, error) {
	if s.ro && !readWP(args) {
		return nil, core.ErrReadOnly
	}
	return nil, core.ErrNoWPCLI
}

// config reads the database settings once.
func (s *localSite) config(ctx context.Context) (dbConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg != nil {
		return *s.cfg, nil
	}
	c, err := readDBConfig(ctx, s, s.stack, s.dir, s.ro)
	if err != nil {
		return dbConfig{}, err
	}
	s.cfg = &c
	return c, nil
}

func (s *localSite) DBIdentity(ctx context.Context) (core.DBIdentity, error) {
	c, err := s.config(ctx)
	if err != nil {
		return core.DBIdentity{}, err
	}
	id := c.identity("")
	if c.driver == core.SQLite {
		id.Name = c.endpoint(s.dir).Path
	}
	return id, nil
}

func (s *localSite) dbEndpoint(ctx context.Context) (core.Endpoint, error) {
	c, err := s.config(ctx)
	if err != nil {
		return core.Endpoint{}, err
	}
	return c.endpoint(s.dir), nil
}

func (s *localSite) Source(ctx context.Context, tables []string) (core.Source, error) {
	e, err := s.dbEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	return dbSource(ctx, e, tables)
}

func (s *localSite) Sink(ctx context.Context) (core.Sink, error) {
	if s.ro {
		return nil, core.ErrReadOnly
	}
	e, err := s.dbEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	return dbSink(ctx, e, s.o.FixCollation)
}

func (s *localSite) RemoteCommands() []string { return nil }

// Backup writes .roadie/backups/<UTC ts>.sql.gz. The restore line for
// WordPress uses WP-CLI with --path, so it works from any folder.
func (s *localSite) Backup(ctx context.Context) (string, string, error) {
	if s.ro {
		return "", "", core.ErrReadOnly
	}
	e, err := s.dbEndpoint(ctx)
	if err != nil {
		return "", "", err
	}
	p, err := localBackup(ctx, s.o.BackupDir, e.Driver, func(ctx context.Context) (core.Source, error) {
		return dbSource(ctx, e, nil)
	})
	if err != nil {
		return "", "", err
	}
	if s.stack == core.StackWordPress {
		return p, "gunzip -c " + shellWord(p) + " | wp --path=" + shellWord(s.dir) + " db import -", nil
	}
	return p, clientRestore(p, e), nil
}

func (s *localSite) Close() error { return s.root.Close() }
