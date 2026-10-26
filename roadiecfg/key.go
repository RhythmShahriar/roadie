// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// KeyFile is where the consistency key lives, under the project folder.
const KeyFile = Dir + "/key"

// Where a consistency key came from, as LoadKey returns it.
const (
	KeyFromEnv  = "env"
	KeyFromFile = "file"
	KeyFromNew  = "new"
)

// parseKey reads base64 of 32 bytes. It is a local copy of the format
// pseudo.ParseKey reads, so loading a key doesn't wait on the faker.
func parseKey(s string) (pseudo.Key, bool) {
	var k pseudo.Key
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(k) {
		return k, false
	}
	copy(k[:], b)
	return k, true
}

func loadKey(dir string, env func(string) (string, bool)) (pseudo.Key, string, error) {
	if env != nil {
		if v, ok := env("ROADIE_KEY"); ok && v != "" {
			k, ok := parseKey(v)
			if !ok {
				return k, "", core.Errorf(core.ErrConfig, 2, "ROADIE_KEY isn't base64 of 32 bytes. Make one with rhythm-roadie config key rotate, or unset it.")
			}
			return k, KeyFromEnv, nil
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(KeyFile))) // #nosec G304 -- the project's own key file
	switch {
	case err == nil:
		k, ok := parseKey(string(b))
		if !ok {
			return k, "", core.Errorf(core.ErrConfig, 2, "%s isn't base64 of 32 bytes. Run rhythm-roadie config key rotate to make a new one (fakes change once).", KeyFile)
		}
		return k, KeyFromFile, nil
	case !errors.Is(err, fs.ErrNotExist):
		return pseudo.Key{}, "", core.Errorf(core.ErrConfig, 2, "can't read %s: %v", KeyFile, errors.Unwrap(err))
	}
	k, err := newKey()
	return k, KeyFromNew, err
}

// newKey makes a key from crypto/rand.
func newKey() (pseudo.Key, error) {
	var k pseudo.Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	return k, nil
}

// RunKey returns the key one copy uses (PRD 11.1). With consistency
// per-run and no keepKey it is a new key each time, never saved; stable
// is false so the report shows no fingerprint. With stable or keepKey it
// is LoadKey's key; a new one is saved to .roadie/key so the next copy
// gets the same fakes. A nil c means the defaults, so per-run.
func RunKey(dir string, c *Config, keepKey bool, env func(string) (string, bool)) (k pseudo.Key, source string, stable bool, err error) {
	if c == nil {
		c = Default()
	}
	if c.Pseudonymise.Consistency != "stable" && !keepKey {
		k, err = newKey()
		return k, KeyFromNew, false, err
	}
	k, source, err = loadKey(dir, env)
	if err != nil {
		return k, "", false, err
	}
	if source == KeyFromNew {
		if err := saveKey(dir, k); err != nil {
			return k, "", false, err
		}
	}
	return k, source, true, nil
}

func saveKey(dir string, k pseudo.Key) error {
	folder := filepath.Join(dir, Dir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves an existing folder's mode alone; the key needs the
	// folder closed too. chmod on Windows only changes the read-only bit.
	if err := os.Chmod(folder, 0o700); err != nil { // #nosec G302 -- a folder needs its x bit
		return err
	}
	data := []byte(base64.StdEncoding.EncodeToString(k[:]) + "\n")
	tmp, err := os.CreateTemp(folder, ".key-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op after the rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, filepath.FromSlash(KeyFile)))
}

func forgetKey(dir string) error {
	err := os.Remove(filepath.Join(dir, filepath.FromSlash(KeyFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
