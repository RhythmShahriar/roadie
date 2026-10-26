// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// genSource makes n batches of one row on the fly, so a test can stream
// gigabytes without holding them.
type genSource struct {
	s     *core.Schema
	n     int
	size  int
	next  int
	value func(i int) []byte
}

func newGenSource(n, size int) *genSource {
	t := table("t", "id:int(11)", "c:longtext")
	return &genSource{s: &core.Schema{Dialect: core.MySQL, Tables: []*core.Table{t}}, n: n, size: size}
}

func (g *genSource) Schema() *core.Schema { return g.s }
func (g *genSource) Next(ctx context.Context) (*core.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.next >= g.n {
		return nil, io.EOF
	}
	i := g.next
	g.next++
	v := []byte(fmt.Sprintf("row %d", i))
	if g.value != nil {
		v = g.value(i)
	} else if g.size > 0 {
		v = bytes.Repeat([]byte("x"), g.size)
	}
	return &core.Batch{Seq: uint64(i), Table: g.s.Tables[0], Columns: []int{0, 1}, First: true, Last: true,
		Rows:   []core.Row{{Values: []core.Value{{Kind: core.Number, Raw: []byte(fmt.Sprint(i))}, {Kind: core.String, Raw: v}}}},
		Offset: int64(i+1) * int64(max(g.size, 8))}, nil
}
func (g *genSource) Fingerprint() core.Fingerprint {
	return core.Fingerprint{Kind: "sha256-file", Value: "gen"}
}
func (g *genSource) Close() error { return nil }

// sqlConfig is a generic SQL run over table t, column c.
func sqlConfig(rule string) *roadiecfg.Config {
	cfg := roadiecfg.Default()
	cfg.Stack = "sql"
	cfg.Columns = []roadiecfg.ColumnRule{{Table: "t", Column: "c", Rule: rule}}
	return cfg
}

func genPipeline(t *testing.T, n, size int, cfg *roadiecfg.Config) (*pipeline, *testutil.Sink) {
	t.Helper()
	defs, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	sink := testutil.FakeSink()
	in := &funcInput{fn: func(int) (core.Source, error) { return newGenSource(n, size), nil }}
	return &pipeline{mode: Copy, cfg: cfg, defs: defs, in: in, sink: sink, opts: Options{Jobs: 8}}, sink
}

// delayWorker sleeps a random while per batch, so batches finish out of
// order.
type delayWorker struct{ n *atomic.Int64 }

func (w delayWorker) transform(_ context.Context, b *core.Batch, _ *profile.Compiled) (*batchOut, error) {
	time.Sleep(time.Duration(rand.IntN(1500)) * time.Microsecond) // #nosec G404 -- test timing only
	w.n.Add(1)
	return &batchOut{}, nil
}

// The writer restores input order whatever order the workers finish in.
func TestPipelineKeepsOrder(t *testing.T) {
	useFakes(t)
	p, sink := genPipeline(t, 300, 0, sqlConfig("email"))
	var n atomic.Int64
	p.newWorker = func(*shared, core.Faker) batchWorker { return delayWorker{&n} }
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := sink.Batches()
	if len(got) != 300 || n.Load() != 300 {
		t.Fatalf("%d batches written, %d transformed", len(got), n.Load())
	}
	for i, b := range got {
		if b.Seq != uint64(i) {
			t.Fatalf("batch %d has seq %d", i, b.Seq)
		}
	}
	if !sink.Committed() || out.Result.TargetState != core.Replaced {
		t.Errorf("committed %v, state %s", sink.Committed(), out.Result.TargetState)
	}
}

// cancelWorker cancels the run once it has seen at batches.
type cancelWorker struct {
	n      *atomic.Int64
	at     int64
	cancel context.CancelFunc
}

func (w cancelWorker) transform(ctx context.Context, b *core.Batch, _ *profile.Compiled) (*batchOut, error) {
	if w.n.Add(1) == w.at {
		w.cancel()
	}
	return &batchOut{}, ctx.Err()
}

// A stream is spooled in pass 1 and read back in pass 2; canceling pass 2
// aborts the sink and removes the spool.
func TestPipelineCancel(t *testing.T) {
	f := useFakes(t)
	raw := []byte("-- a dump that can be read once\n")
	oldOpen := openDump
	opened := 0
	openDump = func(_ context.Context, r io.Reader, _ dump.Options) (core.Source, error) {
		opened++
		if opened == 2 {
			if b, _ := io.ReadAll(r); !bytes.Equal(b, raw) {
				t.Errorf("pass 2 read %q from the spool", b)
			}
		}
		return newGenSource(500, 0), nil
	}
	t.Cleanup(func() { openDump = oldOpen })
	defs, _ := profile.Builtin()
	sink := testutil.FakeSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int64
	p := &pipeline{mode: Copy, cfg: sqlConfig("email"), defs: defs, sink: sink, opts: Options{Jobs: 4},
		in: &streamInput{r: bytes.NewReader(raw), dir: t.TempDir()}}
	p.newWorker = func(*shared, core.Faker) batchWorker { return cancelWorker{n: &n, at: 20, cancel: cancel} }
	out, err := p.run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if !sink.Aborted() || sink.Committed() || !f.spool.Removed() {
		t.Errorf("aborted %v committed %v spool removed %v", sink.Aborted(), sink.Committed(), f.spool.Removed())
	}
	if out.Result.TargetState != sink.State() || len(sink.Batches()) >= 500 {
		t.Errorf("state %s, %d batches", out.Result.TargetState, len(sink.Batches()))
	}
}

