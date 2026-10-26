// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// maxJobs is the most workers a run uses (PRD 10.2): past eight the
// ordered writer, not the transform, is what limits the speed.
const maxJobs = 8

// input is a source each pass opens in turn: pass 1 indexes, pass 2
// transforms. close removes whatever the input kept on disk.
type input interface {
	open(ctx context.Context, pass int) (core.Source, error)
	close() error
}

// fileInput is a dump file, read twice.
type fileInput struct {
	path string
	o    dump.Options
}

func (f *fileInput) open(ctx context.Context, _ int) (core.Source, error) {
	return openDumpFile(ctx, f.path, f.o)
}

func (f *fileInput) close() error { return nil }

// streamInput is a dump that can be read once (ssh, stdin, a wp-cli
// alias). Pass 1 writes what it reads to the encrypted spool (PRD 10.3)
// and pass 2 reads the spool back; --one-pass reads the stream itself.
type streamInput struct {
	r       io.Reader
	rc      io.Closer // closed with the input when set: the site's export
	dir     string
	o       dump.Options
	onePass bool

	sp      spool
	spooled bool // pass 1 read the stream to its end into sp
}

func (s *streamInput) open(ctx context.Context, pass int) (core.Source, error) {
	if s.onePass {
		return openDump(ctx, s.r, s.o)
	}
	if pass == 1 {
		sp, err := newSpool(s.dir)
		if err != nil {
			return nil, err
		}
		s.sp = sp
		w := sp.Writer()
		src, err := openDump(ctx, io.TeeReader(s.r, w), s.o)
		if err != nil {
			_ = w.Close()
			return nil, err
		}
		return &spoolSource{Source: src, r: s.r, w: w, done: &s.spooled}, nil
	}
	if s.sp == nil {
		return nil, errors.New("engine: pass 2 before pass 1")
	}
	if !s.spooled {
		return nil, errors.New("engine: pass 2 after a pass 1 that stopped early")
	}
	rc, err := s.sp.Reader()
	if err != nil {
		return nil, err
	}
	src, err := openDump(ctx, rc, s.o)
	if err != nil {
		_ = rc.Close()
		return nil, err
	}
	return &closingSource{Source: src, c: rc}, nil
}

func (s *streamInput) close() error {
	var err error
	if s.rc != nil {
		err = s.rc.Close()
	}
	if s.sp == nil {
		return err
	}
	if rerr := s.sp.Remove(); err == nil {
		err = rerr
	}
	return err
}

// spoolSource is pass 1 over a stream: once the parser has reached the
// end, Close copies whatever it didn't read (bytes after the last
// statement) to the spool too, so pass 2 sees the same input byte for
// byte. A pass that stopped early (a parse error, a canceled run) skips
// the copy: no pass 2 follows. Reading the rest of an ssh export or
// waiting for stdin to end would only delay the stop.
type spoolSource struct {
	core.Source
	r    io.Reader
	w    io.WriteCloser
	eof  bool
	done *bool // set once the whole stream is in the spool
}

// Next reads a batch and notes when the parser reached the end.
func (s *spoolSource) Next(ctx context.Context) (*core.Batch, error) {
	b, err := s.Source.Next(ctx)
	if errors.Is(err, io.EOF) {
		s.eof = true
	}
	return b, err
}

func (s *spoolSource) Close() error {
	err := s.Source.Close()
	if s.eof {
		if _, cerr := io.Copy(s.w, s.r); err == nil {
			err = cerr
		}
	}
	if cerr := s.w.Close(); err == nil {
		err = cerr
	}
	if s.eof && err == nil {
		*s.done = true
	}
	return err
}

// siteInput is a live database through a site or DSN, which can be read
// again at no cost.
type siteInput struct{ s core.Site }

func (s *siteInput) open(ctx context.Context, _ int) (core.Source, error) {
	return s.s.Source(ctx, nil)
}
func (s *siteInput) close() error { return nil }

