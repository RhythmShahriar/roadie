// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/roadie/core"
	"gopkg.in/yaml.v3"
)

// Dir is the project folder Roadie keeps its files in.
const Dir = ".roadie"

// File is the settings file inside Dir.
const File = "config.yaml"

// WikiURL is the reference page every unknown-key message points at.
const WikiURL = "https://github.com/RhythmShahriar/rhythm/wiki/Roadie-Configuration"

// ErrNoConfig is what Find returns when no folder it searched has a
// .roadie/config.yaml.
var ErrNoConfig = fmt.Errorf("no .roadie/config.yaml here or above; run rhythm-roadie config init: %w", fs.ErrNotExist)

// find is Find: it walks up from dir to the first .roadie/config.yaml.
func find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if h, err := filepath.Abs(h); err == nil {
			home = h
		}
	}
	for {
		p := filepath.Join(dir, Dir, File)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
		// .git is a folder in a clone and a file in a worktree or submodule.
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return "", ErrNoConfig
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == home {
			return "", ErrNoConfig
		}
		dir = parent
	}
}

// ProjectDir returns the folder a settings file belongs to: the parent of
// .roadie for .roadie/config.yaml, else the file's own folder. The key,
// reports and the Echo and Soundcheck settings are looked up there.
func ProjectDir(configPath string) string {
	dir := filepath.Dir(configPath)
	if filepath.Base(dir) == Dir {
		return filepath.Dir(dir)
	}
	return dir
}

// displayName is how messages name the file: ".roadie/config.yaml" for
// the usual place, else the path as given.
func displayName(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == Dir && filepath.Base(path) == File {
		return Dir + "/" + File
	}
	return filepath.ToSlash(path)
}

// loader holds what one Load learns about the file on the way.
type loader struct {
	file    string // display name for messages
	env     func(string) (string, bool)
	lines   map[string]int    // dotted key -> line
	present map[string]bool   // leaf keys the file sets
	interp  map[string]string // dotted key -> ROADIE_ variable read into it
	unset   map[string]string // dotted key -> ROADIE_ variable that isn't set
	// read holds, per dotted key, the variables read into its value and
	// what they held, so a message about the value shows ${NAME} instead
	// of a secret the environment supplied.
	read map[string][]readVar
}

// readVar is one ${ROADIE_*} reference and the value it was replaced by.
type readVar struct{ name, value string }

// errAt builds the *core.Error code config for a problem with key.
func (l *loader) errAt(key, format string, a ...any) *core.Error {
	return core.Errorf(core.ErrConfig, 2, "%s%s: %s", l.file, l.lineOf(key), l.redact(key, fmt.Sprintf(format, a...)))
}

// redact puts the ${NAME} reference back wherever msg shows a value read
// from the environment into key or a key below it. Those values are
// often secrets (a token in an Echo URL, a DSN password); an error
// message reaches the terminal and CI logs. A value of three bytes or
// less is only replaced where it stands quoted, so a short value can't
// eat letters of the message around it.
func (l *loader) redact(key, msg string) string {
	var vars []readVar
	for _, k := range sortedKeys(l.read) {
		if k == key || strings.HasPrefix(k, key+".") || strings.HasPrefix(k, key+"[") {
			vars = append(vars, l.read[k]...)
		}
	}
	// Longest first, so a value that holds another is replaced whole.
	slices.SortStableFunc(vars, func(a, b readVar) int { return len(b.value) - len(a.value) })
	for _, v := range vars {
		if v.value == "" {
			continue
		}
		ref := "${" + v.name + "}"
		q := strconv.Quote(v.value)
		inner := q[1 : len(q)-1] // as %q writes it inside a quoted value
		if len(v.value) <= 3 {
			msg = strings.ReplaceAll(msg, q, `"`+ref+`"`)
			continue
		}
		msg = strings.ReplaceAll(msg, inner, ref)
		if inner != v.value {
			msg = strings.ReplaceAll(msg, v.value, ref)
		}
	}
	return msg
}

// lineOf returns " line N" for key, or for the nearest parent key the
// file has, or "" when there is none.
func (l *loader) lineOf(key string) string {
	for k := key; k != ""; k = parentKey(k) {
		if n := l.lines[k]; n > 0 {
			return fmt.Sprintf(" line %d", n)
		}
	}
	return ""
}

// parentKey drops the last segment of a dotted key: "a.b[2].c" gives
// "a.b[2]", which gives "a.b".
func parentKey(k string) string {
	i := strings.LastIndexAny(k, ".[")
	if i < 0 {
		return ""
	}
	return k[:i]
}

