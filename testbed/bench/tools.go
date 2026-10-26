// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// The other tools the benchmark judges on the same inputs (PRD 22.3,
// qa.md 8.2), each only on what it claims to do. Their configuration is
// committed under testbed/tools/<name>/ and their versions are pinned in
// testbed/Makefile.
const (
	ToolWPCLI         = "wp-cli"
	ToolReplibyte     = "replibyte"
	ToolGreenmask     = "greenmask"
	ToolStagingShield = "staging-shield"
)

// What a tool can be judged on. Anything else is "can't see" for it, not
// "missed".
const (
	MeasureURLs        = "urls"         // production URLs left; other links or emails it broke
	MeasureCanaries    = "canaries"     // seeded personal values left (the oracle)
	MeasureLiveKeys    = "live-keys"    // seeded live keys left (the oracle)
	MeasureIntegrity   = "integrity"    // serialized values that no longer unserialize (real PHP)
	MeasureSideEffects = "side-effects" // sinkhole connections, live stub calls and real recipients (gate 3)
)

// Measures lists every measure in table order.
var Measures = []string{MeasureURLs, MeasureCanaries, MeasureLiveKeys, MeasureIntegrity, MeasureSideEffects}

// NotRun is a tool that couldn't run here; the reason goes with it.
const NotRun = "not run"

// ToolSpec is one other tool: what it is judged on and on which inputs.
type ToolSpec struct {
	Name   string
	Title  string
	Judged []string
	Inputs []string
}

// ToolSpecs are the tools in the order docs/benchmark.md lists them.
var ToolSpecs = []ToolSpec{
	{ToolWPCLI, "WP-CLI search-replace", []string{MeasureURLs}, []string{"r01", "r09", "r12"}},
	{ToolReplibyte, "Replibyte", []string{MeasureCanaries, MeasureLiveKeys, MeasureIntegrity}, []string{"r01", "r12"}},
	{ToolGreenmask, "Greenmask", []string{MeasureCanaries, MeasureLiveKeys, MeasureIntegrity}, []string{"r01", "r12"}},
	{ToolStagingShield, "Staging Shield", []string{MeasureSideEffects}, []string{"r01", "r13"}},
}

// ToolNames lists the names -tools accepts.
func ToolNames() []string {
	out := make([]string, len(ToolSpecs))
	for i, s := range ToolSpecs {
		out[i] = s.Name
	}
	return out
}

// ToolResult is one tool's score: not run with the reason, or a result
// per input.
type ToolResult struct {
	Name    string      `json:"name"`
	Title   string      `json:"title"`
	Version string      `json:"version,omitempty"`
	Judged  []string    `json:"judged"`
	NotRun  string      `json:"not_run,omitempty"`
	Inputs  []ToolInput `json:"inputs,omitempty"`
}

// ToolInput is a tool's result on one input. A nil count is a measure the
// tool isn't judged on ("can't see").
type ToolInput struct {
	Input   string `json:"input"`
	Outcome string `json:"outcome"`
	// Detail says what happened in IDs and counts, never a value.
	Detail string `json:"detail,omitempty"`

	URLs *Count `json:"production_urls,omitempty"`
	// Collateral counts what had to stay and changed: lookalike hosts,
	// other subdomains and addresses at the production domain. It can
	// only tell that some were rewritten, so it is a floor.
	Collateral  *Count `json:"collateral,omitempty"`
	Canaries    *Count `json:"canaries,omitempty"`
	LiveKeys    *Count `json:"live_keys,omitempty"`
	Integrity   *Count `json:"integrity_failures,omitempty"`
	SideEffects *Count `json:"side_effects,omitempty"`
}

// Count is how many were left (or failed) of how many there were. Of is
// 0 where there is no total (side effects).
type Count struct {
	Left int `json:"left"`
	Of   int `json:"of"`
}

// score sets the outcome: error when the tool failed to run, caught when
// every judged count is zero, missed otherwise.
func (t *ToolInput) score() {
	if t.Outcome == Errored || t.Outcome == NotRun {
		return
	}
	t.Outcome = Caught
	for _, c := range []*Count{t.URLs, t.Collateral, t.Canaries, t.LiveKeys, t.Integrity, t.SideEffects} {
		if c != nil && c.Left > 0 {
			t.Outcome = Missed
		}
	}
}

