// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// dsnSite is a database with no files: the kill switch can only change
// data there (PRD 13.4), so every file call says the file isn't there.
type dsnSite struct {
	ep core.Endpoint
	ro bool
	o  Options
}

func openDSN(e core.Endpoint, o Options) core.Site {
	return &dsnSite{ep: e, ro: o.ReadOnly, o: o}
}

func (s *dsnSite) Endpoint() core.Endpoint { return s.ep }
func (s *dsnSite) Stack() core.Stack       { return core.StackSQL }
func (s *dsnSite) ReadOnly() bool          { return s.ro }

func (s *dsnSite) ReadFile(_ context.Context, rel string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: rel, Err: fs.ErrNotExist}
}

func (s *dsnSite) WriteFile(_ context.Context, rel string, _ []byte, _ fs.FileMode) error {
	if s.ro {
		return core.ErrReadOnly
	}
	return &fs.PathError{Op: "write", Path: rel, Err: fs.ErrNotExist}
}

func (s *dsnSite) Remove(context.Context, string) error {
	if s.ro {
		return core.ErrReadOnly
	}
	return nil
}

func (s *dsnSite) Exists(context.Context, string) (bool, error) { return false, nil }

func (s *dsnSite) List(context.Context, string, int) ([]core.FileInfo, error) { return nil, nil }

func (s *dsnSite) WP(_ context.Context, args ...string) ([]byte, error) {
	if s.ro && !readWP(args) {
		return nil, core.ErrReadOnly
	}
	return nil, core.ErrNoWPCLI
}

// DBIdentity comes from the DSN itself, so the guards never need a
// connection.
func (s *dsnSite) DBIdentity(context.Context) (core.DBIdentity, error) {
	e := s.ep
	id := core.DBIdentity{Engine: e.Driver, Host: strings.ToLower(e.Host), Port: e.Port, Name: e.Path, User: e.User}
	if id.Port == 0 {
		id.Port = defaultPort(e.Driver)
	}
	if e.Driver == core.SQLite {
		if abs, err := filepath.Abs(e.Path); err == nil {
			id.Name = abs
		}
	}
	return id, nil
}

func (s *dsnSite) Source(ctx context.Context, tables []string) (core.Source, error) {
	return dbSource(ctx, s.ep, tables)
}

func (s *dsnSite) Sink(ctx context.Context) (core.Sink, error) {
	if s.ro {
		return nil, core.ErrReadOnly
	}
	return dbSink(ctx, s.ep, s.o.FixCollation)
}

func (s *dsnSite) RemoteCommands() []string { return nil }

// Backup writes .roadie/backups/<UTC ts>.sql.gz and returns the client
// command that loads it back.
func (s *dsnSite) Backup(ctx context.Context) (string, string, error) {
	if s.ro {
		return "", "", core.ErrReadOnly
	}
	if s.ep.Driver == core.SQLite {
		return sqliteBackup(s.o.BackupDir, s.ep)
	}
	p, err := localBackup(ctx, s.o.BackupDir, s.ep.Driver, func(ctx context.Context) (core.Source, error) {
		return dbSource(ctx, s.ep, nil)
	})
	if err != nil {
		return "", "", err
	}
	return p, clientRestore(p, s.ep), nil
}

func (s *dsnSite) Close() error { return nil }