// spoolDir is where the encrypted spool goes: the cache folder, 0700.
func spoolDir() (string, error) {
	dir, err := config.CacheDir("roadie")
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// shared is what every worker of a run reads and none changes.
type shared struct {
	stack core.Stack
	cfg   *roadiecfg.Config
	opts  Options
	known core.Known
	spans core.Spanner
	urls  urlRewriter // nil: nothing to rewrite
	// stagingPath is the subfolder of --url, "" for none.
	stagingPath string
	kept        *keptUsers
	siteUsers   *siteUsers // copy --site's user limit, nil for none
	adminID     string     // the first administrator's ID
	addr        *addrIndex // sealed; nil with --one-pass
	adminHash   []byte     // the kept users' password hash for --admin-password-env
	key         pseudo.Key // keys the reset hashes, so a stable key repeats them
	onePass     bool
	parts       parts
}

// parts is what prepare --only leaves out; the zero value does every
// part. The kill switch and noindex parts are Steps and data rules the
// orchestration picks, so only the transform's own parts are here.
type parts struct {
	noData bool // no pseudonymising: no profile rules, row fixes or uncovered stop
	noKeys bool // no live key swap
	noURLs bool // no URL rewrite
}

// batchWorker transforms batches in one worker goroutine.
type batchWorker interface {
	transform(ctx context.Context, b *core.Batch, c *profile.Compiled) (*batchOut, error)
}

// defaultWorker builds the worker a run uses; it holds its own faker
// clone. Tests replace it.
var defaultWorker = func(sh *shared, f core.Faker) batchWorker { return newTransformer(sh, f) }

// changeKey groups changes by rule, table and column.
type changeKey struct{ rule, table, column string }

// batchOut is what one batch's transform did, merged in input order by
// the writer so the result never depends on worker timing.
type batchOut struct {
	changes    map[changeKey]*core.Change
	keys       []core.KeyHit
	findings   []core.Finding
	keptRows   int64
	keptValues [][]byte
	firstAdmin string
	indexFixed int64
}

func (o *batchOut) change(rule, table, column string, a core.Action) *core.Change {
	if o.changes == nil {
		o.changes = map[changeKey]*core.Change{}
	}
	k := changeKey{rule, table, column}
	c := o.changes[k]
	if c == nil {
		c = &core.Change{Rule: rule, Table: table, Column: column, Action: a}
		o.changes[k] = c
	}
	return c
}

// tally is the run's sum of batchOuts.
type tally struct {
	changes    map[changeKey]*core.Change
	keys       []core.KeyHit
	agg        core.Aggregator
	keptRows   int64
	keptValues map[string]bool
	firstAdmin string
	indexFixed int64
	rows       map[string]int64
}

func newTally() *tally {
	return &tally{changes: map[changeKey]*core.Change{}, keptValues: map[string]bool{}, rows: map[string]int64{}}
}

func (t *tally) merge(b *core.Batch, o *batchOut) {
	if b.Table != nil {
		t.rows[b.Table.Name] += int64(len(b.Rows))
	}
	if o == nil {
		return
	}
	for k, c := range o.changes {
		s := t.changes[k]
		if s == nil {
			cp := *c
			t.changes[k] = &cp
			continue
		}
		s.Rows += c.Rows
		s.Values += c.Values
		s.Kept += c.Kept
	}
	t.keys = append(t.keys, o.keys...)
	for _, f := range o.findings {
		t.agg.Add(f)
	}
	t.keptRows += o.keptRows
	for _, v := range o.keptValues {
		t.keptValues[normToken(v)] = true
	}
	if t.firstAdmin == "" {
		t.firstAdmin = o.firstAdmin
	}
	t.indexFixed += o.indexFixed
}

// job is one batch on its way through the workers to the writer.
type job struct {
	b    *core.Batch
	cost int64 // batchCost, held in the window until written
	comp *profile.Compiled
	out  *batchOut
	err  error
	done chan struct{}
}

// pipeline is one run of the two passes.
type pipeline struct {
	mode     Mode
	cfg      *roadiecfg.Config // the effective settings: config with the flags over it
	opts     Options
	defs     []profile.Def
	in       input
	sink     core.Sink // nil: a discard sink (plan)
	site     core.Site // the source site, or nil for a dump
	key      pseudo.Key
	progress core.Progress
	// importing names pass 2 "import" (a database target) instead of
	// "transform".
	importing bool
	// indexOnly stops after pass 1 (plan --schema-only).
	indexOnly bool
	newWorker func(sh *shared, f core.Faker) batchWorker
	// dataRules gives the kill switch's option-level rules for the stack
	// the run finds; they join the compiled rules. nil joins none.
	dataRules func(core.Stack) []core.Rule
	// gatewayIDs are the WooCommerce gateways of the source's options,
	// from pass 1, for the kill switch's data rules.
	gatewayIDs []string
	parts      parts
	// result is filled in place when set, so the orchestration's fields
	// stay; nil starts a new one.
	result *core.Result
	// beforeWrite runs once pass 1 has passed its checks and before pass
	// 2 reads a batch: the orchestration applies the file-level kill
	// switch, shows the maintenance page and opens the sink there, so a
	// stop in pass 1 (an uncovered column, a url-map error) leaves the
	// target untouched. nil keeps the sink set at the start.
	beforeWrite func(ctx context.Context) error
	// aborted is set once the sink was told to take its writes back.
	aborted bool
}

// output is a run's result plus what verify and the orchestration need.
type output struct {
	Result   *core.Result
	Known    core.Known // sealed; values of kept users never match
	Compiled *profile.Compiled
	Pairs    []urlrw.Pair
	Schema   *core.Schema
}

// jobs is the worker count: --jobs, else the CPUs, at most maxJobs.
func (p *pipeline) jobs() int {
	n := p.opts.Jobs
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	return max(1, min(n, maxJobs))
}

func (p *pipeline) phase(name string) {
	if p.progress != nil {
		p.progress.Phase(name, 0)
	}
}

// advance reports input read since the last call.
func (p *pipeline) advance(last *int64, off int64) {
	if p.progress != nil && off > *last {
		p.progress.Advance(off - *last)
		*last = off
	}
}

// run runs pass 1 and pass 2. On an error the result still says what
// state the target is in.
func (p *pipeline) run(ctx context.Context) (out *output, err error) {
	res := p.result
	if res == nil {
		res = &core.Result{}
	}
	res.Kind, res.TargetState, res.URL = string(p.mode), core.Untouched, p.opts.URL
	out = &output{Result: res}
	defer func() {
		if cerr := p.in.close(); err == nil && cerr != nil {
			err = cerr
		}
	}()
	// Any stop takes back what the sink holds, also one before the first
	// write: a file sink may already have its temp file.
	defer func() {
		if err != nil {
			p.abort(ctx, out)
		}
	}()
	if limit := memoryLimit(p.opts.MaxMemory); limit > 0 {
		prev := debug.SetMemoryLimit(limit)
		defer debug.SetMemoryLimit(prev)
	}
	if p.opts.OnePass && len(p.cfg.Pseudonymise.KeepUsers) > 0 {
		return out, core.Errorf(core.ErrConfig, 2,
			"--one-pass can't keep users: a dump has their user meta before the users table. Drop --one-pass or keep_users.")
	}
	if p.opts.OnePass && len(p.opts.Sites) > 0 {
		return out, core.Errorf(core.ErrConfig, 2,
			"--one-pass can't limit the copy to --site: the blogs are known only after the whole dump is read. Drop one of them.")
	}
	known := newKnown(p.key)
	faker, err := newFaker(p.key, pseudo.Options{EmailDomain: p.cfg.Pseudonymise.EmailDomain,
		Locale: p.cfg.Pseudonymise.Locale, PhoneStyle: p.cfg.Pseudonymise.PhoneStyle})
	if err != nil {
		return out, err
	}
	if known == nil || faker == nil {
		return out, core.ErrNotBuilt
	}
	idx := newIndexer(ctx, p.cfg, p.defs, p.site, known)
	idx.siteBlogs = p.opts.Sites
	if p.opts.OnePass {
		idx.entries = nil // Known is sealed before the field-type rules exist
	}
	var (
		final  *profile.Compiled
		schema *core.Schema
		plan   urlPlan
		uncov  uncoveredResult
	)
	if !p.opts.OnePass {
		p.phase("index")
		fp, s, err := p.index(ctx, idx)
		if err != nil {
			return out, err
		}
		out.Result.Input = &fp
		schema = s
		p.gatewayIDs = killswitch.SourceGatewayIDs(idx.gatewayOpts)
		if final, err = p.finalCompile(idx, schema); err != nil {
			return out, err
		}
		known.Seal()
		idx.addr.seal()
		idx.siteUsers.seal(idx.kept)
		uncov = p.uncovered(schema, final, idx)
		if plan, err = p.urlPlan(ctx, idx, final); err != nil {
			return out, err
		}
		p.fill(out, idx, final, schema, plan, uncov, nil)
		if len(uncov.Uncovered) > 0 && p.mode != Plan && !p.opts.AllowUncovered {
			return out, uncoveredError(uncov.Uncovered)
		}
		if p.indexOnly {
			out.Known = known
			return out, nil
		}
	} else if !p.parts.noURLs {
		if plan, err = buildURLPlan(urlSources{Production: p.cfg.Production}, p.opts.URL, p.cfg.URLMap); err != nil {
			return out, err
		}
	}
	if p.beforeWrite != nil {
		if err := p.beforeWrite(ctx); err != nil {
			return out, err
		}
		// A broken import then says "after 37 of 61 tables".
		if tc, ok := p.sink.(site.TableCounter); ok && schema != nil {
			tc.ExpectTables(len(schema.Tables))
		}
	}
	if words := hostWords(plan.Production); len(words) > 0 {
		known = &hostWordKnown{Known: known, words: words}
	}
	t, src, err := p.transform(ctx, idx, final, plan, known, faker, out)
	if src != nil {
		fp := src.Fingerprint()
		out.Result.Input = &fp
		if schema == nil {
			schema = src.Schema()
		}
		_ = src.Close()
	}
	if err != nil {
		return out, err
	}
	if p.opts.OnePass {
		known.Seal()
		if final, err = p.finalCompile(idx, schema); err != nil {
			p.abort(ctx, out)
			return out, err
		}
		uncov = p.uncovered(schema, final, idx)
		if len(uncov.Uncovered) > 0 && p.mode != Plan && !p.opts.AllowUncovered {
			p.abort(ctx, out)
			st := core.Untouched
			if p.sink != nil {
				st = p.sink.State()
			}
			return out, uncoveredErrorIn(uncov.Uncovered, st)
		}
	}
	p.fill(out, idx, final, schema, plan, uncov, t)
	out.Known = &keptKnown{Known: known, excl: t.keptValues}
	if p.sink != nil {
		if err := p.sink.Commit(ctx); err != nil {
			p.abort(ctx, out)
			return out, err
		}
		out.Result.TargetState = p.sink.State()
	}
	return out, nil
}

// abort takes back what pass 2 wrote, as far as the sink can.
func (p *pipeline) abort(ctx context.Context, out *output) {
	if p.sink == nil || p.aborted {
		return
	}
	p.aborted = true
	_ = p.sink.Abort(context.WithoutCancel(ctx))
	out.Result.TargetState = p.sink.State()
}

// index is pass 1: it reads every batch and hands it to the indexer.
func (p *pipeline) index(ctx context.Context, idx *indexer) (core.Fingerprint, *core.Schema, error) {
	src, err := p.in.open(ctx, 1)
	if err != nil {
		return core.Fingerprint{}, nil, err
	}
	var last int64
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = src.Close()
			return core.Fingerprint{}, nil, err
		}
		idx.observe(b, src.Schema())
		p.advance(&last, b.Offset)
	}
	fp, s := src.Fingerprint(), src.Schema()
	return fp, s, src.Close()
}

