// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package testutil

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- matches the md5 encodings exact mode looks for
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/RhythmShahriar/roadie/core"
)

// Source is a core.Source over fixed batches.
type Source struct {
	S       *core.Schema
	Batches []*core.Batch
	// Err, when set, is returned by Next after the batches instead of io.EOF.
	Err error
	FP  core.Fingerprint
	// Closed is set by Close.
	Closed bool
	next   int
}

// FakeSource returns a source that yields batches in order, then io.EOF.
func FakeSource(schema *core.Schema, batches []*core.Batch) *Source {
	if schema == nil {
		schema = &core.Schema{Dialect: core.MySQL}
	}
	return &Source{S: schema, Batches: batches, FP: core.Fingerprint{Kind: "sha256-file", Value: "fake"}}
}

// Schema returns the schema the source was made with.
func (s *Source) Schema() *core.Schema { return s.S }

// Next returns the next batch, or Err or io.EOF after the last one.
func (s *Source) Next(ctx context.Context) (*core.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.next < len(s.Batches) {
		s.next++
		return s.Batches[s.next-1], nil
	}
	if s.Err != nil {
		return nil, s.Err
	}
	return nil, io.EOF
}

// Fingerprint returns FP.
func (s *Source) Fingerprint() core.Fingerprint { return s.FP }

// Close marks the source closed.
func (s *Source) Close() error { s.Closed = true; return nil }

// Sink is a core.Sink that records what it is given.
type Sink struct {
	mu        sync.Mutex
	batches   []*core.Batch
	committed bool
	aborted   bool
	// WriteErr, when set, is returned by Write after FailAfter batches.
	WriteErr  error
	FailAfter int
}

// FakeSink returns an empty recording sink.
func FakeSink() *Sink { return &Sink{} }

// Write records b.
func (s *Sink) Write(ctx context.Context, b *core.Batch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.WriteErr != nil && len(s.batches) >= s.FailAfter {
		return s.WriteErr
	}
	s.batches = append(s.batches, b)
	return nil
}

// Commit marks the sink committed.
func (s *Sink) Commit(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.committed = true
	return nil
}

// Abort marks the sink aborted.
func (s *Sink) Abort(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = true
	return nil
}

// State is Replaced after Commit, Mixed after an Abort that followed a
// write and Untouched otherwise.
func (s *Sink) State() core.TargetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.committed:
		return core.Replaced
	case s.aborted && len(s.batches) > 0:
		return core.Mixed
	}
	return core.Untouched
}

// Batches returns the recorded batches in the order written.
func (s *Sink) Batches() []*core.Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*core.Batch(nil), s.batches...)
}

// Committed and Aborted report whether Commit or Abort ran.
func (s *Sink) Committed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.committed }

// Aborted reports whether Abort ran.
func (s *Sink) Aborted() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.aborted }

// Bytes writes the recorded batches as MySQL text with a minimal encoder:
// an untouched row with source bytes is copied as is, a changed one is
// encoded value by value. It is for asserting on content, not a real writer.
func (s *Sink) Bytes() []byte {
	var b bytes.Buffer
	for _, bt := range s.Batches() {
		if bt.Pass != nil {
			b.Write(bt.Pass.Text)
			continue
		}
		if bt.First {
			b.Write(bt.Head)
		}
		for _, r := range bt.Rows {
			if r.Drop {
				continue
			}
			b.Write(r.Lead)
			if r.Src != nil && !dirty(r) {
				b.Write(r.Src)
				continue
			}
			b.WriteByte('(')
			for i, v := range r.Values {
				if i > 0 {
					b.WriteByte(',')
				}
				encode(&b, v)
			}
			b.WriteByte(')')
		}
		if bt.Last {
			b.Write(bt.Tail)
		}
	}
	return b.Bytes()
}

func dirty(r core.Row) bool {
	for _, v := range r.Values {
		if v.Dirty {
			return true
		}
	}
	return false
}

