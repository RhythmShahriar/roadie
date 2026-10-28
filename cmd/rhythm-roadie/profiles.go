// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// profileIDs are the profiles of PRD 6.10, in its order. explain has a
// text for each, as a rule family.
var profileIDs = []string{
	"wordpress", "multisite", "woocommerce", "woocommerce-hpos", "action-scheduler",
	"woocommerce-subscriptions", "automatewoo", "gravity-forms", "wpforms", "contact-form-7-flamingo",
	"ninja-forms", "fluent-forms", "mailpoet", "edd", "memberpress", "learndash", "wp-mail-smtp",
	"post-smtp", "fluent-smtp", "wp-mail-logging", "email-log", "laravel", "laravel-cashier", "sql",
}

// profileJSON is one profile in profiles --json.
type profileJSON struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	CheckedAgainst string      `json:"checked_against"`
	Detect         *detectJSON `json:"detect,omitempty"`
	Rules          []ruleJSON  `json:"rules,omitempty"`
	Truncate       []string    `json:"truncate,omitempty"`
	Drop           []string    `json:"drop,omitempty"`
	Keep           []string    `json:"keep,omitempty"`
}

type detectJSON struct {
	TablesAny  []string `json:"tables_any,omitempty"`
	OptionsAny []string `json:"options_any,omitempty"`
	Files      []string `json:"files,omitempty"`
}

type ruleJSON struct {
	ID     string            `json:"id"`
	Table  string            `json:"table"`
	Column string            `json:"column"`
	Where  map[string]string `json:"where,omitempty"`
	Path   string            `json:"path,omitempty"`
	Action string            `json:"action"`
	Fake   string            `json:"fake,omitempty"`
	// Kept names the pseudonymise.keep part that keeps the value as it
	// is ("country"), so the rule's action doesn't run.
	Kept string `json:"kept,omitempty"`
}

// keptPart is the pseudonymise.keep part that keeps a rule's value, ""
// when the rule runs.
func keptPart(d profile.Def, ruleID string, keep []string) string {
	if part := d.Parts[ruleID]; part != "" && slices.Contains(keep, part) {
		return part
	}
	return ""
}

// keptLabel is how a kept rule reads: "kept (keep: country)".
func keptLabel(part string) string { return "kept (keep: " + part + ")" }

// profileKeep is the keep list of the config the command would use; the
// default ([country, state]) when there is none or it doesn't load.
func profileKeep(p *parsed) []string {
	if cfg, _, err := p.loadConfig(); err == nil && cfg != nil {
		return cfg.Pseudonymise.Keep
	}
	if cfg := dep.defaultConfig(); cfg != nil {
		return cfg.Pseudonymise.Keep
	}
	return nil
}

// runProfiles is profiles [name] [--json] (PRD 6.10).
func runProfiles(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	if len(p.args) > 1 {
		return usageError(stderr, "profiles", "one profile at a time; got %d", len(p.args))
	}
	defs, pack, err := dep.loadProfiles(ctx, p.offline(dep.env))
	if err != nil {
		return runError(err)
	}
	if len(p.args) == 0 {
		if p.f.g.json {
			list := make([]profileJSON, len(defs))
			for i, d := range defs {
				list[i] = profileJSON{ID: d.ID, Name: d.Name, CheckedAgainst: d.CheckedAgainst}
			}
			return writeEnvelope(stdout, "profiles", map[string]any{"profile_pack": pack, "profiles": list})
		}
		fmt.Fprintf(stdout, "Profiles (pack %s):\n\n", safe(pack))
		rows := make([][]string, len(defs))
		for i, d := range defs {
			rows[i] = []string{safe(d.ID), safe(d.Name), safe(d.CheckedAgainst)}
		}
		writeColumns(stdout, rows)
		fmt.Fprintf(stdout, "\n%s profiles <name> lists one profile's rules.\n", prog())
		return nil
	}
	name := p.args[0]
	for _, d := range defs {
		if d.ID == name {
			if p.f.g.json {
				return writeEnvelope(stdout, "profiles", map[string]any{"profile_pack": pack, "profiles": []profileJSON{toJSON(d, profileKeep(p))}})
			}
			printProfile(stdout, d, profileKeep(p))
			return nil
		}
	}
	ids := make([]string, len(defs))
	for i, d := range defs {
		ids[i] = d.ID
	}
	msg := fmt.Sprintf("no profile %q", name)
	if s := cli.Suggest(name, ids); s != "" {
		msg += "\n  did you mean: " + prog() + " profiles " + s
	}
	return usageError(stderr, "profiles", "%s", msg)
}

