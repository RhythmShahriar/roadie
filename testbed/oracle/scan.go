// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"
)

// pattern is one form of one canary, or a table marker (canary -1).
type pattern struct {
	b        []byte
	canary   int
	enc      string
	boundary bool // the bytes on either side must not be digits
}

// matcher finds many fixed byte patterns in one pass over a stream. Every
// pattern is at least w bytes long; its first w bytes index it; a
// bitset over a hash of those bytes rejects almost every position before
// the map is looked at. It runs at memory speed on dumps of many GB,
// where a pass per canary would take hours.
type matcher struct {
	pats   []pattern
	index  map[uint64][]int32
	bits   []uint64
	w      int
	maxLen int
}

const (
	bloomBits = 22
	// minWindow is the shortest pattern the matcher indexes; canaries are
	// made longer than this (testbed/seed).
	minWindow = 6
	maxWindow = 8
	chunkSize = 1 << 20
	// lookahead is how many bytes after a match stay in the buffer, so a
	// marker's table name is complete when the callback reads it.
	lookahead = 160
)

func newMatcher(pats []pattern) (*matcher, error) {
	m := &matcher{pats: pats, index: map[uint64][]int32{}, bits: make([]uint64, 1<<bloomBits/64), w: maxWindow}
	for _, p := range pats {
		if len(p.b) < minWindow {
			return nil, errors.New("oracle: a pattern is shorter than the matcher's window")
		}
		m.w = min(m.w, len(p.b))
		m.maxLen = max(m.maxLen, len(p.b))
	}
	m.maxLen = max(m.maxLen, lookahead)
	for i, p := range pats {
		k := m.key(p.b)
		m.index[k] = append(m.index[k], int32(i)) // #nosec G115 -- pattern counts are far below 2^31
		h := hash(k)
		m.bits[h/64] |= 1 << (h % 64)
	}
	return m, nil
}

// key is the first w bytes of b as a little-endian number. Eight bytes
// are loaded at once whenever b has them and masked down to w.
func (m *matcher) key(b []byte) uint64 {
	mask := ^uint64(0) >> (64 - 8*m.w)
	if len(b) >= 8 {
		return binary.LittleEndian.Uint64(b) & mask
	}
	var k uint64
	for i := min(len(b), 8) - 1; i >= 0; i-- {
		k = k<<8 | uint64(b[i])
	}
	return k & mask
}

func hash(k uint64) uint64 { return (k * 0x9e3779b97f4a7c15) >> (64 - bloomBits) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// scan calls fn for every match in r with its offset, the pattern and the
// buffer from the match on (at least lookahead bytes unless r ends).
func (m *matcher) scan(r io.Reader, fn func(off int64, p *pattern, rest []byte)) error {
	buf := make([]byte, 0, chunkSize+m.maxLen+1)
	var base int64
	prev, hasPrev := byte(0), false
	eof := false
	for !eof {
		n, err := io.ReadFull(r, buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		switch {
		case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
			eof = true
		case err != nil:
			return err
		}
		// Positions whose longest possible match and the byte after it
		// are in the buffer; at the end, every position left.
		limit := len(buf) - m.maxLen
		if eof {
			limit = len(buf) - m.w + 1
		}
		for i := 0; i < limit; i++ {
			k := m.key(buf[i:])
			if h := hash(k); m.bits[h/64]&(1<<(h%64)) == 0 {
				continue
			}
			for _, pi := range m.index[k] {
				p := &m.pats[pi]
				end := i + len(p.b)
				if end > len(buf) || !bytes.Equal(buf[i:end], p.b) {
					continue
				}
				if p.boundary {
					before, hasBefore := prev, hasPrev
					if i > 0 {
						before, hasBefore = buf[i-1], true
					}
					if (hasBefore && isDigit(before)) || (end < len(buf) && isDigit(buf[end])) {
						continue
					}
				}
				fn(base+int64(i), p, buf[i:])
			}
		}
		if limit <= 0 {
			if eof {
				break
			}
			continue
		}
		prev, hasPrev = buf[limit-1], true
		base += int64(limit)
		buf = buf[:copy(buf, buf[limit:])]
	}
	return nil
}

// span is one match of a canary.
type span struct {
	start, end int64
	enc, where string
}

// outermost keeps the matches that no other match of the same canary
// contains: the raw form inside a serialized match is that serialized
// match, not a second survivor.
func outermost(spans []span) []span {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].where != spans[j].where {
			return spans[i].where < spans[j].where
		}
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var out []span
	for _, s := range spans {
		if n := len(out); n > 0 && out[n-1].where == s.where && s.start >= out[n-1].start && s.end <= out[n-1].end {
			continue
		}
		out = append(out, s)
	}
	return out
}
