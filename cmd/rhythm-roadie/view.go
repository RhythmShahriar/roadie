// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/term"
	"github.com/RhythmShahriar/rhythm/kit/ui"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/report"
)

// The layout of Soundcheck and Echo: 76 columns, badges 10 wide.
const (
	width      = 76
	badgeWidth = 10
	labelWidth = 13
)

// The exact strings of PRD 16.1 and 19.1 that tests pin.
const (
	honestyLine = "Pseudonymised, not anonymised. The copy still holds order totals, dates, products and anything Roadie can't recognise in free text. Treat it as personal data."
	// notStopped is PRD 13.5's list of what the kill switch can't stop.
	notStopped = report.KillSwitchLimits
	// cachesLine is the third ROADMAP item the ALSO block names (PRD 5).
	cachesLine = "object caches and search indexes (Redis, Algolia, ElasticPress) are not copied or cleared"
)

// notCovered is PRD 19.1's Not covered sentence, as the reports write it.
var notCovered = report.NotCoveredSentence

// viewOpts are the switches of the terminal view.
type viewOpts struct {
	plain   bool // no color, boxes or glyphs: --plain, or stdout isn't a terminal
	quiet   bool // only the result box line and the counts
	verbose bool // every table, rule and file edit
	debug   bool // timings and counts too
}

// view writes the terminal output of one run.
type view struct {
	w io.Writer
	o viewOpts
}

var (
	muted = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	bold  = lipgloss.NewStyle().Bold(true)
)

// setupColor picks the view's look from the flags and the environment:
// the boxed layout on a terminal or with CLICOLOR_FORCE, plain text
// otherwise and with --plain; color unless --plain, --no-color or
// NO_COLOR. It sets lipgloss's default renderer, which kit's ui styles use.
func setupColor(g *globals, stdoutTerminal bool, env envFunc) viewOpts {
	forced := false
	if v, ok := env("CLICOLOR_FORCE"); ok && v != "" && v != "0" {
		forced = true
	}
	plain := g.plain || (!stdoutTerminal && !forced)
	_, noColorEnv := env("NO_COLOR")
	color := !plain && !g.noColor && !noColorEnv
	switch {
	case !color:
		lipgloss.SetColorProfile(termenv.Ascii)
	case !stdoutTerminal:
		// Forced color into a pipe: there's no terminal to ask about its
		// background or depth, so pick both.
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(true)
	default:
		lipgloss.SetColorProfile(termenv.NewOutput(os.Stdout).EnvColorProfile())
	}
	return viewOpts{plain: plain, quiet: g.quiet, verbose: g.verbose, debug: g.debug}
}

// safe makes text from a site, a dump or a config safe to print.
func safe(s string) string { return term.SafeText(s) }

// header starts the output: the banner with a label on the right and a rule.
func (v *view) header(label string) {
	if v.o.quiet {
		return
	}
	label = safe(label)
	if v.o.plain {
		fmt.Fprintf(v.w, "rhythm roadie %s - %s\n\n", displayVersion(), label)
		return
	}
	left := ui.Brand.Render("♪") + " " + bold.Render("rhythm roadie") + " " + muted.Render(displayVersion())
	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(label))
	fmt.Fprintf(v.w, "\n  %s%s%s\n  %s\n\n", left, strings.Repeat(" ", gap), muted.Render(label), muted.Render(strings.Repeat("─", width)))
}

// setting prints one "▸ Label  value" line; a long value wraps under itself.
func (v *view) setting(label, value string) {
	if v.o.quiet {
		return
	}
	value = safe(value)
	if v.o.plain {
		fmt.Fprintf(v.w, "%s: %s\n", label, value)
		return
	}
	lines := wrap(value, width-labelWidth-5)
	fmt.Fprintf(v.w, "  %s %s %s\n", ui.Brand.Render("▸"), muted.Bold(true).Width(labelWidth).Render(label), lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(v.w, "  %s%s\n", strings.Repeat(" ", labelWidth+3), l)
	}
}

// section prints a heading with a count, "DATA  21 tables ───"; n < 0
// leaves the count out.
func (v *view) section(label string, n int, unit string, color lipgloss.TerminalColor) {
	if v.o.quiet {
		return
	}
	head := label
	if n >= 0 {
		head += "  " + strconv.Itoa(n)
		if unit != "" {
			head += " " + unit
		}
	}
	if v.o.plain {
		fmt.Fprintf(v.w, "\n%s\n", head)
		return
	}
	styled := lipgloss.NewStyle().Foreground(color).Bold(true).Render(head)
	fmt.Fprintf(v.w, "\n  %s %s\n\n", styled, muted.Render(strings.Repeat("─", max(2, width-lipgloss.Width(head)-1))))
}

