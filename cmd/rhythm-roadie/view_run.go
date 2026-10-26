// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/rhythm/kit/ui"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
)

// The screens of plan, copy and prepare (product.md 5.2, 5.3 and 5.5).

// shownTables is how many tables plan lists without -v.
const shownTables = 10

// runLabel is the right-hand label of the banner.
func runLabel(j engine.Job) string {
	switch j.Mode {
	case engine.Plan:
		return "plan · nothing changes"
	case engine.Prepare:
		return "prepare · " + endpointName(j.To, j.Opts.URL)
	}
	return endpointName(j.From, "") + " → " + endpointName(j.To, j.Opts.URL)
}

// endpointName is a short name for an endpoint: the URL's host when there
// is one, else the host, alias or last part of the path.
func endpointName(e core.Endpoint, url string) string {
	if h := hostOf(url); h != "" {
		return h
	}
	switch e.Kind {
	case core.SSHSite, core.DSN:
		if e.Host != "" {
			return e.Host
		}
	case core.WPAlias:
		return e.Alias
	}
	return shortName("", core.EndpointInfo{Kind: e.Kind, Display: e.Display})
}

// before prints what is known before the engine runs: the banner and the
// endpoints as typed (never their passwords: Display is masked).
func (v *view) before(j engine.Job) {
	v.header(runLabel(j))
	if j.Mode == engine.Plan {
		return // the plan's own lines describe both ends in full
	}
	if j.Mode == engine.Prepare || (j.Mode == engine.Plan && j.From.Display == j.To.Display && j.From.Kind != core.DumpFile) {
		v.setting("Staging", j.To.Display)
		return
	}
	v.setting("Source", j.From.Display)
	if j.To.Display != "" {
		v.setting("Target", j.To.Display)
	}
}

// stackLine is "WordPress + WooCommerce (HPOS) + Gravity Forms".
func stackLine(s core.StackInfo) string {
	if s.Name == "" {
		return ""
	}
	name := map[core.Stack]string{core.StackWordPress: "WordPress", core.StackLaravel: "Laravel", core.StackSQL: "SQL"}[s.Name]
	if name == "" {
		name = string(s.Name)
	}
	if s.Multisite {
		name += fmt.Sprintf(" multisite (%d sites)", s.Sites)
	}
	for _, p := range s.Profiles {
		if p == string(s.Name) || p == "multisite" || p == "woocommerce-hpos" {
			continue // the stack itself; HPOS is said on WooCommerce
		}
		label := profileNames[p]
		if label == "" {
			label = p
		}
		if p == "woocommerce" && s.HPOS {
			label += " (HPOS)"
		}
		name += " + " + label
	}
	return name
}

// profileNames are the profiles of PRD 6.10 as the Stack line names them.
var profileNames = map[string]string{
	"woocommerce": "WooCommerce", "action-scheduler": "Action Scheduler",
	"woocommerce-subscriptions": "Subscriptions", "automatewoo": "AutomateWoo", "gravity-forms": "Gravity Forms",
	"wpforms": "WPForms", "contact-form-7-flamingo": "Contact Form 7 + Flamingo", "ninja-forms": "Ninja Forms",
	"fluent-forms": "Fluent Forms", "mailpoet": "MailPoet", "edd": "Easy Digital Downloads", "memberpress": "MemberPress",
	"learndash": "LearnDash", "wp-mail-smtp": "WP Mail SMTP", "post-smtp": "Post SMTP", "fluent-smtp": "FluentSMTP",
	"wp-mail-logging": "WP Mail Logging", "email-log": "Email Log", "laravel-cashier": "Cashier", "sql": "SQL",
}

// endpointLine describes a result's source or target: "prod.sql.gz (MySQL
// 8.0 dump, 2.4 GB, 61 tables)".
func endpointLine(e core.EndpointInfo, tables int) string {
	var parts []string
	if e.Engine != "" {
		eng := e.Engine
		if e.Server != "" {
			eng += " " + e.Server
		}
		if e.Kind == core.DumpFile || e.Kind == core.Stdio {
			eng += " dump"
		}
		parts = append(parts, eng)
	}
	if e.Bytes > 0 {
		parts = append(parts, size(e.Bytes))
	}
	if tables > 0 {
		parts = append(parts, plural(int64(tables), "table", "tables"))
	}
	if len(parts) == 0 {
		return e.Display
	}
	return e.Display + " (" + strings.Join(parts, ", ") + ")"
}

