// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site"
)

// ErrNoStack is the PRD 17 stack error: nothing in the schema says which
// application it belongs to and the config lists no columns of its own.
var ErrNoStack = &core.Error{Code: core.ErrStack, Exit: 2,
	Msg: "stack: no profile matches this dump (no wp_options or migrations table). For a Laravel app with no migrations table, set stack: laravel (or pass --stack laravel); for your own schema, list the tables under columns: and set stack: sql."}

// Stacks lists the values --stack and stack: take.
func Stacks() []string { return []string{"auto", "wordpress", "laravel", "sql"} }

// WordPressPrefix finds the table prefix of a WordPress schema: the
// schema's own Prefix when the reader set one (from wp-config.php
// $table_prefix), else the prefix of an options table that has
// option_name and option_value. With several, the shortest wins, so a
// multisite's wp_2_options doesn't hide wp_options.
func WordPressPrefix(s *core.Schema) (string, bool) {
	if s == nil {
		return "", false
	}
	if s.Prefix != "" {
		return s.Prefix, true
	}
	best, found := "", false
	for _, t := range s.Tables {
		p, ok := strings.CutSuffix(t.Name, "options")
		if !ok || t.Col("option_name") < 0 || t.Col("option_value") < 0 {
			continue
		}
		if !found || len(p) < len(best) || (len(p) == len(best) && p < best) {
			best, found = p, true
		}
	}
	return best, found
}

// StackOf tells the stack from a schema alone: an options table gives
// WordPress, a migrations table gives Laravel, else "".
func StackOf(s *core.Schema) core.Stack {
	if _, ok := WordPressPrefix(s); ok {
		return core.StackWordPress
	}
	if t := s.Table("migrations"); t != nil && t.Col("migration") >= 0 {
		return core.StackLaravel
	}
	return ""
}

// ResolveStack turns stack: or --stack into the stack of this run. An
// explicit value wins. auto asks the site (site.DetectStack) when there is
// one and falls back to the schema, which also decides when the site
// says only sql. When nothing matches, a config with
// columns: runs as sql; without them it is ErrNoStack (exit 2). An
// unknown value is a config error; the CLI refuses it first as a usage
// error.
func ResolveStack(ctx context.Context, want string, s *core.Schema, st core.Site, hasColumns bool) (core.Stack, error) {
	switch want {
	case "wordpress", "laravel", "sql":
		return core.Stack(want), nil
	case "", "auto":
	default:
		return "", core.Errorf(core.ErrConfig, 2, "stack %q isn't one of auto, wordpress, laravel or sql", want)
	}
	var fromSite core.Stack
	if st != nil {
		got, err := site.DetectStack(ctx, st)
		switch {
		case err == nil && got != "" && got != core.StackSQL:
			return got, nil
		case err == nil:
			// sql only says the site has no WordPress or Laravel files,
			// as for a DSN; the schema can still say which app it holds.
			fromSite = got
		case !errors.Is(err, core.ErrNotBuilt):
			return "", err
		}
	}
	if got := StackOf(s); got != "" {
		return got, nil
	}
	if hasColumns || fromSite != "" {
		return core.StackSQL, nil
	}
	return "", ErrNoStack
}

// Sites returns the multisite blog prefixes the schema has: "2_", "11_"
// for wp_2_options and wp_11_options under prefix wp_, sorted by number.
// Every blog has its own options or posts table, which keeps a plugin's
// wp_404_log from passing for blog 404. The main site ("") is not in the
// list.
func Sites(s *core.Schema, prefix string) []string {
	if s == nil {
		return nil
	}
	seen := map[int]bool{}
	for _, t := range s.Tables {
		rest, ok := strings.CutPrefix(t.Name, prefix)
		if !ok {
			continue
		}
		num, _, ok := strings.Cut(rest, "_")
		if !ok || num == "" || strings.TrimLeft(num, "0123456789") != "" || num[0] == '0' {
			continue
		}
		blog := prefix + num + "_"
		if s.Table(blog+"options") == nil && s.Table(blog+"posts") == nil {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil && n > 0 {
			seen[n] = true
		}
	}
	nums := make([]int, 0, len(seen))
	for n := range seen {
		nums = append(nums, n)
	}
	slices.Sort(nums)
	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = strconv.Itoa(n) + "_"
	}
	return out
}

// Expand turns a profile table name into the names it stands for:
// {prefix} becomes prefix and {site} becomes "" plus every site prefix.
func Expand(table, prefix string, sites []string) []string {
	t := strings.ReplaceAll(table, PlaceholderPrefix, prefix)
	if !strings.Contains(t, PlaceholderSite) {
		return []string{t}
	}
	out := []string{strings.ReplaceAll(t, PlaceholderSite, "")}
	for _, s := range sites {
		out = append(out, strings.ReplaceAll(t, PlaceholderSite, s))
	}
	return out
}
