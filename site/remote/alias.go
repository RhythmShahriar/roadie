// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/core"
)

// aliasFiles are the WP-CLI project config files, the local one winning.
var aliasFiles = []string{"wp-cli.yml", "wp-cli.local.yml"}

// ErrNoAlias is wrapped when an alias is in no wp-cli.yml.
var ErrNoAlias = errors.New("WP-CLI alias not found")

// sshAliasRE is WP-CLI's ssh value: [ssh:][user@]host[:port][/path].
var sshAliasRE = regexp.MustCompile(`^(?:ssh:)?(?:([^@/\s]+)@)?(\[[^\]]+\]|[^:/\s]+)(?::([0-9]*))?(/.*)?$`)

// ResolveAlias finds alias ("@prod") in wp-cli.yml or wp-cli.local.yml
// in dir or the nearest folder above it that has either file, as WP-CLI
// does. An alias with ssh becomes an SSHSite with Alias set; one with only
// a path becomes a LocalSite (a relative path is from the file's folder).
func ResolveAlias(dir, alias string) (core.Endpoint, error) {
	name := strings.TrimPrefix(alias, "@")
	cfgDir, files, err := findAliasFiles(dir)
	if err != nil {
		return core.Endpoint{}, err
	}
	var entry map[string]any
	var from string
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the user's own wp-cli.yml
		if err != nil {
			return core.Endpoint{}, err
		}
		var doc map[string]any
		if err := yaml.Unmarshal(quoteAliases(b), &doc); err != nil {
			return core.Endpoint{}, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		v, ok := doc["@"+name]
		if !ok {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return core.Endpoint{}, fmt.Errorf("@%s in %s is an alias group or not a mapping; give one site", name, filepath.Base(f))
		}
		if entry == nil {
			entry = map[string]any{}
		}
		for k, x := range m {
			entry[k] = x
		}
		from = f
	}
	if entry == nil {
		return core.Endpoint{}, fmt.Errorf("%w: @%s is in no wp-cli.yml or wp-cli.local.yml from %s up", ErrNoAlias, name, dir)
	}
	sshVal, _ := entry["ssh"].(string)
	pathVal, _ := entry["path"].(string)
	e := core.Endpoint{Alias: "@" + name, Display: "@" + name, Raw: "@" + name}
	if sshVal == "" {
		if pathVal == "" {
			return core.Endpoint{}, fmt.Errorf("@%s in %s has neither ssh nor path", name, filepath.Base(from))
		}
		if !filepath.IsAbs(pathVal) {
			pathVal = filepath.Join(cfgDir, pathVal)
		}
		e.Kind, e.Path = core.LocalSite, pathVal
		return e, nil
	}
	m := sshAliasRE.FindStringSubmatch(sshVal)
	if m == nil || strings.Contains(sshVal, "://") || strings.HasPrefix(sshVal, "docker") || strings.HasPrefix(sshVal, "vagrant") {
		return core.Endpoint{}, fmt.Errorf("@%s: ssh: %q is not user@host[:port]/path (Roadie runs only ssh aliases)", name, sshVal)
	}
	e.Kind, e.User, e.Host, e.Path = core.SSHSite, m[1], m[2], m[4]
	if m[3] != "" {
		p, err := strconv.Atoi(m[3])
		if err != nil || p < 1 || p > 65535 {
			return core.Endpoint{}, fmt.Errorf("@%s: ssh: bad port %q", name, m[3])
		}
		e.Port = p
	}
	if pathVal != "" {
		e.Path = pathVal
	}
	if err := CheckHost(e.Host); err != nil {
		return core.Endpoint{}, fmt.Errorf("@%s: %w", name, err)
	}
	if err := CheckUser(e.User); err != nil {
		return core.Endpoint{}, fmt.Errorf("@%s: %w", name, err)
	}
	if e.Path == "" {
		return core.Endpoint{}, fmt.Errorf("@%s: ssh: %q names no site folder (add /path, or a path: key)", name, sshVal)
	}
	return e, nil
}

var (
	aliasKeyRE  = regexp.MustCompile(`(?m)^([ \t]*)(@[A-Za-z0-9_.-]+)([ \t]*:)`)
	aliasItemRE = regexp.MustCompile(`(?m)^([ \t]*-[ \t]+)(@[A-Za-z0-9_.-]+)[ \t]*$`)
)

// quoteAliases quotes "@name" keys and list items. WP-CLI's YAML reader
// takes them bare; '@' can't start a plain YAML scalar.
func quoteAliases(b []byte) []byte {
	b = aliasKeyRE.ReplaceAll(b, []byte(`$1"$2"$3`))
	return aliasItemRE.ReplaceAll(b, []byte(`$1"$2"`))
}

// findAliasFiles walks up from dir to the first folder holding
// wp-cli.yml or wp-cli.local.yml.
func findAliasFiles(dir string) (string, []string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		var found []string
		for _, n := range aliasFiles {
			p := filepath.Join(d, n)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				found = append(found, p)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", nil, err
			}
		}
		if len(found) > 0 {
			return d, found, nil
		}
		if filepath.Dir(d) == d {
			return "", nil, fmt.Errorf("%w: no wp-cli.yml or wp-cli.local.yml in %s or above", ErrNoAlias, abs)
		}
	}
}
