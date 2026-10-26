// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// checkRun checks what the run itself reported: exit code, error code,
// expected and forbidden IDs, finding locations and target state.
func (h *Harness) checkRun(cr *caseRun, run *Run, env *Envelope, perr error) {
	e := cr.c.Expect
	cr.res.Exit = run.Exit
	cr.res.ErrorCode = env.ErrorCode()
	cr.res.IDs = env.IDs()
	if perr != nil && (len(e.FindingIDs) > 0 || e.ErrorCode != "" || e.TargetState != "" || len(cr.c.Forbid) > 0) {
		cr.res.add("envelope", false, "%s", perr)
	}
	if e.ExitCode != nil {
		cr.res.add("exit", run.Exit == *e.ExitCode, "want %d, got %s", *e.ExitCode, describe(run, env))
	}
	if e.ErrorCode != "" {
		cr.res.add("error-code", env.ErrorCode() == e.ErrorCode, "want %s, got %s", e.ErrorCode, orDash(env.ErrorCode()))
	}
	if len(e.FindingIDs) > 0 {
		var missing []string
		for _, id := range e.FindingIDs {
			if !slices.Contains(cr.res.IDs, id) {
				missing = append(missing, id)
			}
		}
		cr.res.add("finding-ids", len(missing) == 0, "missing %s", orDash(strings.Join(missing, ", ")))
	}
	if len(cr.c.Forbid) > 0 {
		var found []string
		for _, id := range cr.c.Forbid {
			if slices.Contains(cr.res.IDs, id) {
				found = append(found, id)
			}
		}
		cr.res.add("forbid", len(found) == 0, "found %s", orDash(strings.Join(found, ", ")))
	}
	if len(e.Locations) > 0 {
		locs := env.Locations()
		var missing []string
		for _, l := range e.Locations {
			if !slices.Contains(locs, l) {
				missing = append(missing, l)
			}
		}
		cr.res.add("locations", len(missing) == 0, "missing %s", orDash(strings.Join(missing, ", ")))
	}
	if e.TargetState != "" {
		got := ""
		if env != nil {
			got = env.Data.TargetState
		}
		cr.res.add("target-state", got == e.TargetState, "want %s, got %s", e.TargetState, orDash(got))
	}
}

// checkOutputs runs the oracle (gates 1 and 2), the joins and the
// imports on the case's output.
func (h *Harness) checkOutputs(ctx context.Context, cr *caseRun) {
	e, out := cr.c.Expect, cr.c.Output
	if out.DB == "" && out.Tree == "" {
		return
	}
	dump, tree, err := h.outputs(ctx, cr)
	if err != nil {
		cr.res.add("output", false, "%s", err)
		return
	}
	if dump != "" {
		cr.res.OutputHash = hashFile(dump)
		if cr.st.s.Kind == manifest.KindCopy && e.VerifyExit != nil && *e.VerifyExit == 0 && cr.res.Exit == 0 {
			h.kept = append(h.kept, keptOutput{ID: caseID(cr), Path: dump})
		}
	}
	input := inputDump(cr)
	if e.Canaries != nil || e.Integrity || e.RowsOnly {
		o := oracle.Options{Canaries: cr.st.canaries, Dump: dump, Tree: tree}
		if e.Integrity || e.RowsOnly {
			o.Input = input
		}
		if e.Integrity {
			cells, err := oracle.LoadSerialized(serializedFor(cr))
			if err != nil {
				cr.res.add("integrity", false, "no serialized.json for the input: %s", err)
			} else {
				o.Serialized, o.PHP = cells, h.PHP
				if o.Serialized == nil {
					o.Serialized = []oracle.Location{}
				}
			}
		}
		rep, err := oracle.Run(ctx, o)
		if err != nil {
			cr.res.add("oracle", false, "%s", err)
			return
		}
		h.oracleGates(cr, rep, input)
	}
	for _, j := range cr.st.s.Joins {
		if dump == "" {
			break
		}
		a, err1 := joinCount(input, j)
		b, err2 := joinCount(dump, j)
		if err1 != nil || err2 != nil {
			cr.res.add("join "+j.Right, false, "reading the dumps: %v %v", err1, err2)
			continue
		}
		cr.res.add("join "+j.Right, a == b, "%s matches %s in %d rows before, %d after", j.Right, j.Left, a, b)
	}
	if dump != "" && cr.res.Exit == 0 {
		for _, db := range cr.st.s.ImportInto {
			err := h.importInto(ctx, db, dump)
			cr.res.add("import "+db, err == nil, "%v", errOrEmpty(err))
		}
	}
}

