// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"gopkg.in/yaml.v3"
)

// The placeholders a profile's table names hold. {prefix} is the
// WordPress table prefix; {site} is "" for the main site plus "<N>_" for
// every multisite blog the schema has, for the tables each blog owns.
const (
	PlaceholderPrefix = "{prefix}"
	PlaceholderSite   = "{site}"
)

// RowFixes are the named fixes a profile can ask for. Each is Go code in
// the engine, run on a row after its rules: values that must agree with
// each other (a hash of a JSON value, an index built from address parts)
// or rules that depend on a form's own field list.
var RowFixes = []string{
	"action-scheduler-args", "hpos-address-index", "gf-field-types", "wpforms-field-types", "ninja-field-types",
}

// AddressParts are the address parts pseudonymise.keep names. A rule
// with a part is kept when the part is listed and blanked or faked when
// it isn't.
var AddressParts = []string{"country", "state", "city", "postcode"}

// fileDef is a profile file as written in YAML.
type fileDef struct {
	ID             string    `yaml:"id"`
	Name           string    `yaml:"name"`
	Stack          string    `yaml:"stack"`
	CheckedAgainst string    `yaml:"checked_against"`
	Detect         fileDet   `yaml:"detect"`
	Rules          []fileRul `yaml:"rules"`
	Truncate       []string  `yaml:"truncate"`
	Drop           []string  `yaml:"drop"`
	Keep           []string  `yaml:"keep"`
	RowFixes       []string  `yaml:"row_fixes"`
}

type fileDet struct {
	TablesAny  []string `yaml:"tables_any"`
	OptionsAny []string `yaml:"options_any"`
	Files      []string `yaml:"files"`
}

type fileRul struct {
	ID      string            `yaml:"id"` // optional suffix after "<profile>.", for rules a where clause tells apart
	Table   string            `yaml:"table"`
	Column  string            `yaml:"column"`
	Where   map[string]string `yaml:"where"`
	Path    string            `yaml:"path"` // "json:billing.email"; the rule may also carry it as json:<path>=<rule>
	Rule    string            `yaml:"rule"`
	Part    string            `yaml:"part"` // an address part of pseudonymise.keep
	Explain string            `yaml:"explain"`
}

// Parse reads one profile file. Problems name the file and the rule.
func Parse(name string, b []byte) (Def, error) {
	var f fileDef
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return Def{}, fmt.Errorf("profile %s: %w", name, err)
	}
	d := Def{ID: f.ID, Name: f.Name, CheckedAgainst: f.CheckedAgainst, Stack: core.Stack(f.Stack),
		Detect:   DetectDef{TablesAny: f.Detect.TablesAny, OptionsAny: f.Detect.OptionsAny, Files: f.Detect.Files},
		Truncate: f.Truncate, Drop: f.Drop, Keep: f.Keep, RowFixes: f.RowFixes, Parts: map[string]string{}}
	if err := d.check(name); err != nil {
		return Def{}, err
	}
	seen := map[string]bool{}
	for i, r := range f.Rules {
		rule, err := d.rule(r)
		if err != nil {
			return Def{}, fmt.Errorf("profile %s rule %d (%s.%s): %w", name, i+1, r.Table, r.Column, err)
		}
		if seen[rule.ID] {
			return Def{}, fmt.Errorf("profile %s: rule ID %s is used twice; give one its own id", name, rule.ID)
		}
		seen[rule.ID] = true
		d.Rules = append(d.Rules, rule)
		if r.Part != "" {
			d.Parts[rule.ID] = r.Part
		}
	}
	return d, nil
}

// check validates the parts of a profile outside its rules.
func (d *Def) check(name string) error {
	switch {
	case d.ID == "" || strings.ContainsAny(d.ID, " ./"):
		return fmt.Errorf("profile %s: id %q must be a name like woocommerce", name, d.ID)
	case d.Name == "":
		return fmt.Errorf("profile %s: name is empty", name)
	case !strings.HasPrefix(d.CheckedAgainst, "install: ") && !strings.HasPrefix(d.CheckedAgainst, "docs: ") && !strings.HasPrefix(d.CheckedAgainst, "config: "):
		return fmt.Errorf("profile %s: checked_against must start with \"install: \", \"docs: \" or \"config: \"", name)
	case !slices.Contains([]core.Stack{core.StackWordPress, core.StackLaravel, core.StackSQL}, d.Stack):
		return fmt.Errorf("profile %s: stack %q must be wordpress, laravel or sql", name, d.Stack)
	}
	for _, fix := range d.RowFixes {
		if !slices.Contains(RowFixes, fix) {
			return fmt.Errorf("profile %s: row fix %q isn't one of %s", name, fix, strings.Join(RowFixes, ", "))
		}
	}
	for _, list := range [][]string{d.Truncate, d.Drop, d.Keep, d.Detect.TablesAny} {
		for _, t := range list {
			if err := checkTable(t); err != nil {
				return fmt.Errorf("profile %s: table %q: %w", name, t, err)
			}
		}
	}
	return nil
}

