// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"gopkg.in/yaml.v3"
)

// siblings are the other Rhythm tools whose production: list Roadie
// reads (PRD 8.2), in the order they are merged.
var siblings = []struct{ source, file string }{
	{SourceEcho, ".echo/config.yaml"},
	{SourceSoundcheck, ".soundcheck/config.yaml"},
}

// MergeProduction adds the production: entries of .echo/config.yaml and
// .soundcheck/config.yaml in projectDir to c.Production and records where
// each came from. Those files belong to their own tools, so only
// production: is read and every other key is ignored; a file that isn't
// there is skipped. A file that can't be parsed or holds a bad entry
// stops the run: a production site Roadie silently missed could become a
// target.
func (c *Config) MergeProduction(projectDir string) error {
	if c.ProductionFrom == nil {
		c.ProductionFrom = map[string]string{}
	}
	// The entries already there came from the file, or from a flag the CLI
	// marked; the default list is empty.
	own := c.Sources["production"]
	if own == "" || own == SourceDefault {
		own = SourceFile
	}
	for _, p := range c.Production {
		if c.ProductionFrom[p] == "" {
			c.ProductionFrom[p] = own
		}
	}
	from := []string{}
	if len(c.Production) > 0 {
		from = append(from, own)
	}
	for _, s := range siblings {
		entries, err := readProduction(filepath.Join(projectDir, filepath.FromSlash(s.file)), s.file)
		if err != nil {
			return err
		}
		added := false
		for _, p := range entries {
			if slices.Contains(c.Production, p) {
				continue
			}
			c.Production = append(c.Production, p)
			c.ProductionFrom[p] = s.source
			added = true
		}
		if added {
			from = append(from, s.source)
		}
	}
	if c.Sources == nil {
		c.Sources = map[string]string{}
	}
	if len(from) > 0 {
		c.Sources["production"] = strings.Join(from, ", ")
	}
	return nil
}

// readProduction reads only production: from another tool's settings.
func readProduction(path, display string) ([]string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- a sibling tool's settings file in the project
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Errorf(core.ErrConfig, 2, "can't read %s for its production: list: %v", display, errors.Unwrap(err))
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, core.Errorf(core.ErrConfig, 2, "%s isn't valid YAML, so Roadie can't read its production: list: %s", display, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value != "production" {
			continue
		}
		var list []string
		if err := v.Decode(&list); err != nil {
			return nil, core.Errorf(core.ErrConfig, 2, "%s line %d: production should be a list of sites, e.g. [\"shop.com\"]", display, v.Line)
		}
		for _, p := range list {
			if err := CheckProduction(p); err != nil {
				return nil, core.Errorf(core.ErrConfig, 2, "%s line %d: production has %q, which isn't a site like shop.com, *.shop.com or https://shop.com/shop/", display, v.Line, p)
			}
		}
		return list, nil
	}
	return nil, nil
}

// MarkFlag records that a flag set key, for config show.
func (c *Config) MarkFlag(key string) {
	if c.Sources == nil {
		c.Sources = map[string]string{}
	}
	c.Sources[key] = SourceFlag
}

// Need fails when any of keys reads a ${ROADIE_*} variable that isn't
// set: the run is about to use the value, so it can't go on without it.
// The error is code config, exit 2.
func (c *Config) Need(keys ...string) error {
	for _, k := range keys {
		for _, u := range sortedKeys(c.Unset) {
			if u != k && !strings.HasPrefix(u, k+".") && !strings.HasPrefix(u, k+"[") {
				continue
			}
			line := ""
			if n := c.lines[u]; n > 0 {
				line = fmt.Sprintf(" line %d", n)
			}
			file := c.file
			if file == "" {
				file = Dir + "/" + File
			}
			return core.Errorf(core.ErrConfig, 2, "%s%s: %s reads %s, which isn't set. Set it in the environment, or remove the entry.", file, line, u, refs(c.Unset[u]))
		}
	}
	return nil
}

// refs writes variable names as ${NAME} references.
func refs(names string) string {
	parts := strings.Split(names, ", ")
	for i, p := range parts {
		parts[i] = "${" + p + "}"
	}
	return strings.Join(parts, ", ")
}