func errOrEmpty(err error) any {
	if err == nil {
		return "ok"
	}
	return err
}

// caseID is "r18/latin1" or "r01".
func caseID(cr *caseRun) string {
	if cr.c.Name != "" {
		return cr.st.s.ID + "/" + cr.c.Name
	}
	return cr.st.s.ID
}

// outputs returns the local dump and tree the oracle reads: a path the
// run wrote (collected back from a container) or a database dumped and
// a site copied out with the real tools.
func (h *Harness) outputs(ctx context.Context, cr *caseRun) (dump, tree string, err error) {
	out := cr.c.Output
	switch out.DB {
	case "":
	case manifest.OutStagingDB, manifest.OutLaravelDB, manifest.OutPG:
		dump = filepath.Join(cr.work, "out-db.sql")
		if err := h.dumpDB(ctx, out.DB, dump); err != nil {
			return "", "", err
		}
	default:
		if dump, err = manifest.Expand(out.DB, cr.local); err != nil {
			return "", "", err
		}
		if _, err := os.Stat(dump); err != nil {
			return "", "", fmt.Errorf("the output %s: %w", filepath.Base(dump), err)
		}
	}
	switch out.Tree {
	case "":
	case manifest.OutStagingWP, manifest.OutLaravelApp:
		tree = filepath.Join(cr.work, "out-tree")
		links, err := h.collectTree(ctx, out.Tree, tree)
		if err != nil {
			return "", "", err
		}
		if len(links) > 0 {
			cr.run.Notes = append(cr.run.Notes, fmt.Sprintf("output tree: %d link(s) not copied or read: %s", len(links), strings.Join(links, ", ")))
		}
	default:
		if tree, err = manifest.Expand(out.Tree, cr.local); err != nil {
			return "", "", err
		}
	}
	return dump, tree, nil
}

// collectTree copies a site out of its container: the staging docroot
// without WordPress core and the plugins (third-party code the copy
// never touches), or the Laravel site. It returns the links in the site,
// which aren't copied.
func (h *Harness) collectTree(ctx context.Context, service, dst string) ([]string, error) {
	var ctr string
	var args []string
	switch service {
	case manifest.OutStagingWP:
		ctr = ctrStagingWP
		args = []string{"tar", "-C", docroot, "--exclude=./wp-includes", "--exclude=./wp-admin", "--exclude=./wp-content/plugins", "-cf", "-", "."}
	case manifest.OutLaravelApp:
		ctr = ctrLaravel
		args = []string{"tar", "-C", ctrRoot + "/site", "-cf", "-", "."}
	default:
		return nil, fmt.Errorf("no site in %s", service)
	}
	b, err := h.Ex.Exec(ctx, ctr, "root", nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("copying the site out of %s: %w", ctr, errDetail(err))
	}
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	return untar(bytes.NewReader(b), dst)
}

// inputDump is the input the case read: the file after --from when it is
// a seeded one, else the fixture's db.sql (or the generated db.sql.gz).
func inputDump(cr *caseRun) string {
	for i, a := range cr.c.Command {
		if a == "--from" && i+1 < len(cr.c.Command) {
			if rest, ok := strings.CutPrefix(cr.c.Command[i+1], "${in}/"); ok {
				return filepath.Join(cr.st.in, rest)
			}
		}
	}
	if p := filepath.Join(cr.st.in, "db.sql"); fileExists(p) {
		return p
	}
	return filepath.Join(cr.st.in, "db.sql.gz")
}