// checkTable validates a table name or glob with its placeholders.
func checkTable(t string) error {
	if t == "" {
		return fmt.Errorf("empty table name")
	}
	if strings.Contains(t, PlaceholderSite) && !strings.HasPrefix(t, PlaceholderPrefix+PlaceholderSite) {
		return fmt.Errorf("{site} must follow {prefix}: {prefix}{site}posts")
	}
	rest := strings.ReplaceAll(strings.ReplaceAll(t, PlaceholderPrefix, ""), PlaceholderSite, "")
	if strings.ContainsAny(rest, "{}") {
		return fmt.Errorf("unknown placeholder; only {prefix} and {site}")
	}
	return roadiecfg.ValidGlob(rest)
}

// rule turns one YAML rule into a core.Rule.
func (d *Def) rule(r fileRul) (core.Rule, error) {
	if err := checkTable(r.Table); err != nil {
		return core.Rule{}, err
	}
	if r.Column == "" {
		return core.Rule{}, fmt.Errorf("column is empty")
	}
	spec, err := roadiecfg.ParseRule(r.Rule)
	if err != nil {
		return core.Rule{}, err
	}
	if r.Path != "" {
		if spec.Path != "" {
			return core.Rule{}, fmt.Errorf("path is given twice, in path: and in the rule")
		}
		spec.Path = r.Path
	}
	for c, g := range r.Where {
		if err := roadiecfg.ValidGlob(g); err != nil {
			return core.Rule{}, fmt.Errorf("where %s: %q isn't a valid glob", c, g)
		}
	}
	if r.Part != "" && !slices.Contains(AddressParts, r.Part) {
		return core.Rule{}, fmt.Errorf("part %q isn't one of %s", r.Part, strings.Join(AddressParts, ", "))
	}
	if r.Explain == "" {
		return core.Rule{}, fmt.Errorf("explain is empty: say what the column holds and why the rule fits")
	}
	id := r.ID
	if id == "" {
		id = ruleSuffix(r.Table, r.Column, r.Where, spec.Path)
	}
	return core.Rule{ID: d.ID + "." + id, Profile: d.ID, Table: r.Table, Column: r.Column, Where: r.Where,
		Path: spec.Path, Action: spec.Action, Fake: spec.Fake, Value: spec.Value, Explain: r.Explain}, nil
}

// ruleSuffix builds "<table>.<column>[.<where values>][.<path>]" with the
// table's placeholders removed, as the rule ID format asks.
func ruleSuffix(table, column string, where map[string]string, p string) string {
	parts := []string{BareTable(table), column}
	keys := make([]string, 0, len(where))
	for k := range where {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, where[k])
	}
	if p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, ".")
}

// BareTable drops {prefix} and {site} from a profile table name.
func BareTable(t string) string {
	return strings.ReplaceAll(strings.ReplaceAll(t, PlaceholderPrefix, ""), PlaceholderSite, "")
}

// builtinFS holds the profiles built into the binary.
//
//go:embed builtin/*.yaml
var builtinFS embed.FS

// BuiltinVersion is the version of the profiles built into this binary
// when the binary's own version isn't a release (a dev build): the
// release they first shipped in. A downloaded pack is used only when its
// release version is newer (see Version).
const BuiltinVersion = "1.0.0"

// builtin parses every embedded profile, sorted by ID.
func builtin() ([]Def, error) {
	names, err := fs.Glob(builtinFS, "builtin/*.yaml")
	if err != nil {
		return nil, err
	}
	defs := make([]Def, 0, len(names))
	for _, n := range names {
		b, err := builtinFS.ReadFile(n)
		if err != nil {
			return nil, err
		}
		d, err := Parse(path.Base(n), b)
		if err != nil {
			return nil, err
		}
		if want := strings.TrimSuffix(path.Base(n), ".yaml"); d.ID != want {
			return nil, fmt.Errorf("profile %s: id %q differs from its file name", n, d.ID)
		}
		defs = append(defs, d)
	}
	slices.SortFunc(defs, func(a, b Def) int { return strings.Compare(a.ID, b.ID) })
	return defs, nil
}

// ByID returns the profile with this ID.
func ByID(defs []Def, id string) (Def, bool) {
	for _, d := range defs {
		if d.ID == id {
			return d, true
		}
	}
	return Def{}, false
}
