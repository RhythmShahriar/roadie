// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// gitExcludes are the paths config init keeps out of git (PRD 6.8).
var gitExcludes = roadiecfg.GitExcludes()

// stackWords say what config init wrote the file for (PRD 16.1).
var stackWords = map[string]string{
	"wordpress": "a WordPress site", "woocommerce": "a WooCommerce site",
	"laravel": "a Laravel app", "sql": "your own SQL schema",
}

// runConfig is config init|check|show|key (PRD 6.8).
func runConfig(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	subs := []string{"init", "check", "show", "key"}
	if len(p.args) == 0 {
		return usageError(stderr, "config", "say what to do: %s", orList(subs))
	}
	switch p.args[0] {
	case "init":
		return configInit(ctx, p, stdout, stderr)
	case "check":
		return configCheck(p, stdout, stderr)
	case "show":
		return configShow(ctx, p, stdout, stderr)
	case "key":
		return configKey(ctx, p, stdout, stderr)
	}
	msg := fmt.Sprintf("unknown subcommand %q; want %s", p.args[0], orList(subs))
	if s := cli.Suggest(p.args[0], subs); s != "" {
		msg += "\n  did you mean: " + prog() + " config " + s
	}
	return usageError(stderr, "config", "%s", msg)
}

// configInit writes .roadie/config.yaml from the template of the stack.
// With --from it reads the source's schema as plan --schema-only does and
// adds the columns no profile covers as commented suggestions.
func configInit(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	if len(p.args) > 1 {
		return usageError(stderr, "config", "config init takes no arguments; got %q", p.args[1])
	}
	if _, err := os.Stat(configPath); err == nil && !p.f.force {
		return usageError(stderr, "config", "%s exists; pass --force to replace it", configPath)
	}
	stack := p.f.stack
	var suggestions []core.Uncovered
	if p.f.from != "" {
		r, err := initScan(ctx, p, stderr)
		if err != nil {
			return err
		}
		suggestions = r.Uncovered
		if stack == "" {
			stack = detectedStack(r.Stack)
		}
	}
	if stack == "" {
		stack = "wordpress"
	}
	tmpl := dep.initTemplate(stack)
	if len(tmpl) == 0 {
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: errors.New("config init: the template for " + stack + " isn't built yet")}
	}
	b := addSuggestions(tmpl, suggestions)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: err}
	}
	// The config is meant to be committed and read by the team: the
	// user's usual file mode, never a secret (PRD 8.1).
	if err := os.WriteFile(configPath, b, 0o644); err != nil { // #nosec G306 -- a committed settings file, no secrets
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: err}
	}
	fmt.Fprintf(stdout, "Wrote .roadie/config.yaml for %s.\n", stackWords[stack])
	if len(suggestions) > 0 {
		fmt.Fprintf(stdout, "Listed %s that look personal under columns: as suggestions; check each one.\n",
			plural(int64(len(suggestions)), "column", "columns"))
	}
	fmt.Fprintln(stdout, "Set source:, target: and url: in it, then run rhythm-roadie plan.")
	excludeReports(stdout, stderr)
	return nil
}

// excludeReports adds gitExcludes to .git/info/exclude (for config init
// and the picker's save) and says so when it changed anything.
func excludeReports(stdout, stderr io.Writer) {
	changed, err := dep.excludeFromGit(".", gitExcludes)
	if err != nil {
		fmt.Fprintf(stderr, "%s config: couldn't update .git/info/exclude: %v\n", prog(), err)
	} else if changed {
		fmt.Fprintln(stdout, "Added .roadie/reports, .roadie/key, .roadie/backups and .roadie/basic-auth to .git/info/exclude.")
	}
}