// finalCompile is the rule set of pass 2: the profiles the whole schema
// and the options show, plus the per-form rules of the field-type fixes.
func (p *pipeline) finalCompile(idx *indexer, schema *core.Schema) (*profile.Compiled, error) {
	if schema == nil {
		schema = &core.Schema{}
	}
	c, err := profile.CompileWith(nil, p.defs, schema, p.cfg, profile.Hints{Options: idx.options, Site: p.site, Ctx: idx.ctx})
	if err != nil {
		return nil, err
	}
	// Drop the other blogs first: it compiles the rules again, which
	// would lose the field-type rules added below.
	if c, err = p.dropOtherBlogs(idx, schema, c); err != nil {
		return nil, err
	}
	ft := profile.NewFieldTypes(c)
	for _, f := range idx.forms {
		vals := f.vals
		ft.Observe(f.table, func(col string) ([]byte, bool) { v, ok := vals[col]; return v, ok })
	}
	if rs := ft.Rules(); len(rs) > 0 {
		c = c.WithRules(rs)
		// Before Known is sealed: the entry values these rules cover.
		idx.entries.addKnown(idx.known, rs)
	}
	idx.entries = nil
	return p.join(c), nil
}

// join adds the kill switch's data rules for the set's stack.
func (p *pipeline) join(c *profile.Compiled) *profile.Compiled {
	if p.dataRules == nil || c == nil {
		return c
	}
	return c.Join(p.dataRules(c.Stack()))
}