func encode(b *bytes.Buffer, v core.Value) {
	switch v.Kind {
	case core.Null:
		b.WriteString("NULL")
	case core.String, core.BinaryString:
		if v.Kind == core.BinaryString {
			b.WriteString("_binary ")
		}
		b.WriteByte('\'')
		for _, c := range v.Raw {
			switch c {
			case '\'', '\\':
				b.WriteByte('\\')
				b.WriteByte(c)
			case 0:
				b.WriteString(`\0`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('\'')
	default:
		b.Write(v.Raw)
	}
}

// Faker is a deterministic core.Faker: the n-th distinct value of a kind
// becomes fake-<kind>-<n> (an email fake-email-<n>@example.invalid).
// Clones share the mapping, so fakes match across workers.
type Faker struct {
	st *fakerState
}

type fakerState struct {
	mu    sync.Mutex
	seen  map[string][]byte
	count map[core.FakeKind]int
	calls int
}

// FakeFaker returns an empty Faker.
func FakeFaker() *Faker {
	return &Faker{st: &fakerState{seen: map[string][]byte{}, count: map[core.FakeKind]int{}}}
}

// Fake returns the fake for original, cut to o.MaxLen bytes when set.
func (f *Faker) Fake(kind core.FakeKind, original []byte, o core.FakeOpts) ([]byte, error) {
	f.st.mu.Lock()
	defer f.st.mu.Unlock()
	f.st.calls++
	key := string(kind) + "\x00" + string(original)
	out, ok := f.st.seen[key]
	if !ok {
		f.st.count[kind]++
		out = fmt.Appendf(nil, "fake-%s-%d", kind, f.st.count[kind])
		if kind == core.KEmail {
			out = append(out, "@example.invalid"...)
		}
		f.st.seen[key] = out
	}
	if o.MaxLen > 0 && len(out) > o.MaxLen {
		out = out[:o.MaxLen]
	}
	return bytes.Clone(out), nil
}

// Clone returns a Faker sharing this one's mapping.
func (f *Faker) Clone() core.Faker { return &Faker{st: f.st} }

// Calls returns how many times Fake ran on this faker and its clones.
func (f *Faker) Calls() int { f.st.mu.Lock(); defer f.st.mu.Unlock(); return f.st.calls }

// Known is a core.Known over plain maps: it keeps the lower-cased value
// and its md5 and sha256 hex, unkeyed (the real one keys its hashes).
type Known struct {
	mu     sync.Mutex
	vals   map[string]knownVal
	sealed bool
	// raw counts the distinct values, so Len (which the transform asks
	// for every leaf) takes no lock and walks no map.
	raw atomic.Int64
}

type knownVal struct {
	kind core.FakeKind
	enc  string
}

// FakeKnown returns an empty Known.
func FakeKnown() *Known { return &Known{vals: map[string]knownVal{}} }

// Add stores original in its raw, md5 and sha256 forms.
func (k *Known) Add(kind core.FakeKind, original []byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := bytes.ToLower(bytes.TrimSpace(original))
	m := md5.Sum(n) // #nosec G401 -- an encoding to look for, not a security use
	s := sha256.Sum256(n)
	if v, ok := k.vals[string(n)]; !ok || v.enc != "raw" {
		k.raw.Add(1)
	}
	k.vals[string(n)] = knownVal{kind, "raw"}
	k.vals[hex.EncodeToString(m[:])] = knownVal{kind, "md5"}
	k.vals[hex.EncodeToString(s[:])] = knownVal{kind, "sha256"}
}

// Seal ends pass 1.
func (k *Known) Seal() { k.mu.Lock(); k.sealed = true; k.mu.Unlock() }

// Contains reports whether token is known; always false before Seal.
func (k *Known) Contains(token []byte) (core.FakeKind, string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.sealed {
		return "", "", false
	}
	v, ok := k.vals[string(bytes.ToLower(bytes.TrimSpace(token)))]
	return v.kind, v.enc, ok
}

// Len returns the number of values added (each counted once).
func (k *Known) Len() int { return int(k.raw.Load()) }

// Spanner is a core.Spanner that finds email addresses only.
type Spanner struct{}

// FakeSpanner returns a Spanner.
func FakeSpanner() Spanner { return Spanner{} }

// Spans returns every email-shaped run of text: local@domain with a dot in the domain.
func (Spanner) Spans(text []byte) []core.Span {
	var out []core.Span
	last := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		start := i
		for start > last && isLocal(text[start-1]) {
			start--
		}
		end := i + 1
		for end < len(text) && isDomain(text[end]) {
			end++
		}
		for end > i+1 && text[end-1] == '.' {
			end--
		}
		if start == i || !bytes.Contains(text[i+1:end], []byte(".")) {
			continue
		}
		out = append(out, core.Span{Start: start, End: end, Kind: core.KEmail})
		last, i = end, end-1
	}
	return out
}

func isLocal(c byte) bool {
	return isDomain(c) || c == '_' || c == '%' || c == '+'
}

func isDomain(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-'
}

// Detector is a core.Detector that reports every occurrence of its needles.
type Detector struct {
	Name     string
	Severity core.Severity
	Needles  []string
}

// FakeDetector returns a Detector with this ID and severity.
func FakeDetector(id string, sev core.Severity, needles ...string) *Detector {
	return &Detector{Name: id, Severity: sev, Needles: needles}
}

// ID returns the detector's finding ID.
func (d *Detector) ID() string { return d.Name }

// Scan emits a hit for each occurrence of each needle in v.
func (d *Detector) Scan(v []byte, _ core.ScanContext, emit func(core.Hit)) {
	for _, n := range d.Needles {
		if n == "" {
			continue
		}
		for off := 0; ; {
			i := bytes.Index(v[off:], []byte(n))
			if i < 0 {
				break
			}
			emit(core.Hit{ID: d.Name, Severity: d.Severity, Start: off + i, End: off + i + len(n), Masked: "•••", Encoding: "raw"})
			off += i + len(n)
		}
	}
}