// changeWords are the actions as the DATA lines say them.
var changeWords = map[core.Action]string{
	core.Fake: "fake", core.Blank: "blank", core.SetNull: "null", core.Keep: "kept",
	core.Truncate: "truncated", core.DropTable: "left out", core.DeleteRow: "deleted",
	core.Scrub: "scrub", core.RewriteURL: "URLs rewritten", core.SwapKey: "keys swapped",
	core.Fixed: "fixed value", core.ResetPassword: "reset",
}

// tableWhat says what happens to one table: its columns grouped by action,
// "user_email, user_login → fake; user_pass → reset".
func tableWhat(t core.TableReport, changes []core.Change) string {
	switch core.Action(t.Action) {
	case core.Truncate:
		return "truncated" + profileNote(t.Profile)
	case core.DropTable:
		return "left out of the copy"
	case core.Keep:
		return "kept as it is"
	}
	byCol := map[string]core.Action{}
	for _, c := range changes {
		if c.Table == t.Name {
			byCol[c.Column] = c.Action
		}
	}
	var order []core.Action
	cols := map[core.Action][]string{}
	for _, c := range t.Columns {
		a := byCol[c.Name]
		if a == "" {
			a = core.Fake
		}
		if a == core.Keep {
			continue
		}
		if _, ok := cols[a]; !ok {
			order = append(order, a)
		}
		cols[a] = append(cols[a], c.Name)
	}
	var parts []string
	for _, a := range order {
		parts = append(parts, strings.Join(cols[a], ", ")+" → "+changeWords[a])
	}
	var kept int64
	for _, c := range t.Columns {
		if byCol[c.Name] != core.Keep {
			kept = max(kept, c.Kept)
		}
	}
	s := strings.Join(parts, "; ")
	if kept > 0 {
		s += " (" + num(kept) + " kept)"
	}
	if s == "" {
		return "no column changes"
	}
	return s
}

func profileNote(p string) string {
	if p == "" {
		return ""
	}
	return " (" + p + ")"
}

// changedTables are the tables a run changes, in result order.
func changedTables(r *core.Result) []core.TableReport {
	var out []core.TableReport
	for _, t := range r.Tables {
		if core.Action(t.Action) != core.Keep {
			out = append(out, t)
		}
	}
	return out
}

// dataSection prints DATA: each changed table (every table with -v), with
// the rule of each column under it with -v.
func (v *view) dataSection(r *core.Result) {
	tables := changedTables(r)
	if v.o.verbose {
		tables = r.Tables
	}
	v.section("DATA", len(changedTables(r)), "tables", ui.Signal)
	for i, t := range tables {
		if i == shownTables && !v.o.verbose {
			v.item(fmt.Sprintf("... %d more (rhythm-roadie plan -v lists them)", len(tables)-shownTables))
			break
		}
		v.row(t.Name, num(t.Rows)+" rows", tableWhat(t, r.Changes))
		if v.o.verbose {
			for _, c := range t.Columns {
				line := fmt.Sprintf("%s.%s: rule %s, %s changed, %s kept", t.Name, c.Name, c.Rule, num(c.Changed), num(c.Kept))
				v.under("      " + line)
			}
		}
	}
}

// liveKeys are the keys that are live, the ones the KEYS block counts.
func liveKeys(keys []core.KeyHit) []core.KeyHit {
	var out []core.KeyHit
	for _, k := range keys {
		if k.Env != "test" {
			out = append(out, k)
		}
	}
	return out
}

// isSite reports whether an endpoint has files Roadie edits.
func isSite(k core.EndpointKind) bool {
	return k == core.LocalSite || k == core.SSHSite || k == core.WPAlias
}

