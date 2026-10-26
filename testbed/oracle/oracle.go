// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options says what one oracle run judges.
type Options struct {
	Canaries []Canary
	// Dump is the output database dump (plain or gzip), Tree a directory
	// of output files. Either may be empty.
	Dump string
	Tree string
	// Input is the scenario's input dump, for the row count comparison.
	Input string
	// Serialized lists the input cells that held valid serialized PHP;
	// PHP runs the check. Both are needed for the integrity half.
	Serialized []Location
	PHP        PHP
}

// Survivor is a canary found in the output, by ID only.
type Survivor struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Encoding string `json:"encoding"`
	Where    string `json:"where"` // db:<table>, db or file:<path>
	Count    int    `json:"count"`
}

// TableRows compares one table's row count.
type TableRows struct {
	Table  string `json:"table"`
	Input  int64  `json:"input"`
	Output int64  `json:"output"`
}

// Report is the oracle's verdict. It holds IDs, encodings, places and
// counts, never a canary value.
type Report struct {
	Canaries        int            `json:"canaries"`
	Survivors       []Survivor     `json:"survivors"`
	ByEncoding      map[string]int `json:"by_encoding"`
	LiveKeys        int            `json:"live_keys_survived"`
	TestKeysMissing []string       `json:"test_keys_missing"`
	Integrity       *Integrity     `json:"integrity,omitempty"`
	Rows            []TableRows    `json:"rows,omitempty"`
	RowsDiffer      bool           `json:"rows_differ"`
	// Links are the symbolic links in the tree, by path inside it. The
	// oracle doesn't follow them (a link may point anywhere on this
	// machine), so whatever they point to went unread: reported, never
	// counted as clean or as a survivor.
	Links []string `json:"links,omitempty"`
}

// Clean reports whether the output passed gates 1 and 2: no canary or
// live key survived, every test key is still there, every serialized
// value still unserializes and every table kept its row count.
func (r Report) Clean() bool {
	ok := len(r.Survivors) == 0 && len(r.TestKeysMissing) == 0 && !r.RowsDiffer
	if r.Integrity != nil {
		ok = ok && r.Integrity.Failures == 0 && r.Integrity.Missing == 0
	}
	return ok
}

// markers name the table that the rows after them belong to.
var markers = []string{"INSERT INTO ", "\nCOPY "}

// boundaryKinds are canaries made of digits and dots, which could sit
// inside a longer number: a match needs a non-digit on each side.
var boundaryKinds = map[string]bool{"ip": true, "phone": true, "card": true}

func patterns(canaries []Canary, inDump bool) []pattern {
	var pats []pattern
	for _, m := range markers {
		pats = append(pats, pattern{b: []byte(m), canary: -1})
	}
	for i, c := range canaries {
		for _, f := range forms(c.Value, inDump) {
			if len(f.b) < minWindow {
				continue
			}
			text := !strings.HasPrefix(f.enc, EncBase64) && f.enc != EncMD5 && f.enc != EncSHA256
			pats = append(pats, pattern{b: f.b, canary: i, enc: f.enc, boundary: text && boundaryKinds[c.Kind]})
		}
	}
	return pats
}

// isDumpName reports whether a file in a tree is a SQL dump by its name
// (a backup left in the uploads, say), so it is read as one.
func isDumpName(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".sql") || strings.HasSuffix(p, ".sql.gz")
}

// tableAfter reads the table name after a marker.
func tableAfter(rest []byte, marker string) string {
	rest = bytes.TrimLeft(rest[len(marker):], " ")
	end := bytes.IndexAny(rest, " (\n")
	if end < 0 {
		return ""
	}
	return unquoteIdent(string(rest[:end]))
}

type scanState struct {
	canaries []Canary
	spans    map[int][]span
}

func (s *scanState) scanStream(m *matcher, r io.Reader, where string, tables bool) error {
	current := where
	return m.scan(r, func(off int64, p *pattern, rest []byte) {
		if p.canary < 0 {
			if tables {
				if t := tableAfter(rest, string(p.b)); t != "" {
					current = where + ":" + t
				}
			}
			return
		}
		s.spans[p.canary] = append(s.spans[p.canary], span{off, off + int64(len(p.b)), p.enc, current})
	})
}