// mark prints a status line: "✓ Label  detail" (or ✗, ·).
func (v *view) mark(glyph, label, detail string, color lipgloss.TerminalColor) {
	v.markCol(glyph, label, detail, color, labelWidth+1)
}

// markCol is mark with the label column col cells wide.
func (v *view) markCol(glyph, label, detail string, color lipgloss.TerminalColor, col int) {
	if v.o.quiet {
		return
	}
	detail = safe(detail)
	if v.o.plain {
		word := map[string]string{"✓": "ok", "✗": "FAIL", "·": "-", "!": "warn"}[glyph]
		fmt.Fprintf(v.w, "[%s] %s: %s\n", word, label, detail)
		return
	}
	lines := wrap(detail, width-col-5)
	g := lipgloss.NewStyle().Foreground(color).Render(glyph)
	fmt.Fprintf(v.w, "  %s %s %s\n", g, ui.Pad(label, col), lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(v.w, "  %s%s\n", strings.Repeat(" ", col+3), l)
	}
}

// item prints an indented line under a section, "    · text".
func (v *view) item(text string) {
	if v.o.quiet {
		return
	}
	text = safe(text)
	if v.o.plain {
		fmt.Fprintf(v.w, "- %s\n", text)
		return
	}
	for i, l := range wrap(text, width-6) {
		lead := "    · "
		if i > 0 {
			lead = "      "
		}
		fmt.Fprintln(v.w, lead+l)
	}
}

// row prints a table line of the DATA section: name, count and what
// changes, the last wrapped in its own column.
func (v *view) row(name, count, what string) {
	if v.o.quiet {
		return
	}
	name, what = safe(name), safe(what)
	if v.o.plain {
		fmt.Fprintf(v.w, "%s (%s): %s\n", name, count, what)
		return
	}
	left := "    " + ui.Pad(ui.Truncate(name, 26), 26) + " " + fmt.Sprintf("%13s", count) + "   "
	lines := wrap(what, max(20, width-lipgloss.Width(left)+2))
	fmt.Fprintln(v.w, left+lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintln(v.w, strings.Repeat(" ", lipgloss.Width(left))+l)
	}
}

// card prints a finding or key block with a severity badge and the ▌
// rail: title and right-hand ID, then detail lines and a → fix.
func (v *view) card(sev core.Severity, title, right string, details []string, fix string) {
	if v.o.quiet {
		return
	}
	title, right, fix = safe(title), safe(right), safe(fix)
	name := strings.ToUpper(sev.String())
	if v.o.plain {
		head := fmt.Sprintf("[%s] %s", name, title)
		if right != "" {
			head += " (" + right + ")"
		}
		fmt.Fprintln(v.w, head)
		for _, d := range details {
			if d != "" {
				fmt.Fprintf(v.w, "  %s\n", safe(d))
			}
		}
		if fix != "" {
			fmt.Fprintf(v.w, "  fix: %s\n", fix)
		}
		return
	}
	color := ui.SeverityColor(sev.String())
	titleCol := width - badgeWidth - 4
	head := ui.Badge(name, color, true, badgeWidth) + "  "
	if right != "" && lipgloss.Width(title)+lipgloss.Width(right)+2 <= titleCol {
		head += ui.Pad(bold.Render(title), titleCol-lipgloss.Width(right)) + muted.Render(right)
	} else {
		head += bold.Render(ui.Truncate(title, titleCol))
	}
	body := []string{head}
	pad := strings.Repeat(" ", badgeWidth+2)
	for _, d := range details {
		for _, l := range wrap(safe(d), width-badgeWidth-4) {
			if l != "" {
				body = append(body, pad+muted.Render(l))
			}
		}
	}
	if fix != "" {
		for i, l := range wrap(fix, width-badgeWidth-6) {
			lead := ui.Brand.Render("→") + " "
			if i > 0 {
				lead = "  "
			}
			body = append(body, pad+lead+l)
		}
	}
	rail := lipgloss.NewStyle().Border(lipgloss.Border{Left: "▌"}, false, false, false, true).BorderForeground(color).PaddingLeft(1).MarginLeft(1)
	fmt.Fprintln(v.w, rail.Render(strings.Join(body, "\n")))
}

