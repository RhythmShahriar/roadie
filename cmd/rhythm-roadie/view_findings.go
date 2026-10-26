// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/rhythm/kit/ui"

	"github.com/RhythmShahriar/roadie/core"
)

// The findings, keys, checks and side-effect blocks that plan, copy,
// prepare and verify share.

// checkLabels name verify's checks as the screen says them (product.md
// 5.3). A check without a label shows its ID.
var checkLabels = map[string]string{
	core.FindingExactSurvivors:       "No pseudonymised value survives",
	core.FindingPIIEmail:             "No real-looking emails",
	core.FindingPIIPhone:             "No real-looking phones",
	core.FindingPIIName:              "No real-looking names",
	core.FindingPIIAddress:           "No real-looking addresses",
	core.FindingPIIIP:                "No public IP addresses",
	core.FindingPIICard:              "No card numbers",
	core.FindingPIIIBAN:              "No IBANs",
	"keys":                           "No live keys",
	core.FindingURLProduction:        "No production URLs",
	core.FindingURLProductionSiteURL: "Site URL is the copy's",
	"side-effects":                   "Side effects off",
	core.FindingConfigEnvironment:    "WP_ENVIRONMENT_TYPE is staging",
	core.FindingSearchIndexable:      "Kept out of search engines",
	core.FindingUploadsPersonal:      "No personal files in uploads",
	core.FindingUncoveredColumn:      "Every personal-looking column covered",
}

// effectLabels name the side effects in the SIDE EFFECTS block and their
// short forms in copy's "✓ Side effects" line.
var effectLabels = map[string][2]string{
	core.FindingSideEffectMail:            {"Echo connector, sealed", "Echo seal"},
	core.FindingConfigEnvironment:         {"WP_ENVIRONMENT_TYPE", "WP_ENVIRONMENT_TYPE"},
	core.FindingSideEffectCron:            {"WP-Cron", "cron"},
	core.FindingSideEffectActionScheduler: {"Action Scheduler", "Action Scheduler"},
	core.FindingSideEffectWebhooks:        {"WooCommerce webhooks", "webhooks"},
	core.FindingSideEffectGateways:        {"Gateways", "gateways"},
	core.FindingSideEffectSubscriptions:   {"Subscriptions", "Subscriptions"},
	core.FindingSideEffectJetpack:         {"Jetpack", "Jetpack"},
	core.FindingSideEffectAutomateWoo:     {"AutomateWoo", "AutomateWoo"},
	core.FindingSideEffectQueue:           {"Queue", "queue"},
	core.FindingSideEffectConnectorChange: {"Echo connector", "Echo connector"},
	core.FindingSideEffectNotApplicable:   {"Database only", "database only"},
	core.FindingConfigSalts:               {"Salts and APP_KEY", "salts"},
	core.FindingSearchIndexable:           {"noindex", "noindex"},
}

func effectLabel(id string, short bool) string {
	l, ok := effectLabels[id]
	if !ok {
		return id
	}
	if short {
		return l[1]
	}
	return l[0]
}

// sevColor is the theme color of a severity.
func sevColor(s core.Severity) lipgloss.TerminalColor { return ui.SeverityColor(s.String()) }

// keyCards prints each key as a ▌ block: provider and where, then the
// masked value and what happens to it.
func (v *view) keyCards(keys []core.KeyHit) {
	for _, k := range keys {
		title := k.Provider + " key"
		switch k.Env {
		case "live":
			title = k.Provider + " live key"
		case "test":
			title = k.Provider + " test key"
		}
		at := k.At
		leaf := at.Path
		if i := strings.LastIndex(leaf, ":"); i >= 0 {
			leaf = leaf[i+1:]
		}
		if leaf == "" {
			leaf = at.Column
		}
		where := at
		where.Path = ""
		detail := k.Masked
		if leaf != "" {
			detail = leaf + " = " + k.Masked
		}
		v.card(k.Severity, title, where.String(), []string{detail}, keyAction(k.Action))
	}
}

// keyAction says what Roadie does with a key.
func keyAction(action string) string {
	switch action {
	case "swapped":
		return "swap: the test key from keys.test_keys"
	case "blanked":
		return "blank"
	case "kept":
		return "kept as it is (a test key)"
	case "manual":
		return "edit by hand: Roadie only edits the database, wp-config.php and .env"
	}
	return action
}

