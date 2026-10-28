// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/site"
)

// Hooks into other lanes' packages, so this package's tests can run the
// orchestration with stand-ins while those return core.ErrNotBuilt.
var (
	leavesOf = nested.Leaves
	formatOf = nested.Format
	// newURLFinder returns a function that finds production URLs in text,
	// or nil when no production host can be matched.
	newURLFinder = func(production []string) func([]byte) []core.Span {
		var pairs []urlrw.Pair
		for _, p := range production {
			if p = strings.TrimSpace(p); p != "" && !strings.Contains(p, "*") {
				// To only has to differ from From: Find never writes it.
				pairs = append(pairs, urlrw.Pair{From: p, To: "https://roadie-verify.invalid"})
			}
		}
		if len(pairs) == 0 {
			return nil
		}
		m, err := urlrw.NewMap(pairs)
		if err != nil || m == nil {
			return nil
		}
		return m.Find
	}
)

// samplePerColumn is the most values per column kept for the uncovered
// scan; sampleBytes caps each one.
const (
	samplePerColumn = detect.UncoveredSample
	sampleBytes     = 4 << 10
)

// run is one Run in progress.
type run struct {
	j    Job
	res  *core.VerifyResult
	agg  core.Aggregator
	skip skipper
	site core.Site
	// keySeen dedupes Keys by finding ID and location without the row.
	keySeen map[string]bool
	start   time.Time // when the run began, by Options.Clock
	filter  *filter   // the baseline and ignore entries in force; nil hides nothing
	urls    bool      // the production URL matcher ran
	// checked is what each check looked at; why is why a check didn't run.
	checked map[string]int64
	why     map[string]string
	// note is a check's detail when it passes, such as a narrower scope.
	note map[string]string
	// unknown marks the checks that ran and couldn't tell the state: the
	// list shows them as skipped, as the side effects table does.
	unknown map[string]bool
	// configs are the site's config files, read before the scan.
	configs map[string]configRead
	// unparsed counts serialized-looking values that didn't parse, by
	// column (table, NUL, column).
	unparsed map[string]int64
	// capt keeps the rows the data checks read on a target with no files;
	// nil when no step has a data check or the target has files.
	capt *capture
}

// add records a finding unless the baseline or an ignore entry hides it.
func (r *run) add(f core.Finding) bool {
	return r.sink().add(f)
}

// sink is the run's aggregator behind its filter.
func (r *run) sink() sink {
	return filtered{agg: &r.agg, f: r.filter, hidden: &r.res.Hidden}
}

// Run scans the copy.
//
// It opens the target read-only when Job.Site and Job.Source are nil,
// streams every row (or the first Options.Sample rows of each table)
// through a pool of Options.Jobs workers running the PII detectors, the
// survivors detector in exact mode, the key rules and the production URL
// matcher over every string leaf, then checks the site's config files,
// code files and uploads and runs the Steps' checks. Findings are grouped
// by core.Aggregator. Findings already in Options.Baseline and those an
// Options.Ignore entry in force names are hidden and counted in
// VerifyResult.Hidden; an entry past its date hides nothing and adds
// info.ignore-expired. VerifyResult.Checks lists every check with its
// outcome. A bad baseline or ignore entry returns *core.Error config
// (exit 2); a run that read no rows returns *core.Error nothing-scanned
// (exit 3).
func Run(ctx context.Context, j Job) (*core.VerifyResult, error) {
	if err := checkOptions(j); err != nil {
		return nil, err
	}
	now := time.Now()
	if j.Opts.Clock != nil {
		now = j.Opts.Clock()
	}
	filt, expired, err := newFilter(j.Opts, now)
	if err != nil {
		return nil, err
	}
	src, st, closeAll, err := openTarget(ctx, j)
	if err != nil {
		return nil, err
	}
	defer closeAll()
	r := &run{j: j, skip: newSkipper(j.Opts.Skip), site: st, keySeen: map[string]bool{}, start: time.Now(),
		checked: map[string]int64{}, why: map[string]string{}, note: map[string]string{}, unknown: map[string]bool{}}
	if j.Opts.Clock != nil {
		r.start = j.Opts.Clock()
	}
	r.filter = filt
	r.res = &core.VerifyResult{Mode: "heuristic"}
	if j.Known != nil {
		r.res.Mode = "exact"
	}
	r.res.Coverage.Sample = max(0, j.Opts.Sample)
	if j.Progress != nil {
		j.Progress.Phase("verify", 0)
	}
	if st != nil {
		r.configs = readConfigs(ctx, st)
	} else if hasDataSteps(j.Steps) {
		r.capt = newCapture()
	}
	samples, tables, err := r.stream(ctx, src)
	if err != nil {
		return nil, err
	}
	r.uncovered(tables, samples)
	r.noteUnparsed()
	if st != nil {
		r.siteChecks(ctx, st)
	} else {
		// A database or dump target has no files: the checks that read
		// only rows run on what the scan kept, the rest are skipped;
		// a DSN's own step says so (side-effect.not-applicable).
		r.databaseSteps(ctx, tables)
	}
	r.extraChecks(ctx)
	if r.res.Coverage.RowsScanned == 0 {
		return nil, nothingScanned(j, r.res.Coverage)
	}
	r.res.Findings = append(r.agg.Findings(), expiredFindings(expired)...)
	sortFindings(r.res.Findings)
	r.res.Checks = r.checks()
	return r.res, nil
}

