// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package compress reads gzip, zstd and bzip2 dumps with an output cap,
// counts and hashes the raw input bytes on the way, and writes gzip and
// zstd output (security.md A2).
//
// The cap matters because streaming keeps memory flat but not disk: a
// 10 KB gzip can inflate to 10 GB, and a zstd frame to far more. Reading
// stops once the output passes MaxRatio times the compressed bytes read so
// far, after a grace of the first 64 MiB, so a small dump with a high
// ratio (a schema of empty tables compresses well) still reads.
package compress

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
)

// The compression names, as core.Endpoint.Compression and
// dump.WriterOptions.Compression spell them. None is plain text.
const (
	None  = ""
	Gzip  = "gzip"
	Zstd  = "zstd"
	Bzip2 = "bzip2" // read only
)

// DefaultMaxRatio is the decompressed to compressed cap when Options
// leaves MaxRatio at 0.
const DefaultMaxRatio = 20

// Grace is how many decompressed bytes may be read before the ratio cap
// applies.
const Grace = 64 << 20

// zstdMaxWindow is the largest zstd window the reader accepts. 128 MiB
// covers zstd --long=27 and every level up to 22; a frame that asks for
// more could make the decoder allocate gigabytes before the cap sees one
// output byte.
const zstdMaxWindow = 128 << 20

// The magic numbers, from RFC 1952, RFC 8878 and the bzip2 format.
var (
	magicGzip  = []byte{0x1f, 0x8b}
	magicZstd  = []byte{0x28, 0xb5, 0x2f, 0xfd}
	magicBzip2 = []byte("BZh")
)

// Detect names the compression of a file from its first bytes, never
// from its name: a dump renamed .sql that is really gzip still reads.
func Detect(head []byte) string {
	switch {
	case bytes.HasPrefix(head, magicGzip):
		return Gzip
	case bytes.HasPrefix(head, magicZstd):
		return Zstd
	case bytes.HasPrefix(head, magicBzip2) && len(head) > 3 && head[3] >= '1' && head[3] <= '9':
		return Bzip2
	}
	return None
}

// FromExt names the output compression a file name asks for: ".gz" gives
// gzip and ".zst" zstd; anything else is plain.
func FromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gz", ".gzip":
		return Gzip
	case ".zst", ".zstd":
		return Zstd
	}
	return None
}

// Options configures NewReader.
type Options struct {
	Name     string        // the file name, for messages
	MaxRatio int           // 0 = DefaultMaxRatio
	Progress core.Progress // gets Advance(raw bytes); may be nil
}

// StreamError is a compressed stream that ended early or failed its
// checksum. The dump reader turns it into dump-truncated with the table
// it was reading, so this package doesn't word the message.
type StreamError struct {
	Kind string // Gzip, Zstd or Bzip2
	Raw  int64  // compressed bytes read when it failed
	Out  int64  // decompressed bytes read when it failed
	Err  error
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("%s stream broken after %d compressed bytes: %v", e.Kind, e.Raw, e.Err)
}

func (e *StreamError) Unwrap() error { return e.Err }

// counter passes reads through and counts, hashes and reports them.
type counter struct {
	r        io.Reader
	n        int64
	h        hash.Hash
	progress core.Progress
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		c.h.Write(p[:n])
		if c.progress != nil {
			c.progress.Advance(int64(n))
		}
	}
	return n, err
}

// Reader is the decompressed input. It counts raw and decompressed bytes,
// hashes the raw ones and stops at the ratio cap.
type Reader struct {
	kind  string
	name  string
	ratio int64
	raw   *counter
	dec   io.Reader
	zdec  *zstd.Decoder
	out   int64
	err   error
}

