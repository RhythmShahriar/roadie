// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/maphash"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/engine/testdata/gen"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// countSink counts what a run writes and keeps nothing, so a benchmark
// measures the engine, not a buffer.
type countSink struct {
	mu        sync.Mutex
	batches   int64
	committed bool
}

func (s *countSink) Write(context.Context, *core.Batch) error {
	s.mu.Lock()
	s.batches++
	s.mu.Unlock()
	return nil
}
func (s *countSink) Commit(context.Context) error { s.committed = true; return nil }
func (s *countSink) Abort(context.Context) error  { return nil }
func (s *countSink) State() core.TargetState {
	if s.committed {
		return core.Replaced
	}
	return core.Untouched
}

// hashKnown is a Known shaped like pseudo's (PRD 10.1: keyed 8-byte
// hashes, about 16 MB for 2 million tokens): a sorted slice, so a
// benchmark's memory is the engine's, not a fake's map of strings.
type hashKnown struct {
	mu     sync.Mutex
	seed   maphash.Seed
	hashes []uint64
}

func newHashKnown() *hashKnown { return &hashKnown{seed: maphash.MakeSeed()} }

func (k *hashKnown) sum(v []byte) uint64 {
	return maphash.Bytes(k.seed, bytes.ToLower(bytes.TrimSpace(v)))
}

func (k *hashKnown) Add(_ core.FakeKind, v []byte) {
	h := k.sum(v)
	k.mu.Lock()
	k.hashes = append(k.hashes, h)
	k.mu.Unlock()
}

func (k *hashKnown) Seal() {
	k.mu.Lock()
	slices.Sort(k.hashes)
	k.hashes = slices.Clip(slices.Compact(k.hashes))
	k.mu.Unlock()
}

func (k *hashKnown) Contains(t []byte) (core.FakeKind, string, bool) {
	_, ok := slices.BinarySearch(k.hashes, k.sum(t))
	return "", "raw", ok
}

func (k *hashKnown) Len() int { return len(k.hashes) }

// timing is a Progress that times each pass.
type timing struct {
	mu     sync.Mutex
	starts map[string]time.Time
	order  []string
}

func (p *timing) Phase(name string, _ int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.starts == nil {
		p.starts = map[string]time.Time{}
	}
	p.starts[name] = time.Now()
	p.order = append(p.order, name)
}
func (p *timing) Advance(int64)            {}
func (p *timing) Table(string, int64)      {}
func (p *timing) Note(string)              {}
func (p *timing) start(n string) time.Time { p.mu.Lock(); defer p.mu.Unlock(); return p.starts[n] }

// peakRSS is the process's peak resident memory in bytes (VmHWM), 0
// where /proc isn't there.
func peakRSS() int64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmHWM:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10
		}
	}
	return 0
}

// speed is one measured run.
type speed struct {
	bytes            int64
	index, transform time.Duration
	rss              int64
}

func (s speed) mbps(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(s.bytes) / (1 << 20) / d.Seconds()
}

// genOptions is the generated shop of one size.
func genOptions(size int64) gen.Options {
	return gen.Options{Size: size, Seed: 42, LiveKey: liveKey}
}

// copyGenerated runs copy over a generated shop of size bytes with eight
// workers: from generated batches (no SQL), or with file set through the
// real dump reader over a file.
func copyGenerated(tb testing.TB, size int64, file string) speed {
	tb.Helper()
	defs := mustDefs(tb)
	cfg := roadiecfg.Default()
	cfg.Production = []string{"shop.example.com"}
	cfg.Keys.TestKeys = map[string]string{"stripe": testKey}
	var in input = &funcInput{fn: func(int) (core.Source, error) { return gen.Source(genOptions(size)), nil }}
	if file != "" {
		in = &fileInput{path: file, o: dump.Options{Name: filepath.Base(file)}}
	}
	newKnown = func(pseudo.Key) core.Known { return newHashKnown() }
	prog := &timing{}
	sink := &countSink{}
	p := &pipeline{mode: Copy, cfg: cfg, opts: Options{URL: "https://staging.shop.test", Jobs: 8, MaxMemory: benchMemory}, defs: defs,
		in: in, sink: sink, progress: prog}
	runtime.GC()
	out, err := p.run(context.Background())
	end := time.Now()
	if err != nil {
		tb.Fatal(err)
	}
	if out.Result.Input == nil || !sink.committed {
		tb.Fatal("no input fingerprint or no commit")
	}
	return speed{bytes: out.Result.Input.Bytes, index: prog.start("transform").Sub(prog.start("index")),
		transform: end.Sub(prog.start("transform")), rss: peakRSS()}
}

func mustDefs(tb testing.TB) []profile.Def {
	tb.Helper()
	defs, err := profile.Builtin()
	if err != nil {
		tb.Fatal(err)
	}
	return defs
}

// report puts a run's numbers on the benchmark line.
func report(b *testing.B, s speed) {
	b.ReportMetric(s.mbps(s.index), "index-MB/s")
	b.ReportMetric(s.mbps(s.transform), "transform-MB/s")
	b.ReportMetric(float64(s.rss)/(1<<20), "peak-RSS-MB")
	b.SetBytes(s.bytes)
}

// BenchmarkCopy200MB copies a 200 MB WooCommerce shop with eight jobs,
// from generated batches: the engine's two passes without the dump
// reader. The other lanes' functions are the fakes until integration.
func BenchmarkCopy200MB(b *testing.B) {
	useFakes(b)
	for b.Loop() {
		report(b, copyGenerated(b, 200<<20, ""))
	}
}