// initScan reads the --from source's schema read-only, as plan
// --schema-only does, for the stack and the uncovered columns.
func initScan(ctx context.Context, p *parsed, stderr io.Writer) (*core.Result, error) {
	from, err := parseEndpoint(p.f.from, "--from", "ROADIE_FROM_PASSWORD", func(msg string) {
		fmt.Fprintf(stderr, "%s config: %s\n", prog(), msg)
	})
	if err != nil {
		return nil, endpointError(stderr, "config", err)
	}
	// --stack reaches the schema read (woocommerce is a WordPress
	// template). With none, a schema no profile matches is read as stack
	// sql: init's job there is to list its personal-looking columns.
	stack := p.f.stack
	if stack == "woocommerce" {
		stack = "wordpress"
	}
	scan := func(stack string) (*core.Result, error) {
		j := engine.Job{Mode: engine.Plan, From: from, Clock: dep.now,
			Opts: engine.Options{SchemaOnly: true, Offline: p.offline(dep.env), Keys: "swap", EmailDomain: defaultEmailDomain, Stack: stack}}
		r, err := dep.run(ctx, j)
		if err == nil && r != nil && r.Err != nil {
			err = r.Err
		}
		return r, err
	}
	r, err := scan(stack)
	var ce *core.Error
	if err != nil && stack == "" && errors.As(err, &ce) && ce.Code == core.ErrStack {
		r, err = scan("sql")
	}
	if err != nil {
		return nil, runError(err)
	}
	if r == nil {
		r = &core.Result{}
	}
	return r, nil
}

// detectedStack is the init template a detected stack gets.
func detectedStack(s core.StackInfo) string {
	switch s.Name {
	case core.StackLaravel:
		return "laravel"
	case core.StackSQL:
		return "sql"
	case core.StackWordPress:
		for _, p := range s.Profiles {
			if strings.HasPrefix(p, "woocommerce") {
				return "woocommerce"
			}
		}
		return "wordpress"
	}
	return ""
}

// ruleGuess is the columns: rule a suggestion starts with, from the name.
func ruleGuess(column string) string {
	c := strings.ToLower(column)
	if c == "ip" || strings.HasPrefix(c, "ip_") || strings.HasSuffix(c, "_ip") || strings.Contains(c, "ip_address") {
		return "ip" // before "address"; never for zip or shipping
	}
	for _, g := range []struct{ part, rule string }{
		{"email", "email"}, {"mail", "email"}, {"phone", "phone"}, {"mobile", "phone"}, {"tel", "phone"}, {"fax", "phone"},
		{"first", "first_name"}, {"last", "last_name"}, {"surname", "last_name"}, {"name", "full_name"},
		{"street", "street"}, {"address", "address"}, {"city", "city"}, {"postcode", "postcode"}, {"zip", "postcode"},
		{"birth", "dob"}, {"dob", "dob"},
	} {
		if strings.Contains(c, g.part) {
			return g.rule
		}
	}
	return "blank"
}

// addSuggestions puts the uncovered columns under the template's
// columns: line as commented entries to check and uncomment.
func addSuggestions(tmpl []byte, us []core.Uncovered) []byte {
	if len(us) == 0 {
		return tmpl
	}
	var block bytes.Buffer
	block.WriteString("# Suggested by config init --from: these columns look personal and no profile covers them.\n")
	block.WriteString("# Check each rule, then uncomment it (and set columns: to the list).\n")
	for _, u := range us {
		fmt.Fprintf(&block, "#  - table: %s\n#    column: %s\n#    rule: %s\n", commentText(u.Table), commentText(u.Column), ruleGuess(u.Column))
	}
	lines := strings.SplitAfter(string(tmpl), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "columns:") {
			out := strings.Join(lines[:i+1], "") + block.String() + strings.Join(lines[i+1:], "")
			return []byte(out)
		}
	}
	return append(append(append([]byte{}, tmpl...), '\n'), block.Bytes()...)
}

// yamlBreaks are what yaml.v3 reads as a line break: CR, LF, NEL, LINE
// SEPARATOR and PARAGRAPH SEPARATOR (term.SafeText keeps the last two).
var yamlBreaks = strings.NewReplacer("\r", " ", "\n", " ", "\u0085", " ", "\u2028", " ", "\u2029", " ")

