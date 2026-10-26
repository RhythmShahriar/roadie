// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// A fixture directory, testbed/fixtures/<id>/, holds:
//
//	fixture.yaml   what the fixture is (Meta)
//	db.sql.tmpl    the input database dump, a template (see template.go)
//	*.sql.tmpl     other dumps of the scenario, such as prod.sql.tmpl for
//	               verify --from; they share the scenario's canaries
//	files/         the input file tree; files ending in .tmpl are rendered
//	               and lose the suffix, the rest are copied as they are
//
// Templates in testbed/fixtures/_shared/*.tmpl are parsed with every
// fixture's templates, so a {{define}} there (the CREATE TABLE of a
// WordPress table, the mysqldump header) is one {{template}} away. A
// directory whose name starts with _ is not a fixture.
//
// Seeding writes runtime/fixtures/<id>/db.sql (and the other dumps),
// runtime/fixtures/<id>/files/,
// runtime/fixtures/<id>/serialized.json (MySQL dumps, when PHP is given)
// and runtime/canaries.json.

// Meta is a fixture's fixture.yaml.
type Meta struct {
	Description string `yaml:"description"`
	Stack       string `yaml:"stack"`   // wordpress, woocommerce, laravel, sql, ...
	Dialect     string `yaml:"dialect"` // mysql (the default) or postgres
	// Canaries says the fixture plants canaries. A clean (good) input
	// plants none and a verify-only fixture may plant only keys.
	Canaries bool `yaml:"canaries"`
	// Generated names the program that writes the fixture's input at make
	// up instead of a template (r17's large dump comes from testbed/gen);
	// that program also adds the fixture's canaries to canaries.json.
	Generated string `yaml:"generated"`
}

// Fixture is one directory under testbed/fixtures.
type Fixture struct {
	ID   string
	Dir  string
	Meta Meta
}

// Fixtures lists the fixtures under dir, sorted by ID.
func Fixtures(dir string) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Fixture
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		f := Fixture{ID: e.Name(), Dir: filepath.Join(dir, e.Name())}
		b, err := os.ReadFile(filepath.Join(f.Dir, "fixture.yaml")) // #nosec G304 -- the testbed's own fixtures
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", f.ID, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&f.Meta); err != nil {
			return nil, fmt.Errorf("fixture %s: fixture.yaml: %w", f.ID, err)
		}
		if f.Meta.Dialect == "" {
			f.Meta.Dialect = "mysql"
		}
		out = append(out, f)
	}
	return out, nil
}

// Options says what Run seeds.
type Options struct {
	Fixtures string // testbed/fixtures
	Out      string // testbed/runtime
	Seed     int64
	// Only limits the run to these fixture IDs; the other scenarios'
	// entries in an existing canaries.json are kept.
	Only []string
	// PHP builds serialized.json; with no command it is not written.
	PHP oracle.PHP
}

// Result is what a run made.
type Result struct {
	Canaries []Canary
	// PerFixture counts canaries (keys included) per fixture ID.
	PerFixture map[string]int
}

// CanaryFileVersion is the canaries.json version (oracle.CanaryFileVersion).
const CanaryFileVersion = 1

type canaryFile struct {
	Version  int      `json:"version"`
	Seed     int64    `json:"seed"`
	Canaries []Canary `json:"canaries"`
}

// Run renders the fixtures and writes the runtime files, 0600 in 0700
// directories: they hold the canaries and the keys.
func Run(ctx context.Context, o Options) (*Result, error) {
	fixtures, err := Fixtures(o.Fixtures)
	if err != nil {
		return nil, err
	}
	res := &Result{PerFixture: map[string]int{}}
	for _, f := range fixtures {
		if len(o.Only) > 0 && !slices.Contains(o.Only, f.ID) {
			continue
		}
		if f.Meta.Generated != "" {
			res.PerFixture[f.ID] = 0
			continue
		}
		cs, err := renderFixture(ctx, f, o)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", f.ID, err)
		}
		res.Canaries = append(res.Canaries, cs...)
		res.PerFixture[f.ID] = len(cs)
	}
	for _, id := range o.Only {
		if _, ok := res.PerFixture[id]; !ok {
			return nil, fmt.Errorf("no fixture %q under %s", id, o.Fixtures)
		}
	}
	// Keep what this run didn't make: the other scenarios' canaries when
	// seeding only some, plus a generated fixture's always (r17's come
	// from testbed/gen, which is slow to rerun).
	generated := map[string]bool{}
	for _, f := range fixtures {
		generated[f.ID] = f.Meta.Generated != ""
	}
	kept, err := keepCanaries(filepath.Join(o.Out, "canaries.json"), func(scenario string) bool {
		if len(o.Only) > 0 {
			return !slices.Contains(o.Only, scenario)
		}
		return generated[scenario]
	})
	if err != nil {
		return nil, err
	}
	res.Canaries = append(kept, res.Canaries...)
	sort.SliceStable(res.Canaries, func(i, j int) bool { return res.Canaries[i].Scenario < res.Canaries[j].Scenario })
	b, err := json.MarshalIndent(canaryFile{Version: CanaryFileVersion, Seed: o.Seed, Canaries: res.Canaries}, "", " ")
	if err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(o.Out, "canaries.json"), append(b, '\n')); err != nil {
		return nil, err
	}
	return res, nil
}