// fail marks an input the tool couldn't be run on.
func (t *ToolInput) fail(format string, a ...any) {
	t.Outcome, t.Detail = Errored, fmt.Sprintf(format, a...)
}

// ParseTools reads -tools: a comma-separated list of ToolNames, "all" or
// "none".
func ParseTools(s string) ([]string, error) {
	switch s {
	case "", "none":
		return nil, nil
	case "all":
		return ToolNames(), nil
	}
	var out []string
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		if !slices.Contains(ToolNames(), n) {
			return nil, fmt.Errorf("-tools %s: no such tool (one of %s, all or none)", n, strings.Join(ToolNames(), ", "))
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// toolInputs are the inputs of spec the run covers: all of them, or those
// -only names.
func toolInputs(spec ToolSpec, only []string) []string {
	if len(only) == 0 {
		return spec.Inputs
	}
	var out []string
	for _, in := range spec.Inputs {
		if slices.Contains(only, in) {
			out = append(out, in)
		}
	}
	return out
}

// toolsNeedDocker reports whether any chosen tool has an input to run.
func toolsNeedDocker(tools, only []string) bool {
	for _, s := range ToolSpecs {
		if slices.Contains(tools, s.Name) && len(toolInputs(s, only)) > 0 {
			return true
		}
	}
	return false
}

// RunTools judges the chosen tools (h.Tools) on their inputs. A tool that
// can't run here (not fetched, no testbed, a release that reads another
// database) is "not run" with the reason, never a failure.
func (h *Harness) RunTools(ctx context.Context, only []string) []ToolResult {
	var out []ToolResult
	for _, spec := range ToolSpecs {
		inputs := toolInputs(spec, only)
		if !slices.Contains(h.Tools, spec.Name) || len(inputs) == 0 {
			continue
		}
		r := ToolResult{Name: spec.Name, Title: spec.Title, Judged: spec.Judged}
		var run func(context.Context, *scenarioState) ToolInput
		r.Version, run, r.NotRun = h.toolReady(ctx, spec.Name)
		if r.NotRun != "" {
			h.logf("tool %s: not run: %s", spec.Name, r.NotRun)
			out = append(out, r)
			continue
		}
		for _, in := range inputs {
			h.logf("tool %s on %s", spec.Name, in)
			var ti ToolInput
			st, err := h.toolState(ctx, spec.Name, in)
			if err != nil {
				ti.fail("setup: %s", err)
			} else {
				ti = run(ctx, st)
			}
			ti.Input = in
			ti.score()
			h.logf("tool %s on %s: %s", spec.Name, in, ti.Outcome)
			r.Inputs = append(r.Inputs, ti)
		}
		out = append(out, r)
	}
	return out
}

// greenmaskReason is why Greenmask is not run: it reads PostgreSQL only.
func greenmaskReason(version string) string {
	return "Greenmask " + version + " dumps and restores PostgreSQL only; these inputs are MySQL dumps of WordPress shops"
}

// toolReady returns a tool's version and its runner, or why it can't run.
func (h *Harness) toolReady(ctx context.Context, name string) (version string, run func(context.Context, *scenarioState) ToolInput, notRun string) {
	if name == ToolGreenmask {
		return h.GreenmaskVersion, nil, greenmaskReason(cmpOr(h.GreenmaskVersion, "(unpinned)"))
	}
	if !h.dockerUp {
		return "", nil, "the testbed isn't running: make -C testbed up"
	}
	switch name {
	case ToolWPCLI:
		out, err := h.Ex.Exec(ctx, ctrSSHD, "roadie", nil, nil, "wp", "--path="+docroot, "cli", "version")
		if err != nil {
			return "", nil, "WP-CLI in the sshd container: " + errDetail(err).Error()
		}
		return strings.TrimPrefix(strings.TrimSpace(string(out)), "WP-CLI "), h.runWPCLI, ""
	case ToolReplibyte:
		dir := filepath.Join(h.Runtime, "tools", name)
		version, notRun = fetched(dir)
		if notRun != "" {
			return version, nil, notRun
		}
		if _, err := docker(ctx, nil, "image", "inspect", "-f", "{{.Id}}", runnerImage); err != nil {
			return version, nil, "the runner image " + runnerImage + " is missing (make -C testbed up builds it)"
		}
		bin, err := unpackBinary(filepath.Join(dir, "release"), filepath.Join(dir, "bin"), "replibyte")
		if err != nil {
			return version, nil, "unpacking the release: " + err.Error()
		}
		return version, func(ctx context.Context, st *scenarioState) ToolInput { return h.runReplibyte(ctx, st, bin) }, ""
	case ToolStagingShield:
		dir := filepath.Join(h.Runtime, "tools", name)
		version, notRun = fetched(dir)
		if notRun != "" {
			return version, nil, notRun
		}
		plugins := filepath.Join(dir, "plugin")
		slug, err := unzipPlugin(filepath.Join(dir, "release"), plugins)
		if err != nil {
			return version, nil, "unpacking the plugin: " + err.Error()
		}
		return version, func(ctx context.Context, st *scenarioState) ToolInput {
			return h.runStagingPlugin(ctx, st, filepath.Join(plugins, slug), slug)
		}, ""
	}
	return "", nil, "no runner for " + name
}

// fetched reads what make tools left in dir: the version plus the reason
// the tool can't run when it wasn't fetched.
func fetched(dir string) (version, notRun string) {
	if b, err := os.ReadFile(filepath.Join(dir, "version")); err == nil { // #nosec G304 -- the testbed's own runtime file
		version = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "not-run")); err == nil { // #nosec G304 -- the testbed's own runtime file
		return version, strings.TrimSpace(string(b))
	}
	if _, err := os.Stat(filepath.Join(dir, "release")); err != nil {
		return version, filepath.Base(dir) + " isn't fetched: make -C testbed tools"
	}
	return version, ""
}

// runnerImage is the testbed's throwaway image (no network) for programs
// the harness runs from a release it fetched.
const runnerImage = "roadie-testbed-runner:1"

// toolState reseeds the input for a tool, in a work folder of its own
// (work/tools/<tool>/<input>), so the scenarios' outputs stay as they were.
func (h *Harness) toolState(ctx context.Context, tool, input string) (*scenarioState, error) {
	s, ok := h.manifest[input]
	if !ok {
		return nil, fmt.Errorf("no scenario %s in the manifest", input)
	}
	s.ID = filepath.Join("tools", tool, input)
	s.Setup = nil
	return h.reset(ctx, s)
}

// SearchReplace is one pair of tools/wp-cli/search-replace.txt.
type SearchReplace struct{ From, To string }

// ReadSearchReplace reads the WP-CLI pairs: "from<TAB>to" per line, with
// # comments and blank lines.
func ReadSearchReplace(path string) ([]SearchReplace, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own committed file
	if err != nil {
		return nil, err
	}
	var out []SearchReplace
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		from, to, ok := strings.Cut(line, "\t")
		if !ok || from == "" || to == "" || strings.Contains(to, "\t") {
			return nil, fmt.Errorf("%s:%d: want from<TAB>to", path, i+1)
		}
		out = append(out, SearchReplace{from, to})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no pairs", path)
	}
	return out, nil
}

