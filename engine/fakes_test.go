// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// The fakes below stand in for the other lanes' packages while those are
// stubs. Each is as small as the engine's tests need and no smaller:
// JSON leaves, email spans, Stripe-shaped keys and plain URL pairs.

// hashFaker is a core.Faker whose fakes depend only on the key, the kind
// and the value, so they are the same in every worker and every run with
// the same key, as the real faker's are.
type hashFaker struct{ key string }

func (f hashFaker) Fake(kind core.FakeKind, original []byte, o core.FakeOpts) ([]byte, error) {
	sum := sha256.Sum256([]byte(f.key + "\x00" + string(kind) + "\x00" + strings.ToLower(strings.TrimSpace(string(original)))))
	h := hex.EncodeToString(sum[:])[:12]
	var out string
	switch kind {
	case core.KEmail:
		out = "u" + h + "@example.invalid"
	case core.KUserAgent:
		out = "Mozilla/5.0 (Roadie staging copy)"
	default:
		out = string(kind) + "-" + h
	}
	if o.MaxLen > 0 && len(out) > o.MaxLen {
		out = out[:o.MaxLen]
	}
	return []byte(out), nil
}

func (f hashFaker) Clone() core.Faker { return f }

// jsonLeaves walks a JSON document's string leaves in document order with
// nested's path syntax ("json:a.b[2]") and calls fn; it rebuilds the
// document only when a leaf changed.
func jsonLeaves(b []byte, fn func(path string, leaf []byte) ([]byte, bool)) ([]byte, bool, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false, false
	}
	changed := false
	var walk func(p string, v any) any
	walk = func(p string, v any) any {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				sep := "."
				if p == "" {
					sep = ""
				}
				x[k] = walk(p+sep+strings.ReplaceAll(k, ".", `\.`), x[k])
			}
			return x
		case []any:
			for i := range x {
				x[i] = walk(p+"["+strconv.Itoa(i)+"]", x[i])
			}
			return x
		case string:
			nv, ch := fn("json:"+p, []byte(x))
			if ch {
				changed = true
				return string(nv)
			}
		}
		return v
	}
	v = walk("", v)
	if !changed {
		return b, false, true
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false, false
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), true, true
}

func isJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(t)
}

func fakeRewrite(b []byte, fn func(path string, leaf []byte) ([]byte, bool), _ nested.Options) ([]byte, bool, error) {
	if fn == nil {
		return b, false, nil
	}
	if isJSON(b) {
		if out, ch, ok := jsonLeaves(b, fn); ok {
			return out, ch, nil
		}
	}
	out, ch := fn("", b)
	return out, ch, nil
}

func fakeLeaves(b []byte, fn func(path string, leaf []byte), o nested.Options) error {
	_, _, err := fakeRewrite(b, func(p string, leaf []byte) ([]byte, bool) { fn(p, leaf); return leaf, false }, o)
	return err
}

func fakeFormat(b []byte) string {
	if isJSON(b) {
		return "json"
	}
	return formatText
}

// fakeScrub replaces the email spans of text by their fakes.
func fakeScrub(text []byte, sp core.Spanner, _ core.Known, f core.Faker) ([]byte, int) {
	if sp == nil {
		return text, 0
	}
	spans := sp.Spans(text)
	if len(spans) == 0 {
		return text, 0
	}
	var out []byte
	last := 0
	for _, s := range spans {
		fake, _ := f.Fake(s.Kind, text[s.Start:s.End], core.FakeOpts{})
		out = append(append(out, text[last:s.Start]...), fake...)
		last = s.End
	}
	return append(out, text[last:]...), len(spans)
}

// liveKey and testKey are Stripe-shaped keys assembled at run time
// (push protection blocks literal ones).
var (
	liveKey = testutil.J("sk_", "live_", "51Habcdefghijklmnop")
	testKey = testutil.J("sk_", "test_", "51Hqrstuvwxyzabcdef")
)

// fakeScanKeys finds Stripe secret keys: live when sk_live_, test when
// sk_test_.
func fakeScanKeys(v []byte, at core.Location, _ map[string]string) []core.KeyHit {
	var hits []core.KeyHit
	for _, env := range []string{"live", "test"} {
		needle := testutil.J("sk_", env, "_")
		for off := 0; ; {
			i := bytes.Index(v[off:], []byte(needle))
			if i < 0 {
				break
			}
			hits = append(hits, core.KeyHit{ID: "key.stripe-" + env, Provider: "stripe", Env: env, Severity: core.SeverityCritical,
				At: at, Masked: core.MaskKey(string(v[off+i:]))})
			off += i + len(needle)
		}
	}
	return hits
}