// nothingScanned is the PRD 17 nothing-scanned error.
func nothingScanned(j Job, c core.Coverage) *core.Error {
	why := "no tables"
	switch {
	case c.TablesTotal == 1 && len(c.Skipped) >= 1:
		why = "1 table, which couldn't be read"
	case c.TablesTotal > 0 && len(c.Skipped) >= c.TablesTotal:
		why = fmt.Sprintf("%d tables, none could be read", c.TablesTotal)
	case c.TablesTotal == 1:
		why = "1 table, empty"
	case c.TablesTotal > 0:
		why = fmt.Sprintf("%d tables, all empty", c.TablesTotal)
	case len(c.Skipped) > 0:
		why = "every read failed: " + c.Skipped[0].Reason
	}
	name := j.Target.Display
	if name == "" {
		name = "the target"
	}
	advice := "check the user's SELECT rights."
	if k := j.Target.Kind; k == core.DumpFile || k == core.Stdio {
		// A dump has no user: it holds no tables or no rows.
		advice = "the dump holds no tables or no rows. Check that the export wrote the database you meant."
	}
	return core.Errorf(core.ErrNothingScanned, 3,
		"nothing-scanned: verify read 0 rows from %s (%s). A verify that sees nothing can't pass; %s", name, why, advice)
}

// openTarget returns the source to scan and the site, if any, opening
// what the job didn't pass. closeAll closes what it opened.
func openTarget(ctx context.Context, j Job) (core.Source, core.Site, func(), error) {
	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	st := j.Site
	if j.Source != nil {
		return j.Source, st, closeAll, nil
	}
	fail := func(err error) (core.Source, core.Site, func(), error) {
		closeAll()
		return nil, nil, func() {}, err
	}
	if st == nil {
		switch j.Target.Kind {
		case core.DumpFile, core.Stdio:
			var rd io.Reader = os.Stdin
			name := "stdin"
			if j.Target.Kind == core.DumpFile {
				f, err := os.Open(j.Target.Path) // #nosec G304 -- the dump the user named
				if err != nil {
					return fail(err)
				}
				closers = append(closers, func() { _ = f.Close() })
				rd, name = f, filepath.Base(j.Target.Path)
			}
			// KeepRoutines: verify reads the dump and never runs it, so a
			// trigger or a function is no reason to refuse it.
			src, err := dump.Open(ctx, rd, dump.Options{Name: name, Progress: j.Progress, KeepRoutines: true,
				MaxValue: j.Opts.MaxValue, MaxRatio: j.Opts.MaxRatio, Schema: j.Schema})
			if err != nil {
				return fail(err)
			}
			closers = append(closers, func() { _ = src.Close() })
			return src, nil, closeAll, nil
		case core.DSN:
			c, err := db.Open(ctx, j.Target, db.ReadOnly)
			if err != nil {
				return fail(err)
			}
			closers = append(closers, func() { _ = c.Close() })
			src, err := c.Source(ctx, nil)
			if err != nil {
				return fail(err)
			}
			closers = append(closers, func() { _ = src.Close() })
			return src, nil, closeAll, nil
		default:
			s, err := site.Open(ctx, j.Target, site.Options{ReadOnly: true, BatchMode: true})
			if err != nil {
				return fail(err)
			}
			closers = append(closers, func() { _ = s.Close() })
			st = s
		}
	}
	src, err := st.Source(ctx, nil)
	if err != nil {
		return fail(err)
	}
	closers = append(closers, func() { _ = src.Close() })
	return src, st, closeAll, nil
}

