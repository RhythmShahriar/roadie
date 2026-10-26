// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

// Where a setting's value came from, as Sources and config show name it.
const (
	SourceFlag       = "flag"
	SourceFile       = "file"
	SourceEcho       = "echo"
	SourceSoundcheck = "soundcheck"
	SourceDefault    = "default"
)

// defaults builds the PRD 8.1 defaults. Lists and maps are empty rather
// than nil, so the YAML and JSON forms print [] and {} as the template does.
func defaults() *Config {
	c := &Config{
		Production: []string{},
		URLMap:     map[string]string{},
		Stack:      "auto",
		Profiles:   []string{"auto"},
		Drop:       []string{},
		Truncate:   []string{},
		Columns:    []ColumnRule{},
		Noindex:    true,
		Sources:    map[string]string{},
		Unset:      map[string]string{},
	}
	c.Remote.WP = "wp"
	p := &c.Pseudonymise
	p.EmailDomain = "example.invalid"
	p.PhoneStyle = "echo"
	p.Locale = "en"
	p.Keep = []string{"country", "state"}
	p.KeepUsers = []string{}
	p.Consistency = "per-run"
	p.OnUnparseable = "blank"
	p.Passwords = "reset"
	p.FreeText = "scrub"
	c.Keys.Mode = "swap"
	c.Keys.TestKeys = map[string]string{}
	c.Keys.Ignore = []Ignore{}
	s := &c.SideEffects
	s.Echo.Install = true
	s.Echo.URL = "http://127.0.0.1:8025"
	s.Echo.Mode = "seal"
	s.Cron = "pause"
	s.ActionScheduler = "pause"
	s.Webhooks = "pause"
	s.Gateways = "test-mode"
	s.Subscriptions = "staging"
	s.Mail = "seal"
	v := &c.Verify
	v.FailOn = "high"
	v.AllowEmails = []string{}
	v.AllowPhones = []string{}
	v.Skip = []string{}
	v.Sign = true
	v.Ignore = []Ignore{}
	c.Laravel.EnvFile = ".env"
	c.Laravel.Tables = map[string]map[string]string{}
	c.Laravel.Env = map[string]string{}
	return c
}