// uncovered is the uncovered-column scan, or nothing when prepare --only
// leaves the data alone: no rule of a profile runs, so every personal
// column would look uncovered.
func (p *pipeline) uncovered(schema *core.Schema, c *profile.Compiled, idx *indexer) uncoveredResult {
	if p.parts.noData {
		return uncoveredResult{}
	}
	return scanUncovered(schema, c, idx.samples)
}

// dropOtherBlogs applies copy --site: the tables of every multisite blog
// not chosen are dropped from the copy, so their people never reach it.
// The main site (blog 1) holds the network's tables and always stays.
func (p *pipeline) dropOtherBlogs(idx *indexer, schema *core.Schema, c *profile.Compiled) (*profile.Compiled, error) {
	if len(p.opts.Sites) == 0 || c.Stack() != core.StackWordPress {
		return c, nil
	}
	have := map[string]bool{}
	for _, s := range c.Sites() {
		have[strings.TrimSuffix(s, "_")] = true
	}
	want := map[string]bool{"1": true}
	for _, id := range p.opts.Sites {
		n := strconv.Itoa(id)
		if id != 1 && !have[n] {
			return nil, core.Errorf(core.ErrConfig, 2, "--site %d: this network has no blog %d. Its blogs are %s.", id, id, humanSites(append([]string{"1_"}, c.Sites()...)))
		}
		want[n] = true
	}
	cfg := *p.cfg
	cfg.Drop = slices.Clone(p.cfg.Drop)
	for _, s := range c.Sites() {
		if n := strings.TrimSuffix(s, "_"); !want[n] {
			cfg.Drop = append(cfg.Drop, c.Prefix()+s+"*")
		}
	}
	nc, err := profile.CompileWith(nil, p.defs, schema, &cfg, profile.Hints{Options: idx.options, Site: p.site, Ctx: idx.ctx})
	if err != nil {
		return nil, err
	}
	p.cfg = &cfg
	return nc, nil
}