// work is one batch for a worker, with the rows to scan.
type work struct {
	idx   int
	b     *core.Batch
	rows  []int
	kinds map[string]core.FakeKind
	ddl   bool // the literals of a DDL statement: not rows of the coverage
}

// ddlColumn is the column a DDL literal's finding names.
const ddlColumn = "(schema)"

// ddlBatch turns the string literals of a CREATE TABLE or ALTER TABLE
// into a batch of one-value rows on the table it names, so the
// detectors and exact mode read them like any value; nil when it has
// none.
func ddlBatch(st *core.Statement) *core.Batch {
	lits := core.DDLLiterals(st.Text)
	if len(lits) == 0 {
		return nil
	}
	name := core.DDLTable(st.Text)
	if name == "" {
		name = "(DDL)"
	}
	b := &core.Batch{Table: &core.Table{Name: name, Columns: []core.Column{{Name: ddlColumn, Type: "text"}}}}
	for _, l := range lits {
		if l[1] > l[0] {
			b.Rows = append(b.Rows, core.Row{Values: []core.Value{{Kind: core.String, Raw: st.Text[l[0]:l[1]]}}})
		}
	}
	if len(b.Rows) == 0 {
		return nil
	}
	return b
}

// allRows is every row index of b.
func allRows(b *core.Batch) []int {
	out := make([]int, len(b.Rows))
	for i := range out {
		out[i] = i
	}
	return out
}

// result is what a worker found in one batch.
type result struct {
	idx      int
	findings []core.Finding
	keys     []core.KeyHit
	hidden   int64
	rows     int64
	bytes    int64
	// unparsed counts serialized-looking values that didn't parse, by
	// column (table, NUL, column).
	unparsed map[string]int64
}

