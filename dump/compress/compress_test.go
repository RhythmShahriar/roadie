// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compress

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/RhythmShahriar/roadie/core"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../testdata/small.sql")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compressed(t testing.TB, kind string, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Every compression of the same dump reads back to the same bytes and the
// fingerprint is sha256sum of the file as stored.
func TestReadersAgree(t *testing.T) {
	plain := readFixture(t)
	bz, err := os.ReadFile("../testdata/small.sql.bz2")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		None:  plain,
		Gzip:  compressed(t, Gzip, plain),
		Zstd:  compressed(t, Zstd, plain),
		Bzip2: bz,
	}
	for kind, file := range files {
		if got := Detect(file); got != kind {
			t.Errorf("Detect = %q, want %q", got, kind)
		}
		var adv int64
		r, err := NewReader(bytes.NewReader(file), kind, Options{Name: "small.sql", Progress: advancerOf(&adv)})
		if err != nil {
			t.Fatal(kind, err)
		}
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(kind, err)
		}
		if !bytes.Equal(out, plain) {
			t.Errorf("%q: decompressed bytes differ", kind)
		}
		if err := r.Drain(); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(file)
		fp := r.Fingerprint()
		if fp.Kind != "sha256-file" || fp.Value != hex.EncodeToString(sum[:]) || fp.Bytes != int64(len(file)) {
			t.Errorf("%q: fingerprint %+v, want sha256 %x of %d bytes", kind, fp, sum, len(file))
		}
		if adv != int64(len(file)) || r.RawBytes() != int64(len(file)) || r.OutBytes() != int64(len(plain)) {
			t.Errorf("%q: progress %d, raw %d, out %d", kind, adv, r.RawBytes(), r.OutBytes())
		}
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}
}

type progress struct{ n *int64 }

func (p progress) Phase(string, int64)  {}
func (p progress) Advance(n int64)      { *p.n += n }
func (p progress) Table(string, int64)  {}
func (p progress) Note(string)          {}
func advancerOf(n *int64) core.Progress { return progress{n} }

// repeat yields b over and over up to total bytes, so a 50 GB bomb never
// exists anywhere but in the reader's arithmetic.
type repeat struct {
	b     []byte
	off   int
	total int64
}

func (r *repeat) Read(p []byte) (int, error) {
	if r.total <= 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.total > 0 {
		c := copy(p[n:], r.b[r.off:])
		if int64(c) > r.total {
			c = int(r.total)
		}
		n += c
		r.total -= int64(c)
		r.off = (r.off + c) % len(r.b)
	}
	return n, nil
}

// bomb returns a stream of concatenated compressed members, each 1 MiB of
// zeros, that inflates to about 50 GB.
func bomb(t testing.TB, kind string) (io.Reader, int64) {
	member := compressed(t, kind, make([]byte, 1<<20))
	members := int64(50 << 10) // 50 GiB of output
	return &repeat{b: member, total: members * int64(len(member))}, members * int64(len(member))
}