// effectLines prints the SIDE EFFECTS block: one line per switch with
// what Roadie does, then PRD 13.5's limits as one "Not stopped" line.
func (v *view) effectLines(effects []core.SideEffect) {
	for _, e := range effects {
		glyph, color := "✓", lipgloss.TerminalColor(lipgloss.Color(ui.Ok))
		switch e.Status {
		case "on":
			glyph, color = "✗", lipgloss.Color(ui.Danger)
		case "unknown":
			glyph, color = "!", lipgloss.Color(ui.Warn)
		case "not-present":
			glyph, color = "·", lipgloss.Color(ui.Muted)
		}
		how := e.How
		if v.o.verbose && e.Undo != "" {
			how += " (undo: " + e.Undo + ")"
		}
		v.markCol(glyph, effectLabel(e.ID, false), how, color, 24)
	}
	if len(effects) > 0 {
		v.blank()
		v.setting("Not stopped", notStopped)
	}
}

// findingGroups prints findings at low and above grouped by severity as
// verify's screen does (product.md 5.4): critical and high as ▌ blocks,
// medium and low as one line each. Info findings are left for infoLines.
func (v *view) findingGroups(fs []core.Finding) {
	for sev := core.SeverityCritical; sev >= core.SeverityLow; sev-- {
		var group []core.Finding
		for _, f := range fs {
			if f.Severity == sev {
				group = append(group, f)
			}
		}
		if len(group) == 0 {
			continue
		}
		v.section(strings.ToUpper(sev.String()), len(group), "", sevColor(sev))
		for _, f := range group {
			if sev >= core.SeverityHigh || v.o.plain {
				v.card(f.Severity, findingTitle(f), f.ID, findingDetails(f, v.o.verbose), f.Fix)
				continue
			}
			v.item(findingTitle(f) + "   " + locationsLine(f) + "  (" + f.ID + ")")
		}
	}
}

// infoLines prints the info findings under the result box.
func (v *view) infoLines(fs []core.Finding) {
	for _, f := range fs {
		if f.Severity == core.SeverityInfo {
			v.under("· info: " + findingTitle(f))
		}
	}
}

// findingTitle is the title with the count in front when it is more than one.
func findingTitle(f core.Finding) string {
	if f.Count > 1 && !strings.HasPrefix(f.Title, num(f.Count)) {
		return num(f.Count) + " " + f.Title
	}
	return f.Title
}

// locationsLine lists where a finding is: "wp_users.user_email 17,880 · wp_posts 390".
func locationsLine(f core.Finding) string {
	var parts []string
	for _, l := range f.Locations {
		loc := l.Location
		loc.Row = "" // -v shows row keys; this line names columns and files
		s := loc.String()
		if l.Count > 0 {
			s += " " + num(l.Count)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// findingDetails are the lines under a finding block: where, an example
// and, with -v, the row keys.
func findingDetails(f core.Finding, verbose bool) []string {
	var out []string
	if l := locationsLine(f); l != "" {
		out = append(out, l)
	}
	if len(f.Examples) > 0 {
		out = append(out, "e.g. "+strings.Join(f.Examples, ", "))
	}
	if f.Detail != "" {
		out = append(out, f.Detail)
	}
	if verbose {
		var rows []string
		for _, l := range f.Locations {
			if l.Row != "" {
				rows = append(rows, l.String())
			}
		}
		if len(rows) > 0 {
			out = append(out, "rows: "+strings.Join(rows, "; "))
		}
	}
	return out
}

// checkLines prints verify's checks with ✓ or ✗.
func (v *view) checkLines(checks []core.Check) {
	for _, c := range checks {
		label := checkLabels[c.ID]
		if label == "" {
			label = c.ID
		}
		detail := c.Detail
		if detail == "" && c.Checked > 0 {
			detail = num(c.Checked) + " checked"
		}
		glyph, color := "✓", lipgloss.TerminalColor(lipgloss.Color(ui.Ok))
		switch c.Status {
		case "fail":
			glyph, color = "✗", lipgloss.Color(ui.Danger)
		case "skip":
			glyph, color = "·", lipgloss.Color(ui.Muted)
			if detail == "" {
				detail = "skipped"
			}
		}
		if v.o.plain {
			v.mark(glyph, label, detail, color)
			continue
		}
		g := lipgloss.NewStyle().Foreground(color).Render(glyph)
		const col = 38
		lines := wrap(safe(detail), width-col-7)
		fmt.Fprintf(v.w, "    %s %s %s\n", g, ui.Pad(safe(ui.Truncate(label, col)), col+1), muted.Render(lines[0]))
		for _, l := range lines[1:] {
			fmt.Fprintf(v.w, "%s%s\n", strings.Repeat(" ", col+7), muted.Render(l))
		}
	}
}