// toolsDB is the scratch database on prod-db the URL tools work in.
const toolsDB = "tools"

// runWPCLI loads the input into a scratch database, runs every pair of
// search-replace.txt through WP-CLI (in the sshd container, which has the
// pinned WP-CLI and reaches prod-db), dumps the result with mysqldump and
// counts the production URLs left. Personal values and side effects are
// outside what search-replace does: can't see.
func (h *Harness) runWPCLI(ctx context.Context, st *scenarioState) (ti ToolInput) {
	pairs, err := ReadSearchReplace(filepath.Join(h.ToolsDir, "wp-cli", "search-replace.txt"))
	if err != nil {
		ti.fail("%s", err)
		return ti
	}
	in := filepath.Join(st.in, "db.sql")
	if _, err := h.sqlIn(ctx, ctrProdDB, strings.NewReader("DROP DATABASE IF EXISTS "+toolsDB+"; CREATE DATABASE "+toolsDB+"; GRANT ALL ON "+toolsDB+".* TO shop;"), true); err != nil {
		ti.fail("creating the scratch database: %s", errDetail(err))
		return ti
	}
	defer func() {
		_, _ = h.sqlIn(context.WithoutCancel(ctx), ctrProdDB, strings.NewReader("DROP DATABASE IF EXISTS "+toolsDB+";"), true)
	}()
	if err := h.loadInto(ctx, ctrProdDB, toolsDB, in); err != nil {
		ti.fail("%s", err)
		return ti
	}
	replaced := 0
	for _, p := range pairs {
		out, err := h.Ex.Exec(ctx, ctrSSHD, "roadie", []string{"WORDPRESS_DB_NAME=" + toolsDB}, nil,
			"wp", "--path="+docroot, "search-replace", p.From, p.To, "--all-tables", "--precise", "--skip-plugins", "--skip-themes", "--format=count")
		if err != nil {
			ti.fail("wp search-replace: %s", errDetail(err))
			return ti
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(lastLine(out))))
		replaced += n
	}
	dump, err := h.Ex.Exec(ctx, ctrProdDB, "", nil, nil, "sh", "-c",
		`MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump -ushop --single-transaction --no-tablespaces --skip-dump-date `+toolsDB)
	if err != nil {
		ti.fail("dumping the result: %s", errDetail(err))
		return ti
	}
	outPath := filepath.Join(st.work, "out.sql")
	if err := os.WriteFile(outPath, dump, 0o600); err != nil {
		ti.fail("%s", err)
		return ti
	}
	before, err := os.ReadFile(in) // #nosec G304 -- a seeded fixture
	if err != nil {
		ti.fail("%s", err)
		return ti
	}
	inProd, inOther := CountHost(before, productionHost)
	outProd, outOther := CountHost(dump, productionHost)
	ti.URLs = &Count{Left: outProd, Of: inProd}
	ti.Collateral = &Count{Left: max(0, inOther-outOther), Of: inOther}
	ti.Detail = fmt.Sprintf("%d replacements in %d passes; %d of %d production URLs left; %d of %d lookalike links and addresses changed",
		replaced, len(pairs), outProd, inProd, ti.Collateral.Left, inOther)
	return ti
}