// commentText makes a name read from the source safe inside a YAML
// comment. The source is untrusted: a table or column name with a line
// break YAML reads as one would end the comment and add real config keys.
func commentText(s string) string { return yamlBreaks.Replace(safe(s)) }

// roadieVar is a ${ROADIE_*} reference in the config.
var roadieVar = regexp.MustCompile(`\$\{(ROADIE_[A-Z0-9_]+)\}`)

// configCheck validates the config and lists the ${ROADIE_*} variables
// that are unset: exit 2 on a bad file, 0 otherwise. With --json it
// writes a rhythm-roadie/v1 envelope of kind config-check.
func configCheck(p *parsed, stdout, stderr io.Writer) error {
	path, err := p.configFile()
	if err != nil {
		return usageError(stderr, "config", "%v", err)
	}
	if _, err := dep.loadConfig(path, dep.env); err != nil {
		if p.f.g.json {
			ce := asCoreError(err)
			if werr := writeEnvelope(stdout, "config-check", map[string]any{"path": path, "valid": false, "error": ce}); werr != nil {
				return werr
			}
			return &cli.ExitError{Code: ce.Exit}
		}
		return runError(err)
	}
	b, err := os.ReadFile(path) // #nosec G304 G703 -- the user's own settings file
	if err != nil {
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: err}
	}
	type unsetVar struct {
		Name string `json:"name"`
		Line int    `json:"line"`
	}
	unset := []unsetVar{}
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		for _, m := range roadieVar.FindAllStringSubmatch(yamlCode(line), -1) {
			if _, ok := dep.env(m[1]); !ok && !seen[m[1]] {
				seen[m[1]] = true
				unset = append(unset, unsetVar{m[1], i + 1})
			}
		}
	}
	if p.f.g.json {
		return writeEnvelope(stdout, "config-check", map[string]any{"path": path, "valid": true, "unset": unset})
	}
	fmt.Fprintf(stdout, "%s is valid.\n", path)
	for _, u := range unset {
		fmt.Fprintf(stdout, "Unset: %s (line %d); set it before a run that reads it.\n", u.Name, u.Line)
	}
	return nil
}

// yamlCode is a config line without its comment: a # at the start or
// after a space or tab, outside quotes, starts one (YAML 1.2).
func yamlCode(line string) string {
	var quote rune
	prev := ' '
	for i, c := range line {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (prev == ' ' || prev == '\t'):
			return line[:i]
		}
		prev = c
	}
	return line
}

// configFile is the settings file config check and show read.
func (p *parsed) configFile() (string, error) {
	if p.f.g.noConfig {
		return "", errors.New("--no-config leaves no file to read")
	}
	if p.f.g.config != "" {
		return p.f.g.config, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	found, err := dep.findConfig(wd)
	if err != nil || found == "" {
		return "", errors.New("no .roadie/config.yaml here or above; rhythm-roadie config init writes one")
	}
	return found, nil
}

// configShow prints the effective config with defaults and sources and
// the rules it gives, expanded: its own columns: and those of every
// profile it may apply. Secrets are masked by Show. With --json it is a
// rhythm-roadie/v1 envelope of kind config.
func configShow(ctx context.Context, p *parsed, stdout, _ io.Writer) error {
	cfg, _, err := p.loadConfig()
	if err != nil {
		return runError(err)
	}
	if cfg == nil {
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: core.ErrNotBuilt}
	}
	b, err := cfg.Show(p.f.g.json)
	if err != nil {
		return runError(err)
	}
	defs, _, err := dep.loadProfiles(ctx, p.offline(dep.env))
	if err != nil {
		return runError(err)
	}
	rules, note := expandedRules(cfg, defs)
	if p.f.g.json {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(b, &data); err != nil {
			return runError(err)
		}
		r, err := json.Marshal(map[string]any{"note": note, "rules": rules})
		if err != nil {
			return runError(err)
		}
		data["rules"] = r
		return writeEnvelope(stdout, "config", data)
	}
	var out bytes.Buffer
	out.Write(b)
	fmt.Fprintf(&out, "\n# Rules, expanded: %s\n", commentText(note))
	from := ""
	for _, r := range rules {
		if r.From != from {
			from = r.From
			fmt.Fprintf(&out, "# %s\n", commentText(from))
		}
		fmt.Fprintf(&out, "#   %s\n", commentText(r.line()))
	}
	_, err = stdout.Write(out.Bytes())
	return err
}

