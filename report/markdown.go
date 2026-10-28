// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/roadie/core"
)

// md is a data string as Markdown text, md code as inline code: every
// value from the envelope goes through one of them (security.md M3).
func md(s string) string     { return term.EscapeMarkdown(s) }
func mdCode(s string) string { return term.MarkdownCode(s) }

// mdCell is inline code for a table cell: GFM splits cells at a pipe
// even inside a code span, unless it is escaped.
func mdCell(s string) string { return strings.ReplaceAll(term.MarkdownCode(s), "|", `\|`) }

// Markdown renders e for a pull request comment or a job summary (PRD
// 16.3): the title, the copy table, the checks with ✅ and ❌, the
// findings, a details block with what changed, Not covered and the
// footer. Every data string is escaped, so a table name can't end a
// table row, open a link or inject HTML.
func Markdown(e *core.Envelope) ([]byte, error) {
	r, err := readReport(e)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	r.markdown(&b)
	return b.Bytes(), nil
}

// MarkdownAll renders several reports worst first, one after another,
// as report does for more than one report.json.
func MarkdownAll(es []*core.Envelope) ([]byte, error) {
	rs, err := readReports(es)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for i, r := range rs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		r.markdown(&b)
	}
	return b.Bytes(), nil
}

func readReports(es []*core.Envelope) ([]*report, error) {
	if len(es) == 0 {
		return nil, fmt.Errorf("report: no reports")
	}
	rs := make([]*report, 0, len(es))
	for _, e := range es {
		r, err := readReport(e)
		if err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	worstFirst(rs)
	return rs, nil
}

func (r *report) markdown(b *bytes.Buffer) {
	d := r.data
	word, line := r.verdict()
	fmt.Fprintf(b, "## Roadie: %s, %s\n\n", word, md(line))
	if d.Error != nil {
		fmt.Fprintf(b, "> %s\n\n", md(d.Error.Msg))
	}

	b.WriteString("| | |\n|---|---|\n")
	row := func(k, v string, esc func(string) string) {
		if v != "" {
			fmt.Fprintf(b, "| %s | %s |\n", k, esc(v))
		}
	}
	row("Source", d.Source.Display, mdCell)
	row("Target", d.Target.Display, mdCell)
	row("Copy", d.URL, md)
	row("Production", strings.Join(d.Production, ", "), md)
	row("Stack", r.stackLine(), md)
	row("Verify", r.coverageLine(), md)
	row("Fail on", d.FailOn, md)
	row("Findings", counts(r.env.Summary), md)
	fmt.Fprintf(b, "| Roadie | %s, profile pack %s |\n", mdCell(r.env.Version), mdCell(orDash(d.ProfilePack)))
	b.WriteString("\n")

	if len(d.Checks) > 0 {
		b.WriteString("| Check | Result |\n|---|---|\n")
		for _, c := range d.Checks {
			mark := "✅"
			switch c.Status {
			case "fail":
				mark = "❌"
			case "warn":
				mark = "⚠️"
			case "skip":
				mark = "➖"
			}
			result := checkResult(c)
			if c.Status == "fail" || c.Status == "warn" {
				// One total per check: the findings list below has each
				// one's title.
				if t := checkTally(r.checkFindings(c.ID)); t != "" {
					result = t
				}
			}
			fmt.Fprintf(b, "| %s | %s %s |\n", md(CheckTitle(c.ID, c.Status)), mark, md(result))
		}
		b.WriteString("\n")
	}

	var listed bool
	for _, s := range severities {
		for _, f := range r.bySeverity(s) {
			if !listed {
				b.WriteString("### Findings\n\n")
				listed = true
			}
			fmt.Fprintf(b, "- **%s** %s %s", strings.ToUpper(s.String()), mdCode(f.ID), md(findingTitle(f)))
			if l := locationLine(f); l != "" {
				fmt.Fprintf(b, ": %s", md(l))
			}
			if len(f.Examples) > 0 {
				ex := make([]string, 0, len(f.Examples))
				for _, e := range f.Examples {
					ex = append(ex, mdCode(e))
				}
				fmt.Fprintf(b, " (e.g. %s)", strings.Join(ex, ", "))
			}
			if f.Fix != "" {
				fmt.Fprintf(b, ". Fix: %s", mdCode(f.Fix))
			}
			b.WriteString("\n")
		}
	}
	for i, u := range d.Uncovered {
		if i == 0 {
			if !listed {
				b.WriteString("### Findings\n\n")
				listed = true
			}
		}
		fmt.Fprintf(b, "- %s looks personal (%d%% of sampled values) and no rule covers it\n", mdCode(u.Table+"."+u.Column), u.Rate)
	}
	if listed {
		b.WriteString("\n")
	}

	tables := r.changedTables()
	if len(tables)+len(d.Keys)+len(d.SideEffects) > 0 {
		var what []string
		for _, n := range []struct {
			n         int
			one, many string
		}{{len(tables), "table", "tables"}, {len(d.Keys), "key", "keys"}, {len(d.SideEffects), "side effect", "side effects"}} {
			if n.n > 0 {
				what = append(what, plural(int64(n.n), n.one, n.many))
			}
		}
		fmt.Fprintf(b, "<details><summary>What changed (%s)</summary>\n\n", strings.Join(what, ", "))
		if len(tables) > 0 {
			b.WriteString("| Table | Rows | What |\n|---|---|---|\n")
			for _, t := range tables {
				fmt.Fprintf(b, "| %s | %s | %s |\n", mdCell(t.Name), num(t.Rows), md(tableWhat(t)))
			}
			b.WriteString("\n")
		}
		if len(d.Keys) > 0 {
			b.WriteString("| Key | Where | Value | Action |\n|---|---|---|---|\n")
			for _, k := range d.Keys {
				fmt.Fprintf(b, "| %s %s | %s | %s | %s |\n", md(k.Provider), md(k.Env), md(k.Location.String()), mdCell(k.Masked), md(k.Action))
			}
			b.WriteString("\n")
		}
		if len(d.SideEffects) > 0 {
			b.WriteString("| Side effect | Status | How | To turn it back on for a test |\n|---|---|---|---|\n")
			for _, s := range d.SideEffects {
				fmt.Fprintf(b, "| %s | %s | %s | %s |\n", md(EffectLabel(s.ID, false)), md(effectStatus(s.Status)), md(s.How), md(s.Undo))
			}
			b.WriteString("\nNot stopped: " + KillSwitchLimits + "\n\n")
		}
		b.WriteString("</details>\n\n")
	}

	// The fixed sentences go as they are, so they read exactly as PRD
	// 19.1 writes them; only what came from the data is escaped.
	b.WriteString(NotCoveredLine(escapeExtras(d.NotCovered, md)) + "\n\n")
	id := r.keyID()
	if id != "" {
		id = md(ShortKeyID(id))
	}
	for _, l := range footerLines(id) {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n_" + ViewNote + "_\n")
}

// orDash is s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
