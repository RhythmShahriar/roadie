// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package testutil

import (
	"bytes"
	"context"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// Site is an in-memory core.Site. Set its exported fields before use; the
// methods record what a run did to it.
type Site struct {
	EP        core.Endpoint
	StackName core.Stack
	RO        bool // ReadOnly: every writing method returns core.ErrReadOnly
	Identity  core.DBIdentity
	// WPFunc answers WP calls; nil means WP-CLI is absent (core.ErrNoWPCLI).
	WPFunc func(args []string) ([]byte, error)
	// SourceFunc and SinkFunc answer Source and Sink; nil returns an empty
	// FakeSource and a FakeSink.
	SourceFunc func(tables []string) (core.Source, error)
	SinkFunc   func() (core.Sink, error)
	// BackupPath and BackupRestore are what Backup returns.
	BackupPath, BackupRestore string

	mu       sync.Mutex
	files    map[string][]byte
	wpCalls  [][]string
	commands []string
	writes   []string
	backups  int
}

// FakeSite returns a writable WordPress site holding files (paths relative
// to the site root, with forward slashes).
func FakeSite(files map[string][]byte) *Site {
	s := &Site{
		EP:            core.Endpoint{Kind: core.LocalSite, Display: "/srv/staging", Path: "/srv/staging"},
		StackName:     core.StackWordPress,
		Identity:      core.DBIdentity{Engine: core.MySQL, Host: "127.0.0.1", Port: 3306, Name: "staging", User: "staging", Prefix: "wp_"},
		BackupPath:    ".roadie/backups/20261026T000000Z.sql.gz",
		BackupRestore: "gunzip -c .roadie/backups/20261026T000000Z.sql.gz | wp db import -",
		files:         map[string][]byte{},
	}
	for k, v := range files {
		s.files[k] = bytes.Clone(v)
	}
	return s
}

// Endpoint returns EP.
func (s *Site) Endpoint() core.Endpoint { return s.EP }

// Stack returns StackName.
func (s *Site) Stack() core.Stack { return s.StackName }

// ReadOnly returns RO.
func (s *Site) ReadOnly() bool { return s.RO }

// ReadFile returns a copy of the file, or fs.ErrNotExist.
func (s *Site) ReadFile(_ context.Context, rel string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[rel]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: fs.ErrNotExist}
	}
	return bytes.Clone(b), nil
}

// WriteFile stores a copy of b and records the write.
func (s *Site) WriteFile(_ context.Context, rel string, b []byte, _ fs.FileMode) error {
	if s.RO {
		return core.ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[rel] = bytes.Clone(b)
	s.writes = append(s.writes, "write "+rel)
	return nil
}

// Remove deletes the file and records it; a missing file is no error.
func (s *Site) Remove(_ context.Context, rel string) error {
	if s.RO {
		return core.ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, rel)
	s.writes = append(s.writes, "remove "+rel)
	return nil
}

// Exists reports whether rel is a file or a folder holding one.
func (s *Site) Exists(_ context.Context, rel string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[rel]; ok {
		return true, nil
	}
	prefix := strings.TrimSuffix(rel, "/") + "/"
	for k := range s.files {
		if strings.HasPrefix(k, prefix) {
			return true, nil
		}
	}
	return false, nil
}

// List returns the files and folders under rel at most depth levels down
// (0 or less means every level), sorted.
func (s *Site) List(_ context.Context, rel string, depth int) ([]core.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := strings.Trim(rel, "/")
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]core.FileInfo{}
	for k, v := range s.files {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		parts := strings.Split(rest, "/")
		for i := range parts {
			if depth > 0 && i >= depth {
				break
			}
			p := prefix + strings.Join(parts[:i+1], "/")
			if i == len(parts)-1 {
				seen[p] = core.FileInfo{Rel: p, Size: int64(len(v))}
			} else if _, ok := seen[p]; !ok {
				seen[p] = core.FileInfo{Rel: p, Dir: true}
			}
		}
	}
	out := make([]core.FileInfo, 0, len(seen))
	for _, fi := range seen {
		out = append(out, fi)
	}
	slices.SortFunc(out, func(a, b core.FileInfo) int { return strings.Compare(a.Rel, b.Rel) })
	return out, nil
}

// WP records the call and answers it with WPFunc.
func (s *Site) WP(_ context.Context, args ...string) ([]byte, error) {
	if s.WPFunc == nil {
		return nil, core.ErrNoWPCLI
	}
	s.mu.Lock()
	s.wpCalls = append(s.wpCalls, slices.Clone(args))
	s.commands = append(s.commands, "wp "+strings.Join(args, " "))
	s.mu.Unlock()
	return s.WPFunc(args)
}

// DBIdentity returns Identity.
func (s *Site) DBIdentity(context.Context) (core.DBIdentity, error) { return s.Identity, nil }

// Source answers with SourceFunc, or an empty FakeSource.
func (s *Site) Source(_ context.Context, tables []string) (core.Source, error) {
	if s.SourceFunc != nil {
		return s.SourceFunc(tables)
	}
	return FakeSource(nil, nil), nil
}

// Sink answers with SinkFunc, or a new FakeSink.
func (s *Site) Sink(context.Context) (core.Sink, error) {
	if s.RO {
		return nil, core.ErrReadOnly
	}
	if s.SinkFunc != nil {
		return s.SinkFunc()
	}
	return FakeSink(), nil
}

// RemoteCommands returns every WP command run so far, plus any added with RecordCommand.
func (s *Site) RemoteCommands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands)
}

// RecordCommand adds a remote command, for fakes of ssh sites.
func (s *Site) RecordCommand(cmd string) {
	s.mu.Lock()
	s.commands = append(s.commands, cmd)
	s.mu.Unlock()
}

// Backup records the call and returns BackupPath and BackupRestore.
func (s *Site) Backup(context.Context) (string, string, error) {
	if s.RO {
		return "", "", core.ErrReadOnly
	}
	s.mu.Lock()
	s.backups++
	s.mu.Unlock()
	return s.BackupPath, s.BackupRestore, nil
}

// Close does nothing.
func (s *Site) Close() error { return nil }

// Files returns a copy of every file, by path.
func (s *Site) Files() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.files))
	for k, v := range s.files {
		out[k] = bytes.Clone(v)
	}
	return out
}

// Writes lists every write and remove, as "write <rel>" or "remove <rel>".
func (s *Site) Writes() []string { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.writes) }

// WPCalls lists the arguments of every WP call.
func (s *Site) WPCalls() [][]string { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.wpCalls) }

// Backups returns how many times Backup ran.
func (s *Site) Backups() int { s.mu.Lock(); defer s.mu.Unlock(); return s.backups }