// stream reads every batch and scans it on the worker pool. It returns
// the uncovered-scan samples and the tables in input order.
func (r *run) stream(ctx context.Context, src core.Source) (map[string]map[string][][]byte, []*core.Table, error) {
	jobs := r.j.Opts.Jobs
	if jobs <= 0 {
		jobs = runtime.GOMAXPROCS(0)
	}
	sc := newScanner(r.j, r.skip, r.filter)
	sc.wcpayDev = wcpayDevMode(r.configs)
	r.urls = sc.findURL != nil
	in := make(chan work, jobs*2)
	out := make(chan result, jobs*2)
	var wg sync.WaitGroup
	for range jobs {
		wg.Go(func() {
			for w := range in {
				out <- sc.batch(w)
			}
		})
	}
	go func() { wg.Wait(); close(out) }()

	// The merge applies results in input order, so examples and locations
	// are the same on every run whatever the workers' timing.
	merged := make(chan struct{})
	go func() {
		defer close(merged)
		pending := map[int]result{}
		next := 0
		for res := range out {
			pending[res.idx] = res
			for {
				p, ok := pending[next]
				if !ok {
					break
				}
				delete(pending, next)
				next++
				r.merge(p)
			}
		}
	}()

	samples := map[string]map[string][][]byte{}
	var tables []*core.Table
	seen := map[string]bool{}
	scanned := map[string]int64{}
	kinds := map[string]map[string]core.FakeKind{}
	covered := map[string]map[string]bool{}
	var readErr error
	idx := 0
	current := ""
	for {
		if err := ctx.Err(); err != nil {
			readErr = err
			break
		}
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			// Read whole: the fingerprint names what this verify checked
			// (a dump's SHA-256, a database's tables).
			if fp := src.Fingerprint(); fp.Kind != "" {
				r.res.Input = &fp
			}
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				readErr = ctx.Err()
				break
			}
			// The source failed past the last table it gave, so the
			// failing table has no name here; whatever came after it was
			// never read either.
			skip := core.Skipped{Table: core.SkippedInput + ")", Reason: err.Error()}
			if current != "" {
				skip = core.Skipped{Table: core.SkippedInput + " after " + current + ")", Reason: err.Error() + "; the tables after it weren't read"}
			}
			r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, skip)
			break
		}
		if b != nil && b.Table == nil && b.Pass != nil && core.IsDDL(b.Pass.Kind) {
			// A production email in a DEFAULT or COMMENT is in the copy
			// as much as one in a row: the literals are scanned as values.
			if db := ddlBatch(b.Pass); db != nil {
				in <- work{idx: idx, b: db, rows: allRows(db), ddl: true}
				idx++
			}
			continue
		}
		if b == nil || b.Table == nil || len(b.Rows) == 0 {
			continue
		}
		t := b.Table
		current = t.Name
		if !seen[t.Name] {
			seen[t.Name] = true
			tables = append(tables, t)
			kinds[t.Name], covered[t.Name] = columnKinds(t, r.j.Rules)
			if r.j.Progress != nil {
				r.j.Progress.Table(t.Name, t.RowsEstimate)
			}
		}
		if r.capt != nil {
			r.capt.add(b)
		}
		var rows []int
		for i := range b.Rows {
			if b.Rows[i].Drop {
				continue
			}
			if s := r.j.Opts.Sample; s > 0 && scanned[t.Name] >= int64(s) {
				break
			}
			scanned[t.Name]++
			rows = append(rows, i)
		}
		if len(rows) == 0 {
			continue
		}
		r.sample(samples, b, rows, covered[t.Name])
		in <- work{idx: idx, b: b, rows: rows, kinds: kinds[t.Name]}
		idx++
		if r.j.Progress != nil {
			r.j.Progress.Advance(int64(len(rows)))
		}
	}
	close(in)
	<-merged
	if readErr != nil {
		return nil, nil, readErr
	}
	c := &r.res.Coverage
	for _, n := range scanned {
		if n > 0 {
			c.TablesScanned++
		}
	}
	if s := src.Schema(); s != nil {
		c.TablesTotal = len(s.Tables)
		for _, t := range s.Tables {
			if !seen[t.Name] {
				tables = append(tables, t)
			}
		}
	}
	c.TablesTotal = max(c.TablesTotal, c.TablesScanned)
	return samples, tables, nil
}

// merge adds one batch's result to the run.
func (r *run) merge(res result) {
	r.res.Coverage.RowsScanned += res.rows
	r.res.Coverage.BytesScanned += res.bytes
	r.res.Hidden += res.hidden
	for col, n := range res.unparsed {
		if r.unparsed == nil {
			r.unparsed = map[string]int64{}
		}
		r.unparsed[col] += n
	}
	for _, f := range res.findings {
		r.agg.Add(f)
	}
	for _, k := range res.keys {
		r.addKey(k)
	}
}

// maxKeys caps VerifyResult.Keys; findings still count every hit.
const maxKeys = 1000

// addKey records a key once per finding ID and location (a column, a
// path or a file line), whatever row it is on.
func (r *run) addKey(k core.KeyHit) {
	id := keySeenID(k.ID, k.At)
	if r.keySeen[id] || len(r.res.Keys) >= maxKeys {
		return
	}
	r.keySeen[id] = true
	r.res.Keys = append(r.res.Keys, k)
}

// keySeenID is the key of keySeen: the finding ID and the location
// without its row.
func keySeenID(id string, at core.Location) string {
	at.Row = ""
	return id + "\x00" + at.String() + "\x00" + fmt.Sprint(at.Line)
}

