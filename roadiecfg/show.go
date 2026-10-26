// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"gopkg.in/yaml.v3"
)

// show renders the config for config show. It works on the YAML node
// tree, so masking and the source comments follow the same dotted keys
// Load records.
func (c *Config) show(asJSON bool) ([]byte, error) {
	var root yaml.Node
	if err := root.Encode(c); err != nil {
		return nil, err
	}
	c.annotate(&root, "")
	if asJSON {
		var v any
		if err := root.Decode(&v); err != nil {
			return nil, err
		}
		out := struct {
			Config         any               `json:"config"`
			Sources        map[string]string `json:"sources"`
			ProductionFrom map[string]string `json:"production_from"`
			Unset          map[string]string `json:"unset"`
		}{v, nonNil(c.Sources), nonNil(c.ProductionFrom), nonNil(c.Unset)}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	}
	var buf bytes.Buffer
	if c.file != "" {
		fmt.Fprintf(&buf, "# Effective settings from %s and the defaults; each value names its source.\n", c.file)
	} else {
		buf.WriteString("# Effective settings (no settings file); each value names its source.\n")
	}
	top := &root
	if top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
		top = top.Content[0]
	}
	if err := c.render(&buf, top, "", 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// render writes a mapping as YAML, one setting a line with its source
// as a comment. A group of settings nests; a list or a map is written in
// flow style on its line, except production, whose entries each name
// the file they came from.
func (c *Config) render(buf *bytes.Buffer, n *yaml.Node, key string, depth int) error {
	indent := strings.Repeat("  ", depth)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		child := join(key, k.Value)
		src, leaf := c.Sources[child]
		if !leaf && v.Kind == yaml.MappingNode {
			fmt.Fprintf(buf, "%s%s:\n", indent, k.Value)
			if err := c.render(buf, v, child, depth+1); err != nil {
				return err
			}
			continue
		}
		if child == "production" && len(v.Content) > 0 {
			fmt.Fprintf(buf, "%s%s:  # %s\n", indent, k.Value, src)
			for _, e := range v.Content {
				val, err := flow(e)
				if err != nil {
					return err
				}
				fmt.Fprintf(buf, "%s  - %s  # %s\n", indent, val, c.ProductionFrom[e.Value])
			}
			continue
		}
		val, err := flow(v)
		if err != nil {
			return err
		}
		if src == "" {
			src = SourceDefault
		}
		fmt.Fprintf(buf, "%s%s: %s  # %s\n", indent, k.Value, val, src)
	}
	return nil
}

// flow writes one value on one line: a scalar quoted only when YAML
// needs it, a list or map in flow style.
func flow(n *yaml.Node) (string, error) {
	setFlow(n)
	b, err := yaml.Marshal(n)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

// setFlow marks every collection under n for flow style.
func setFlow(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style = yaml.FlowStyle
	}
	for _, ch := range n.Content {
		setFlow(ch)
	}
}

// nonNil returns m, or an empty map for nil, so JSON prints {}.
func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// annotate masks secrets in place, by dotted key.
func (c *Config) annotate(n *yaml.Node, key string) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, ch := range n.Content {
			c.annotate(ch, key)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := join(key, k.Value)
			if v.Kind == yaml.ScalarNode {
				v.Value = c.maskedValue(child, v.Value)
			}
			c.annotate(v, child)
		}
	case yaml.SequenceNode:
		for i, v := range n.Content {
			child := fmt.Sprintf("%s[%d]", key, i)
			if v.Kind == yaml.ScalarNode {
				v.Value = c.maskedValue(child, v.Value)
			}
			c.annotate(v, child)
		}
	}
}

// maskedValue returns what config show prints for the value at key: a
// test key or an interpolated value masked, an unset reference as
// written, a URL or DSN without its password.
func (c *Config) maskedValue(key, v string) string {
	if c.Unset[key] != "" {
		return v
	}
	if strings.HasPrefix(key, "keys.test_keys.") || c.interp[key] != "" {
		if v == "" {
			return v
		}
		return core.MaskKey(v)
	}
	switch key {
	case "source", "target", "side_effects.echo.url", "side_effects.echo.proxy":
		return stripPassword(v)
	}
	return v
}

// stripPassword replaces the password of a URL or DSN with •••.
func stripPassword(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return v
	}
	if _, ok := u.User.Password(); !ok {
		return v
	}
	u.User = url.UserPassword(u.User.Username(), "xxx")
	return strings.Replace(u.String(), ":xxx@", ":•••@", 1)
}