// productionHost is the testbed production's host name.
var productionHost = strings.TrimPrefix(productionURL, "https://")

// CountHost counts host in b, case-insensitively, as the production URL
// it is (prod: after a scheme, //, \/\/, an encoded slash or www.) or as
// something that must stay (other: a lookalike such as host.au or
// myhost, another subdomain such as a mail provider's mg.host, or an
// email address at the domain).
func CountHost(b []byte, host string) (prod, other int) {
	lb, h := bytes.ToLower(b), []byte(strings.ToLower(host))
	for i := 0; ; {
		j := bytes.Index(lb[i:], h)
		if j < 0 {
			return prod, other
		}
		at, end := i+j, i+j+len(h)
		i = end
		if isHostByte(at, lb, true) || isHostByte(end, lb, false) {
			other++
			continue
		}
		prod++
	}
}

// isHostByte reports whether the host match at lb[at:] (before) or ending
// at lb[at] (after) continues into a longer name or an email address, so
// it isn't the production host itself.
func isHostByte(at int, lb []byte, before bool) bool {
	if before {
		if at == 0 {
			return false
		}
		c := lb[at-1]
		if c == '@' || at >= 3 && string(lb[at-3:at]) == "%40" {
			return true // an address at the domain
		}
		if at >= 3 && string(lb[at-3:at]) == "%2f" {
			return false // an encoded slash
		}
		if c == '.' {
			// www. is the production site; another subdomain (a mail
			// provider's mg.) is a name of its own.
			return at < 4 || string(lb[at-4:at-1]) != "www" || at >= 5 && (isAlnum(lb[at-5]) || lb[at-5] == '.')
		}
		return isAlnum(c) || c == '-' || c == '_'
	}
	if at >= len(lb) {
		return false
	}
	c := lb[at]
	if c == '.' && at+1 < len(lb) && isAlnum(lb[at+1]) {
		return true // host.au
	}
	return isAlnum(c) || c == '-' || c == '_'
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z'
}

