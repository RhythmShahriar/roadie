// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package roadiecfg loads, validates and prints .roadie/config.yaml and
// keeps the consistency key that makes fakes stable across refreshes.
//
// The exported names in this file are the frozen cross-lane API; the
// engine lane replaces the bodies, which return core.ErrNotBuilt until then.
package roadiecfg

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

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
	// "file", "echo", "soundcheck" or "default", for config show.
	Sources map[string]string `yaml:"-"`
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

// Find returns the nearest .roadie/config.yaml at or above dir.
func Find(dir string) (path string, err error) {
	return "", core.ErrNotBuilt
}

// Load reads path strictly; problems are a *core.Error with code config.
// env reads ROADIE_ variables for ${ROADIE_*} interpolation.
func Load(path string, env func(string) (string, bool)) (*Config, error) {
	return nil, core.ErrNotBuilt
}

// Default returns the config with every default filled in.
func Default() *Config {
	return nil
}

// InitTemplate returns the exact PRD 8.1 text config init writes for stack.
func InitTemplate(stack string) []byte {
	return nil
}

// Show prints the effective config, as YAML or JSON.
func (c *Config) Show(json bool) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// LoadKey returns the consistency key and where it came from: "env",
// "file" or "new".
func LoadKey(dir string, env func(string) (string, bool)) (pseudo.Key, string, error) {
	return pseudo.Key{}, "", core.ErrNotBuilt
}

// SaveKey writes the key to .roadie/key (0600).
func SaveKey(dir string, k pseudo.Key) error {
	return core.ErrNotBuilt
}

// ForgetKey removes .roadie/key.
func ForgetKey(dir string) error {
	return core.ErrNotBuilt
}

// ExcludeFromGit adds paths to .git/info/exclude and reports whether it changed.
func ExcludeFromGit(dir string, paths []string) (bool, error) {
	return false, core.ErrNotBuilt
}