// keepCanaries reads the canaries of every scenario keep accepts from an
// existing canaries.json, so reseeding one scenario keeps the rest.
func keepCanaries(path string, keep func(scenario string) bool) ([]Canary, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own runtime file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f canaryFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Canary
	for _, c := range f.Canaries {
		if keep(c.Scenario) {
			out = append(out, c)
		}
	}
	return out, nil
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600) // #nosec G703 -- under the operator's runtime directory
}

func renderFixture(ctx context.Context, f Fixture, o Options) ([]Canary, error) {
	shared, err := filepath.Glob(filepath.Join(filepath.Dir(f.Dir), "_shared", "*.tmpl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(shared)
	r := &renderer{shared: shared, scenario: f.ID, gen: newGenerator(o.Seed, f.ID), byName: map[string]*Canary{}, counts: map[string]int{}, values: map[string]bool{}}
	out := filepath.Join(o.Out, "fixtures", f.ID)
	if err := os.RemoveAll(out); err != nil {
		return nil, err
	}
	dumps, err := filepath.Glob(filepath.Join(f.Dir, "*.sql.tmpl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(dumps)
	hasDB := false
	for _, tmpl := range dumps {
		name := strings.TrimSuffix(filepath.Base(tmpl), ".tmpl")
		hasDB = hasDB || name == "db.sql"
		b, err := r.render(tmpl)
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(out, name), b); err != nil {
			return nil, err
		}
	}
	files := filepath.Join(f.Dir, "files")
	err = filepath.WalkDir(files, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == files {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(files, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(out, "files", rel)
		var b []byte
		if strings.HasSuffix(path, ".tmpl") {
			dst = strings.TrimSuffix(dst, ".tmpl")
			b, err = r.render(path)
		} else {
			b, err = os.ReadFile(path) // #nosec G304 G122 -- the testbed's own fixtures, not an attacker's tree
		}
		if err != nil {
			return err
		}
		return writeFile(dst, b)
	})
	if err != nil {
		return nil, err
	}
	if hasDB && f.Meta.Dialect == "mysql" && len(o.PHP.Cmd) > 0 {
		rc, err := oracle.OpenDump(filepath.Join(out, "db.sql"))
		if err != nil {
			return nil, err
		}
		cells, err := oracle.SerializedCells(ctx, rc, o.PHP)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		if cells == nil {
			cells = []oracle.Location{}
		}
		b, err := json.MarshalIndent(oracle.SerializedFile{Version: 1, Cells: cells}, "", " ")
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(out, "serialized.json"), append(b, '\n')); err != nil {
			return nil, err
		}
	}
	var cs []Canary
	for _, c := range r.list {
		if len(c.Encodings) == 0 {
			c.Encodings = []string{"raw"}
		}
		slices.Sort(c.Encodings)
		cs = append(cs, *c)
	}
	return cs, nil
}

func (r *renderer) render(path string) ([]byte, error) {
	src, err := os.ReadFile(path) // #nosec G304 -- the testbed's own fixtures
	if err != nil {
		return nil, err
	}
	var t *template.Template
	funcs := r.funcs()
	// include runs a named template into a string, so its output can be
	// an argument (escaped by sql, say), which {{template}} can't be.
	funcs["include"] = func(name string, data ...any) (string, error) {
		var b bytes.Buffer
		var d any
		if len(data) > 0 {
			d = data[0]
		}
		err := t.ExecuteTemplate(&b, name, d)
		return b.String(), err
	}
	t = template.New(filepath.Base(path)).Funcs(funcs).Option("missingkey=error")
	for _, sh := range r.shared {
		b, err := os.ReadFile(sh) // #nosec G304 -- the testbed's own fixtures
		if err != nil {
			return nil, err
		}
		if _, err := t.New(filepath.Base(sh)).Parse(string(b)); err != nil {
			return nil, err
		}
	}
	if _, err := t.Parse(string(src)); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, nil); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
