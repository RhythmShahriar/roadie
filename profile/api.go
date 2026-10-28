// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package profile holds the stack profiles (WordPress, WooCommerce, the
// form, mail-log and shop plugins, Laravel and generic SQL), detects which
// apply to a schema and compiles them with the config into rules per table.
//
// The exported names in this file are the frozen cross-lane API.
package profile

import (
	"context"
	"io"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// Def is one profile as its YAML file defines it.
type Def struct {
	ID, Name, CheckedAgainst string
	Detect                   DetectDef
	Rules                    []core.Rule
	Truncate, Drop           []string
	Keep                     []string
	// Stack is the stack the profile belongs to: auto picks only the
	// profiles of the stack it found.
	Stack core.Stack
	// RowFixes names the Go fixes the engine runs on this profile's rows
	// (see RowFixes).
	RowFixes []string
	// Parts maps a rule ID to the address part it holds (country, state,
	// city, postcode); pseudonymise.keep keeps the listed parts.
	Parts map[string]string
}

// DetectDef says when a profile applies.
type DetectDef struct {
	TablesAny []string
	// ColumnsAny are "table.column" pairs: a profile that adds columns to
	// a core table (Cashier's users.stripe_id) is there when one is,
	// even without a table of its own.
	ColumnsAny []string
	OptionsAny []string
	Files      []string
}

// Builtin returns the profiles built into the binary, sorted by ID.
func Builtin() ([]Def, error) {
	return builtin()
}

// Load returns the profiles and the pack version, from the signed pack
// cache when present, else the built-in ones.
//
// The cached pack is used only when its version is newer than the
// built-in profiles' (Version); its profiles replace the built-in ones
// with the same ID. offline, or RHYTHM_OFFLINE, uses the built-in ones.
func Load(ctx context.Context, offline bool) ([]Def, string, error) {
	return loadWith(deps, offline)
}

// Detect returns the IDs of the profiles that apply, sorted: a table of
// the profile's tables_any (with the WordPress prefix and every multisite
// blog) or a column of its columns_any is in the schema, or a file of its
// files: is on the site. site may be nil for a dump.
func Detect(s *core.Schema, site core.Site, defs []Def) []string {
	return DetectWithOptions(context.Background(), s, site, defs, nil)
}

// Compiled is the rule set of one run.
type Compiled struct {
	c *compiled
}

// Compile expands the chosen profiles and the config over the schema.
// ids are profile IDs or "auto" (none means cfg.profiles); "auto" adds
// every detected profile of the stack. The stack is cfg.stack, or for
// auto what the schema shows (ErrNoStack when nothing does and there are
// no columns:). {prefix} is the schema's prefix (from wp-config.php when
// the reader set it, else the options table) and {site} every multisite
// blog, matched by exact name so wp_1_ never takes wp_11_ tables. A
// columns: rule for the same table and column replaces the profile's.
func Compile(ids []string, defs []Def, s *core.Schema, cfg *roadiecfg.Config) (*Compiled, error) {
	return compile(ids, defs, s, cfg)
}

// Rules returns the rules for one table, most specific first (rules with
// a where clause before the ones without). A table that is kept,
// truncated or dropped has none.
func (c *Compiled) Rules(table string) []core.Rule {
	if c.c.action(table) != core.Fake {
		return nil
	}
	return c.c.rulesFor(table)
}

// Kept reports whether a profile's keep list names the table: kept on
// purpose, as opposed to a table no rule mentions (TableAction gives Keep
// for both). The uncovered-column scan skips the first and reads the
// second.
func (c *Compiled) Kept(table string) bool {
	return c.c.action(table) == core.Keep && matchAny(table, c.c.keep)
}

// TableAction returns Keep, Truncate, DropTable or Fake (has column rules).
func (c *Compiled) TableAction(table string) core.Action {
	return c.c.action(table)
}

// SHA256 returns the hash of the compiled rule set, for the report.
func (c *Compiled) SHA256() string {
	return c.c.sha256()
}

// Stack returns the stack the rules were compiled for.
func (c *Compiled) Stack() core.Stack { return c.c.stack }

// Profiles returns the IDs of the profiles in the set, sorted.
func (c *Compiled) Profiles() []string { return slices.Clone(c.c.profiles) }

// Prefix returns the WordPress table prefix used, "" for other stacks.
func (c *Compiled) Prefix() string { return c.c.prefix }

// Sites returns the multisite blog prefixes found ("2_", "11_").
func (c *Compiled) Sites() []string { return slices.Clone(c.c.sites) }

// RowFixes returns the row fixes the profiles ask for, sorted.
func (c *Compiled) RowFixes() []string { return slices.Clone(c.c.rowFixes) }

// KeepUsers returns the keep_users globs.
func (c *Compiled) KeepUsers() []string { return slices.Clone(c.c.keepUsers) }

// WithRules returns the set with rules added after the compile: the
// per-form rules the gf-field-types, wpforms-field-types and
// ninja-field-types row fixes build from pass 1 (see FieldTypes). A
// columns: rule on the same column still wins; SHA256 covers them.
func (c *Compiled) WithRules(rs []core.Rule) *Compiled { return &Compiled{c: c.c.withRules(rs)} }

// All returns every rule of the set, for verify's column kinds and the
// plan: exact tables in name order, then the glob rules.
func (c *Compiled) All() []core.Rule { return c.c.all() }

// Update refreshes the signed profile pack (rhythm-roadie update).
//
// It downloads checksums.txt, its Sigstore bundle and
// rhythm-roadie_profiles.tar.gz from the latest release, verifies the
// bundle as kit update verifies the binary, checks the archive against
// checksums.txt and keeps its profiles in the cache. RHYTHM_OFFLINE
// skips the network and reports the profiles in use.
func Update(ctx context.Context, w io.Writer) ([]cli.DataPack, error) {
	return deps.install(ctx, w, "")
}

// UpdatePinned brings the pack to the pinned versions.
// The pin is pins["profiles"]; without one it is Update.
func UpdatePinned(ctx context.Context, w io.Writer, pins map[string]string) ([]cli.DataPack, error) {
	return deps.install(ctx, w, strings.TrimPrefix(pins[PackName], "v"))
}
