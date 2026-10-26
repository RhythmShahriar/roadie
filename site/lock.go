// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/remote"
)

// lockFresh is how long a lock file with no content yet counts as held:
// another run may be between creating it and writing its pid.
const lockFresh = 10 * time.Second

// LockNotify is Lock with note called (when not nil) for a stale lock it
// replaced: one whose run is no longer alive. Lock prints that note on
// standard error.
//
// The lock is <cache>/roadie/locks/<sha256 of the target's identity>.lock,
// created with O_EXCL and holding the pid and start time. A stdio target
// has no lock.
func LockNotify(target core.Endpoint, note func(string)) (unlock func(), err error) {
	if note == nil {
		note = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	id := lockIdentity(target)
	if id == "" {
		return func() {}, nil
	}
	dir, err := config.CacheDir("roadie")
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	sum := sha256.Sum256([]byte(id))
	p := filepath.Join(dir, hex.EncodeToString(sum[:])+".lock")
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(rnd[:])
	body := fmt.Sprintf("pid=%d\nstarted=%s\ntarget=%s\ntoken=%s\n", os.Getpid(), time.Now().Format(time.RFC3339), oneLine(target.Display), token)
	for try := 0; try < 3; try++ {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a name Roadie built in its cache
		if err == nil {
			_, werr := f.WriteString(body)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(p)
				return nil, fmt.Errorf("lock: %w", werr)
			}
			var once sync.Once
			return func() { once.Do(func() { releaseLock(p, token) }) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock: %w", err)
		}
		held, info, removed, err := removeStale(p)
		if err != nil {
			return nil, err
		}
		if held {
			return nil, core.Errorf(core.ErrLock, 3,
				"lock: another Roadie run is copying to %s (pid %d, started %s). Wait for it, or remove %s if it crashed.",
				lockTarget(info, target), info.pid, info.startedText(), p)
		}
		if !removed {
			continue // gone already: another run cleared it first
		}
		if info.pid > 0 {
			note(fmt.Sprintf("note: removed a stale lock on %s from pid %d (started %s), which is no longer running.",
				lockTarget(info, target), info.pid, info.startedText()))
		} else {
			note(fmt.Sprintf("note: removed an unreadable stale lock on %s.", target.Display))
		}
	}
	return nil, core.Errorf(core.ErrLock, 3, "lock: another Roadie run took the lock on %s at the same moment. Run again.", target.Display)
}

// breakWait bounds how long LockNotify waits for another run that is
// clearing the same stale lock; tests shorten it.
var breakWait = 2 * time.Second

// removeStale removes the lock at p when its run is gone. Two runs that
// both find the same stale lock would otherwise race: the slower one
// could remove the lock the faster one just made. So the check and the
// removal happen while holding p+".break", made with O_EXCL; a run that
// creates a lock never needs it, since O_EXCL fails while p exists.
func removeStale(p string) (held bool, info lockInfo, removed bool, err error) {
	brk := p + ".break"
	deadline := time.Now().Add(breakWait)
	for {
		f, err := os.OpenFile(brk, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a name Roadie built in its cache
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, info, false, fmt.Errorf("lock: %w", err)
		}
		// A run that died while clearing leaves the break file behind.
		if fi, serr := os.Stat(brk); serr == nil && time.Since(fi.ModTime()) > lockFresh {
			_ = os.Remove(brk)
			continue
		}
		if time.Now().After(deadline) {
			return false, info, false, core.Errorf(core.ErrLock, 3, "lock: another Roadie run is clearing a stale lock in %s. Run again.", filepath.Dir(p))
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() { _ = os.Remove(brk) }()
	held, info = readLock(p)
	if held {
		return true, info, false, nil
	}
	switch err := os.Remove(p); {
	case err == nil:
		return false, info, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, info, false, nil
	default:
		return false, info, false, fmt.Errorf("lock: %w", err)
	}
}

// lockInfo is what a lock file says.
type lockInfo struct {
	pid     int
	started time.Time
	target  string
	token   string // tells this lock from a later one of the same process
}

func (l lockInfo) startedText() string {
	if l.started.IsZero() {
		return "at an unknown time"
	}
	t := l.started.Local()
	if y, m, d := t.Date(); y == time.Now().Year() && m == time.Now().Month() && d == time.Now().Day() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func lockTarget(l lockInfo, e core.Endpoint) string {
	if l.target != "" {
		return l.target
	}
	return e.Display
}

// readLock reads a lock file and reports whether its run still holds it.
// A file still empty a moment after it was made is held: its run may be
// writing it right now.
func readLock(p string) (held bool, info lockInfo) {
	b, err := os.ReadFile(p) // #nosec G304 -- a name Roadie built in its cache
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist), info
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "pid":
			info.pid, _ = strconv.Atoi(v)
		case "started":
			info.started, _ = time.Parse(time.RFC3339, v)
		case "target":
			info.target = v
		case "token":
			info.token = v
		}
	}
	if info.pid <= 0 {
		fi, err := os.Stat(p)
		return err == nil && time.Since(fi.ModTime()) < lockFresh, info
	}
	return info.pid == os.Getpid() || pidRunning(info.pid), info
}

// releaseLock removes the lock when it is still this one.
func releaseLock(p, token string) {
	if _, info := readLock(p); info.token == token {
		_ = os.Remove(p)
	}
}

// oneLine keeps a display name on one line of the lock file.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// lockIdentity names the target however it was typed: an absolute
// local path; the user-less host, port and path for ssh; the server,
// port and database name for a DSN. Stdio has none.
func lockIdentity(e core.Endpoint) string {
	switch e.Kind {
	case core.LocalSite, core.DumpFile:
		return "file:" + absPath(e.Path)
	case core.SSHSite:
		return sshIdentity(e)
	case core.WPAlias:
		if e.Host == "" && e.Path == "" {
			if dir, err := getwd(); err == nil {
				if r, err := remote.ResolveAlias(dir, e.Alias); err == nil {
					if r.Kind == core.LocalSite {
						return "file:" + absPath(r.Path)
					}
					return sshIdentity(r)
				}
			}
			return "alias:" + e.Alias
		}
		if e.Host == "" {
			return "file:" + absPath(e.Path)
		}
		return sshIdentity(e)
	case core.DSN:
		if e.Driver == core.SQLite {
			return "file:" + absPath(e.Path)
		}
		port := e.Port
		if port == 0 {
			port = defaultPort(e.Driver)
		}
		return fmt.Sprintf("db:%s://%s:%d/%s", family(e.Driver), normHost(e.Host), port, e.Path)
	}
	return ""
}

func sshIdentity(e core.Endpoint) string {
	port := e.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("ssh:%s:%d:%s", normHost(e.Host), port, path.Clean(e.Path))
}

// absPath is p made absolute with symlinks resolved; for a file not yet
// there, its folder's.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}
