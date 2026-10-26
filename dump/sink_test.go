// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

func batches(t testing.TB, in []byte) []*core.Batch {
	t.Helper()
	src, err := Open(context.Background(), bytes.NewReader(in), Options{Name: "in.sql"})
	if err != nil {
		t.Fatal(err)
	}
	bs, err := drainAll(t, src)
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

// tempFiles lists the sink temp files left in dir.
func tempFiles(t testing.TB, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.roadie-tmp-*"))
	return m
}

func TestFileSinkRoundTrip(t *testing.T) {
	in := fixture(t, "small.sql")
	for _, name := range []string{"out.sql", "out.sql.gz", "out.sql.zst"} {
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		s, err := NewFileSink(path, WriterOptions{Target: core.MySQL})
		if err != nil {
			t.Fatal(err)
		}
		if s.State() != core.Untouched {
			t.Fatal("state before commit")
		}
		for _, b := range batches(t, in) {
			if err := s.Write(context.Background(), b); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: the target exists before Commit", name)
		}
		if len(tempFiles(t, dir)) != 1 {
			t.Fatalf("%s: no temp file while writing", name)
		}
		if err := s.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s.State() != core.Replaced || len(tempFiles(t, dir)) != 0 {
			t.Fatalf("%s: state %s, temp files %v", name, s.State(), tempFiles(t, dir))
		}
		out, _ := os.ReadFile(path)
		if got := compress.Detect(out); got != compress.FromExt(name) {
			t.Errorf("%s: compression %q", name, got)
		}
		r, _ := compress.NewReader(bytes.NewReader(out), compress.Detect(out), compress.Options{})
		plain, _ := io.ReadAll(r)
		if !bytes.Equal(plain, in) {
			t.Errorf("%s: output differs from the input", name)
		}
		if err := s.Write(context.Background(), &core.Batch{}); err == nil {
			t.Error("write after commit")
		}
	}
}

// An explicit compression wins over the extension; zstd output reads in
// the reference decoder.
func TestFileSinkCompressionOption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.sql")
	s, err := NewFileSink(path, WriterOptions{Compression: compress.Zstd})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range batches(t, fixture(t, "small.sql")) {
		_ = s.Write(context.Background(), b)
	}
	if err := s.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	d, _ := zstd.NewReader(nil)
	if plain, err := d.DecodeAll(out, nil); err != nil || !bytes.Equal(plain, fixture(t, "small.sql")) {
		t.Fatalf("zstd output: %v", err)
	}
}

func TestFileSinkAbort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sql.gz")
	os.WriteFile(path, []byte("the earlier copy"), 0o600)
	s, err := NewFileSink(path, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range batches(t, fixture(t, "small.sql"))[:10] {
		_ = s.Write(context.Background(), b)
	}
	if err := s.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tempFiles(t, dir)) != 0 || s.State() != core.Untouched {
		t.Fatalf("temp files %v, state %s", tempFiles(t, dir), s.State())
	}
	if b, _ := os.ReadFile(path); string(b) != "the earlier copy" {
		t.Fatal("Abort touched the target")
	}
	if err := s.Commit(context.Background()); err == nil {
		t.Error("commit after abort")
	}
	_ = s.Abort(context.Background()) // twice is fine
}