// dockerRun runs a docker command; tests swap it.
var dockerRun = func(ctx context.Context, args ...string) ([]byte, error) { return docker(ctx, nil, args...) }

// runReplibyte runs the fetched Replibyte with tools/replibyte/conf.yaml
// in the runner image with no network: the input dump on stdin into a
// local datastore, then restored to a dump the oracle judges for
// canaries, live keys and serialized integrity.
func (h *Harness) runReplibyte(ctx context.Context, st *scenarioState, bin string) (ti ToolInput) {
	conf, err := os.ReadFile(filepath.Join(h.ToolsDir, "replibyte", "conf.yaml"))
	if err != nil {
		ti.fail("%s", err)
		return ti
	}
	conf = bytes.ReplaceAll(conf, []byte("@@DATASTORE@@"), []byte("/work/datastore"))
	if err := os.WriteFile(filepath.Join(st.work, "conf.yaml"), conf, 0o600); err != nil { // #nosec G703 -- the harness's own work folder
		ti.fail("%s", err)
		return ti
	}
	if err := os.MkdirAll(filepath.Join(st.work, "datastore"), 0o700); err != nil {
		ti.fail("%s", err)
		return ti
	}
	// Replibyte 0.10.0 panics on an INSERT that doesn't name its columns
	// ("Column names do not match values") and keeps only the first row
	// of one that holds several: mysqldump's default style does both.
	// It gets the same rows one INSERT each with their columns named
	// (mysqldump --complete-insert --skip-extended-insert) instead, so it
	// is judged on its masking, not on the dump's style.
	if err := rowInserts(filepath.Join(st.in, "db.sql"), filepath.Join(st.work, "in.sql")); err != nil {
		ti.fail("%s", err)
		return ti
	}
	// -n: no telemetry. There is no network either; the flag says so in
	// the command anyone copies from here.
	script := "replibyte -n -c /work/conf.yaml dump create -s mysql -i < /work/in.sql && replibyte -n -c /work/conf.yaml dump restore local -v latest -o > /work/out.sql"
	if _, err := dockerRun(ctx, "run", "--rm", "--network", "none", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-v", bin+":/usr/local/bin/replibyte:ro", "-v", st.work+":/work", "-w", "/work",
		runnerImage, "sh", "-c", script); err != nil {
		ti.fail("replibyte: %s", firstLine(err.Error()))
		return ti
	}
	h.judgeDump(ctx, st, filepath.Join(st.work, "out.sql"), &ti)
	return ti
}