// load decodes data over Default and validates the result.
func (l *loader) load(data []byte) (*Config, error) {
	c := Default()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, l.syntaxError(err)
	}
	if len(doc.Content) > 0 {
		root := doc.Content[0]
		if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
			root = nil // a file of comments only
		}
		if root != nil {
			if root.Kind != yaml.MappingNode {
				return nil, core.Errorf(core.ErrConfig, 2, "%s line %d: the file must be a list of settings like \"source:\" and \"stack: auto\"", l.file, root.Line)
			}
			if err := l.walk(root, reflect.TypeFor[Config](), ""); err != nil {
				return nil, err
			}
			if err := root.Decode(c); err != nil {
				// walk checked every kind, so this is a value yaml itself
				// refuses; name the file and keep yaml's own words.
				return nil, core.Errorf(core.ErrConfig, 2, "%s: %s", l.file, strings.TrimPrefix(err.Error(), "yaml: "))
			}
		}
	}
	c.Unset = l.unset
	c.interp = l.interp
	c.lines = l.lines
	c.file = l.file
	c.Sources = map[string]string{}
	for _, k := range leafKeys(reflect.TypeFor[Config](), "") {
		if l.present[k] {
			c.Sources[k] = SourceFile
		} else {
			c.Sources[k] = SourceDefault
		}
	}
	if err := l.validate(c); err != nil {
		return nil, err
	}
	return c, nil
}

// yamlLine finds yaml's "line N: what" in a syntax error.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// syntaxError turns a yaml parse error into the config message.
func (l *loader) syntaxError(err error) error {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		return core.Errorf(core.ErrConfig, 2, "%s line %s: this isn't valid YAML: %s", l.file, m[1], m[2])
	}
	return core.Errorf(core.ErrConfig, 2, "%s: this isn't valid YAML: %s", l.file, strings.TrimPrefix(err.Error(), "yaml: "))
}

// fieldNames maps a struct's yaml names to field indexes.
func fieldNames(t reflect.Type) (names []string, index map[string]int) {
	index = map[string]int{}
	for i := range t.NumField() {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		names = append(names, tag)
		index[tag] = i
	}
	return names, index
}

// leafKeys lists the dotted keys of every setting that isn't a group of
// settings: scalars, lists and maps.
func leafKeys(t reflect.Type, prefix string) []string {
	var out []string
	names, index := fieldNames(t)
	for _, n := range names {
		key := join(prefix, n)
		ft := t.Field(index[n]).Type
		if ft.Kind() == reflect.Struct {
			out = append(out, leafKeys(ft, key)...)
			continue
		}
		out = append(out, key)
	}
	return out
}

// join appends a segment to a dotted key.
func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// walk checks n against the Go type t before decoding: every key known,
// every value of the right kind, no key twice. It records lines, the
// leaf keys the file sets and interpolates ${ROADIE_*} in string values.
func (l *loader) walk(n *yaml.Node, t reflect.Type, key string) error {
	if n.Kind == yaml.AliasNode {
		// Aliases can expand a small file into a huge one (the billion
		// laughs attack); a settings file has no need for them.
		return core.Errorf(core.ErrConfig, 2, "%s line %d: %s uses an alias (*%s); write the value out instead", l.file, n.Line, key, n.Value)
	}
	if t.Kind() != reflect.Struct && key != "" {
		l.present[topLeaf(key)] = true
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil // empty means the zero value for any kind
	}
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return l.kindError(key, n, "a group of settings")
		}
		names, index := fieldNames(t)
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := join(key, k.Value)
			fi, ok := index[k.Value]
			if !ok {
				return l.unknownKey(key, k, names)
			}
			if first, dup := seen[k.Value]; dup {
				return core.Errorf(core.ErrConfig, 2, "%s line %d: %s is set twice (first on line %d); keep one", l.file, k.Line, child, first)
			}
			seen[k.Value] = k.Line
			l.lines[child] = k.Line
			if err := l.walk(v, t.Field(fi).Type, child); err != nil {
				return err
			}
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return l.kindError(key, n, "a map of names to values, e.g. {a: b}")
		}
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := key + "." + k.Value
			if first, dup := seen[k.Value]; dup {
				return core.Errorf(core.ErrConfig, 2, "%s line %d: %s is set twice (first on line %d); keep one", l.file, k.Line, child, first)
			}
			seen[k.Value] = k.Line
			l.lines[child] = k.Line
			if err := l.walk(v, t.Elem(), child); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return l.kindError(key, n, "a list, e.g. [a, b]")
		}
		for i, v := range n.Content {
			child := fmt.Sprintf("%s[%d]", key, i)
			l.lines[child] = v.Line
			if err := l.walk(v, t.Elem(), child); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode {
			return l.kindError(key, n, "a text value")
		}
		return l.interpolate(n, key)
	case reflect.Bool:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
			return l.kindError(key, n, "true or false")
		}
	}
	return nil
}