// humanSites lists blog prefixes as numbers: "2, 3 and 11".
func humanSites(sites []string) string {
	ns := make([]string, 0, len(sites))
	for _, s := range sites {
		ns = append(ns, strings.TrimSuffix(s, "_"))
	}
	if len(ns) == 1 {
		return ns[0]
	}
	return strings.Join(ns[:len(ns)-1], ", ") + " and " + ns[len(ns)-1]
}

// urlPlan builds the URL pairs from what pass 1 found, the settings and,
// for a Laravel site, APP_URL in its .env.
func (p *pipeline) urlPlan(ctx context.Context, idx *indexer, c *profile.Compiled) (urlPlan, error) {
	src := urlSources{SiteURLs: idx.siteURLList(), Blogs: idx.blogs, Production: p.cfg.Production}
	if p.site != nil && c.Stack() == core.StackLaravel {
		env := p.cfg.Laravel.EnvFile
		if env == "" {
			env = ".env"
		}
		if b, err := p.site.ReadFile(ctx, env); err == nil {
			if v, ok := dotenvValue(b, "APP_URL"); ok && v != "" {
				src.SiteURLs = append(src.SiteURLs, v)
			}
		}
	}
	return buildURLPlan(src, p.opts.URL, p.cfg.URLMap)
}