// rowInserts copies the mysqldump at src to dst with each
// "INSERT INTO `t` VALUES (...),(...);" written as one INSERT per row
// that names the table's columns, read from its CREATE TABLE: what
// mysqldump --complete-insert --skip-extended-insert writes. Other lines
// are copied as they are, as is an INSERT for a table with no CREATE
// TABLE before it or one whose rows can't be split.
func rowInserts(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- the seeded input
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- the harness's own work folder
	if err != nil {
		return err
	}
	r, w := bufio.NewReaderSize(in, 1<<20), bufio.NewWriterSize(out, 1<<20)
	cols := map[string]string{}
	var table string
	var names []string
	for {
		line, rerr := r.ReadString('\n')
		switch {
		case strings.HasPrefix(line, "CREATE TABLE `"):
			table, names = backquoted(line[len("CREATE TABLE "):]), nil
		case table != "" && strings.HasPrefix(line, ")"):
			cols[table] = "(`" + strings.Join(names, "`, `") + "`)"
			table = ""
		case table != "" && strings.HasPrefix(strings.TrimSpace(line), "`"):
			names = append(names, backquoted(strings.TrimSpace(line)))
		case strings.HasPrefix(line, "INSERT INTO `"):
			t := backquoted(line[len("INSERT INTO "):])
			prefix := "INSERT INTO `" + t + "` VALUES "
			if c, ok := cols[t]; ok && strings.HasPrefix(line, prefix) {
				if rows, ok := splitRows(strings.TrimRight(line[len(prefix):], "\r\n")); ok {
					var b strings.Builder
					for _, row := range rows {
						b.WriteString("INSERT INTO `" + t + "` " + c + " VALUES " + row + ";\n")
					}
					line = b.String()
				}
			}
		}
		if _, err := w.WriteString(line); err != nil {
			_ = out.Close()
			return err
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = out.Close()
			return rerr
		}
	}
	if err := w.Flush(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// splitRows splits "(1,'a'),(2,'b');" into "(1,'a')" and "(2,'b')",
// minding quotes and backslash escapes. It reports false for anything
// else, so the caller keeps the line as it was.
func splitRows(v string) ([]string, bool) {
	v = strings.TrimSuffix(v, ";")
	var rows []string
	depth, start := 0, -1
	var quote byte
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			if depth == 0 {
				start = i
			}
			depth++
		case c == ')':
			depth--
			if depth < 0 {
				return nil, false
			}
			if depth == 0 {
				rows = append(rows, v[start:i+1])
			}
		case depth == 0 && c != ',':
			return nil, false
		}
	}
	return rows, depth == 0 && quote == 0 && len(rows) > 0
}

// backquoted returns the name between the first pair of backquotes in s.
func backquoted(s string) string {
	_, rest, ok := strings.Cut(s, "`")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "`")
	return name
}

// judgeDump runs the oracle on a tool's output dump: canaries, live keys
// and, when the seeder listed the input's serialized cells, integrity.
func (h *Harness) judgeDump(ctx context.Context, st *scenarioState, out string, ti *ToolInput) {
	in := filepath.Join(st.in, "db.sql")
	o := oracle.Options{Canaries: st.canaries, Dump: out, Input: in}
	cells, err := oracle.LoadSerialized(filepath.Join(st.in, "serialized.json"))
	if err == nil && len(h.PHP.Cmd) > 0 {
		o.Serialized, o.PHP = cells, h.PHP
		if o.Serialized == nil {
			o.Serialized = []oracle.Location{}
		}
	}
	rep, err := oracle.Run(ctx, o)
	if err != nil {
		ti.fail("oracle: %s", err)
		return
	}
	people, live := canaryTotals(st.canaries)
	ids, _ := survivorIDs(rep.Survivors)
	ti.Canaries = &Count{Left: len(ids), Of: people}
	ti.LiveKeys = &Count{Left: rep.LiveKeys, Of: live}
	if in := rep.Integrity; in != nil {
		ti.Integrity = &Count{Left: in.Failures + in.Missing, Of: in.Checked}
	}
	var enc []string
	for e := range rep.ByEncoding {
		enc = append(enc, e)
	}
	slices.Sort(enc)
	ti.Detail = fmt.Sprintf("%d of %d personal values and %d of %d live keys left", len(ids), people, rep.LiveKeys, live)
	if len(enc) > 0 {
		ti.Detail += " (encodings: " + strings.Join(enc, ", ") + ")"
	}
}

// canaryTotals counts the personal canaries and the live keys.
func canaryTotals(cs []oracle.Canary) (people, live int) {
	for _, c := range cs {
		switch {
		case c.Kind != "key":
			people++
		case c.Live:
			live++
		}
	}
	return people, live
}