// The spool is removed after a run that went well too.
func TestPipelineSpoolRemovedOnSuccess(t *testing.T) {
	f := useFakes(t)
	oldOpen := openDump
	openDump = func(context.Context, io.Reader, dump.Options) (core.Source, error) { return newGenSource(10, 0), nil }
	t.Cleanup(func() { openDump = oldOpen })
	defs, _ := profile.Builtin()
	p := &pipeline{mode: Copy, cfg: sqlConfig("email"), defs: defs, sink: testutil.FakeSink(),
		in: &streamInput{r: strings.NewReader("x"), dir: t.TempDir()}}
	if _, err := p.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.spool.Removed() {
		t.Error("spool kept")
	}
}

// failingSource stops pass 1 with a parse error after one batch.
type failingSource struct{ core.Source }

func (s failingSource) Next(ctx context.Context) (*core.Batch, error) {
	if _, err := s.Source.Next(ctx); err != nil {
		return nil, err
	}
	return nil, core.Errorf(core.ErrDumpFormat, 2, "dump-format: line 3 isn't SQL.")
}

// countingReader is a long stream that counts what was read from it.
type countingReader struct {
	n    atomic.Int64
	left int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.left)
	r.left -= n
	r.n.Add(n)
	return int(n), nil
}

// A pass 1 that stops early neither reads the rest of the stream nor
// copies it into the spool; the spool is removed.
func TestPipelineSpoolStopsEarly(t *testing.T) {
	f := useFakes(t)
	oldOpen := openDump
	openDump = func(context.Context, io.Reader, dump.Options) (core.Source, error) {
		return failingSource{newGenSource(10, 0)}, nil
	}
	t.Cleanup(func() { openDump = oldOpen })
	defs, _ := profile.Builtin()
	r := &countingReader{left: 64 << 20}
	in := &streamInput{r: r, dir: t.TempDir()}
	p := &pipeline{mode: Copy, cfg: sqlConfig("email"), defs: defs, sink: testutil.FakeSink(), in: in}
	_, err := p.run(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat {
		t.Fatalf("err %v", err)
	}
	if got := r.n.Load(); got != 0 {
		t.Errorf("read %d bytes of the stream after the error", got)
	}
	f.spool.mu.Lock()
	spooled := f.spool.buf.Len()
	f.spool.mu.Unlock()
	if spooled != 0 || !f.spool.Removed() || in.spooled {
		t.Errorf("spooled %d bytes, removed %v, complete %v", spooled, f.spool.Removed(), in.spooled)
	}
	if _, err := in.open(context.Background(), 2); err == nil {
		t.Error("pass 2 opened a spool pass 1 didn't finish")
	}
}

// A pass 1 that reached the end copies the rest of the stream, so pass
// 2 reads the input byte for byte (the positive side of the above).
func TestPipelineSpoolDrainsAtEOF(t *testing.T) {
	f := useFakes(t)
	oldOpen := openDump
	openDump = func(context.Context, io.Reader, dump.Options) (core.Source, error) { return newGenSource(3, 0), nil }
	t.Cleanup(func() { openDump = oldOpen })
	r := &countingReader{left: 1 << 20}
	in := &streamInput{r: r, dir: t.TempDir()}
	src, err := in.open(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := src.Next(context.Background()); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	f.spool.mu.Lock()
	spooled := f.spool.buf.Len()
	f.spool.mu.Unlock()
	if spooled != 1<<20 || !in.spooled {
		t.Errorf("spooled %d bytes, complete %v", spooled, in.spooled)
	}
	if _, err := in.open(context.Background(), 2); err != nil {
		t.Error(err)
	}
}

// 1,000 batches of 4 MiB through 8 workers stay under 300 MiB of heap:
// the window, not the input, bounds memory.
func TestPipelineMemory(t *testing.T) {
	if testing.Short() || raceOn {
		t.Skip("streams 4 GB; heap numbers under -race mean nothing")
	}
	useFakes(t)
	p, _ := genPipeline(t, 1000, core.MaxBatchBytes, sqlConfig("text"))
	p.sink = nil // a discard sink, as plan has
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peak.Load() {
				peak.Store(ms.HeapInuse)
			}
		}
	}()
	_, err := p.run(context.Background())
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() > 300<<20 {
		t.Errorf("peak heap in use %d MiB", peak.Load()>>20)
	}
	t.Logf("peak heap in use %d MiB", peak.Load()>>20)
}