// shownRule is one rule config show lists.
type shownRule struct {
	From   string            `json:"from"` // "columns" or a profile ID
	Table  string            `json:"table"`
	Column string            `json:"column,omitempty"`
	Where  map[string]string `json:"where,omitempty"`
	Path   string            `json:"path,omitempty"`
	Action string            `json:"action"`
	Fake   string            `json:"fake,omitempty"`
	Kept   string            `json:"kept,omitempty"` // the pseudonymise.keep part that keeps it
}

// line is a rule as one line: "wp_users.user_email (meta_key = x) → json:a: fake email".
func (r shownRule) line() string {
	s := r.Table
	if r.Column != "" {
		s += "." + r.Column
	}
	if r.Path != "" {
		s += " → " + r.Path
	}
	for _, w := range sortedWhere(r.Where) {
		s += " (" + w + ")"
	}
	if r.Kept != "" {
		return s + ": " + keptLabel(r.Kept)
	}
	s += ": " + r.Action
	if r.Fake != "" {
		s += " " + r.Fake
	}
	return s
}

// expandedRules lists the rules the config gives: its columns: (and
// laravel.tables) first, then each profile it may apply, with a note on
// when a profile's rules apply. profiles: names them; otherwise every
// profile of the stack (all of them with stack auto) applies when its
// tables, options or files are found.
func expandedRules(cfg *roadiecfg.Config, defs []profile.Def) ([]shownRule, string) {
	out := []shownRule{}
	for _, c := range cfg.Columns {
		r := shownRule{From: "columns", Table: c.Table, Column: c.Column, Where: c.Where, Action: c.Rule}
		if spec, err := roadiecfg.ParseRule(c.Rule); err == nil {
			r.Action, r.Fake, r.Path = string(spec.Action), string(spec.Fake), spec.Path
		}
		out = append(out, r)
	}
	if cfg.Stack == "laravel" {
		for t, cols := range cfg.Laravel.Tables {
			for col, rule := range cols {
				r := shownRule{From: "laravel.tables", Table: t, Column: col, Action: rule}
				if spec, err := roadiecfg.ParseRule(rule); err == nil {
					r.Action, r.Fake, r.Path = string(spec.Action), string(spec.Fake), spec.Path
				}
				out = append(out, r)
			}
		}
		slices.SortStableFunc(out, func(a, b shownRule) int { return strings.Compare(a.From+a.Table+a.Column, b.From+b.Table+b.Column) })
	}
	named := slices.DeleteFunc(slices.Clone(cfg.Profiles), func(id string) bool { return id == "auto" })
	note := "each profile's rules apply when its tables, options or files are found; columns: always apply"
	switch {
	case len(named) > 0:
		note = "the profiles profiles: names; columns: always apply"
	case cfg.Stack == "sql":
		note = "stack sql: only columns: apply"
		return out, note
	}
	for _, d := range defs {
		if len(named) > 0 && !slices.Contains(named, d.ID) || len(named) == 0 && cfg.Stack != "auto" && cfg.Stack != "" && string(d.Stack) != cfg.Stack {
			continue
		}
		for _, r := range d.Rules {
			out = append(out, shownRule{From: d.ID + " (" + d.Name + ")", Table: r.Table, Column: r.Column, Where: r.Where, Path: r.Path,
				Action: string(r.Action), Fake: string(r.Fake), Kept: keptPart(d, r.ID, cfg.Pseudonymise.Keep)})
		}
		for _, l := range []struct {
			action string
			list   []string
		}{{"truncate", d.Truncate}, {"drop", d.Drop}, {"keep", d.Keep}} {
			for _, t := range l.list {
				out = append(out, shownRule{From: d.ID + " (" + d.Name + ")", Table: t, Action: l.action})
			}
		}
	}
	return out, note
}