// box prints the result box: a verdict word and its line, then the
// counts. The color follows the verdict.
func (v *view) box(word, line, counts string, color lipgloss.TerminalColor) {
	line, counts = safe(line), safe(counts)
	if v.o.plain || v.o.quiet {
		if !v.o.quiet {
			fmt.Fprintln(v.w)
		}
		fmt.Fprintf(v.w, "%s   %s\n", word, line)
		if counts != "" {
			fmt.Fprintln(v.w, counts)
		}
		return
	}
	head := lipgloss.NewStyle().Foreground(color).Bold(true).Render(word) + "   " + bold.Render(line)
	content := head
	if counts != "" {
		content += "\n" + counts
	}
	fmt.Fprintf(v.w, "\n%s\n", ui.Panel(content, color))
}

// under prints a muted line under the box.
func (v *view) under(text string) {
	if v.o.quiet {
		return
	}
	text = safe(text)
	if v.o.plain {
		fmt.Fprintln(v.w, text)
		return
	}
	for _, l := range wrap(text, width) {
		fmt.Fprintf(v.w, "  %s\n", muted.Render(l))
	}
}

// para prints a paragraph wrapped to the width, after a blank line.
func (v *view) para(text string) {
	if v.o.quiet {
		return
	}
	text = safe(text)
	fmt.Fprintln(v.w)
	if v.o.plain {
		fmt.Fprintln(v.w, text)
		return
	}
	for _, l := range wrap(text, width) {
		fmt.Fprintf(v.w, "  %s\n", l)
	}
}

// next prints a "→ text" next step.
func (v *view) next(text string) {
	if v.o.quiet {
		return
	}
	text = safe(text)
	if v.o.plain {
		fmt.Fprintln(v.w, text)
		return
	}
	fmt.Fprintf(v.w, "  %s %s\n", ui.Brand.Render("→"), text)
}

// blank prints an empty line outside -q.
func (v *view) blank() {
	if !v.o.quiet {
		fmt.Fprintln(v.w)
	}
}

// wrap breaks s into lines of at most n cells at spaces; a word longer
// than n stays whole on its own line.
func wrap(s string, n int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if lipgloss.Width(cur)+1+lipgloss.Width(w) > n {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

// counts is the PRD 16.1 count line.
func counts(s core.Summary) string {
	return fmt.Sprintf("%d critical · %d high · %d medium · %d low · %d info", s.Critical, s.High, s.Medium, s.Low, s.Info)
}

// num writes n with thousands separators: 18,402.
func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// bigNum writes large counts short: 1.21 M above a million.
func bigNum(n int64) string {
	if n >= 1_000_000 {
		return strconv.FormatFloat(float64(n)/1e6, 'f', 2, 64) + " M"
	}
	return num(n)
}

// size writes a byte count in decimal units, as product.md does: 2.4 GB.
func size(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64) + " " + units[i]
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

// duration writes 1m 22s, 41s or 0.4s.
func duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	case d < time.Minute:
		return strconv.Itoa(int(d.Round(time.Second).Seconds())) + "s"
	case d < time.Hour:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	d = d.Round(time.Minute)
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// plural writes "1 table" or "3 tables".
// hiddenText counts what a baseline or ignore entries hid as findings,
// grouped as the shown ones are, with the hits they hold when there are
// more: "27 findings hidden by ... (1,055 hits)".
func hiddenText(vr *core.VerifyResult, what string) string {
	n := vr.HiddenFindings
	if n == 0 {
		n = vr.Hidden
	}
	s := plural(n, "finding", "findings") + what
	if vr.Hidden > n {
		s += " (" + plural(vr.Hidden, "hit", "hits") + ")"
	}
	return s
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return num(n) + " " + many
}

// shortName is how a result names its target: the URL's host, else the
// last part of a path or file name, else the display form.
func shortName(url string, e core.EndpointInfo) string {
	if h := hostOf(url); h != "" {
		return h
	}
	d := strings.TrimRight(e.Display, "/")
	switch e.Kind {
	case core.DumpFile, core.LocalSite:
		return path.Base(strings.ReplaceAll(d, "\\", "/"))
	case core.Stdio:
		return "stdout"
	}
	return d
}

// hostOf returns the host of a URL, or "" when it has none.
func hostOf(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	h, _, _ := strings.Cut(raw[i+3:], "/")
	if at := strings.LastIndex(h, "@"); at >= 0 {
		h = h[at+1:]
	}
	return h
}