// BenchmarkCopy200MBDump is the same through a dump file and the real
// reader, once the io lane has built it.
func BenchmarkCopy200MBDump(b *testing.B) {
	useFakes(b)
	openDump = dump.Open
	if _, err := dump.Open(context.Background(), strings.NewReader(""), dump.Options{}); errors.Is(err, core.ErrNotBuilt) {
		b.Skip("needs the io lane")
	}
	file := filepath.Join(b.TempDir(), "shop.sql")
	writeDump(b, file, 200<<20)
	for b.Loop() {
		report(b, copyGenerated(b, 200<<20, file))
	}
}

// writeDump writes a generated shop to file.
func writeDump(tb testing.TB, file string, size int64) {
	tb.Helper()
	f, err := os.Create(file) // #nosec G304 -- a temp file of the test
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := gen.Write(w, genOptions(size)); err != nil {
		tb.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
}

// TestCopyLarge runs 200 MB and 2 GB copies (ROADIE_BENCH_LARGE=1) and
// logs MB/s per pass and peak RSS, against PRD 22.3: index 300 MB/s,
// transform 100 MB/s, RSS 300 MB at eight jobs and within 1.5x between
// the two sizes.
func TestCopyLarge(t *testing.T) {
	if os.Getenv("ROADIE_BENCH_LARGE") != "1" {
		t.Skip("set ROADIE_BENCH_LARGE=1 to run the 2 GB copy")
	}
	useFakes(t)
	small := copyGenerated(t, 200<<20, "")
	large := copyGenerated(t, 2<<30, "")
	for _, s := range []speed{small, large} {
		t.Logf("%d MB: index %.0f MB/s, transform %.0f MB/s, peak RSS %d MB",
			s.bytes>>20, s.mbps(s.index), s.mbps(s.transform), s.rss>>20)
	}
	if small.rss > 0 && float64(large.rss) > 1.5*float64(small.rss) {
		t.Errorf("peak RSS grew from %d MB to %d MB", small.rss>>20, large.rss>>20)
	}
}

// The generator gives the same bytes for the same options, holds a
// table per WooCommerce part and lands near the size asked for.
func TestGenerator(t *testing.T) {
	o := genOptions(1 << 20)
	a, b := gen.Bytes(o), gen.Bytes(o)
	if !bytes.Equal(a, b) {
		t.Fatal("two runs differ")
	}
	if n := int64(len(a)); n < o.Size/2 || n > o.Size*2 {
		t.Errorf("asked for %d bytes, got %d", o.Size, n)
	}
	for _, want := range []string{"CREATE TABLE `wp_wc_orders`", "CREATE TABLE `wp_actionscheduler_actions`", "woocommerce_stripe_settings",
		liveKey, "-- Dump completed", "@example.com", "+44 7700 900"} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("dump lacks %q", want)
		}
	}
	o.Seed++
	if bytes.Equal(a, gen.Bytes(o)) {
		t.Error("another seed gave the same dump")
	}
	// The batch source holds the same rows as the SQL.
	src := gen.Source(genOptions(1 << 20))
	defer func() { _ = src.Close() }()
	rows := 0
	for {
		b, err := src.Next(context.Background())
		if err != nil {
			break
		}
		rows += len(b.Rows)
	}
	if want := bytes.Count(a, []byte("),(")) + bytes.Count(a, []byte("VALUES (")); rows != want {
		t.Errorf("source has %d rows, the dump %d", rows, want)
	}
}

// A copy with a stable key gives byte-identical output and the same
// result JSON every time, with eight workers (run with -count=3 too).
func TestRunDeterminism(t *testing.T) {
	f := useRunFakes(t)
	f.prod.SourceFunc = func([]string) (core.Source, error) { return gen.Source(genOptions(4 << 20)), nil }
	var sinks []*testutil.Sink
	newFileSink = func(string, dump.WriterOptions) (core.Sink, error) {
		s := testutil.FakeSink()
		sinks = append(sinks, s)
		return s, nil
	}
	runKey = func(string, *roadiecfg.Config, bool, func(string) (string, bool)) (pseudo.Key, string, bool, error) {
		return pseudo.Key{7, 7, 7}, roadiecfg.KeyFromFile, true, nil
	}
	out := filepath.Join(t.TempDir(), "staging.sql")
	var first, firstJSON []byte
	for i := range 3 {
		j := f.job(Copy)
		j.To = core.Endpoint{Kind: core.DumpFile, Raw: out, Display: "staging.sql", Path: out}
		j.Clock = testutil.FixedClock(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
		j.Opts.Jobs = 8
		j.Config.Production = []string{"shop.example.com"}
		j.Config.Keys.TestKeys = map[string]string{"stripe": testKey}
		res, err := Run(context.Background(), j)
		if err != nil {
			t.Fatal(err)
		}
		got := sinks[len(sinks)-1].Bytes()
		js, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first, firstJSON = got, js
			if res.Consistency.Mode != "stable" || len(got) == 0 {
				t.Fatalf("consistency %+v, %d bytes", res.Consistency, len(got))
			}
			continue
		}
		if !bytes.Equal(got, first) {
			t.Errorf("run %d wrote different bytes", i+1)
		}
		if !bytes.Equal(js, firstJSON) {
			t.Errorf("run %d gave a different result:\n%s\n%s", i+1, js, firstJSON)
		}
	}
	for _, leak := range []string{"@example.com", liveKey, "https://shop.example.com"} {
		if bytes.Contains(first, []byte(leak)) {
			t.Errorf("output keeps %q", leak)
		}
	}
}

// benchMemory is the --max-memory of the benchmarks; ROADIE_BENCH_MEMORY
// (bytes) changes it to see how the soft limit trades speed for RSS.
var benchMemory = func() int64 {
	if v, err := strconv.ParseInt(os.Getenv("ROADIE_BENCH_MEMORY"), 10, 64); err == nil {
		return v
	}
	return 0
}()
