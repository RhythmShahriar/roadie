// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package roadiecfg loads, validates and prints .roadie/config.yaml and
// keeps the consistency key that makes fakes stable across refreshes.
//
// The exported names in this file are the frozen cross-lane API; the
// engine lane replaces the bodies, which return core.ErrNotBuilt until then.
package roadiecfg

import "github.com/RhythmShahriar/roadie/pseudo"

// Config holds every key of PRD 8.1; the yaml tags are the snake_case keys.
type Config struct {
	Source     string            `yaml:"source"`
	Target     string            `yaml:"target"`
	URL        string            `yaml:"url"`
	Production []string          `yaml:"production"`
	URLMap     map[string]string `yaml:"url_map"`
	Stack      string            `yaml:"stack"`    // auto, wordpress, laravel, sql
	Profiles   []string          `yaml:"profiles"` // [auto] or profile IDs
	Remote     struct {
		WP string `yaml:"wp"`
	} `yaml:"remote"`
	Pseudonymise struct {
		EmailDomain   string   `yaml:"email_domain"`
		PhoneStyle    string   `yaml:"phone_style"` // echo, country
		Locale        string   `yaml:"locale"`      // en, de, fr, es, nl, it
		Keep          []string `yaml:"keep"`        // address parts kept
		KeepUsers     []string `yaml:"keep_users"`
		Consistency   string   `yaml:"consistency"`    // per-run, stable
		OnUnparseable string   `yaml:"on_unparseable"` // blank, fail
		Passwords     string   `yaml:"passwords"`      // reset, keep
		FreeText      string   `yaml:"free_text"`      // scrub, blank, keep
	} `yaml:"pseudonymise"`
	Drop     []string     `yaml:"drop"`
	Truncate []string     `yaml:"truncate"`
	Columns  []ColumnRule `yaml:"columns"`
	Keys     struct {
		Mode     string            `yaml:"mode"`
		TestKeys map[string]string `yaml:"test_keys"`
		Ignore   []Ignore          `yaml:"ignore"`
	} `yaml:"keys"`
	SideEffects struct {
		Echo struct {
			Install bool   `yaml:"install"`
			URL     string `yaml:"url"`
			Mode    string `yaml:"mode"`
			Proxy   string `yaml:"proxy"`
		} `yaml:"echo"`
		Cron            string `yaml:"cron"`
		ActionScheduler string `yaml:"action_scheduler"`
		Webhooks        string `yaml:"webhooks"`
		Gateways        string `yaml:"gateways"`
		Subscriptions   string `yaml:"subscriptions"`
		Mail            string `yaml:"mail"`
	} `yaml:"side_effects"`
	Noindex   bool `yaml:"noindex"`
	BasicAuth bool `yaml:"basic_auth"`
	Verify    struct {
		FailOn      string   `yaml:"fail_on"`
		AllowEmails []string `yaml:"allow_emails"`
		AllowPhones []string `yaml:"allow_phones"`
		Skip        []string `yaml:"skip"`
		Sign        bool     `yaml:"sign"`
		Ignore      []Ignore `yaml:"ignore"`
	} `yaml:"verify"`
	Laravel struct {
		EnvFile string                       `yaml:"env_file"`
		Tables  map[string]map[string]string `yaml:"tables"`
		Env     map[string]string            `yaml:"env"`
	} `yaml:"laravel"`
	// Sources maps a dotted key to where its value came from: "flag",
	// "file", "echo", "soundcheck" or "default", for config show. A
	// production list merged from several files names each, "file, echo".
	Sources map[string]string `yaml:"-"`
	// ProductionFrom maps each production entry to the file it came from:
	// "file", "echo" or "soundcheck" (or "flag" once the CLI adds one).
	ProductionFrom map[string]string `yaml:"-"`
	// Unset maps a dotted key to the ${ROADIE_*} variables it reads that
	// aren't set. config check lists them; Need fails a run that uses one.
	Unset map[string]string `yaml:"-"`

	interp map[string]string // dotted key -> variable read into it, masked by Show
	lines  map[string]int    // dotted key -> line in the file, for messages
	file   string            // the file's display name, "" for Default
}

// ColumnRule is one entry of columns:.
type ColumnRule struct {
	Table  string            `yaml:"table"`
	Column string            `yaml:"column"`
	Where  map[string]string `yaml:"where"`
	Rule   string            `yaml:"rule"`
}

// Ignore is one keys.ignore or verify.ignore entry. Until is "YYYY-MM-DD"
// or ""; the CLI turns it into verify.Ignore.
type Ignore struct {
	ID       string `yaml:"id"`
	Location string `yaml:"location"`
	Reason   string `yaml:"reason"`
	Until    string `yaml:"until"`
}

// Find returns the absolute path of the nearest .roadie/config.yaml in
// dir or a folder above it, as git finds .git. The search ends at the
// repository root (the folder holding .git, which is searched) and below
// $HOME (which isn't, unless dir is $HOME): a stray ~/.roadie would
// otherwise catch every project outside a repository. With nothing found
// it returns ErrNoConfig, which matches fs.ErrNotExist.
func Find(dir string) (path string, err error) {
	return find(dir)
}

// Load reads path strictly; problems are a *core.Error with code config
// (exit 2) naming the line. env reads ROADIE_ variables for ${ROADIE_*}
// interpolation; nil reads none. The production: lists of
// .echo/config.yaml and .soundcheck/config.yaml in the same project are
// merged in.
func Load(path string, env func(string) (string, bool)) (*Config, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	l := &loader{file: displayName(path), env: env, lines: map[string]int{}, present: map[string]bool{},
		interp: map[string]string{}, unset: map[string]string{}}
	c, err := l.load(b)
	if err != nil {
		return nil, err
	}
	if err := c.MergeProduction(ProjectDir(path)); err != nil {
		return nil, err
	}
	return c, nil
}

// Default returns the config with every default of PRD 8.1 filled in.
func Default() *Config {
	return defaults()
}

// InitTemplate returns the exact PRD 8.1 text config init writes for
// stack: wordpress and woocommerce (and auto or "") get the same file,
// laravel and sql their own. Any other stack gives nil.
func InitTemplate(stack string) []byte {
	return initTemplate(stack)
}

// Show prints the effective config as YAML with each value's source as
// a comment, or as JSON with the sources beside it. Test keys and every
// interpolated value are masked; a DSN password never shows.
func (c *Config) Show(json bool) ([]byte, error) {
	return c.show(json)
}

// LoadKey returns the consistency key of the project in dir and where it
// came from: "env" (ROADIE_KEY, base64 of 32 bytes, wins), "file"
// (.roadie/key) or "new" (made now and not saved; SaveKey keeps it).
func LoadKey(dir string, env func(string) (string, bool)) (pseudo.Key, string, error) {
	return loadKey(dir, env)
}

// SaveKey writes the key to .roadie/key (0600, folder 0700).
func SaveKey(dir string, k pseudo.Key) error {
	return saveKey(dir, k)
}

// ForgetKey removes .roadie/key; a missing file is not an error.
func ForgetKey(dir string) error {
	return forgetKey(dir)
}

// ExcludeFromGit adds paths (relative to dir, slash-separated) to the
// .git/info/exclude of the work tree dir is in and reports whether it
// changed the file. Outside a work tree it does nothing.
func ExcludeFromGit(dir string, paths []string) (bool, error) {
	return excludeFromGit(dir, paths)
}