// transform is pass 2: one reader, the workers and the ordered writer.
// The returned source is open; the caller reads its fingerprint and
// closes it.
func (p *pipeline) transform(ctx context.Context, idx *indexer, final *profile.Compiled, plan urlPlan,
	known core.Known, faker core.Faker, out *output) (*tally, core.Source, error) {
	t := newTally()
	if p.importing {
		p.phase("import")
	} else {
		p.phase("transform")
	}
	sh := &shared{cfg: p.cfg, opts: p.opts, known: known, spans: textSpans(), kept: idx.kept, key: p.key,
		siteUsers: idx.siteUsers, adminID: idx.firstAdmin(), addr: idx.addr, onePass: p.opts.OnePass, parts: p.parts}
	if p.opts.OnePass {
		// The reader goroutine fills the address lengths and Known while
		// the workers run: the workers get neither. Known can't be read
		// before it is sealed, so the scrubber finds names only with the
		// detectors; an address index keeps its own text rule.
		sh.addr, sh.known = nil, emptyKnown{}
	}
	if final != nil {
		sh.stack = final.Stack()
	}
	if len(plan.Pairs) > 0 && !p.parts.noURLs {
		m, err := newURLMap(plan.Pairs)
		if err != nil {
			return t, nil, err
		}
		sh.urls, sh.stagingPath = m, plan.StagingPath
	}
	if err := p.adminPassword(sh); err != nil {
		return t, nil, err
	}
	src, err := p.in.open(ctx, 2)
	if err != nil {
		return t, nil, err
	}
	newWorker := p.newWorker
	if newWorker == nil {
		newWorker = defaultWorker
	}
	jobs := p.jobs()
	ctx2, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// The window: the reader waits when 2 x jobs batches or the window's
	// bytes are in flight, whichever comes first.
	order := make(chan *job, 2*jobs)
	win := newWindow(windowFor(p.opts.MaxMemory))
	work := make(chan *job, jobs)
	var wg sync.WaitGroup
	for range jobs {
		w := newWorker(sh, faker.Clone())
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				if err := ctx2.Err(); err != nil {
					j.err = context.Cause(ctx2)
				} else {
					j.out, j.err = w.transform(ctx2, j.b, j.comp)
				}
				close(j.done)
			}
		}()
	}
	var readErr error
	var wide, joined *profile.Compiled // the one-pass rule set so far, with the data rules
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(work)
		defer close(order)
		for {
			b, err := src.Next(ctx2)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				readErr = err
				cancel(err)
				return
			}
			comp := final
			if p.opts.OnePass {
				idx.observe(b, src.Schema())
				if idx.wide != wide {
					wide, joined = idx.wide, p.join(idx.wide)
				}
				comp = joined
				if comp == nil && b.Table != nil {
					// Nothing tells the stack yet: no rule could apply;
					// writing the rows as they are would copy them unread.
					readErr = profile.ErrNoStack
					cancel(readErr)
					return
				}
			}
			if comp != nil && skipBatch(b, comp) {
				continue
			}
			j := &job{b: b, comp: comp, done: make(chan struct{}), cost: batchCost(b)}
			if b.Table == nil {
				close(j.done)
			}
			if err := win.take(ctx2, j.cost); err != nil {
				return
			}
			select {
			case order <- j:
			case <-ctx2.Done():
				return
			}
			if b.Table != nil {
				select {
				case work <- j:
				case <-ctx2.Done():
					return
				}
			}
		}
	}()
	var werr error
	var last int64
	for j := range order {
		select {
		case <-j.done:
		case <-ctx2.Done():
			werr = context.Cause(ctx2)
		}
		if werr == nil && j.err != nil {
			werr = j.err
		}
		if werr == nil {
			t.merge(j.b, j.out)
			if p.sink != nil {
				werr = p.sink.Write(ctx2, j.b)
			}
			p.advance(&last, j.b.Offset)
		}
		win.give(j.cost)
		if werr != nil {
			cancel(werr)
			break
		}
	}
	for j := range order {
		_ = j // drained so the reader can stop
	}
	<-readDone
	wg.Wait()
	if werr == nil {
		werr = readErr
	}
	if werr == nil && ctx.Err() != nil {
		werr = context.Cause(ctx)
	}
	if werr != nil {
		p.abort(ctx, out)
		return t, src, werr
	}
	return t, src, nil
}