var stripeKeyRe = func(b []byte) [][2]int {
	var out [][2]int
	needle := []byte(testutil.J("sk_", "live_"))
	for off := 0; ; {
		i := bytes.Index(b[off:], needle)
		if i < 0 {
			return out
		}
		s := off + i
		e := s + len(needle)
		for e < len(b) && (b[e] >= 'a' && b[e] <= 'z' || b[e] >= 'A' && b[e] <= 'Z' || b[e] >= '0' && b[e] <= '9') {
			e++
		}
		out = append(out, [2]int{s, e})
		off = e
	}
}

// fakeReplaceKey swaps every live key in value for the stripe test key,
// or blanks it.
func fakeReplaceKey(_ core.KeyHit, value []byte, mode string, testKeys map[string]string) ([]byte, string, error) {
	to, action := "", "blanked"
	if mode != "blank" && testKeys["stripe"] != "" {
		to, action = testKeys["stripe"], "swapped"
	}
	var out []byte
	last := 0
	for _, r := range stripeKeyRe(value) {
		out = append(append(out, value[last:r[0]]...), to...)
		last = r[1]
	}
	return append(out, value[last:]...), action, nil
}

// pairMap is a urlRewriter over plain From/To pairs: both schemes of a
// From origin become To.
type pairMap struct{ pairs []urlrw.Pair }

func (m *pairMap) Rewrite(text []byte) ([]byte, int) {
	n := 0
	out := text
	for _, p := range m.pairs {
		host := p.From
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		for _, scheme := range []string{"https://", "http://"} {
			from := []byte(scheme + host)
			if c := bytes.Count(out, from); c > 0 {
				n += c
				out = bytes.ReplaceAll(out, from, []byte(p.To))
			}
		}
	}
	return out, n
}

func (m *pairMap) RewriteHost(host []byte) ([]byte, bool) {
	for _, p := range m.pairs {
		from, to := p.From, p.To
		if i := strings.Index(from, "://"); i >= 0 {
			from = from[i+3:]
		}
		if i := strings.Index(to, "://"); i >= 0 {
			to = to[i+3:]
		}
		if strings.EqualFold(string(host), from) {
			return []byte(to), true
		}
	}
	return host, false
}

// memSpool is an in-memory spool that remembers whether it was removed.
type memSpool struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	removed bool
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (s *memSpool) Writer() io.WriteCloser { return nopWriteCloser{&lockedWriter{s}} }
func (s *memSpool) Reader() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.NopCloser(bytes.NewReader(bytes.Clone(s.buf.Bytes()))), nil
}
func (s *memSpool) Remove() error { s.mu.Lock(); s.removed = true; s.mu.Unlock(); return nil }
func (s *memSpool) Removed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.removed }

type lockedWriter struct{ s *memSpool }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.s.buf.Write(p)
}

// fakes swaps every other lane's function for its fake and puts them back
// when the test ends. Tests that call it don't run in parallel.
type fakes struct {
	known *testutil.Known
	spool *memSpool
}

func useFakes(t testing.TB) *fakes {
	t.Helper()
	f := &fakes{known: testutil.FakeKnown(), spool: &memSpool{}}
	saved := []func(){}
	swap := func(restore func()) { saved = append(saved, restore) }
	{
		old := newFaker
		newFaker = func(k pseudo.Key, _ pseudo.Options) (core.Faker, error) { return hashFaker{key: string(k[:])}, nil }
		swap(func() { newFaker = old })
	}
	{
		old := newKnown
		newKnown = func(pseudo.Key) core.Known { return f.known }
		swap(func() { newKnown = old })
	}
	{
		old := textSpans
		textSpans = func() core.Spanner { return testutil.FakeSpanner() }
		swap(func() { textSpans = old })
	}
	{
		old := scrubText
		scrubText = fakeScrub
		swap(func() { scrubText = old })
	}
	{
		old := resetHash
		resetHash = func(core.Stack, pseudo.Key, []byte) ([]byte, error) { return []byte("$P$roadieRESET"), nil }
		swap(func() { resetHash = old })
	}
	{
		old := passwordHash
		passwordHash = func(_ core.Stack, pw []byte) ([]byte, error) {
			return []byte("$P$hash-of-" + strconv.Itoa(len(pw))), nil
		}
		swap(func() { passwordHash = old })
	}
	{
		old1, old2, old3 := nestedRewrite, nestedLeaves, nestedFormat
		nestedRewrite, nestedLeaves, nestedFormat = fakeRewrite, fakeLeaves, fakeFormat
		swap(func() { nestedRewrite, nestedLeaves, nestedFormat = old1, old2, old3 })
	}
	{
		old1, old2 := scanKeys, replaceKey
		scanKeys, replaceKey = fakeScanKeys, fakeReplaceKey
		swap(func() { scanKeys, replaceKey = old1, old2 })
	}
	{
		old := newURLMap
		newURLMap = func(p []urlrw.Pair) (urlRewriter, error) { return &pairMap{pairs: p}, nil }
		swap(func() { newURLMap = old })
	}
	{
		old := newSpool
		newSpool = func(string) (spool, error) { return f.spool, nil }
		swap(func() { newSpool = old })
	}
	{
		old := detectUncovered
		detectUncovered = func(*core.Table, map[string]bool, map[string][][]byte) []core.Uncovered { return nil }
		swap(func() { detectUncovered = old })
	}
	t.Cleanup(func() {
		for i := len(saved) - 1; i >= 0; i-- {
			saved[i]()
		}
	})
	return f
}