// runStagingPlugin makes staging a plain copy of the input (the raw data,
// as a copy without Roadie would be), installs and activates the plugin
// and runs the side-effect drill with traffic: gate 3 is its score.
func (h *Harness) runStagingPlugin(ctx context.Context, st *scenarioState, plugin, slug string) (ti ToolInput) {
	if err := h.resetStaging(ctx, st); err != nil {
		ti.fail("resetting staging: %s", err)
		return ti
	}
	if err := h.loadInto(ctx, ctrStagingDB, "shop", filepath.Join(st.in, "db.sql")); err != nil {
		ti.fail("%s", err)
		return ti
	}
	if err := h.overlay(ctx, ctrStagingWP, docroot+"/wp-content/plugins/"+slug, plugin, nil); err != nil {
		ti.fail("%s", err)
		return ti
	}
	if _, err := wpCLI(ctx, h.Ex, "plugin", "activate", slug); err != nil {
		ti.fail("wp plugin activate %s: %s", slug, errDetail(err))
		return ti
	}
	w := &Window{Logs: h.logs(), Echo: Echo{Base: gateEcho, Token: h.echoTok}, Real: st.real}
	paths, cookies := stagingPages(ctx, h.Ex)
	t := Traffic{Base: gateStaging, Host: stagingHost, Paths: paths, Admin: []string{"/wp-admin/"}, Cookies: cookies, Every: 500 * time.Millisecond}
	if err := w.Start(ctx, t, 0); err != nil {
		ti.fail("%s", err)
		return ti
	}
	Drill{Exec: h.Ex, For: h.DrillFor, Pause: 5 * time.Second}.Run(ctx)
	w.Stop()
	g, err := w.Judge(ctx)
	if err != nil {
		ti.fail("%s", err)
		return ti
	}
	ti.SideEffects = &Count{Left: g.Sinkhole + g.StubLive + g.EchoRealRecips}
	ti.Detail = fmt.Sprintf("%d connections, %d stub calls with a live key, %d captures to real recipients", g.Sinkhole, g.StubLive, g.EchoRealRecips)
	return ti
}

// unpackBinary extracts the file called name from a release (.tar.gz or a
// bare binary) into dir and returns its path, executable.
func unpackBinary(release, dir, name string) (string, error) {
	f, err := os.Open(release) // #nosec G304 -- a release make tools fetched and checked
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%s isn't a .tar.gz: %w", filepath.Base(release), err)
	}
	if _, err := untar(zr, dir); err != nil {
		return "", err
	}
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name && found == "" {
			found = p
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("no %s in %s", name, filepath.Base(release))
	}
	return found, os.Chmod(found, 0o700) // #nosec G302 -- a program the runner image executes
}

// unzipPlugin extracts a wordpress.org plugin zip into dir and returns
// its folder (the slug). Entries outside dir are refused.
func unzipPlugin(release, dir string) (string, error) {
	zr, err := zip.OpenReader(release)
	if err != nil {
		return "", err
	}
	defer func() { _ = zr.Close() }()
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	slug := ""
	for _, f := range zr.File {
		name := filepath.Clean(filepath.FromSlash(f.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s: entry %q is outside the plugin", filepath.Base(release), f.Name)
		}
		top, _, _ := strings.Cut(filepath.ToSlash(name), "/")
		if slug == "" {
			slug = top
		} else if top != slug {
			return "", errors.New("the zip holds more than one plugin folder")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if err := extractZipFile(f, filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	if slug == "" {
		return "", errors.New("the zip is empty")
	}
	return slug, nil
}

func extractZipFile(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- dst is inside dir (checked by the caller)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, io.LimitReader(rc, 256<<20))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

// toolLines flattens the tools' scores for -diff: each tool's not-run
// reason and each input's outcome and judged counts. Side effects are
// left out: the traffic generator makes their counts vary.
func toolLines(ts []ToolResult, out map[string]string) {
	for _, t := range ts {
		k := "tool " + t.Name
		if t.NotRun != "" {
			out[k] = NotRun + ": " + t.NotRun
			continue
		}
		for _, in := range t.Inputs {
			v := in.Outcome
			for _, c := range []struct {
				name string
				c    *Count
			}{{"urls", in.URLs}, {"collateral", in.Collateral}, {"canaries", in.Canaries}, {"live-keys", in.LiveKeys}, {"integrity", in.Integrity}} {
				if c.c != nil {
					v += fmt.Sprintf(" %s %d/%d", c.name, c.c.Left, c.c.Of)
				}
			}
			out[k+"/"+in.Input] = v
		}
	}
}