// adminPassword reads --admin-password-env: the hash every kept user's
// password becomes.
func (p *pipeline) adminPassword(sh *shared) error {
	name := p.opts.AdminPasswordEnv
	if name == "" || p.parts.noData {
		return nil
	}
	if !strings.HasPrefix(name, "ROADIE_") {
		return core.Errorf(core.ErrConfig, 2, "--admin-password-env names %s; only ROADIE_ variables are read. Rename it ROADIE_%s.", name, name)
	}
	pw, ok := lookupEnv(name)
	if !ok || pw == "" {
		return core.Errorf(core.ErrConfig, 2, "--admin-password-env names %s, which isn't set. Set it in the environment, or drop the flag.", name)
	}
	if len(p.cfg.Pseudonymise.KeepUsers) == 0 {
		return core.Errorf(core.ErrConfig, 2, "--admin-password-env sets the password of kept users; keep_users names none.")
	}
	stack := sh.stack
	if stack == "" {
		stack = core.StackWordPress
	}
	h, err := passwordHash(stack, []byte(pw))
	if err != nil {
		return err
	}
	sh.adminHash = h
	return nil
}

// statementTable reads the table a CREATE TABLE, DROP TABLE, LOCK
// TABLES, ALTER TABLE or CREATE INDEX statement names, so a dropped
// table loses its statements with its rows.
var statementTable = regexp.MustCompile("(?is)^(?:\\s|/\\*!\\d*)*(?:CREATE\\s+(?:TEMPORARY\\s+)?TABLE(?:\\s+IF\\s+NOT\\s+EXISTS)?|DROP\\s+TABLE(?:\\s+IF\\s+EXISTS)?|LOCK\\s+TABLES|ALTER\\s+TABLE(?:\\s+ONLY)?|CREATE\\s+(?:UNIQUE\\s+)?INDEX\\s+\\S+\\s+ON(?:\\s+ONLY)?)\\s+(?:[`\"]?[A-Za-z0-9_$]+[`\"]?\\.)?[`\"]?([A-Za-z0-9_$]+)")

// skipBatch reports whether pass 2 leaves a batch out: the rows of a
// truncated or dropped table and the statements of a dropped one.
func skipBatch(b *core.Batch, c *profile.Compiled) bool {
	if b.Table != nil {
		a := c.TableAction(b.Table.Name)
		return a == core.Truncate || a == core.DropTable
	}
	if b.Pass == nil {
		return false
	}
	switch b.Pass.Kind {
	case core.StmtCreateTable, core.StmtDropTable, core.StmtLock, core.StmtAlterKeys, core.StmtCreateIndex, "alter-table":
	default:
		return false
	}
	head := b.Pass.Text[:min(len(b.Pass.Text), 512)]
	m := statementTable.FindSubmatch(head)
	return m != nil && c.TableAction(string(m[1])) == core.DropTable
}

// fill writes what the run knows into the result. t is nil after pass 1
// alone.
func (p *pipeline) fill(out *output, idx *indexer, c *profile.Compiled, schema *core.Schema, plan urlPlan, uncov uncoveredResult, t *tally) {
	r := out.Result
	out.Compiled, out.Schema, out.Pairs = c, schema, plan.Pairs
	r.Production = plan.Production
	r.Covered, r.Uncovered = uncov.Covered, uncov.Uncovered
	var agg core.Aggregator
	for _, f := range uncov.Findings {
		agg.Add(f)
	}
	if c != nil {
		r.RulesetSHA256 = c.SHA256()
		r.Stack = core.StackInfo{Name: c.Stack(), Profiles: c.Profiles(), HPOS: idx.hposOn()}
		if c.Stack() == core.StackWordPress {
			r.Stack.Sites = len(c.Sites()) + 1
			r.Stack.Multisite = len(c.Sites()) > 0 || len(idx.blogs) > 0
		}
	}
	if n := len(idx.kept.ids); n > 0 {
		agg.Add(core.Finding{ID: core.FindingInfoKeptUsers, Severity: core.SeverityInfo,
			Title:     fmt.Sprintf("%d kept users left as they are (keep_users)", n),
			Count:     int64(n),
			Locations: []core.LocationCount{{Location: core.Location{Table: idx.prefix + "users"}, Count: int64(n)}}})
		r.KeptUsers = int64(n)
	}
	rows := idx.rows
	if t != nil {
		for _, f := range t.agg.Findings() {
			agg.Add(f)
		}
		r.FirstAdmin = t.firstAdmin
		r.Changes = sortedChanges(t.changes)
		r.Keys = t.keys
		if p.opts.OnePass {
			rows = t.rows
		}
	}
	r.Findings = agg.Findings()
	r.Tables = tableReports(schema, c, rows, r.Changes)
}

