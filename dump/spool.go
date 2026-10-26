// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	be "encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// SpoolChunk is the plaintext size of one spool chunk (PRD 10.3).
const SpoolChunk = 1 << 20

// The additional data of a chunk says whether it is the last one, so a
// spool cut at a chunk boundary fails to read instead of looking whole.
var (
	adMore = []byte{0}
	adLast = []byte{1}
)

// errSpoolClosed is a write after the spool writer was closed or the
// spool removed.
var errSpoolClosed = errors.New("dump: the spool is closed")

// spool is the state behind Spool. The key lives only in this struct:
// once the process ends, the file on disk can't be read by anyone.
type spool struct {
	mu      sync.Mutex
	path    string
	dir     string
	aead    cipher.AEAD
	w       *spoolWriter
	written bool // the writer was closed and the file holds a last chunk
	removed bool
}

// newSpool is NewSpool: dir is created 0700 when missing and the file
// '<16 hex>.spool' 0600 inside it.
func newSpool(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("dump: spool folder: %w", err)
	}
	if err := tightenSpoolDir(dir); err != nil {
		return nil, fmt.Errorf("dump: spool folder: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	clear(key) // the cipher keeps its own expanded copy
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, hex.EncodeToString(rnd[:])+".spool")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a random name in Roadie's own cache folder
	if err != nil {
		return nil, fmt.Errorf("dump: spool file: %w", err)
	}
	s := &Spool{spool{path: path, dir: dir, aead: aead}}
	s.w = &spoolWriter{s: &s.spool, f: f, bw: bufio.NewWriterSize(f, 256<<10), buf: make([]byte, 0, SpoolChunk)}
	return s, nil
}

// nonce is the 12-byte big-endian chunk index.
func nonce(i uint64) []byte {
	var n [12]byte
	be.BigEndian.PutUint64(n[4:], i)
	return n[:]
}

// spoolWriter encrypts what is written in SpoolChunk chunks, each one
// written as its 4-byte big-endian ciphertext length and the ciphertext.
type spoolWriter struct {
	s      *spool
	f      *os.File
	bw     *bufio.Writer
	buf    []byte // plaintext of the chunk being filled
	sealed []byte
	n      uint64 // index of the chunk being filled
	err    error
	closed bool
}

func (w *spoolWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	if w.closed || w.s.removed {
		return 0, errSpoolClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	done := 0
	for len(p) > 0 {
		k := min(len(p), SpoolChunk-len(w.buf))
		w.buf = append(w.buf, p[:k]...)
		p, done = p[k:], done+k
		if len(w.buf) == SpoolChunk {
			if err := w.seal(adMore); err != nil {
				return done, err
			}
		}
	}
	return done, nil
}

// seal encrypts the buffered chunk and writes it. A full chunk is
// sealed as soon as it fills, so the last chunk (sealed by Close) may be
// empty: that keeps the end marker on a chunk of its own when the input
// is a whole number of chunks.
func (w *spoolWriter) seal(ad []byte) error {
	w.sealed = w.s.aead.Seal(w.sealed[:0], nonce(w.n), w.buf, ad)
	var l [4]byte
	be.BigEndian.PutUint32(l[:], uint32(len(w.sealed))) // #nosec G115 -- at most SpoolChunk plus the tag
	if _, err := w.bw.Write(l[:]); err != nil {
		return w.fail(err)
	}
	if _, err := w.bw.Write(w.sealed); err != nil {
		return w.fail(err)
	}
	w.n++
	w.buf = w.buf[:0]
	return nil
}

// fail keeps the first write error, worded as disk-full when the disk is.
func (w *spoolWriter) fail(err error) error {
	if isNoSpace(err) {
		free, _ := freeSpace(w.s.dir)
		err = diskFull(SpoolChunk, free, w.s.dir)
	}
	w.err = err
	return err
}

// Close seals the last chunk, flushes and closes the file. It is safe to
// call more than once.
func (w *spoolWriter) Close() error {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.s.removed {
		return errSpoolClosed
	}
	if w.err == nil {
		if err := w.seal(adLast); err == nil {
			if err := w.bw.Flush(); err != nil {
				_ = w.fail(err) // kept in w.err
			}
		}
	}
	clear(w.buf)
	if err := w.f.Close(); err != nil && w.err == nil {
		w.err = err
	}
	w.s.written = w.err == nil
	return w.err
}

// abandon closes the file without sealing, for Remove.
func (w *spoolWriter) abandon() {
	if !w.closed {
		w.closed = true
		clear(w.buf)
		_ = w.f.Close()
	}
}

func (s *spool) writer() io.WriteCloser { return s.w }

// reader opens the spool for one more pass.
func (s *spool) reader() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.removed:
		return nil, errSpoolClosed
	case !s.written:
		return nil, errors.New("dump: the spool is read before its writer was closed")
	}
	f, err := os.Open(s.path) // #nosec G304 -- the spool's own file
	if err != nil {
		return nil, err
	}
	return &spoolReader{aead: s.aead, f: f, br: bufio.NewReaderSize(f, 256<<10)}, nil
}

// spoolReader decrypts a spool chunk by chunk.
type spoolReader struct {
	aead  cipher.AEAD
	f     *os.File
	br    *bufio.Reader
	ct    []byte
	plain []byte // what is left of the current chunk
	buf   []byte
	n     uint64
	last  bool // the last chunk was read
	err   error
}

// errSpoolDamaged is a spool file that was changed, cut or reordered.
var errSpoolDamaged = errors.New("dump: the spool file is damaged or was changed; run again")

func (r *spoolReader) Read(p []byte) (int, error) {
	for len(r.plain) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if r.last {
			// Nothing may follow the last chunk.
			if _, err := r.br.ReadByte(); !errors.Is(err, io.EOF) {
				r.err = errSpoolDamaged
				return 0, r.err
			}
			r.err = io.EOF
			return 0, io.EOF
		}
		if err := r.chunk(); err != nil {
			r.err = err
			return 0, err
		}
	}
	n := copy(p, r.plain)
	r.plain = r.plain[n:]
	return n, nil
}

// chunk reads and opens the next chunk. A chunk that fails to
// authenticate is an error, never skipped.
func (r *spoolReader) chunk() error {
	var l [4]byte
	if _, err := io.ReadFull(r.br, l[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errSpoolDamaged // ended before the last chunk
		}
		return err
	}
	n := be.BigEndian.Uint32(l[:])
	if n < uint32(r.aead.Overhead()) || n > SpoolChunk+uint32(r.aead.Overhead()) { // #nosec G115 -- the GCM tag size
		return errSpoolDamaged
	}
	r.ct = append(r.ct[:0], make([]byte, n)...)
	if _, err := io.ReadFull(r.br, r.ct); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errSpoolDamaged
		}
		return err
	}
	var err error
	if r.buf, err = r.aead.Open(r.buf[:0], nonce(r.n), r.ct, adMore); err != nil {
		if r.buf, err = r.aead.Open(r.buf[:0], nonce(r.n), r.ct, adLast); err != nil {
			return errSpoolDamaged
		}
		r.last = true
	}
	r.n++
	r.plain = r.buf
	return nil
}

func (r *spoolReader) Close() error {
	clear(r.buf)
	r.plain = nil
	return r.f.Close()
}

// remove deletes the spool file; a spool already removed gives nil.
func (s *spool) remove() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removed {
		return nil
	}
	s.removed = true
	s.w.abandon()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