// NewReader wraps r, whose compression is kind (from Detect), in a
// decompressing reader with the output cap.
func NewReader(r io.Reader, kind string, o Options) (*Reader, error) {
	ratio := o.MaxRatio
	if ratio <= 0 {
		ratio = DefaultMaxRatio
	}
	c := &counter{r: r, h: sha256.New(), progress: o.Progress}
	rd := &Reader{kind: kind, name: o.Name, ratio: int64(ratio), raw: c}
	// The decompressors read through a bufio.Reader so a small Read from
	// the counter never costs a syscall per byte.
	br := bufio.NewReaderSize(c, 256<<10)
	switch kind {
	case None:
		rd.dec = br
	case Gzip:
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, &StreamError{Kind: kind, Raw: c.n, Err: err}
		}
		rd.dec = zr
	case Zstd:
		zr, err := zstd.NewReader(br,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(zstdMaxWindow))
		if err != nil {
			return nil, &StreamError{Kind: kind, Raw: c.n, Err: err}
		}
		rd.dec, rd.zdec = zr, zr
	case Bzip2:
		rd.dec = bzip2.NewReader(br)
	default:
		return nil, fmt.Errorf("compress: unknown compression %q", kind)
	}
	return rd, nil
}

// Read returns decompressed bytes. Past the grace it fails with
// dump-bomb once the output is over the ratio times the raw bytes read.
func (r *Reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.dec.Read(p)
	r.out += int64(n)
	if r.kind != None && r.out > Grace && r.out > r.ratio*r.raw.n {
		r.err = core.Errorf(core.ErrDumpBomb, 3,
			"dump-bomb: %s grew past %d times its size (%s) while reading. If it's real, pass --max-output-ratio %d; the target is untouched.",
			r.name, r.ratio, size.Human(r.out), r.ratio*5/2)
		return n, r.err
	}
	if err != nil && !errors.Is(err, io.EOF) && r.kind != None {
		r.err = &StreamError{Kind: r.kind, Raw: r.raw.n, Out: r.out, Err: err}
		return n, r.err
	}
	if err != nil {
		r.err = err
	}
	return n, err
}

// RawBytes is the number of input bytes read so far.
func (r *Reader) RawBytes() int64 { return r.raw.n }

// OutBytes is the number of decompressed bytes returned so far.
func (r *Reader) OutBytes() int64 { return r.out }

// Drain reads the rest of the raw input, so Fingerprint covers the whole
// file even when the decompressor stopped before its end.
func (r *Reader) Drain() error {
	_, err := io.Copy(io.Discard, r.raw)
	return err
}

// Fingerprint is the SHA-256 of the raw bytes read so far, equal to
// sha256sum of the file once Read has returned io.EOF and Drain ran.
func (r *Reader) Fingerprint() core.Fingerprint {
	return core.Fingerprint{Kind: "sha256-file", Value: hex.EncodeToString(r.raw.h.Sum(nil)), Bytes: r.raw.n}
}

// Close releases the decompressor. It doesn't close the underlying reader,
// which belongs to the caller.
func (r *Reader) Close() error {
	if r.zdec != nil {
		r.zdec.Close()
	}
	if c, ok := r.dec.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Head decompresses up to max bytes from the start of a compressed head,
// for sniffing. A head cut mid-stream is normal here, so its errors are
// ignored and whatever decompressed is returned.
func Head(head []byte, kind string, max int) []byte {
	var dec io.Reader
	switch kind {
	case None:
		return head[:min(len(head), max)]
	case Gzip:
		zr, err := gzip.NewReader(bytes.NewReader(head))
		if err != nil {
			return nil
		}
		dec = zr
	case Zstd:
		zr, err := zstd.NewReader(bytes.NewReader(head),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(zstdMaxWindow))
		if err != nil {
			return nil
		}
		defer zr.Close()
		dec = zr
	case Bzip2:
		dec = bzip2.NewReader(bytes.NewReader(head))
	default:
		return nil
	}
	out := make([]byte, max)
	n, _ := io.ReadFull(dec, out)
	return out[:n]
}

// NewWriter compresses to w: gzip at level 6, zstd at level 3, or plain
// (w itself, with a Close that does nothing). Close flushes the stream
// but never closes w.
func NewWriter(w io.Writer, kind string) (io.WriteCloser, error) {
	switch kind {
	case None:
		return nopCloser{w}, nil
	case Gzip:
		return gzip.NewWriterLevel(w, 6)
	case Zstd:
		return zstd.NewWriter(w,
			zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)),
			zstd.WithEncoderConcurrency(1))
	}
	return nil, fmt.Errorf("compress: can't write %q", kind)
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
