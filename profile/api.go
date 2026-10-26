// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package profile holds the stack profiles (WordPress, WooCommerce, the
// form, mail-log and shop plugins, Laravel and generic SQL), detects which
// apply to a schema and compiles them with the config into rules per table.
//
// The exported names in this file are the frozen cross-lane API; the
// engine lane replaces the bodies, which return core.ErrNotBuilt until then.
package profile

import (
	"context"
	"io"

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
}

// DetectDef says when a profile applies.
type DetectDef struct {
	TablesAny  []string
	OptionsAny []string
	Files      []string
}

// Builtin returns the profiles built into the binary.
func Builtin() ([]Def, error) {
	return nil, core.ErrNotBuilt
}

// Load returns the profiles and the pack version, from the signed pack
// cache when present, else the built-in ones.
func Load(ctx context.Context, offline bool) ([]Def, string, error) {
	return nil, "", core.ErrNotBuilt
}

// Detect returns the IDs of the profiles that apply.
func Detect(s *core.Schema, site core.Site, defs []Def) []string {
	return nil
}

// Compiled is the rule set of one run.
type Compiled struct{} // its fields are unexported and added by the owning lane

// Compile expands the chosen profiles and the config over the schema.
func Compile(ids []string, defs []Def, s *core.Schema, cfg *roadiecfg.Config) (*Compiled, error) {
	return nil, core.ErrNotBuilt
}

// Rules returns the rules for one table.
func (c *Compiled) Rules(table string) []core.Rule {
	return nil
}

// TableAction returns Keep, Truncate, DropTable or Fake (has column rules).
func (c *Compiled) TableAction(table string) core.Action {
	return ""
}

// SHA256 returns the hash of the compiled rule set, for the report.
func (c *Compiled) SHA256() string {
	return ""
}

// Update refreshes the signed profile pack (rhythm-roadie update).
func Update(ctx context.Context, w io.Writer) ([]cli.DataPack, error) {
	return nil, core.ErrNotBuilt
}

// UpdatePinned brings the pack to the pinned versions.
func UpdatePinned(ctx context.Context, w io.Writer, pins map[string]string) ([]cli.DataPack, error) {
	return nil, core.ErrNotBuilt
}