// A gzip bomb stops at the cap within seconds and without holding its
// output (security.md A2). Each member of the stream is about 1 KiB, so
// the first 10 KiB of it already inflate to 10 MiB; the reader stops just
// past the 64 MiB grace.
func TestBombStops(t *testing.T) {
	for _, kind := range []string{Gzip, Zstd} {
		src, n := bomb(t, kind)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		r, err := NewReader(src, kind, Options{Name: "prod.sql.gz"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.CopyBuffer(io.Discard, r, make([]byte, 64<<10))
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpBomb || ce.Exit != 3 {
			t.Fatalf("%s: err = %v, want dump-bomb", kind, err)
		}
		if !strings.HasPrefix(ce.Msg, "dump-bomb: prod.sql.gz grew past 20 times its size (") ||
			!strings.HasSuffix(ce.Msg, ") while reading. If it's real, pass --max-output-ratio 50; the target is untouched.") {
			t.Errorf("message %q", ce.Msg)
		}
		if took > 5*time.Second {
			t.Errorf("%s: took %v to stop", kind, took)
		}
		if a := after.TotalAlloc - before.TotalAlloc; a > 64<<20 {
			t.Errorf("%s: allocated %d MiB", kind, a>>20)
		}
		if r.OutBytes() > Grace+(4<<20) || r.RawBytes() >= n {
			t.Errorf("%s: read %d out, %d raw", kind, r.OutBytes(), r.RawBytes())
		}
		_ = r.Close()
	}
}

// A small file with a high ratio is fine: the cap waits for 64 MiB.
func TestHighRatioUnderGrace(t *testing.T) {
	file := compressed(t, Gzip, make([]byte, 2<<20))
	r, err := NewReader(bytes.NewReader(file), Gzip, Options{})
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, r)
	if err != nil || n != 2<<20 {
		t.Fatalf("read %d, %v", n, err)
	}
}

// --max-output-ratio raises the cap.
func TestRatioOption(t *testing.T) {
	src, _ := bomb(t, Gzip)
	r, err := NewReader(src, Gzip, Options{Name: "x.gz", MaxRatio: 3000})
	if err != nil {
		t.Fatal(err)
	}
	// 1 MiB of zeros gzips to about 1 KiB, so 3000 is above the bomb's
	// ratio: 100 MiB read without an error.
	if _, err := io.CopyN(io.Discard, r, 100<<20); err != nil {
		t.Fatalf("ratio 3000: %v", err)
	}
}

// A plain file is never capped.
func TestPlainNotCapped(t *testing.T) {
	r, err := NewReader(&repeat{b: []byte("-- x\n"), total: 100 << 20}, None, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := io.Copy(io.Discard, r); err != nil || n != 100<<20 {
		t.Fatalf("read %d, %v", n, err)
	}
}

// A compressed stream cut short is a StreamError, never a clean end.
func TestCutStream(t *testing.T) {
	plain := bytes.Repeat(readFixture(t), 50)
	for _, kind := range []string{Gzip, Zstd} {
		file := compressed(t, kind, plain)
		r, err := NewReader(bytes.NewReader(file[:len(file)/2]), kind, Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(r)
		var se *StreamError
		if !errors.As(err, &se) || se.Kind != kind {
			t.Errorf("%s: err = %v, want a StreamError", kind, err)
		}
	}
	// A gzip with a bad checksum.
	file := compressed(t, Gzip, plain)
	file[len(file)-6] ^= 0xff
	r, _ := NewReader(bytes.NewReader(file), Gzip, Options{})
	if _, err := io.ReadAll(r); !errors.Is(err, gzip.ErrChecksum) {
		t.Errorf("bad checksum: %v", err)
	}
}

func TestDetect(t *testing.T) {
	for head, want := range map[string]string{
		"\x1f\x8b\x08": Gzip, "\x28\xb5\x2f\xfd": Zstd, "BZh9": Bzip2, "BZhx": None,
		"-- MySQL dump": None, "": None, "BZ": None,
	} {
		if got := Detect([]byte(head)); got != want {
			t.Errorf("Detect(%q) = %q, want %q", head, got, want)
		}
	}
}

func TestFromExt(t *testing.T) {
	for path, want := range map[string]string{
		"a.sql.gz": Gzip, "a.SQL.GZ": Gzip, "a.sql.zst": Zstd, "a.sql": None, "a": None, "a.sql.bz2": None,
	} {
		if got := FromExt(path); got != want {
			t.Errorf("FromExt(%q) = %q, want %q", path, got, want)
		}
	}
}

// The writers produce streams the readers (and the reference decoders) read.
func TestWriters(t *testing.T) {
	plain := readFixture(t)
	gz := compressed(t, Gzip, plain)
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := io.ReadAll(zr); !bytes.Equal(out, plain) {
		t.Error("gzip round trip differs")
	}
	zs := compressed(t, Zstd, plain)
	d, _ := zstd.NewReader(nil)
	if out, err := d.DecodeAll(zs, nil); err != nil || !bytes.Equal(out, plain) {
		t.Errorf("zstd round trip: %v", err)
	}
	if _, err := NewWriter(io.Discard, Bzip2); err == nil {
		t.Error("bzip2 writer should be refused")
	}
	if _, err := NewReader(bytes.NewReader(nil), "lz4", Options{}); err == nil {
		t.Error("unknown compression should be refused")
	}
}

// Head decompresses what it can of a cut stream. The text varies so a
// zstd frame holds many blocks: a cut inside the first block decodes nothing.
func TestHead(t *testing.T) {
	plain := make([]byte, 1<<20)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range plain {
		plain[i] = 'a' + byte(rng.IntN(26))
	}
	for _, kind := range []string{Gzip, Zstd} {
		file := compressed(t, kind, plain)
		got := Head(file[:len(file)/2], kind, 4096)
		if len(got) == 0 || !bytes.HasPrefix(plain, got) {
			t.Errorf("%s: head of %d bytes", kind, len(got))
		}
	}
	if got := Head([]byte("plain text"), None, 5); string(got) != "plain" {
		t.Errorf("plain head %q", got)
	}
	if got := Head([]byte("\x1f\x8bnot gzip"), Gzip, 5); len(got) != 0 {
		t.Errorf("broken gzip head %q", got)
	}
}