// funcInput is an input over a function that builds a fresh source per
// pass (pass 2 changes the rows it is given).
type funcInput struct {
	fn     func(pass int) (core.Source, error)
	opened []int
	closed bool
}

func (f *funcInput) open(_ context.Context, pass int) (core.Source, error) {
	f.opened = append(f.opened, pass)
	return f.fn(pass)
}
func (f *funcInput) close() error { f.closed = true; return nil }

// rows is a table's rows for a fixture: each value a string, or nil for
// NULL.
type rows [][]any

// tableData is one table of a fixture dump.
type tableData struct {
	t    *core.Table
	rows rows
}

// batches turns fixture tables into batches, one per table, with a
// CREATE TABLE statement before each.
func batches(tables []tableData) []*core.Batch {
	var out []*core.Batch
	seq := uint64(0)
	for _, td := range tables {
		out = append(out, &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtCreateTable,
			Text: []byte("CREATE TABLE `" + td.t.Name + "` (...);\n")}})
		seq++
		b := &core.Batch{Seq: seq, Table: td.t, First: true, Last: true,
			Head: []byte("INSERT INTO `" + td.t.Name + "` VALUES "), Tail: []byte(";\n")}
		for i := range td.t.Columns {
			b.Columns = append(b.Columns, i)
		}
		for ri, r := range td.rows {
			row := core.Row{}
			if ri > 0 {
				row.Lead = []byte(",")
			}
			for _, v := range r {
				switch x := v.(type) {
				case nil:
					row.Values = append(row.Values, core.Value{Kind: core.Null})
				case int:
					row.Values = append(row.Values, core.Value{Kind: core.Number, Raw: []byte(strconv.Itoa(x))})
				case string:
					row.Values = append(row.Values, core.Value{Kind: core.String, Raw: []byte(x)})
				case core.Value:
					row.Values = append(row.Values, core.Value{Kind: x.Kind, Raw: bytes.Clone(x.Raw)})
				default:
					panic(fmt.Sprintf("fixture value %T", v))
				}
			}
			b.Rows = append(b.Rows, row)
		}
		out = append(out, b)
		seq++
	}
	return out
}

// schemaOf is the schema of fixture tables.
func schemaOf(prefix string, tables []tableData) *core.Schema {
	s := &core.Schema{Dialect: core.MySQL, Prefix: prefix}
	for _, td := range tables {
		s.Tables = append(s.Tables, td.t)
	}
	return s
}

// fixture builds a pipeline over fixture tables (fresh rows per pass)
// writing to a recording sink.
func fixture(t *testing.T, mode Mode, prefix string, tables func() []tableData, cfg *roadiecfg.Config, opts Options) (*pipeline, *testutil.Sink, *funcInput) {
	t.Helper()
	defs, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = roadiecfg.Default()
	}
	in := &funcInput{fn: func(int) (core.Source, error) {
		td := tables()
		return testutil.FakeSource(schemaOf(prefix, td), batches(td)), nil
	}}
	sink := testutil.FakeSink()
	p := &pipeline{mode: mode, cfg: cfg, opts: opts, defs: defs, in: in, sink: sink}
	return p, sink, in
}

// valueOf returns a written value: table, row index, column name.
func valueOf(t *testing.T, sink *testutil.Sink, table string, row int, col string) (string, bool) {
	t.Helper()
	for _, b := range sink.Batches() {
		if b.Table == nil || b.Table.Name != table {
			continue
		}
		ci := b.Table.Col(col)
		for i, c := range b.Columns {
			if c != ci {
				continue
			}
			if row >= len(b.Rows) {
				t.Fatalf("%s has %d rows", table, len(b.Rows))
			}
			if b.Rows[row].Drop {
				return "<dropped>", false
			}
			v := b.Rows[row].Values[i]
			return string(v.Raw), v.Kind != core.Null
		}
	}
	t.Fatalf("no %s.%s written", table, col)
	return "", false
}

func isCode(err error, code string) bool {
	var ce *core.Error
	return errors.As(err, &ce) && ce.Code == code
}