// topLeaf maps a key inside a leaf setting ("columns[2].rule",
// "url_map.cdn.shop.com") to the leaf it belongs to ("columns", "url_map").
func topLeaf(key string) string {
	if i := strings.IndexByte(key, '['); i >= 0 {
		key = key[:i]
	}
	for _, m := range mapLeaves {
		if key == m || strings.HasPrefix(key, m+".") {
			return m
		}
	}
	return key
}

// mapLeaves are the settings whose own keys are free names.
var mapLeaves = []string{"url_map", "keys.test_keys", "laravel.tables", "laravel.env"}

// kindError says a value has the wrong shape.
func (l *loader) kindError(key string, n *yaml.Node, want string) error {
	got := "a text value"
	switch {
	case n.Kind == yaml.MappingNode:
		got = "a map"
	case n.Kind == yaml.SequenceNode:
		got = "a list"
	case n.Tag == "!!bool":
		got = "true or false"
	case n.Tag == "!!int" || n.Tag == "!!float":
		got = "a number"
	}
	return core.Errorf(core.ErrConfig, 2, "%s line %d: %s is %s; it takes %s", l.file, n.Line, key, got, want)
}

// unknownKey names a key the struct doesn't have, with the closest one.
func (l *loader) unknownKey(parent string, k *yaml.Node, names []string) error {
	key := join(parent, k.Value)
	if s := suggest(k.Value, names); s != "" {
		return core.Errorf(core.ErrConfig, 2, "%s line %d: unknown key %s; did you mean %s?", l.file, k.Line, key, join(parent, s))
	}
	return core.Errorf(core.ErrConfig, 2, "%s line %d: unknown key %s; every key is listed at %s", l.file, k.Line, key, WikiURL)
}

// suggest returns the option closest to in, or "".
func suggest(in string, options []string) string { return cli.Suggest(in, options) }

// varRef finds ${NAME} references.
var varRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// roadieName is the only shape of variable the config reads.
var roadieName = regexp.MustCompile(`^ROADIE_[A-Z0-9_]+$`)

// notName strips what can't be in a variable name, for the rename hint.
var notName = regexp.MustCompile(`[^A-Z0-9_]+`)

// interpolate replaces ${ROADIE_NAME} in a string value with the
// variable's value. A set variable is recorded, so Show masks the value;
// an unset one stays as written and is listed in Unset, so config check
// can name it and the run fails when it uses the value. Any other name is
// refused: the config never reads the rest of the environment.
func (l *loader) interpolate(n *yaml.Node, key string) error {
	if !strings.Contains(n.Value, "${") {
		return nil
	}
	var bad error
	var unset []string
	var read []readVar
	out := varRef.ReplaceAllStringFunc(n.Value, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if !roadieName.MatchString(name) {
			if bad == nil {
				fixed := notName.ReplaceAllString(strings.ToUpper(name), "_")
				fixed = "ROADIE_" + strings.Trim(strings.TrimPrefix(fixed, "ROADIE_"), "_")
				bad = core.Errorf(core.ErrConfig, 2, "%s line %d: %s reads %s; only ROADIE_ variables are read. Rename it %s.", l.file, n.Line, key, ref, fixed)
			}
			return ref
		}
		l.interp[key] = name
		if v, ok := l.lookup(name); ok {
			read = append(read, readVar{name, v})
			return v
		}
		unset = append(unset, name)
		return ref
	})
	if bad != nil {
		return bad
	}
	if len(unset) > 0 {
		l.unset[key] = strings.Join(unset, ", ")
		return nil
	}
	if l.read == nil {
		l.read = map[string][]readVar{}
	}
	l.read[key] = read
	n.Value, n.Tag, n.Style = out, "!!str", yaml.DoubleQuotedStyle
	return nil
}

// lookup reads one variable through env; a nil env reads nothing.
func (l *loader) lookup(name string) (string, bool) {
	if l.env == nil {
		return "", false
	}
	return l.env(name)
}

// readFile loads path as the config file, or a *core.Error code config.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the settings file the user named
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, core.Errorf(core.ErrConfig, 2, "%s doesn't exist; run rhythm-roadie config init or pass --config with another file", filepath.ToSlash(path))
		}
		return nil, core.Errorf(core.ErrConfig, 2, "can't read %s: %v", filepath.ToSlash(path), errors.Unwrap(err))
	}
	return b, nil
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