// serializedFor is the serialized.json of the case's input: the seeder's
// for db.sql (r18's other formats hold the same rows), the generator's
// next to a generated dump.
func serializedFor(cr *caseRun) string {
	return filepath.Join(cr.st.in, "serialized.json")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// oracleGates turns the oracle's report into gates 1 and 2. Only the
// input's own tables count for rows: a site target keeps the tables of
// its fresh install that the dump doesn't hold. A link in an output tree
// fails gate 1: the oracle never reads what it points to.
func (h *Harness) oracleGates(cr *caseRun, rep oracle.Report, input string) {
	e := cr.c.Expect
	cr.res.Oracle = &rep
	people, live := 0, 0
	for _, c := range cr.st.canaries {
		switch {
		case c.Kind != "key":
			people++
		case c.Live:
			live++
		}
	}
	cr.res.Canaries, cr.res.LiveKeys = people, live
	if len(rep.Links) > 0 {
		// Whatever a link points to (a production backup, say) went
		// unread, so the output can't pass gate 1 with one in it.
		cr.res.add("links", false, "%d link(s) in the output tree not followed: %s", len(rep.Links), strings.Join(rep.Links, ", "))
	}
	if e.Canaries != nil {
		ids, hits := survivorIDs(rep.Survivors)
		var shown []string
		for _, s := range rep.Survivors {
			if v := s.ID + " (" + s.Encoding + ")"; s.Kind != "key" && !slices.Contains(shown, v) && len(shown) < 5 {
				shown = append(shown, v)
			}
		}
		cr.res.add("canaries", len(ids) == 0, "%d of %d survived (%d hits) %s", len(ids), people, hits, strings.Join(shown, ", "))
		cr.res.add("live-keys", rep.LiveKeys == 0, "%d of %d survived", rep.LiveKeys, live)
		cr.res.add("test-keys", len(rep.TestKeysMissing) == 0, "changed %s", orDash(strings.Join(rep.TestKeysMissing, ", ")))
	}
	if e.Integrity && rep.Integrity != nil {
		in := rep.Integrity
		cr.res.add("integrity", in.Failures == 0 && in.Missing == 0, "%d checked, %d fail to unserialize, %d missing", in.Checked, in.Failures, in.Missing)
	}
	if e.Integrity || e.RowsOnly {
		tables, err := dumpTables(input)
		if err != nil {
			cr.res.add("rows", false, "reading the input: %s", err)
			return
		}
		var differ []string
		for _, r := range rep.Rows {
			if slices.Contains(tables, r.Table) && r.Input != r.Output {
				differ = append(differ, fmt.Sprintf("%s %d to %d", r.Table, r.Input, r.Output))
			}
		}
		cr.res.add("rows", len(differ) == 0, "%s", orDash(strings.Join(differ, ", ")))
	}
}

// dumpTables lists the tables a dump creates or fills.
func dumpTables(path string) ([]string, error) {
	rc, err := oracle.OpenDump(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	st, err := oracle.ReadDump(rc, nil)
	return st.Tables, err
}

// joinCount counts the rows of j.Right whose value is one of j.Left's
// values in a dump.
func joinCount(path string, j manifest.Join) (int, error) {
	lt, lc, _ := strings.Cut(j.Left, ".")
	rt, rc, _ := strings.Cut(j.Right, ".")
	f, err := oracle.OpenDump(path)
	if err != nil {
		return 0, err
	}
	left := map[string]bool{}
	var right []string
	_, err = oracle.ReadDump(f, func(r oracle.Row) error {
		if v, ok := r.Get(lc); ok && r.Table == lt && !v.Null {
			left[v.S] = true
		}
		if v, ok := r.Get(rc); ok && r.Table == rt && !v.Null {
			right = append(right, v.S)
		}
		return nil
	})
	_ = f.Close()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range right {
		if left[v] {
			n++
		}
	}
	return n, nil
}

// importInto loads an output dump into a scratch database on MySQL 8.4
// (prod-db) or MariaDB 11.4 (staging-db) and drops it again.
func (h *Harness) importInto(ctx context.Context, db, dump string) error {
	ctr := ctrProdDB
	if db == "mariadb" {
		ctr = ctrStagingDB
	}
	create := "DROP DATABASE IF EXISTS roadie_import; CREATE DATABASE roadie_import; GRANT ALL ON roadie_import.* TO shop;"
	if _, err := h.sqlIn(ctx, ctr, strings.NewReader(create), true); err != nil {
		return errDetail(err)
	}
	defer func() {
		_, _ = h.sqlIn(context.Background(), ctr, strings.NewReader("DROP DATABASE IF EXISTS roadie_import;"), true)
	}()
	return h.loadInto(ctx, ctr, "roadie_import", dump)
}

// checkStaging runs the boot check and the WP-CLI checks on staging.
func (h *Harness) checkStaging(ctx context.Context, cr *caseRun) {
	if cr.st.s.Boot && cr.c.Expect.Canaries != nil && cr.res.Exit == 0 {
		h.boot(ctx, cr)
	}
	for _, w := range cr.c.WPChecks {
		args := w.WP
		if w.PHP != "" {
			args = []string{"eval", w.PHP}
		}
		_, err := wpCLI(ctx, h.Ex, args...)
		code := 0
		if err != nil {
			code = -1
			if ee, ok := err.(*ExitError); ok { //nolint:errorlint // Exec returns it unwrapped
				code = ee.Code
			}
		}
		cr.res.add("wp "+w.Name, err == nil, "exit %d", code)
	}
}

// boot requests home, shop, cart and checkout (and every multisite
// blog's home) on staging through the gate: each must answer 200.
func (h *Harness) boot(ctx context.Context, cr *caseRun) {
	type page struct{ host, path string }
	var pages []page
	paths, _ := stagingPages(ctx, h.Ex)
	paths = bootPaths(paths)
	for _, p := range paths {
		pages = append(pages, page{stagingHost, p})
	}
	if cr.st.s.Multisite != "" {
		out, err := wpCLI(ctx, h.Ex, "site", "list", "--field=url")
		if err == nil {
			for _, u := range strings.Fields(string(out)) {
				host, path := splitURL(u)
				if !slices.Contains(pages, page{host, path}) {
					pages = append(pages, page{host, path})
				}
			}
		}
	}
	c := localClient(30 * time.Second)
	var bad []string
	for _, p := range pages {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateStaging+p.path, nil)
		if err != nil {
			bad = append(bad, p.path+" "+err.Error())
			continue
		}
		req.Host = p.host
		resp, err := c.Do(req)
		if err != nil {
			bad = append(bad, p.host+p.path+" no answer")
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			bad = append(bad, fmt.Sprintf("%s%s %d", p.host, p.path, resp.StatusCode))
		}
	}
	cr.res.add("boot", len(bad) == 0 && len(pages) > 0, "%d pages; %s", len(pages), orDash(strings.Join(bad, ", ")))
	sc := Soundcheck{Bin: h.soundcheck, Base: gateStaging}
	cr.run.Notes = append(cr.run.Notes, sc.Health(ctx, filepath.Join(cr.work, "soundcheck"), paths))
}

// splitURL splits "http://fr.staging-wp/blog/" into host and path.
func splitURL(u string) (host, path string) {
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host, path, _ = strings.Cut(rest, "/")
	return host, "/" + path
}

// checkVerify is gate 4: a fresh verify, then report check on the report
// it signed.
func (h *Harness) checkVerify(ctx context.Context, cr *caseRun) {
	e := cr.c.Expect
	if e.VerifyExit == nil || len(cr.c.Verify) == 0 {
		return
	}
	args, err := manifest.ExpandAll(append([]string{"verify"}, cr.c.Verify...), cr.vars)
	if err != nil {
		cr.res.add("verify", false, "%s", err)
		return
	}
	before := reports(cr.work)
	run, err := h.roadie(ctx, cr, args, nil)
	if err != nil {
		cr.res.add("verify", false, "%s", err)
		return
	}
	env, _ := ParseEnvelope(run.Stdout)
	cr.res.add("verify", run.Exit == *e.VerifyExit, "want exit %d, got %s; at or above medium: %s",
		*e.VerifyExit, describe(run, env), orDash(strings.Join(env.FindingsAtOrAbove(core.SeverityMedium), ", ")))
	_ = cr.env.Collect(ctx)
	h.reportCheck(ctx, cr, before)
}

// reportCheck runs report check on the newest report a run signed.
func (h *Harness) reportCheck(ctx context.Context, cr *caseRun, before []string) {
	var fresh []string
	for _, r := range reports(cr.work) {
		if !slices.Contains(before, r) {
			fresh = append(fresh, r)
		}
	}
	if len(fresh) == 0 {
		cr.res.add("report-check", false, "the run saved no report.json")
		return
	}
	path := fresh[len(fresh)-1]
	if _, ok := cr.env.(*containerEnv); ok {
		rel, _ := filepath.Rel(cr.work, path)
		path = ctrWork + "/" + filepath.ToSlash(rel)
	}
	run, err := h.roadie(ctx, cr, []string{"report", "check", path}, nil)
	if err != nil {
		cr.res.add("report-check", false, "%s", err)
		return
	}
	cr.res.add("report-check", run.Exit == 0, "exit %d", run.Exit)
}

// reports lists the saved report.json files under a work folder, sorted
// (their folders are timestamps).
func reports(work string) []string {
	m, _ := filepath.Glob(filepath.Join(work, ".roadie", "reports", "*", "report.json"))
	slices.Sort(m)
	return m
}

// checkGate3 runs the drill (when the scenario has one) and judges the
// window: gate 3.
func (h *Harness) checkGate3(ctx context.Context, cr *caseRun) {
	w := cr.st.window
	if cr.st.s.Drill {
		paths, cookies := stagingPages(ctx, h.Ex)
		t := Traffic{Base: gateStaging, Host: stagingHost, Paths: paths, Admin: []string{"/wp-admin/"}, Cookies: cookies, Every: 500 * time.Millisecond}
		tctx, cancel := context.WithCancel(ctx)
		done := make(chan TrafficStats, 1)
		go func() { done <- t.Run(tctx) }()
		dr := Drill{Exec: h.Ex, For: h.DrillFor, Pause: 5 * time.Second}.Run(ctx)
		cancel()
		ts := <-done
		cr.run.Drill = &dr
		if cr.run.Traffic == nil {
			cr.run.Traffic = &ts
		}
	}
	if cr.c.Action.Name == manifest.ActionEgress {
		out, err := wpCLI(ctx, h.Ex, "testbed-egress")
		if err != nil {
			cr.res.add("egress-plugin", false, "wp testbed-egress: %s", errDetail(err))
		} else {
			cr.run.Notes = append(cr.run.Notes, "egress (PHP's view): "+string(lastLine(out)))
		}
	}
	g, err := w.Judge(ctx)
	if err != nil {
		cr.res.add("sinkhole", false, "%s", err)
		return
	}
	cr.res.Gate3, cr.run.Gate3 = &g, &g
	cr.run.Notes = append(cr.run.Notes, g.Notes...)
	detail := fmt.Sprintf("%d connections, %d stub calls with a live key, %d captures to real recipients",
		g.Sinkhole, g.StubLive, g.EchoRealRecips)
	if g.EchoUnread {
		detail += "; Echo's captures couldn't be read"
	}
	cr.res.add("sinkhole", g.Pass(), "%s", detail)
	if cr.c.Action.Name == manifest.ActionEgress {
		cr.res.add("egress", len(g.Egress) == 0, "reached the sinkhole: %s", orDash(strings.Join(g.Egress, ", ")))
	}
}

// checkAbsent is the absent gate: no marker file on this machine, where
// Roadie ran or on the staging database server.
func (h *Harness) checkAbsent(ctx context.Context, cr *caseRun) {
	globs := cr.st.s.Absent
	if len(globs) == 0 {
		return
	}
	found, err := (&hostEnv{}).Absent(ctx, globs)
	if err == nil {
		var more []string
		if more, err = cr.env.Absent(ctx, globs); err == nil {
			found = append(found, more...)
			more, err = absentIn(ctx, h.Ex, ctrStagingDB, globs)
			found = append(found, more...)
		}
	}
	if err != nil {
		cr.res.add("absent", false, "%s", err)
		return
	}
	cr.res.add("absent", len(found) == 0, "found %s", orDash(strings.Join(found, ", ")))
}

// survivorIDs returns the distinct personal (non-key) canaries that
// survived and how often they were found.
func survivorIDs(ss []oracle.Survivor) (ids []string, hits int) {
	for _, s := range ss {
		if s.Kind == "key" {
			continue
		}
		hits += s.Count
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	return ids, hits
}