// scanFile scans one file, then its decompressed bytes when it is gzip,
// or each compressed member when it is a zip archive.
func (s *scanState) scanFile(m *matcher, path, where string, tables bool) error {
	f, err := os.Open(path) // #nosec G304 -- output the operator names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := s.scanStream(m, f, where, tables); err != nil {
		return err
	}
	var magic [4]byte
	n, _ := f.ReadAt(magic[:], 0)
	switch {
	case n >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil // not really gzip: the raw scan above covered it
		}
		zr.Multistream(true)
		return s.scanStream(m, zr, where, tables)
	case n == 4 && string(magic[:]) == "PK\x03\x04":
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return nil // not really zip: the raw scan above covered it
		}
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			if err := s.scanMember(m, zf, where+"!"+zf.Name, tables || isDumpName(zf.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanMember scans one zip member. A stored member's bytes are already
// in the archive as they are, so the raw scan of the archive saw them:
// it is read again only when it is gzip. A member that can't be opened
// (encrypted, an unknown method) is skipped like any unreadable bytes.
func (s *scanState) scanMember(m *matcher, zf *zip.File, where string, tables bool) error {
	rc, err := zf.Open()
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	br := bufio.NewReader(rc)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil
		}
		zr.Multistream(true)
		return s.scanStream(m, zr, where, tables)
	}
	if zf.Method == zip.Store {
		return nil
	}
	return s.scanStream(m, br, where, tables)
}

// Run judges one output.
func Run(ctx context.Context, o Options) (Report, error) {
	rep := Report{Canaries: len(o.Canaries), ByEncoding: map[string]int{}, Survivors: []Survivor{}, TestKeysMissing: []string{}}
	// One matcher for dumps and one for plain files: they differ only in
	// which encoding a doubly readable match is named after (see forms).
	dm, err := newMatcher(patterns(o.Canaries, true))
	if err != nil {
		return rep, err
	}
	fm, err := newMatcher(patterns(o.Canaries, false))
	if err != nil {
		return rep, err
	}
	st := &scanState{canaries: o.Canaries, spans: map[int][]span{}}
	if o.Dump != "" {
		if err := st.scanFile(dm, o.Dump, "db", true); err != nil {
			return rep, err
		}
	}
	if o.Tree != "" {
		err := filepath.WalkDir(o.Tree, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink != 0 {
				rel, _ := filepath.Rel(o.Tree, path)
				rep.Links = append(rep.Links, filepath.ToSlash(rel))
			}
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, _ := filepath.Rel(o.Tree, path)
			m, isDump := fm, isDumpName(path)
			if isDump {
				m = dm
			}
			return st.scanFile(m, path, "file:"+filepath.ToSlash(rel), isDump)
		})
		if err != nil {
			return rep, err
		}
	}
	type key struct{ id, kind, enc, where string }
	counts := map[key]int{}
	for i, c := range o.Canaries {
		spans := outermost(st.spans[i])
		if c.Kind == "key" && !c.Live {
			if len(spans) == 0 {
				rep.TestKeysMissing = append(rep.TestKeysMissing, c.ID)
			}
			continue
		}
		for _, sp := range spans {
			counts[key{c.ID, c.Kind, sp.enc, sp.where}]++
		}
		if c.Kind == "key" && len(spans) > 0 {
			rep.LiveKeys++
		}
	}
	for k, n := range counts {
		rep.Survivors = append(rep.Survivors, Survivor{k.id, k.kind, k.enc, k.where, n})
		rep.ByEncoding[k.enc] += n
	}
	sort.Slice(rep.Survivors, func(i, j int) bool {
		a, b := rep.Survivors[i], rep.Survivors[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		return a.Encoding < b.Encoding
	})
	sort.Strings(rep.TestKeysMissing)
	if o.Dump != "" && o.Input != "" {
		if rep.Rows, rep.RowsDiffer, err = compareRows(o.Input, o.Dump); err != nil {
			return rep, err
		}
	}
	if o.Dump != "" && o.Serialized != nil {
		rc, err := OpenDump(o.Dump)
		if err != nil {
			return rep, err
		}
		in, err := CheckIntegrity(ctx, rc, o.Serialized, o.PHP)
		_ = rc.Close()
		if err != nil {
			return rep, err
		}
		rep.Integrity = &in
	}
	return rep, nil
}

func countRows(path string) (DumpStats, error) {
	rc, err := OpenDump(path)
	if err != nil {
		return DumpStats{}, err
	}
	defer func() { _ = rc.Close() }()
	return ReadDump(rc, nil)
}

func compareRows(input, output string) ([]TableRows, bool, error) {
	in, err := countRows(input)
	if err != nil {
		return nil, false, errors.Join(errors.New("oracle: reading the input dump"), err)
	}
	out, err := countRows(output)
	if err != nil {
		return nil, false, errors.Join(errors.New("oracle: reading the output dump"), err)
	}
	names := map[string]bool{}
	for t := range in.Rows {
		names[t] = true
	}
	for t := range out.Rows {
		names[t] = true
	}
	var rows []TableRows
	differ := false
	for t := range names {
		r := TableRows{Table: t, Input: in.Rows[t], Output: out.Rows[t]}
		_, inHas := in.Rows[t]
		_, outHas := out.Rows[t]
		if r.Input != r.Output || inHas != outHas {
			differ = true
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Table < rows[j].Table })
	return rows, differ, nil
}