// basicAuthDir is where --basic-auth writes its snippets (PRD 13.2).
const basicAuthDir = ".roadie/basic-auth/"

// uploadExcludes are the PRD 15.1 paths that hold personal data in a
// WordPress site's files, for the rsync line of the ALSO block.
var uploadExcludes = []string{
	"wp-content/debug.log", "wp-content/uploads/wc-logs", "wp-content/uploads/gravity_forms",
	"wp-content/updraft", "wp-content/ai1wm-backups", "*.sql", "*.sql.gz", "*.zip",
}

// rsyncLine is the rsync command that copies a WordPress site's files
// without the folders that hold personal data. Roadie copies no files
// (PRD 5), so it only prints it.
func rsyncLine(j engine.Job) string {
	src, dst := "<production>/", "<staging>/"
	if p := rsyncPath(j.From); p != "" {
		src = p
	}
	if p := rsyncPath(j.To); p != "" {
		dst = p
	}
	var b strings.Builder
	b.WriteString("rsync -a")
	for _, e := range uploadExcludes {
		b.WriteString(" --exclude " + shellQuote(e))
	}
	b.WriteString(" " + shellQuote(src) + " " + shellQuote(dst))
	return b.String()
}

// rsyncPath is an endpoint as rsync names it, "" when it isn't a site.
func rsyncPath(e core.Endpoint) string {
	switch e.Kind {
	case core.LocalSite:
		return strings.TrimRight(e.Path, "/") + "/"
	case core.SSHSite:
		host := e.Host
		if e.User != "" {
			host = e.User + "@" + host
		}
		return host + ":" + strings.TrimRight(e.Path, "/") + "/"
	}
	return ""
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("-_./:@", r)
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// alsoLines are the ALSO block: what Roadie changes outside the data and
// the three first ROADMAP items of PRD 5 when they apply (files and
// uploads, basic auth, object caches and search indexes).
func alsoLines(r *core.Result, j engine.Job) []string {
	var out []string
	wp := r.Stack.Name == core.StackWordPress
	site := isSite(j.To.Kind)
	if site && wp && !j.Opts.NoIndex {
		out = append(out, "noindex: X-Robots-Tag, robots.txt Disallow: / and the crawler block (blog_public in the database left as it is)")
	}
	var urls int64
	tables := map[string]bool{}
	for _, c := range r.Changes {
		if c.Action == core.RewriteURL && c.Values > 0 {
			urls += c.Values
			tables[c.Table] = true
		}
	}
	if urls > 0 {
		out = append(out, fmt.Sprintf("%s production URLs rewritten in %s", num(urls), plural(int64(len(tables)), "table", "tables")))
	}
	if wp && j.From.Display != j.To.Display {
		out = append(out, "Files are not copied. For uploads, leave out the folders that hold personal data: "+rsyncLine(j))
	}
	if site {
		if j.Opts.BasicAuth != "" {
			out = append(out, "Basic auth snippets for Apache, nginx and Caddy go in "+basicAuthDir+"; add the one for your server (Roadie never edits server config)")
		} else {
			out = append(out, "Basic auth is off: --basic-auth <user> writes snippets for Apache, nginx and Caddy (Roadie never edits server config)")
		}
	}
	if wp || r.Stack.Name == core.StackLaravel {
		out = append(out, strings.ToUpper(cachesLine[:1])+cachesLine[1:])
	}
	return out
}

// planBox is the plan's verdict: whether it would stop and on what.
func planBox(r *core.Result, j engine.Job) (word, line string) {
	what := "copy"
	if j.From.Display == j.To.Display && j.From.Kind != core.DumpFile {
		what = "prepare"
	}
	if n := len(r.Uncovered); n > 0 && !j.Opts.AllowUncovered {
		return "PLAN", fmt.Sprintf("%s would stop: %s", what, plural(int64(n), "uncovered column", "uncovered columns"))
	}
	return "PLAN", "ready: nothing changes until you run " + what
}

// wouldStop reports whether the copy a plan describes would stop.
func wouldStop(r *core.Result, j engine.Job) bool {
	return len(r.Uncovered) > 0 && !j.Opts.AllowUncovered
}

// uncoveredSection prints UNCOVERED with a ▌ block for each column.
func (v *view) uncoveredSection(us []core.Uncovered) {
	if len(us) == 0 {
		return
	}
	v.section("UNCOVERED", len(us), "", sevColor(core.SeverityMedium))
	for _, u := range us {
		title := u.Table + "." + u.Column + " looks personal and no rule covers it"
		var details []string
		if u.Rate > 0 && u.Rate < 100 {
			details = append(details, fmt.Sprintf("%d%% of sampled values look personal", u.Rate))
		}
		v.card(core.SeverityMedium, title, "", details, "add it under columns: in .roadie/config.yaml, or --allow-uncovered")
	}
}

// planView is plan's screen (product.md 5.2) after the banner.
func (v *view) planView(r *core.Result, j engine.Job, tm *timings) {
	v.setting("Source", endpointLine(r.Source, len(r.Tables)))
	if r.Target.Display != "" && r.Target.Display != r.Source.Display {
		v.setting("Target", endpointLine(r.Target, 0))
	}
	if len(r.Production) > 0 {
		v.setting("Target check", "not in production: ["+strings.Join(r.Production, ", ")+"]")
	}
	v.setting("Stack", stackLine(r.Stack))
	if r.URL != "" || j.Opts.URL != "" {
		from := r.URL
		if from == "" {
			from = "production's URL"
		}
		to := j.Opts.URL
		if to == "" {
			to = "(no --url)"
		}
		v.setting("URLs", from+" → "+to+" (serialized and JSON safe)")
	}
	v.setting("Consistency", consistencyLine(r.Consistency))
	if !j.Opts.OnePass && j.Opts.SchemaOnly {
		v.setting("Rows", "not read (--schema-only): counts are estimates")
	}

	v.dataSection(r)
	if keys := liveKeys(r.Keys); len(keys) > 0 {
		v.section("KEYS", len(keys), "live keys", sevColor(core.SeverityCritical))
		v.keyCards(keys)
	}
	v.effectsBlock(r, j)
	v.editsBlock(r)
	if len(r.RemoteCommands) > 0 {
		v.section("REMOTE COMMANDS", len(r.RemoteCommands), "", lipgloss.Color(ui.Muted))
		for _, c := range r.RemoteCommands {
			v.item(c)
		}
	}
	if also := alsoLines(r, j); len(also) > 0 {
		v.section("ALSO", -1, "", lipgloss.Color(ui.Muted))
		for _, a := range also {
			v.item(a)
		}
	}
	v.uncoveredSection(r.Uncovered)
	var others []core.Finding
	for _, f := range r.Findings {
		if f.ID != core.FindingUncoveredColumn {
			others = append(others, f)
		}
	}
	v.findingGroups(others)

	word, line := planBox(r, j)
	color := lipgloss.TerminalColor(lipgloss.Color(ui.Ok))
	if wouldStop(r, j) {
		color = sevColor(core.SeverityMedium)
	}
	v.box(word, line, "", color)
	read := "read schema and counts"
	if j.Opts.SchemaOnly {
		read = "read the schema"
	}
	v.under(fmt.Sprintf("%s in %s; no rows changed", read, duration(r.Duration)))
	v.infoLines(r.Findings)
	v.debugLines(r, tm)
	v.blank()
	if wouldStop(r, j) {
		v.next("rhythm-roadie explain uncovered-column")
	} else if j.Opts.ProductionURL == "" && !j.Opts.SchemaOnly {
		v.next("run the same command line with copy (or prepare) instead of plan to make the change")
	}
}

// consistencyLine says how fakes are keyed (PRD 11.1).
func consistencyLine(c core.ConsistencyInfo) string {
	if c.Mode == "stable" && c.KeyFingerprint != "" {
		return "stable key " + c.KeyFingerprint + ": the same fake for each real person on every refresh"
	}
	return "per-run key: each real person gets one fake, across every table"
}

// effectsBlock prints SIDE EFFECTS; a database-only target gets PRD 13.4's sentence.
func (v *view) effectsBlock(r *core.Result, j engine.Job) {
	if j.Opts.NoKillSwitch {
		v.section("SIDE EFFECTS", 0, "", lipgloss.Color(ui.Muted))
		v.item("left as they are (--no-kill-switch): verify will FAIL on side effects")
		return
	}
	if j.To.Kind == core.DSN {
		v.section("SIDE EFFECTS", len(r.SideEffects), "", lipgloss.Color(ui.Muted))
		v.item("This target is a database only: Roadie can't install the Echo seal or edit config files. Turn mail and jobs off where the app runs.")
		v.effectLines(r.SideEffects)
		return
	}
	if len(r.SideEffects) > 0 {
		v.section("SIDE EFFECTS", len(r.SideEffects), "", lipgloss.Color(ui.Ok))
		v.effectLines(r.SideEffects)
	}
}

// editsBlock prints the file edits of a plan; -v adds the step of each.
func (v *view) editsBlock(r *core.Result) {
	if len(r.PlannedEdits) == 0 {
		return
	}
	v.section("FILE EDITS", len(r.PlannedEdits), "", lipgloss.Color(ui.Muted))
	for _, e := range r.PlannedEdits {
		line := e.File + ": " + e.What
		if v.o.verbose && e.Step != "" {
			line += " (" + e.Step + ")"
		}
		v.item(line)
	}
}

// resultView is the screen of a finished copy or prepare (product.md
// 5.3): what changed, the verify, the box and what to do next.
func (v *view) resultView(r *core.Result, j engine.Job, reportPath string, tm *timings) {
	s := r.Summary()
	v.setting("Stack", stackLine(r.Stack))
	v.setting("Plan", fmt.Sprintf("%s pseudonymised, %s, %s (plan -v for all)",
		plural(int64(s.TablesPseudonymised), "table", "tables"), plural(int64(len(liveKeys(r.Keys))), "live key", "live keys"),
		plural(int64(len(r.SideEffects)), "side effect", "side effects")))
	if v.o.verbose {
		v.dataSection(r)
		v.editsBlock(r)
	}
	v.blank()
	ok := lipgloss.TerminalColor(lipgloss.Color(ui.Ok))
	v.mark("✓", "Data", fmt.Sprintf("%s values pseudonymised in %s, %s URLs rewritten",
		bigNum(s.ValuesPseudonymised), plural(int64(s.TablesPseudonymised), "table", "tables"), num(s.URLsRewritten)), ok)
	v.keysLine(r, s)
	v.effectsLine(r, j)
	if isSite(j.To.Kind) && r.Stack.Name == core.StackWordPress {
		if j.Opts.NoIndex {
			v.mark("·", "Search", "noindex off (--noindex=off)", lipgloss.Color(ui.Muted))
		} else {
			v.mark("✓", "Search", "noindex, robots.txt", ok)
		}
	}
	if r.Backup != "" {
		v.mark("✓", "Backup", r.Backup+"; to undo: "+r.Restore, ok)
	}
	switch j.To.Kind {
	case core.DumpFile:
		written := r.Target.Display
		if r.Output != nil && r.Output.Bytes > 0 {
			written += " (" + size(r.Output.Bytes) + ")"
		}
		v.mark("✓", "Written", written, ok)
	case core.Stdio:
		v.mark("✓", "Written", "to stdout", ok)
	default:
		v.mark("✓", "Import", plural(int64(len(r.Tables)), "table", "tables")+" into "+r.Target.Display, ok)
	}
	if len(r.SideEffects) > 0 && !j.Opts.NoKillSwitch {
		v.blank()
		v.setting("Not stopped", notStopped)
	}
	if vr := r.Verify; vr != nil {
		v.section("VERIFY ("+vr.Mode+")", -1, "", ui.Signal)
		v.checkLines(vr.Checks)
	}
	v.findingGroups(r.AllFindings())
	v.verdictBox(r, j)
	v.underBox(r, tm)
	v.footer(r, j, reportPath)
}

// keysLine is copy's "✓ Keys" line.
func (v *view) keysLine(r *core.Result, s core.Summary) {
	manual := 0
	for _, k := range r.Keys {
		if k.Action == "manual" {
			manual++
		}
	}
	switch {
	case manual > 0:
		v.mark("✗", "Keys", fmt.Sprintf("%d swapped to a test key, %d blanked, %d to edit by hand", s.KeysSwapped, s.KeysBlanked, manual), lipgloss.Color(ui.Danger))
	case s.KeysFound == 0:
		v.mark("✓", "Keys", "no live keys found", lipgloss.Color(ui.Ok))
	default:
		v.mark("✓", "Keys", fmt.Sprintf("%d swapped to a test key, %d blanked", s.KeysSwapped, s.KeysBlanked), lipgloss.Color(ui.Ok))
	}
}

// effectsLine is copy's "✓ Side effects" line.
func (v *view) effectsLine(r *core.Result, j engine.Job) {
	if j.Opts.NoKillSwitch {
		v.mark("·", "Side effects", "left as they are (--no-kill-switch)", lipgloss.Color(ui.Muted))
		return
	}
	var off, on []string
	for _, e := range r.SideEffects {
		switch e.Status {
		case "off":
			off = append(off, effectLabel(e.ID, true))
		case "on", "unknown":
			on = append(on, effectLabel(e.ID, true))
		}
	}
	switch {
	case len(on) > 0:
		v.mark("✗", "Side effects", "still on: "+strings.Join(on, ", "), lipgloss.Color(ui.Danger))
	case len(off) > 0:
		v.mark("✓", "Side effects", strings.Join(off, ", "), lipgloss.Color(ui.Ok))
	}
}

// verdict is the result box's word and line for a copy, prepare or verify.
func verdict(r *core.Result, failOn core.Severity, name string) (word, line string, color lipgloss.TerminalColor) {
	if r.Verify == nil {
		return "DONE", "copied to " + name + "; not checked (--no-verify)", lipgloss.Color(ui.Warn)
	}
	for _, f := range r.AllFindings() {
		if failOn.Fails(f.Severity) {
			return "FAIL", name + " is not a safe copy yet", lipgloss.Color(ui.Danger)
		}
	}
	if r.Verify.Coverage.Sample > 0 {
		return "PASS (sample)", name + " is a pseudonymised copy", lipgloss.Color(ui.Ok)
	}
	return "PASS", name + " is a pseudonymised copy", lipgloss.Color(ui.Ok)
}

// verdictBox prints the result box of a copy, prepare or verify.
func (v *view) verdictBox(r *core.Result, j engine.Job) {
	word, line, color := verdict(r, j.Opts.FailOn, shortName(j.Opts.URL, r.Target))
	v.box(word, line, counts(r.Summary()), color)
}

// underBox is the muted line under the box: size, tables, rows and time,
// then the info findings and with --debug the timings.
func (v *view) underBox(r *core.Result, tm *timings) {
	var parts []string
	var b int64
	if r.Input != nil {
		b = r.Input.Bytes
	}
	if b == 0 {
		b = r.Source.Bytes
	}
	if b > 0 {
		parts = append(parts, size(b))
	}
	if n := len(r.Tables); n > 0 {
		parts = append(parts, plural(int64(n), "table", "tables"))
	} else if vr := r.Verify; vr != nil && vr.Coverage.TablesScanned > 0 {
		parts = append(parts, plural(int64(vr.Coverage.TablesScanned), "table", "tables"), plural(vr.Coverage.RowsScanned, "row", "rows"))
	}
	parts = append(parts, duration(r.Duration))
	v.under(strings.Join(parts, " · "))
	v.infoLines(r.AllFindings())
	v.debugLines(r, tm)
}

// debugLines are --debug's timings and counts; never values.
func (v *view) debugLines(r *core.Result, tm *timings) {
	if !v.o.debug {
		return
	}
	if tm != nil {
		if t := tm.String(); t != "" {
			v.under("timings: " + t)
		}
	}
	s := r.Summary()
	line := fmt.Sprintf("counts: %s rows · %s values pseudonymised · %s URLs rewritten · %d keys · %d of %d side effects off",
		num(s.Rows), num(s.ValuesPseudonymised), num(s.URLsRewritten), s.KeysFound, s.SideEffectsOff, s.SideEffectsTotal)
	if vr := r.Verify; vr != nil {
		line += fmt.Sprintf(" · verify read %s rows, %s in %d of %d tables", num(vr.Coverage.RowsScanned), size(vr.Coverage.BytesScanned),
			vr.Coverage.TablesScanned, vr.Coverage.TablesTotal)
	}
	v.under(line)
}

// echoLine is PRD 16.1's line after a copy or prepare of a WordPress or
// Laravel site.
func echoLine(url string) string {
	return "Mail from this copy goes to Echo at " + url + ". Run rhythm echo up on the staging host, or the copy's mail will fail closed."
}

// loginLine is PRD 11.1's line when no user was kept.
func loginLine(firstAdmin string) string {
	return "No user was kept, so nobody can log in with a real password. Set one: wp user update " + firstAdmin + " --user_pass=<new password>"
}

// footer prints, after the box: the honesty line, the Echo line, the
// login line, the ALSO block and the next steps.
func (v *view) footer(r *core.Result, j engine.Job, reportPath string) {
	v.para(honestyLine)
	appStack := r.Stack.Name == core.StackWordPress || r.Stack.Name == core.StackLaravel
	if isSite(j.To.Kind) && appStack && !j.Opts.NoKillSwitch {
		v.para(echoLine(j.Opts.EchoURL))
	}
	if r.KeptUsers == 0 && r.FirstAdmin != "" && r.Stack.Name == core.StackWordPress {
		v.para(loginLine(r.FirstAdmin))
	}
	if also := alsoLines(r, j); len(also) > 0 && !v.o.quiet {
		v.section("ALSO", -1, "", lipgloss.Color(ui.Muted))
		for _, a := range also {
			v.item(a)
		}
	}
	v.blank()
	failed := false
	for _, f := range r.AllFindings() {
		if j.Opts.FailOn.Fails(f.Severity) {
			failed = true
			break
		}
	}
	if failed && isSite(j.To.Kind) {
		v.next("rhythm-roadie prepare " + j.To.Display + " fixes what it can of these")
	}
	if reportPath != "" {
		v.next("signed report: " + reportPath)
	}
	if !failed && r.Verify != nil {
		url := j.Opts.URL
		if url == "" {
			url = "<url>"
		}
		v.next("next: rhythm echo up, then rhythm soundcheck run " + url)
	}
}

// basicAuthLine prints the generated basic-auth password once, after the
// result box; it is never in a report.
func (v *view) basicAuthLine(user, password string) {
	if password == "" {
		return
	}
	fmt.Fprintf(v.w, "\n  Basic auth for %s: password %s (shown once; %s holds only its hash)\n", safe(user), safe(password), basicAuthDir)
}

// stopView is the screen of a run that stopped with an error: what it
// found so far, a box with the code and the target's state, the PRD 17
// message and where to read more.
func (v *view) stopView(r *core.Result, j engine.Job) {
	e := r.Err
	if e.Code == core.ErrUncovered {
		v.uncoveredSection(r.Uncovered)
	}
	if e.Code == core.ErrKeyManual || e.Code == core.ErrKillSwitch {
		v.findingGroups(r.AllFindings())
	}
	state := ""
	switch r.TargetState {
	case core.Untouched:
		state = "; the target is untouched"
	case core.Mixed:
		state = "; the target is in a mixed state"
	case core.Replaced:
		state = "; the target was replaced"
	}
	word := "STOPPED"
	if j.Mode == engine.Plan {
		word = "PLAN"
	}
	v.box(word, e.Code+state, "", sevColor(core.SeverityCritical))
	v.para(e.Msg)
	v.blank()
	if core.KnownID(e.Code) && e.Code != core.ErrCodeNotBuilt {
		v.next("rhythm-roadie explain " + e.Code)
	}
}