// wpUsers is a small WordPress fixture.
func wpUsers() []tableData {
	return []tableData{
		{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
			{1, "siteurl", "https://shop.com"}, {2, "home", "https://shop.com"}, {3, "admin_email", "owner@shop.com"},
			{4, "blogdescription", "Visit https://shop.com/sale"}, {5, "stripe_settings", `{"secret_key":"` + liveKey + `"}`}}},
		{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_pass"), rows{
			{1, "anna", "anna.lee@example.com", "$P$Bxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
			{2, "ben", "ben@example.com", "$P$Byyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}}},
	}
}

// --one-pass reads the input once, builds Known while it transforms and
// seals it at the end.
func TestPipelineOnePass(t *testing.T) {
	f := useFakes(t)
	p, sink, in := fixture(t, Copy, "wp_", wpUsers, nil, Options{OnePass: true, URL: "https://staging.shop.test"})
	p.cfg.Production = []string{"shop.com"}
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(in.opened, []int{2}) {
		t.Errorf("passes %v, want one", in.opened)
	}
	if v, _ := valueOf(t, sink, "wp_users", 0, "user_email"); !strings.HasSuffix(v, "@example.invalid") {
		t.Errorf("email %q", v)
	}
	if _, enc, ok := f.known.Contains([]byte("anna.lee@example.com")); !ok || enc != "raw" {
		t.Error("Known doesn't hold the email seen in pass 2")
	}
	if out.Known == nil || out.Result.Stack.Name != core.StackWordPress {
		t.Errorf("stack %s", out.Result.Stack.Name)
	}
	if v, _ := valueOf(t, sink, "wp_options", 3, "option_value"); v != "Visit https://staging.shop.test/sale" {
		t.Errorf("production: URL not rewritten in one pass: %q", v)
	}
	// keep_users needs the users table before their meta, which a dump
	// doesn't give.
	p, _, _ = fixture(t, Copy, "wp_", wpUsers, nil, Options{OnePass: true})
	p.cfg.Pseudonymise.KeepUsers = []string{"anna"}
	if _, err := p.run(context.Background()); !isCode(err, core.ErrConfig) {
		t.Errorf("one-pass with keep_users: %v", err)
	}
}

// Two passes: pass 1 indexes, pass 2 writes; Known is sealed before pass
// 2 and holds the structured values.
func TestPipelineTwoPass(t *testing.T) {
	f := useFakes(t)
	cfg := roadiecfg.Default()
	cfg.Keys.TestKeys = map[string]string{"stripe": testKey}
	p, sink, in := fixture(t, Copy, "wp_", wpUsers, cfg, Options{URL: "https://staging.shop.test"})
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := valueOf(t, sink, "wp_options", 4, "option_value"); v != `{"secret_key":"`+testKey+`"}` {
		t.Errorf("live key in JSON: %q", v)
	}
	if len(out.Result.Keys) != 1 || out.Result.Keys[0].At.Path != "json:secret_key" || out.Result.Keys[0].At.Row != "option_name=stripe_settings" {
		t.Errorf("keys %+v", out.Result.Keys)
	}
	if !slices.Equal(in.opened, []int{1, 2}) || !in.closed {
		t.Errorf("passes %v closed %v", in.opened, in.closed)
	}
	if _, _, ok := f.known.Contains([]byte("ben@example.com")); !ok {
		t.Error("Known lacks a users email")
	}
	if v, _ := valueOf(t, sink, "wp_options", 0, "option_value"); v != "https://staging.shop.test" {
		t.Errorf("siteurl %q", v)
	}
	if !slices.Contains(out.Result.Production, "shop.com") || out.Result.RulesetSHA256 == "" || out.Result.Input == nil {
		t.Errorf("result %+v", out.Result)
	}
	if len(out.Result.Tables) != 2 || out.Result.Tables[1].Name != "wp_users" || out.Result.Tables[1].Rows != 2 {
		t.Errorf("tables %+v", out.Result.Tables)
	}
}

// A truncated table loses its rows and a dropped one its statements too.
func TestPipelineDropAndTruncate(t *testing.T) {
	useFakes(t)
	tables := func() []tableData {
		td := wpUsers()
		// WooCommerce is known from its option alone; its profile truncates sessions.
		td[0].rows = append(td[0].rows, []any{5, "woocommerce_version", "9.8.0"})
		return append(td,
			tableData{table("wp_woocommerce_sessions", "session_id:bigint(20)", "session_value"), rows{{1, "a:1:{}"}}},
			tableData{table("wp_old_log", "id:int(11)", "line"), rows{{1, "x"}}})
	}
	cfg := roadiecfg.Default()
	cfg.Drop = []string{"wp_old_*"}
	p, sink, _ := fixture(t, Copy, "wp_", tables, cfg, Options{})
	if _, err := p.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, b := range sink.Batches() {
		switch {
		case b.Table != nil:
			seen = append(seen, "rows:"+b.Table.Name)
		case b.Pass != nil:
			seen = append(seen, "stmt:"+string(b.Pass.Text[:bytes.IndexByte(b.Pass.Text, '(')-1]))
		}
	}
	want := []string{"stmt:CREATE TABLE `wp_options`", "rows:wp_options", "stmt:CREATE TABLE `wp_users`", "rows:wp_users",
		"stmt:CREATE TABLE `wp_woocommerce_sessions`"}
	if !slices.Equal(seen, want) {
		t.Errorf("written %v", seen)
	}
	for _, s := range []string{"DROP TABLE IF EXISTS `wp_old_log`;", "/*!40000 ALTER TABLE `wp_old_log` DISABLE KEYS */;", "LOCK TABLES `wp_old_log` WRITE;"} {
		m := statementTable.FindStringSubmatch(s)
		if m == nil || m[1] != "wp_old_log" {
			t.Errorf("%s: %v", s, m)
		}
	}
}

// multisite is a subdomain network with one mapped custom domain.
func multisite() []tableData {
	return []tableData{
		{table("wp_blogs", "blog_id:bigint(20)", "domain", "path"), rows{{1, "shop.com", "/"}, {2, "fr.shop.com", "/"}, {3, "shop.de", "/"}}},
		{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
			{1, "siteurl", "https://shop.com"}, {2, "home", "https://shop.com"}, {3, "blogdescription", "see https://fr.shop.com/x"}}},
		{table("wp_sitemeta", "meta_id:bigint(20)", "site_id:bigint(20)", "meta_key", "meta_value"), rows{{1, 1, "site_name", "Shop"}, {2, 1, "dm_domain", "shop.de"}}},
	}
}

// Subdomain blogs map by suffix; a custom domain needs --url-map; the
// run stops (exit 2) naming it until one is given.
func TestPipelineURLMap(t *testing.T) {
	useFakes(t)
	p, sink, _ := fixture(t, Copy, "wp_", multisite, nil, Options{URL: "https://staging.shop.test"})
	_, err := p.run(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrURLMap || ce.Exit != 2 ||
		ce.Msg != "url-map: shop.de has no staging URL; add --url-map shop.de=shop.staging.shop.test." {
		t.Fatalf("err %v", err)
	}
	if len(sink.Batches()) != 0 {
		t.Error("wrote before the url-map check")
	}
	cfg := roadiecfg.Default()
	cfg.URLMap = map[string]string{"shop.de": "de.staging.shop.test"}
	p, sink, _ = fixture(t, Copy, "wp_", multisite, cfg, Options{URL: "https://staging.shop.test"})
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []urlrw.Pair{{From: "https://fr.shop.com", To: "https://fr.staging.shop.test"},
		{From: "shop.de", To: "de.staging.shop.test"}, {From: "https://shop.com", To: "https://staging.shop.test"}} {
		if !slices.Contains(out.Pairs, want) {
			t.Errorf("pairs %v lack %v", out.Pairs, want)
		}
	}
	if v, _ := valueOf(t, sink, "wp_blogs", 1, "domain"); v != "fr.staging.shop.test" {
		t.Errorf("blog 2 domain %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_options", 2, "option_value"); v != "see https://fr.staging.shop.test/x" {
		t.Errorf("blog URL in text %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_sitemeta", 1, "meta_value"); v != "de.staging.shop.test" {
		t.Errorf("sitemeta domain key %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_sitemeta", 0, "meta_value"); v != "Shop" {
		t.Errorf("sitemeta site_name %q", v)
	}
	if !out.Result.Stack.Multisite {
		t.Error("not multisite")
	}
}

// A staging site in a subfolder: URLs get the folder, host columns only
// the host and the network's path columns the folder in front.
func TestPipelineStagingPath(t *testing.T) {
	useFakes(t)
	cfg := roadiecfg.Default()
	cfg.URLMap = map[string]string{"shop.de": "de.staging.shop.test"}
	p, sink, _ := fixture(t, Copy, "wp_", multisite, cfg, Options{URL: "https://staging.shop.test/sub/"})
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []urlrw.Pair{{From: "https://shop.com", To: "https://staging.shop.test/sub"},
		{From: "https://fr.shop.com", To: "https://fr.staging.shop.test/sub"}} {
		if !slices.Contains(out.Pairs, want) {
			t.Errorf("pairs %v lack %v", out.Pairs, want)
		}
	}
	if v, _ := valueOf(t, sink, "wp_options", 0, "option_value"); v != "https://staging.shop.test/sub" {
		t.Errorf("siteurl %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_blogs", 1, "domain"); v != "fr.staging.shop.test" {
		t.Errorf("blog 2 domain %q", v)
	}
	for i := range 3 {
		if v, _ := valueOf(t, sink, "wp_blogs", i, "path"); v != "/sub/" {
			t.Errorf("blog %d path %q", i+1, v)
		}
	}
	// Already in the folder: left as it is.
	if nv, ok := stagingBlogPath("/sub", []byte("/sub/fr/")); ok || string(nv) != "/sub/fr/" {
		t.Errorf("prefixed twice: %q", nv)
	}
	if nv, ok := stagingBlogPath("", []byte("/fr/")); ok || string(nv) != "/fr/" {
		t.Errorf("no folder: %q", nv)
	}
}

func TestIsBareHost(t *testing.T) {
	for _, h := range []string{"shop.com", "fr.shop.com", "shop.com:8443", "xn--bcher-kva.example"} {
		if !isBareHost([]byte(h)) {
			t.Errorf("%q is a host", h)
		}
	}
	for _, h := range []string{"", "shop", "https://shop.com", "shop.com/x", "two words.com", ".shop.com", "-shop.com", "a@shop.com"} {
		if isBareHost([]byte(h)) {
			t.Errorf("%q isn't a bare host", h)
		}
	}
	if got := string(hostOnly([]byte("https://staging.shop.test/sub"))); got != "staging.shop.test" {
		t.Errorf("hostOnly %q", got)
	}
}

func TestBuildURLPlan(t *testing.T) {
	src := urlSources{SiteURLs: []string{"https://shop.com", "https://shop.com/wp"},
		Blogs:      []blogDomain{{Domain: "shop.com"}, {Domain: "www.shop.com"}, {Domain: "fr.shop.com"}, {Domain: "shop.de"}, {Domain: "shop.fr"}, {Domain: "cdn.other.net"}},
		Production: []string{"*.shop.com", "https://shop.co.uk/"}}
	_, err := buildURLPlan(src, "https://staging.shop.test", map[string]string{"cdn.other.net": ""})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Msg != "url-map: shop.de and shop.fr have no staging URL; add --url-map shop.de=shop.staging.shop.test,shop.fr=shop.staging.shop.test." {
		t.Fatalf("err %v", err)
	}
	plan, err := buildURLPlan(src, "https://staging.shop.test", map[string]string{"cdn.other.net": "", "shop.de": "de.staging.shop.test", "shop.fr": "https://fr2.staging.shop.test"})
	if err != nil {
		t.Fatal(err)
	}
	want := []urlrw.Pair{{From: "shop.de", To: "de.staging.shop.test"}, {From: "shop.fr", To: "https://fr2.staging.shop.test"},
		{From: "https://shop.com", To: "https://staging.shop.test"}, {From: "https://shop.co.uk", To: "https://staging.shop.test"},
		{From: "https://fr.shop.com", To: "https://fr.staging.shop.test"}}
	if !slices.Equal(plan.Pairs, want) {
		t.Errorf("pairs\n%v\nwant\n%v", plan.Pairs, want)
	}
	if !slices.Equal(plan.Excluded, []string{"cdn.other.net"}) || !slices.Contains(plan.Production, "*.shop.com") || !slices.Contains(plan.Production, "fr.shop.com") {
		t.Errorf("excluded %v production %v", plan.Excluded, plan.Production)
	}
	for _, bad := range []string{"https://staging.shop.test/?a=1", "https://staging.shop.test/#x"} {
		if _, err := buildURLPlan(urlSources{}, bad, nil); !isCode(err, core.ErrConfig) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if plan, err := buildURLPlan(urlSources{SiteURLs: []string{"https://shop.com"}}, "https://staging.shop.test/a/b/", nil); err != nil ||
		plan.StagingPath != "/a/b" || !slices.Equal(plan.Pairs, []urlrw.Pair{{From: "https://shop.com", To: "https://staging.shop.test/a/b"}}) {
		t.Errorf("subfolder plan %+v %v", plan, err)
	}
	// No staging URL: nothing to rewrite to, so nothing to stop for.
	if plan, err := buildURLPlan(src, "", nil); err != nil || len(plan.Pairs) != 0 {
		t.Errorf("no url: %v %v", plan.Pairs, err)
	}
	if _, err := buildURLPlan(src, "staging.shop.test", nil); !isCode(err, core.ErrConfig) {
		t.Errorf("bare host url: %v", err)
	}
	if v, ok := dotenvValue([]byte("APP_NAME=x\nexport APP_URL=\"https://shop.com\"\n"), "APP_URL"); !ok || v != "https://shop.com" {
		t.Errorf("APP_URL %q", v)
	}
}

// loyalty is a WordPress fixture with a plugin table no profile knows.
func loyalty(n int) func() []tableData {
	return loyaltyCols(n, "id:int(11)", "email", "phone")
}

// loyaltyUntyped is loyalty as a data-only dump reads it: the INSERT
// names the columns and nothing types them.
func loyaltyUntyped(n int) func() []tableData {
	return loyaltyCols(n, "id:", "email:", "phone:")
}

func loyaltyCols(n int, cols ...string) func() []tableData {
	return func() []tableData {
		var rs rows
		for i := range n {
			rs = append(rs, []any{i, fmt.Sprintf("m%d@example.com", i), ""})
		}
		return append(wpUsers(), tableData{table("wp_loyalty_members", cols...), rs})
	}
}

// An uncovered personal column stops copy before anything is written
// (exit 1, PRD 17); --allow-uncovered goes on and lists it; plan never
// stops. detect.Uncovered sees at most 1,000 non-empty samples.
func TestPipelineUncovered(t *testing.T) {
	useFakes(t)
	calls := fakeUncovered(t)
	p, sink, in := fixture(t, Copy, "wp_", loyalty(1500), nil, Options{})
	out, err := p.run(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Exit != 1 || ce.Msg != "uncovered: 2 columns look personal and no rule covers them: wp_loyalty_members.email, wp_loyalty_members.phone. Add them under columns: (rhythm-roadie explain uncovered-column), or --allow-uncovered to copy and list them as findings. The target is untouched." {
		t.Fatalf("err %v", err)
	}
	if len(sink.Batches()) != 0 || sink.Committed() || out.Result.TargetState != core.Untouched || !slices.Equal(in.opened, []int{1}) {
		t.Errorf("wrote %d batches, state %s, passes %v", len(sink.Batches()), out.Result.TargetState, in.opened)
	}
	for _, c := range *calls {
		if c.table == "wp_loyalty_members" && (len(c.samples["email"]) != maxSamples || len(c.samples["phone"]) != 0) {
			t.Errorf("samples: %d emails, %d phones", len(c.samples["email"]), len(c.samples["phone"]))
		}
		if c.table == "wp_users" {
			t.Error("a table every text column of which is covered reached detect")
		}
	}
	p, sink, _ = fixture(t, Copy, "wp_", loyalty(10), nil, Options{AllowUncovered: true})
	out, err = p.run(context.Background())
	if err != nil || !sink.Committed() {
		t.Fatalf("allow-uncovered: %v", err)
	}
	if len(out.Result.Uncovered) != 2 || !slices.ContainsFunc(out.Result.Findings, func(f core.Finding) bool { return f.ID == core.FindingUncoveredColumn }) {
		t.Errorf("findings %+v", out.Result.Findings)
	}
	p, _, _ = fixture(t, Plan, "wp_", loyalty(10), nil, Options{})
	p.sink = nil
	if _, err := p.run(context.Background()); err != nil {
		t.Errorf("plan stopped: %v", err)
	}
}

// An untyped personal column, as a data-only dump gives it, stops copy
// the same way: its values are sampled and detect sees it.
func TestPipelineUncoveredUntyped(t *testing.T) {
	useFakes(t)
	calls := fakeUncovered(t)
	p, sink, _ := fixture(t, Copy, "wp_", loyaltyUntyped(30), nil, Options{})
	_, err := p.run(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrUncovered || ce.Exit != 1 ||
		!strings.Contains(ce.Msg, "wp_loyalty_members.email, wp_loyalty_members.phone") {
		t.Fatalf("err %v", err)
	}
	if len(sink.Batches()) != 0 || sink.Committed() {
		t.Errorf("wrote %d batches", len(sink.Batches()))
	}
	saw := false
	for _, c := range *calls {
		if c.table == "wp_loyalty_members" {
			saw = len(c.samples["email"]) == 30
		}
	}
	if !saw {
		t.Error("the untyped email column wasn't sampled for detect")
	}
}

// fileLikeSink is a recording sink whose Abort takes everything back,
// as a dump-file sink does by removing its temp file.
type fileLikeSink struct{ *testutil.Sink }

func (s fileLikeSink) State() core.TargetState {
	if s.Aborted() {
		return core.Untouched
	}
	return s.Sink.State()
}

// With --one-pass the uncovered check runs after pass 2 wrote, so a
// database target the abort leaves mixed is reported as mixed in the
// error and the result, never as untouched; a file target the abort
// takes back stays untouched.
func TestPipelineOnePassUncoveredState(t *testing.T) {
	useFakes(t)
	fakeUncovered(t)
	p, sink, _ := fixture(t, Copy, "wp_", loyalty(10), nil, Options{OnePass: true})
	out, err := p.run(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrUncovered || ce.Exit != 1 {
		t.Fatalf("err %v", err)
	}
	if len(sink.Batches()) == 0 || !sink.Aborted() || sink.Committed() {
		t.Fatalf("wrote %d batches, aborted %v", len(sink.Batches()), sink.Aborted())
	}
	if ce.State != core.Mixed || out.Result.TargetState != core.Mixed || strings.Contains(ce.Msg, "untouched") ||
		!strings.HasSuffix(ce.Msg, "The target is in a mixed state and may hold the columns as they were in production: run the same command again once they have a rule, or without --one-pass to check before writing.") {
		t.Errorf("state %s, result %s, msg %q", ce.State, out.Result.TargetState, ce.Msg)
	}

	p, sink, _ = fixture(t, Copy, "wp_", loyalty(10), nil, Options{OnePass: true})
	p.sink = fileLikeSink{sink}
	out, err = p.run(context.Background())
	if !errors.As(err, &ce) || ce.State != core.Untouched || out.Result.TargetState != core.Untouched ||
		!strings.HasSuffix(ce.Msg, "The target is untouched.") {
		t.Errorf("file target: err %v, result %s", err, out.Result.TargetState)
	}
}

// A dump with no stack and no columns: stack (exit 2) before writing.
func TestPipelineNoStack(t *testing.T) {
	useFakes(t)
	tables := func() []tableData {
		return []tableData{{table("customers", "id:int(11)", "email"), rows{{1, "a@example.com"}}}}
	}
	p, sink, _ := fixture(t, Copy, "", tables, nil, Options{})
	if _, err := p.run(context.Background()); !isCode(err, core.ErrStack) || len(sink.Batches()) != 0 {
		t.Errorf("err %v", err)
	}
}

func TestGuessPrefix(t *testing.T) {
	s := &core.Schema{Tables: []*core.Table{table("wp_2_commentmeta", "meta_id", "comment_id"), table("wp_2_comments", "comment_ID")}}
	if p, ok := guessPrefix(s); !ok || p != "wp_" {
		t.Errorf("blog tables first: %q", p)
	}
	ps := provisionalSchema(s)
	if ps.Prefix != "wp_" || ps.Table("wp_2_options") == nil || s.Table("wp_2_options") != nil {
		t.Error("provisional schema")
	}
	if !slices.Equal(profile.Sites(ps, "wp_"), []string{"2_"}) {
		t.Errorf("sites %v", profile.Sites(ps, "wp_"))
	}
	if p, ok := guessPrefix(&core.Schema{Tables: []*core.Table{table("shop_posts", "ID", "post_author")}}); !ok || p != "shop_" {
		t.Errorf("shop_: %q", p)
	}
	if _, ok := guessPrefix(&core.Schema{Tables: []*core.Table{table("orders", "id")}}); ok {
		t.Error("guessed a prefix for a non-WordPress schema")
	}
	for in, want := range map[string]string{"wp_2_": "wp_", "wp_": "wp_", "wp_12_": "wp_", "x2_": "x2_", "2_": "2_"} {
		if got := stripBlog(in); got != want {
			t.Errorf("stripBlog(%q) %q", in, got)
		}
	}
}

// A dump file is opened once per pass and closed after each.
func TestFileInput(t *testing.T) {
	useFakes(t)
	file := t.TempDir() + "/prod.sql"
	if err := os.WriteFile(file, []byte("-- dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldOpen := openDump
	var reads []string
	openDump = func(_ context.Context, r io.Reader, o dump.Options) (core.Source, error) {
		b, _ := io.ReadAll(r)
		reads = append(reads, o.Name+":"+string(b))
		return newGenSource(3, 0), nil
	}
	t.Cleanup(func() { openDump = oldOpen })
	defs, _ := profile.Builtin()
	p := &pipeline{mode: Copy, cfg: sqlConfig("email"), defs: defs, sink: testutil.FakeSink(),
		in: &fileInput{path: file, o: dump.Options{Name: "prod.sql"}}}
	if _, err := p.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reads, []string{"prod.sql:-- dump\n", "prod.sql:-- dump\n"}) {
		t.Errorf("reads %q", reads)
	}
	if _, err := (&fileInput{path: file + ".missing"}).open(context.Background(), 1); err == nil {
		t.Error("a missing file opened")
	}
}

// A site's database is read through Site.Source once per pass.
func TestSiteInput(t *testing.T) {
	useFakes(t)
	st := testutil.FakeSite(nil)
	n := 0
	st.SourceFunc = func(tables []string) (core.Source, error) {
		n++
		if tables != nil {
			t.Errorf("tables %v", tables)
		}
		return newGenSource(2, 0), nil
	}
	defs, _ := profile.Builtin()
	p := &pipeline{mode: Copy, cfg: sqlConfig("email"), defs: defs, sink: testutil.FakeSink(), in: &siteInput{s: st}}
	if _, err := p.run(context.Background()); err != nil || n != 2 {
		t.Fatalf("%d reads, %v", n, err)
	}
}

// The spool lives in the cache folder, 0700.
func TestSpoolDir(t *testing.T) {
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	dir, err := spoolDir()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || filepath.Base(dir) != "spool" || filepath.Base(filepath.Dir(dir)) != "roadie" {
		t.Fatalf("%s: %v", dir, err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
}

// --schema is read to its end through dump.Open for its CREATE TABLEs.
func TestReadSchemaFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "schema.sql")
	if err := os.WriteFile(file, []byte("CREATE TABLE ..."), 0o600); err != nil {
		t.Fatal(err)
	}
	oldOpen := openDump
	g := newGenSource(5, 0)
	openDump = func(_ context.Context, _ io.Reader, o dump.Options) (core.Source, error) {
		if o.Name != "schema.sql" {
			t.Errorf("name %q", o.Name)
		}
		return g, nil
	}
	t.Cleanup(func() { openDump = oldOpen })
	s, err := readSchemaFile(context.Background(), file)
	if err != nil || s.Table("t") == nil || g.next != 5 {
		t.Fatalf("schema %v, read %d batches: %v", s, g.next, err)
	}
}

// progress records the phases of a run.
type progress struct{ phases []string }

func (p *progress) Phase(name string, _ int64) { p.phases = append(p.phases, name) }
func (p *progress) Advance(int64)              {}
func (p *progress) Table(string, int64)        {}
func (p *progress) Note(string)                {}

// The phases are index then transform, or import for a database target;
// --max-memory is set for the run and put back after.
func TestPipelinePhasesAndMemoryLimit(t *testing.T) {
	useFakes(t)
	before := debug.SetMemoryLimit(-1)
	for _, importing := range []bool{false, true} {
		p, _ := genPipeline(t, 3, 0, sqlConfig("email"))
		pr := &progress{}
		p.progress, p.importing = pr, importing
		p.opts.MaxMemory = 512 << 20
		p.newWorker = func(*shared, core.Faker) batchWorker {
			return limitWorker{t}
		}
		if _, err := p.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := []string{"index", "transform"}
		if importing {
			want[1] = "import"
		}
		if !slices.Equal(pr.phases, want) {
			t.Errorf("phases %v", pr.phases)
		}
	}
	if after := debug.SetMemoryLimit(-1); after != before {
		t.Errorf("memory limit left at %d, was %d", after, before)
	}
}

// Without --max-memory a run sets the default soft limit, unless one
// (GOMEMLIMIT) is already set; it puts the limit back after.
func TestPipelineDefaultMemoryLimit(t *testing.T) {
	useFakes(t)
	before := debug.SetMemoryLimit(math.MaxInt64)
	defer debug.SetMemoryLimit(before)
	var seen []int64
	var mu sync.Mutex
	for _, preset := range []int64{math.MaxInt64, 700 << 20} {
		debug.SetMemoryLimit(preset)
		p, _ := genPipeline(t, 3, 0, sqlConfig("email"))
		p.newWorker = func(*shared, core.Faker) batchWorker { return seenWorker{&mu, &seen} }
		if _, err := p.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if after := debug.SetMemoryLimit(-1); after != preset {
			t.Errorf("memory limit left at %d, was %d", after, preset)
		}
	}
	if len(seen) == 0 || seen[0] != defaultMemory || seen[len(seen)-1] != 700<<20 {
		t.Errorf("limits during the runs %v", seen)
	}
	if windowFor(0) != windowBytes || windowFor(200<<20) != 50<<20 || windowFor(1<<20) != core.MaxBatchBytes || windowFor(4<<30) != windowBytes {
		t.Error("windowFor")
	}
}

// seenWorker records the memory limit while the run transforms.
type seenWorker struct {
	mu   *sync.Mutex
	seen *[]int64
}

func (w seenWorker) transform(context.Context, *core.Batch, *profile.Compiled) (*batchOut, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.seen = append(*w.seen, debug.SetMemoryLimit(-1))
	return &batchOut{}, nil
}

// limitWorker checks the memory limit is set while the run transforms.
type limitWorker struct{ t *testing.T }

func (w limitWorker) transform(context.Context, *core.Batch, *profile.Compiled) (*batchOut, error) {
	if got := debug.SetMemoryLimit(-1); got != 512<<20 {
		w.t.Errorf("memory limit %d during the run", got)
	}
	return &batchOut{}, nil
}

// mysqldump and wp db export write tables by name, so a network's
// wp_blogs and wp_site rows come before wp_options says the prefix. The
// blog hosts must still be read in pass 1: mapped ones rewritten and an
// unmapped custom domain refused.
func TestPipelineMultisiteDumpOrder(t *testing.T) {
	useFakes(t)
	oldOpen := openDump
	openDump = dump.Open
	t.Cleanup(func() { openDump = oldOpen })
	file := filepath.Join(t.TempDir(), "network.sql")
	text := "CREATE TABLE `wp_blogs` (`blog_id` bigint(20) NOT NULL, `site_id` bigint(20) NOT NULL, `domain` varchar(200) NOT NULL, `path` varchar(100) NOT NULL, `lang_id` int(11) NOT NULL);\n" +
		"INSERT INTO `wp_blogs` VALUES (1,1,'shop.com','/',0),(2,1,'fr.shop.com','/',0),(3,1,'shop.de','/',0);\n" +
		"CREATE TABLE `wp_options` (`option_id` bigint(20) NOT NULL, `option_name` varchar(191) NOT NULL, `option_value` longtext NOT NULL);\n" +
		"INSERT INTO `wp_options` VALUES (1,'siteurl','https://shop.com'),(2,'home','https://shop.com');\n" +
		"CREATE TABLE `wp_site` (`id` bigint(20) NOT NULL, `domain` varchar(200) NOT NULL, `path` varchar(100) NOT NULL);\n" +
		"INSERT INTO `wp_site` VALUES (1,'shop.com','/');\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, _ := profile.Builtin()
	run := func(cfg *roadiecfg.Config) (*testutil.Sink, error) {
		sink := testutil.FakeSink()
		p := &pipeline{mode: Copy, cfg: cfg, defs: defs, sink: sink, opts: Options{URL: "https://staging.shop.test"},
			in: &fileInput{path: file, o: dump.Options{Name: "network.sql"}}}
		_, err := p.run(context.Background())
		return sink, err
	}
	_, err := run(roadiecfg.Default())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrURLMap || ce.Msg != "url-map: shop.de has no staging URL; add --url-map shop.de=shop.staging.shop.test." {
		t.Fatalf("unmapped domain: %v", err)
	}
	cfg := roadiecfg.Default()
	cfg.URLMap = map[string]string{"shop.de": "de.staging.shop.test"}
	sink, err := run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"staging.shop.test", "fr.staging.shop.test", "de.staging.shop.test"} {
		if v, _ := valueOf(t, sink, "wp_blogs", i, "domain"); v != want {
			t.Errorf("blog %d domain %q, want %q", i+1, v, want)
		}
	}
	if v, _ := valueOf(t, sink, "wp_site", 0, "domain"); v != "staging.shop.test" {
		t.Errorf("network domain %q", v)
	}
}

// An app's own "site" or "blogs" table is no WordPress core table.
func TestGuessPrefixNetworkTables(t *testing.T) {
	s := &core.Schema{Tables: []*core.Table{{Name: "app_site", Columns: []core.Column{{Name: "id"}, {Name: "domain"}}},
		{Name: "blogs", Columns: []core.Column{{Name: "blog_id"}, {Name: "lang_id"}}}}}
	if p, ok := guessPrefix(s); ok {
		t.Errorf("guessed %q from app tables", p)
	}
	s.Tables = append(s.Tables, &core.Table{Name: "wp_sitemeta", Columns: []core.Column{{Name: "meta_id"}, {Name: "site_id"}, {Name: "meta_key"}}})
	if p, ok := guessPrefix(s); !ok || p != "wp_" {
		t.Errorf("guessed %q %v from wp_sitemeta", p, ok)
	}
}

// A dump of a few WordPress tables (wp db export --tables=wp_comments)
// has no options table; its core tables still give the prefix, so the
// WordPress rules apply instead of a stop for an unknown stack.
func TestPipelinePartialWordPressDump(t *testing.T) {
	useFakes(t)
	p, sink, _ := fixture(t, Copy, "", func() []tableData {
		return []tableData{{table("wp_comments", "comment_ID:bigint(20)", "comment_author", "comment_author_email"), rows{{1, "Anna Lee", "anna.lee@example.com"}}}}
	}, nil, Options{URL: "https://staging.shop.test"})
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Result.Stack.Name != core.StackWordPress {
		t.Errorf("stack %+v", out.Result.Stack)
	}
	if v, _ := valueOf(t, sink, "wp_comments", 0, "comment_author_email"); v == "anna.lee@example.com" {
		t.Error("the comment's email wasn't faked")
	}
}
