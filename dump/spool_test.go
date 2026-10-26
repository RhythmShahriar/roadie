// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fill writes data to a new spool in dir through odd-sized writes.
func fill(t *testing.T, dir string, data []byte) *Spool {
	t.Helper()
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Remove() })
	w := s.Writer()
	for p := data; len(p) > 0; {
		k := min(len(p), 77777)
		if _, err := w.Write(p[:k]); err != nil {
			t.Fatal(err)
		}
		p = p[k:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func readSpool(s *Spool) ([]byte, error) {
	r, err := s.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// spoolFile is the one spool file in dir.
func spoolFile(t *testing.T, dir string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.spool"))
	if len(m) != 1 {
		t.Fatalf("spool files: %v", m)
	}
	return m[0]
}

func TestSpoolRoundTripTwice(t *testing.T) {
	data := make([]byte, 10<<20+123)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	s := fill(t, t.TempDir(), data)
	for pass := range 2 {
		got, err := readSpool(s)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("pass %d: %d bytes back, want %d", pass, len(got), len(data))
		}
	}
}

// Two readers open at once each read the whole spool, in steps that
// cross chunk boundaries, and reading reuses one ciphertext buffer.
func TestSpoolTwoReaders(t *testing.T) {
	data := make([]byte, 3*SpoolChunk+5)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	s := fill(t, t.TempDir(), data)
	r1, err := s.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close()
	r2, err := s.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	var got1, got2 bytes.Buffer
	step := make([]byte, 300_001)
	for done1, done2 := false, false; !done1 || !done2; {
		for _, c := range []struct {
			r    io.Reader
			out  *bytes.Buffer
			done *bool
		}{{r1, &got1, &done1}, {r2, &got2, &done2}} {
			if *c.done {
				continue
			}
			n, err := c.r.Read(step)
			c.out.Write(step[:n])
			if errors.Is(err, io.EOF) {
				*c.done = true
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
	if !bytes.Equal(got1.Bytes(), data) || !bytes.Equal(got2.Bytes(), data) {
		t.Fatalf("got %d and %d bytes, want %d", got1.Len(), got2.Len(), len(data))
	}
	sr := r1.(*spoolReader)
	if cap(sr.ct) > SpoolChunk+64 {
		t.Errorf("ciphertext buffer grew to %d", cap(sr.ct))
	}
}

// Empty input and input of whole chunks both read back exactly.
func TestSpoolSizes(t *testing.T) {
	for _, n := range []int{0, 1, SpoolChunk, 2 * SpoolChunk} {
		data := bytes.Repeat([]byte{'a'}, n)
		got, err := readSpool(fill(t, t.TempDir(), data))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%d bytes: got %d, %v", n, len(got), err)
		}
	}
}

func TestSpoolName(t *testing.T) {
	dir := t.TempDir()
	fill(t, dir, []byte("x"))
	name := filepath.Base(spoolFile(t, dir))
	if len(name) != len("0123456789abcdef.spool") {
		t.Errorf("spool name %q", name)
	}
}

// The canary never shows on disk, not even a 16-byte window of it.
func TestSpoolNoPlaintext(t *testing.T) {
	canary := []byte("roadie-spool-canary-" + "8f3a1c9e7b2d4f60")
	data := bytes.Repeat(append(canary, '\n'), 50000)
	dir := t.TempDir()
	fill(t, dir, data)
	disk, err := os.ReadFile(spoolFile(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+16 <= len(canary); i++ {
		if bytes.Contains(disk, canary[i:i+16]) {
			t.Fatalf("plaintext window %q on disk", canary[i:i+16])
		}
	}
}

// One flipped byte anywhere fails the read with an error.
func TestSpoolTampered(t *testing.T) {
	data := bytes.Repeat([]byte("row data\n"), 300000) // about 2.6 MiB, three chunks
	for _, at := range []int{0, 10, SpoolChunk + 40, -1} {
		dir := t.TempDir()
		s := fill(t, dir, data)
		path := spoolFile(t, dir)
		disk, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		i := at
		if i < 0 {
			i = len(disk) - 1
		}
		disk[i] ^= 0x01
		if err := os.WriteFile(path, disk, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSpool(s); !errors.Is(err, errSpoolDamaged) {
			t.Errorf("flip at %d: %v, want damaged", i, err)
		}
	}
}

// A spool cut at a chunk boundary or with bytes added fails too.
func TestSpoolCutOrGrown(t *testing.T) {
	data := bytes.Repeat([]byte{'z'}, 2*SpoolChunk+5)
	for _, change := range []string{"cut", "grown", "swapped"} {
		dir := t.TempDir()
		s := fill(t, dir, data)
		path := spoolFile(t, dir)
		disk, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		chunk := 4 + SpoolChunk + 16
		switch change {
		case "cut":
			disk = disk[:chunk]
		case "grown":
			disk = append(disk, 0)
		case "swapped":
			a := append([]byte(nil), disk[:chunk]...)
			disk = append(append(append([]byte(nil), disk[chunk:2*chunk]...), a...), disk[2*chunk:]...)
		}
		if err := os.WriteFile(path, disk, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSpool(s); !errors.Is(err, errSpoolDamaged) {
			t.Errorf("%s: %v, want damaged", change, err)
		}
	}
}

func TestSpoolRemove(t *testing.T) {
	dir := t.TempDir()
	s := fill(t, dir, []byte("data"))
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left after Remove: %v", left)
	}
	if err := s.Remove(); err != nil {
		t.Errorf("second Remove: %v", err)
	}
	if _, err := s.Reader(); err == nil {
		t.Error("Reader after Remove")
	}
}

// Remove before the writer closes (an error or Ctrl-C mid-pass) also
// leaves nothing and stops the writer.
func TestSpoolRemoveWhileWriting(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := s.Writer()
	if _, err := w.Write(make([]byte, SpoolChunk+1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left after Remove: %v", left)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("write after Remove")
	}
}

func TestSpoolReadBeforeClose(t *testing.T) {
	s, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	if _, err := s.Reader(); err == nil {
		t.Error("Reader before the writer closed")
	}
	if _, err := s.Writer().Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Writer().Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Writer().Write([]byte("x")); err == nil {
		t.Error("write after Close")
	}
}

// The folder is created when missing; a folder path that is a file fails.
func TestSpoolDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "spool")
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Remove()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSpool(filepath.Join(file, "spool")); err == nil {
		t.Error("NewSpool under a file")
	}
}