func toJSON(d profile.Def, keep []string) profileJSON {
	j := profileJSON{ID: d.ID, Name: d.Name, CheckedAgainst: d.CheckedAgainst, Truncate: d.Truncate, Drop: d.Drop, Keep: d.Keep,
		Detect: &detectJSON{TablesAny: d.Detect.TablesAny, OptionsAny: d.Detect.OptionsAny, Files: d.Detect.Files}}
	for _, r := range d.Rules {
		j.Rules = append(j.Rules, ruleJSON{ID: r.ID, Table: r.Table, Column: r.Column, Where: r.Where, Path: r.Path, Action: string(r.Action), Fake: string(r.Fake),
			Kept: keptPart(d, r.ID, keep)})
	}
	return j
}

// printProfile lists one profile: what detects it, then its rules as a
// table, then what it truncates, drops and keeps. A rule whose address
// part pseudonymise.keep names reads "kept (keep: country)".
func printProfile(w io.Writer, d profile.Def, keep []string) {
	fmt.Fprintf(w, "%s (%s)\nchecked against: %s\n", safe(d.Name), safe(d.ID), safe(d.CheckedAgainst))
	var det []string
	if len(d.Detect.TablesAny) > 0 {
		det = append(det, "a table of "+strings.Join(d.Detect.TablesAny, ", "))
	}
	if len(d.Detect.OptionsAny) > 0 {
		det = append(det, "an option of "+strings.Join(d.Detect.OptionsAny, ", "))
	}
	if len(d.Detect.Files) > 0 {
		det = append(det, "a file of "+strings.Join(d.Detect.Files, ", "))
	}
	if len(det) > 0 {
		fmt.Fprintf(w, "detected by: %s\n", safe(strings.Join(det, "; or ")))
	}
	if len(d.Rules) > 0 {
		rows := [][]string{{"TABLE", "COLUMN", "RULE"}}
		for _, r := range d.Rules {
			col := r.Column
			if r.Path != "" {
				col += " → " + r.Path
			}
			for _, k := range sortedWhere(r.Where) {
				col += " (" + k + ")"
			}
			rule := string(r.Action)
			if r.Fake != "" {
				rule += " " + string(r.Fake)
			}
			if part := keptPart(d, r.ID, keep); part != "" {
				rule = keptLabel(part)
			}
			rows = append(rows, []string{safe(r.Table), safe(col), safe(rule)})
		}
		fmt.Fprintln(w)
		writeColumns(w, rows)
	}
	for _, l := range []struct {
		label string
		list  []string
	}{{"truncated", d.Truncate}, {"dropped", d.Drop}, {"kept", d.Keep}} {
		if len(l.list) > 0 {
			fmt.Fprintf(w, "\n%s: %s\n", l.label, safe(strings.Join(l.list, ", ")))
		}
	}
	fmt.Fprintf(w, "\n%s explain %s says why.\n", prog(), safe(d.ID))
}

// writeColumns prints rows indented by two spaces, each column as wide
// as its longest value and at least two spaces before the next.
func writeColumns(w io.Writer, rows [][]string) {
	var width []int
	for _, r := range rows {
		for i, c := range r {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], utf8.RuneCountInString(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", width[i]-utf8.RuneCountInString(c)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func sortedWhere(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+" = "+v)
	}
	slices.Sort(out)
	return out
}

// writeEnvelope writes a rhythm-roadie/v1 document for a command that has
// no run result (profiles, explain).
func writeEnvelope(w io.Writer, kind string, data any) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	e := core.Envelope{Schema: core.EnvelopeSchema, Kind: kind, Tool: toolName, Version: displayVersion(), Complete: true, Verdict: "ok", Data: d}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}