func TestFileSinkCancel(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSink(filepath.Join(dir, "out.sql"), WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bs := batches(t, fixture(t, "small.sql"))
	_ = s.Write(context.Background(), bs[0])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Write(ctx, bs[1]); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(tempFiles(t, dir)) != 0 {
		t.Fatal("temp file left after cancel")
	}
	// Cancel at Commit too.
	s, _ = NewFileSink(filepath.Join(dir, "out2.sql"), WriterOptions{})
	_ = s.Write(context.Background(), bs[0])
	if err := s.Commit(ctx); !errors.Is(err, context.Canceled) || len(tempFiles(t, dir)) != 0 {
		t.Fatalf("commit: %v, %v", err, tempFiles(t, dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "out2.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a canceled commit wrote the target")
	}
}

func setFree(t *testing.T, free int64) {
	old := freeSpace
	freeSpace = func(string) (int64, bool) { return free, true }
	t.Cleanup(func() { freeSpace = old })
}

func wantDiskFull(t *testing.T, err error, dir string) {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDiskFull || ce.Exit != 3 {
		t.Fatalf("err = %v, want disk-full", err)
	}
	if !strings.HasPrefix(ce.Msg, "disk-full: not enough free disk space: need about ") ||
		!strings.Contains(ce.Msg, " free in "+dir+". Free some space or write the copy to another disk; the target is untouched.") {
		t.Errorf("message %q", ce.Msg)
	}
	if exit, _ := core.CodeExit(ce.Code); exit != 3 {
		t.Error("disk-full exit")
	}
}

// ExpectBytes over the free space refuses before a temp file exists.
func TestDiskFullUpFront(t *testing.T) {
	dir := t.TempDir()
	setFree(t, 1_000_000_000)
	_, err := NewFileSink(filepath.Join(dir, "out.sql"), WriterOptions{ExpectBytes: 2_100_000_000})
	wantDiskFull(t, err, dir)
	if err.Error() != "disk-full: not enough free disk space: need about 2.1 GB, 1.0 GB free in "+dir+". Free some space or write the copy to another disk; the target is untouched." {
		t.Errorf("message %q", err)
	}
	if len(tempFiles(t, dir)) != 0 {
		t.Fatal("temp file created")
	}
}

// The free space is checked again every 64 MiB written.
func TestDiskFullWhileWriting(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSink(filepath.Join(dir, "out.sql"), WriterOptions{ExpectBytes: 200 << 20})
	if err != nil {
		t.Fatal(err)
	}
	setFree(t, 10<<20)
	big := []byte("INSERT INTO `t` VALUES ('" + strings.Repeat("x", 1<<20) + "');\n")
	var werr error
	for i := uint64(0); i < 100 && werr == nil; i++ {
		werr = s.Write(context.Background(), &core.Batch{Seq: i, Pass: &core.Statement{Kind: core.StmtOther, Text: big}})
	}
	wantDiskFull(t, werr, dir)
	if len(tempFiles(t, dir)) != 0 {
		t.Fatal("temp file left")
	}
}

// Without ExpectBytes a nearly full disk isn't refused while the writes
// still fit: the rest of the dump may be small.
func TestDiskFreeUnknownSize(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSink(filepath.Join(dir, "out.sql"), WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	setFree(t, 10<<20)
	big := []byte("INSERT INTO `t` VALUES ('" + strings.Repeat("x", 1<<20) + "');\n")
	for i := uint64(0); i < 80; i++ {
		if err := s.Write(context.Background(), &core.Batch{Seq: i, Pass: &core.Statement{Kind: core.StmtOther, Text: big}}); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}
	if err := s.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Less free space than one write is still disk-full.
	s, err = NewFileSink(filepath.Join(dir, "out2.sql"), WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	setFree(t, 1<<10)
	var werr error
	for i := uint64(0); i < 80 && werr == nil; i++ {
		werr = s.Write(context.Background(), &core.Batch{Seq: i, Pass: &core.Statement{Kind: core.StmtOther, Text: big}})
	}
	wantDiskFull(t, werr, dir)
	if len(tempFiles(t, dir)) != 0 {
		t.Fatal("temp file left")
	}
}

type enospc struct{}

func (enospc) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}
}

// A write that fails with ENOSPC is disk-full and the temp file goes.
func TestDiskFullENOSPC(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewFileSink(filepath.Join(dir, "out.sql"), WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sink.(*fileSink).cnt.dst = enospc{}
	var werr error
	for _, b := range batches(t, fixture(t, "small.sql")) {
		if werr = sink.Write(context.Background(), b); werr != nil {
			break
		}
	}
	if werr == nil {
		werr = sink.Commit(context.Background())
	}
	wantDiskFull(t, werr, dir)
	if len(tempFiles(t, dir)) != 0 {
		t.Fatal("temp file left")
	}
}

func TestStreamSink(t *testing.T) {
	in := fixture(t, "small.sql")
	for _, kind := range []string{compress.None, compress.Gzip, compress.Zstd} {
		var out bytes.Buffer
		s := NewStreamSink(&out, WriterOptions{Compression: kind, Target: core.MariaDB})
		for _, b := range batches(t, in) {
			if err := s.Write(context.Background(), b); err != nil {
				t.Fatal(err)
			}
		}
		if s.State() != core.Untouched {
			t.Fatal("state before commit")
		}
		if err := s.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		r, _ := compress.NewReader(&out, kind, compress.Options{})
		plain, _ := io.ReadAll(r)
		if want := append([]byte("/*M!999999\\- enable the sandbox mode */\n"), in...); !bytes.Equal(plain, want) || s.State() != core.Replaced {
			t.Errorf("%q: stream output differs", kind)
		}
		_ = s.Abort(context.Background())
	}
	s := NewStreamSink(io.Discard, WriterOptions{Compression: compress.Bzip2})
	if err := s.Write(context.Background(), &core.Batch{}); err == nil {
		t.Error("bzip2 stream accepted")
	}
	if err := s.Commit(context.Background()); err == nil {
		t.Error("commit after a setup error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = NewStreamSink(io.Discard, WriterOptions{})
	if err := s.Write(ctx, &core.Batch{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled write: %v", err)
	}
}

func TestSinkTargets(t *testing.T) {
	pg, err := NewFileSink(filepath.Join(t.TempDir(), "x.sql"), WriterOptions{Target: core.Postgres, Source: core.Postgres})
	if err != nil {
		t.Errorf("postgres: %v", err)
	} else {
		_ = pg.Abort(context.Background())
	}
	dir := t.TempDir()
	var ce *core.Error
	for _, o := range []WriterOptions{{Target: core.MySQL, Source: core.Postgres}, {Target: core.Postgres, Source: core.MariaDB}} {
		_, err = NewFileSink(filepath.Join(dir, "x.sql"), o)
		if !errors.As(err, &ce) || ce.Code != core.ErrDialectMismatch || ce.Exit != 2 || len(tempFiles(t, dir)) != 0 {
			t.Errorf("%s to %s: %v", o.Source, o.Target, err)
		}
	}
	_, err = NewFileSink(filepath.Join(dir, "x.sql"), WriterOptions{Target: core.SQLite})
	if !errors.As(err, &ce) || ce.Code != core.ErrDialectMismatch || len(tempFiles(t, dir)) != 0 {
		t.Errorf("sqlite: %v", err)
	}
	if _, err := NewFileSink(filepath.Join(dir, "missing", "x.sql"), WriterOptions{}); err == nil {
		t.Error("a missing folder was accepted")
	}
}