// columnKinds returns the fake kind and coverage of each column of t from
// the rules whose table glob matches it.
func columnKinds(t *core.Table, rules []core.Rule) (map[string]core.FakeKind, map[string]bool) {
	kinds := map[string]core.FakeKind{}
	covered := map[string]bool{}
	for _, rl := range rules {
		if ok, err := path.Match(rl.Table, t.Name); (err != nil || !ok) && rl.Table != t.Name {
			continue
		}
		covered[rl.Column] = true
		if rl.Path == "" && rl.Fake != "" && len(rl.Where) == 0 {
			kinds[rl.Column] = rl.Fake
		}
	}
	return kinds, covered
}

// sample keeps up to samplePerColumn non-empty values of each text column
// no rule covers, for the uncovered scan.
func (r *run) sample(samples map[string]map[string][][]byte, b *core.Batch, rows []int, covered map[string]bool) {
	t := b.Table
	m := samples[t.Name]
	if m == nil {
		m = map[string][][]byte{}
		samples[t.Name] = m
	}
	for _, ri := range rows {
		for pos, v := range b.Rows[ri].Values {
			if v.Kind != core.String || len(v.Raw) == 0 {
				continue
			}
			c := columnAt(b, pos)
			if c == nil || covered[c.Name] || len(m[c.Name]) >= samplePerColumn {
				continue
			}
			m[c.Name] = append(m[c.Name], append([]byte(nil), v.Raw[:min(len(v.Raw), sampleBytes)]...))
		}
	}
}

// columnAt returns the column of value position pos in b.
func columnAt(b *core.Batch, pos int) *core.Column {
	i := pos
	if b.Columns != nil {
		if pos >= len(b.Columns) {
			return nil
		}
		i = b.Columns[pos]
	}
	if i < 0 || i >= len(b.Table.Columns) {
		return nil
	}
	return &b.Table.Columns[i]
}

// uncovered lists the uncovered columns of every table (PRD 11.3).
func (r *run) uncovered(tables []*core.Table, samples map[string]map[string][][]byte) {
	var n int64
	for _, t := range tables {
		if r.j.ScanTable == nil || r.j.ScanTable(t.Name) {
			n++
		}
	}
	if n == 0 && len(tables) > 0 {
		r.skipCheck(core.FindingUncoveredColumn, "this run left every table's columns as they were")
		return
	}
	r.count(core.FindingUncoveredColumn, n)
	for _, t := range tables {
		if r.j.ScanTable != nil && !r.j.ScanTable(t.Name) {
			continue
		}
		_, covered := columnKinds(t, r.j.Rules)
		for _, u := range detect.Uncovered(t, covered, samples[t.Name]) {
			kept := r.add(core.Finding{
				ID: core.FindingUncoveredColumn, Severity: core.SeverityMedium,
				Title:     detect.UncoveredTitle(u, samples[t.Name][u.Column]),
				Locations: []core.LocationCount{{Location: core.Location{Table: u.Table, Column: u.Column}}},
				Fix:       core.UncoveredFix,
			})
			if kept {
				r.res.Uncovered = append(r.res.Uncovered, u)
			}
		}
	}
}

// noteUnparsed lists the columns holding serialized-looking values that
// didn't parse under coverage skipped. Verify reads such a value as plain
// text, so an email inside it is still found; one written escaped or
// with a length prefix that broke the parse may not be. Only copy and
// prepare report data.unparseable, since only they can blank the value.
func (r *run) noteUnparsed() {
	cols := make([]string, 0, len(r.unparsed))
	for col := range r.unparsed {
		cols = append(cols, col)
	}
	slices.Sort(cols)
	for _, col := range cols {
		n := r.unparsed[col]
		t, c, _ := strings.Cut(col, "\x00")
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: t + "." + c,
			Reason: fmt.Sprintf("%d serialized-looking %s didn't parse; read as plain text, so data nested inside may be missed", n, plural(n, "value", "values"))})
	}
}