// sortedChanges lists the changes by table, column and rule.
func sortedChanges(m map[changeKey]*core.Change) []core.Change {
	out := make([]core.Change, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b core.Change) int {
		if c := strings.Compare(a.Table, b.Table); c != 0 {
			return c
		}
		if c := strings.Compare(a.Column, b.Column); c != 0 {
			return c
		}
		return strings.Compare(a.Rule, b.Rule)
	})
	return out
}

// tableReports says per table what the run did, in name order.
func tableReports(s *core.Schema, c *profile.Compiled, rows map[string]int64, changes []core.Change) []core.TableReport {
	if s == nil {
		return nil
	}
	byTable := map[string][]core.Change{}
	for _, ch := range changes {
		byTable[ch.Table] = append(byTable[ch.Table], ch)
	}
	names := make([]string, 0, len(s.Tables))
	for _, t := range s.Tables {
		names = append(names, t.Name)
	}
	slices.Sort(names)
	out := make([]core.TableReport, 0, len(names))
	for _, n := range names {
		tr := core.TableReport{Name: n, Rows: rows[n], Action: string(core.Keep)}
		if c != nil {
			tr.Action = string(c.TableAction(n))
			if rs := c.Rules(n); len(rs) > 0 {
				tr.Profile = rs[0].Profile
			}
		}
		for _, ch := range byTable[n] {
			tr.Columns = append(tr.Columns, core.ColumnReport{Name: ch.Column, Rule: ch.Rule, Changed: ch.Values, Kept: ch.Kept})
		}
		out = append(out, tr)
	}
	return out
}

// normToken is how kept values and tokens are compared: trimmed, lower
// case.
func normToken(b []byte) string { return strings.ToLower(strings.TrimSpace(string(b))) }

// emptyKnown is a sealed Known with nothing in it: the scrubber's
// Known in --one-pass, where the real one is still being filled.
type emptyKnown struct{}

func (emptyKnown) Add(core.FakeKind, []byte) {}
func (emptyKnown) Seal()                     {}
func (emptyKnown) Contains([]byte) (core.FakeKind, string, bool) {
	return "", "", false
}
func (emptyKnown) Len() int { return 0 }

// hostWordKnown is Known without the shop's own name words: a name
// that holds a word of a production host ("Shop Owner" on
// shop.example.com) is the business's account, and the word is in page
// titles, slugs, post types and addresses all over the site. Looked up
// as a name it would make the scrubber rewrite "Shop" in every note and
// verify report shop_order as a survivor.
type hostWordKnown struct {
	core.Known
	words map[string]bool
}

// Contains is Known.Contains except for a name that holds a host word.
func (k *hostWordKnown) Contains(token []byte) (core.FakeKind, string, bool) {
	kind, enc, ok := k.Known.Contains(token)
	if !ok || enc != "raw" || kind != core.KFirstName && kind != core.KLastName && kind != core.KFullName {
		return kind, enc, ok
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(string(token)), notLetter) {
		if k.words[w] {
			return "", "", false
		}
	}
	return kind, enc, ok
}

// hostWords are the words of the production hosts: the labels of 3 or
// more letters, lower-cased, without the top-level domain.
func hostWords(hosts []string) map[string]bool {
	words := map[string]bool{}
	for _, h := range hosts {
		labels := strings.Split(strings.TrimPrefix(bareHost(h), "*."), ".")
		if len(labels) > 1 {
			labels = labels[:len(labels)-1]
		}
		for _, l := range labels {
			for _, w := range strings.FieldsFunc(l, notLetter) {
				if len([]rune(w)) >= 3 && w != "www" {
					words[w] = true
				}
			}
		}
	}
	return words
}

func notLetter(r rune) bool { return !unicode.IsLetter(r) }

// keptKnown is the run's Known for verify: a kept user's own values are
// still in the copy on purpose, so they never count as survivors.
type keptKnown struct {
	core.Known
	excl map[string]bool
}

// Contains is Known.Contains except for the kept users' values.
func (k *keptKnown) Contains(token []byte) (core.FakeKind, string, bool) {
	if k.excl[normToken(token)] {
		return "", "", false
	}
	return k.Known.Contains(token)
}