// projectDir is the folder .roadie/ lives in: the config's, else here.
func (p *parsed) projectDir() string {
	if path, err := p.configFile(); err == nil {
		return roadiecfg.ProjectDir(path)
	}
	return "."
}

// keyExists reports whether the key file is there. Any error but "not
// there" counts as there, so forget still tries and reports it.
func keyExists(path string) bool {
	_, err := os.Stat(path) // #nosec G703 -- the project's own key file
	return !errors.Is(err, fs.ErrNotExist)
}

// configKey is config key show|rotate|forget.
func configKey(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	acts := []string{"show", "rotate", "forget"}
	if len(p.args) != 2 {
		return usageError(stderr, "config", "config key needs one of %s", orList(acts))
	}
	dir := p.projectDir()
	keyFile := filepath.Join(dir, ".roadie", "key")
	_, fromEnv := dep.env("ROADIE_KEY")
	switch p.args[1] {
	case "show":
		k, source, err := dep.loadKey(dir, dep.env)
		if err != nil {
			return runError(err)
		}
		switch source {
		case "env":
			if keyExists(keyFile) {
				fmt.Fprintf(stdout, "%s from ROADIE_KEY (it wins over %s)\n", k.Fingerprint(), keyFile)
			} else {
				fmt.Fprintf(stdout, "%s from ROADIE_KEY\n", k.Fingerprint())
			}
		case "file":
			fmt.Fprintf(stdout, "%s from %s\n", k.Fingerprint(), keyFile)
		default:
			fmt.Fprintln(stdout, "No consistency key is kept: each copy uses a new one (consistency: per-run). --keep-key or consistency: stable keeps one in .roadie/key.")
		}
		return nil
	case "rotate":
		if dep.terminal() && !p.f.force {
			if !newLineReader(dep.stdin).yesNo(ctx, stderr, "Replace the consistency key? Every person gets a new fake on the next copy. [y/N]", false) {
				return nil
			}
		} else if !p.f.force {
			return usageError(stderr, "config", "config key rotate replaces the key; pass --force to do it without a terminal")
		}
		k, err := dep.newKey()
		if err == nil {
			err = dep.saveKey(dir, k)
		}
		if err != nil {
			return runError(err)
		}
		fmt.Fprintf(stdout, "New consistency key %s in %s. Each person gets a new fake on the next copy.\n", k.Fingerprint(), keyFile)
		if fromEnv {
			fmt.Fprintln(stdout, "ROADIE_KEY is set and wins over the file; change it there too.")
		}
		return nil
	case "forget":
		if !keyExists(keyFile) {
			fmt.Fprintf(stdout, "No %s to remove.\n", keyFile)
		} else {
			if err := dep.forgetKey(dir); err != nil {
				return runError(err)
			}
			fmt.Fprintf(stdout, "Removed %s. Copies use a new key each run until one is kept again.\n", keyFile)
		}
		if fromEnv {
			fmt.Fprintln(stdout, "ROADIE_KEY is still set and is used while it is.")
		}
		return nil
	}
	msg := fmt.Sprintf("unknown key action %q; want %s", p.args[1], orList(acts))
	if s := cli.Suggest(p.args[1], acts); s != "" {
		msg += "\n  did you mean: " + prog() + " config key " + s
	}
	return usageError(stderr, "config", "%s", msg)
}
