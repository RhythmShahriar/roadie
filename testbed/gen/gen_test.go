// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package gen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

func write(t *testing.T, o Options) ([]byte, *Result) {
	t.Helper()
	if o.Values == nil {
		o.Values = seed.NewValues(o.Seed, Scenario)
	}
	var buf bytes.Buffer
	res, err := Write(&buf, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != int64(buf.Len()) {
		t.Fatalf("result says %d bytes, wrote %d", res.Bytes, buf.Len())
	}
	return buf.Bytes(), res
}

func TestSizeWithinOnePercent(t *testing.T) {
	for _, size := range []int64{2 << 20, 5_000_000, 24 << 20} {
		_, res := write(t, Options{Size: size, Seed: 1})
		if d := float64(res.Bytes-size) / float64(size); d > 0.01 || d < -0.01 {
			t.Errorf("asked %d, wrote %d (%.2f%% off)", size, res.Bytes, d*100)
		}
	}
	if _, err := Write(io.Discard, Options{Size: 10_000, Values: seed.NewValues(1, Scenario)}); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Errorf("a tiny size must be refused, got %v", err)
	}
	if _, err := Write(io.Discard, Options{Size: 2 << 20}); err == nil {
		t.Error("no value maker must be an error")
	}
}

func TestDeterministic(t *testing.T) {
	a, ra := write(t, Options{Size: 3 << 20, Seed: 7})
	b, rb := write(t, Options{Size: 3 << 20, Seed: 7})
	c, _ := write(t, Options{Size: 3 << 20, Seed: 8})
	if sha256.Sum256(a) != sha256.Sum256(b) {
		t.Error("one seed gave two dumps")
	}
	if sha256.Sum256(a) == sha256.Sum256(c) {
		t.Error("two seeds gave one dump")
	}
	if !slices.Equal(ra.Placed, rb.Placed) {
		t.Error("canary offsets differ for one seed")
	}
}

// The dump reads back (the oracle's own reader), has the shape r17 needs
// and every canary sits at its recorded offset: the first row, the last
// row and both sides of the 4 GiB boundary (moved to 768 KiB here).
func TestShapeAndCanaries(t *testing.T) {
	// The boundary inside wp_users, as 4 GiB is in a 20 GB dump.
	const around = 768 << 10
	b, res := write(t, Options{Size: 6 << 20, Seed: 1, Around: around, SerializedEvery: 50})
	s := string(b)
	for _, want := range []string{"-- MySQL dump 10.13", "DELIMITER ;;", "TRIGGER `wp_users_lower_email`", "DELIMITER ;\n", ",'thumb-1.webp',0x", "-- Dump completed on"} {
		if !strings.Contains(s, want) {
			t.Errorf("the dump lacks %q", want)
		}
	}
	if !strings.HasSuffix(s, "-- Dump completed on 2026-10-01 12:00:00\n") {
		t.Error("the dump doesn't end with mysqldump's trailer")
	}
	st, err := oracle.ReadDump(bytes.NewReader(b), nil)
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range res.Rows {
		if st.Rows[table] != n {
			t.Errorf("%s: %d rows read back, %d written", table, st.Rows[table], n)
		}
	}
	if want := int64(6<<20) / 4; res.BigRow.Length < want || !strings.HasPrefix(s[res.BigRow.Offset:], `a:1:{s:6:\"report\";s:`) {
		t.Errorf("big row %+v", res.BigRow)
	}
	where := map[string]int{}
	for _, p := range res.Placed {
		where[p.Where]++
		i := slices.IndexFunc(res.Canaries, func(c seed.Canary) bool { return c.ID == p.ID })
		if i < 0 {
			t.Fatalf("placed %s isn't a canary", p.ID)
		}
		v := res.Canaries[i].Value
		if got := s[p.Offset : p.Offset+int64(len(v))]; got != v {
			t.Errorf("%s at %d holds something else", p.ID, p.Offset)
		}
		switch p.Where {
		case "first-row":
			if p.Offset > int64(strings.Index(s, "INSERT INTO `wp_users`"))+200 {
				t.Errorf("%s isn't in the first row", p.ID)
			}
		case "last-row":
			if res.Bytes-p.Offset > 1024 {
				t.Errorf("%s is %d bytes from the end", p.ID, res.Bytes-p.Offset)
			}
		case "around-4gib":
			if d := p.Offset - around; d < -(300<<10) || d > 600<<10 {
				t.Errorf("%s is %d bytes from the boundary", p.ID, d)
			}
		}
	}
	if where["first-row"] != 2 || where["last-row"] != 2 || where["around-4gib"] != 4 { // a wp_users row holds an email and a name
		t.Errorf("placed %v", where)
	}
	var before, after bool
	for _, p := range res.Placed {
		if p.Where == "around-4gib" {
			before = before || p.Offset < around
			after = after || p.Offset > around
		}
	}
	if !before || !after {
		t.Error("the boundary canaries must sit on both sides of it")
	}

	// The independent oracle finds every one of them in the raw dump.
	cs := make([]oracle.Canary, len(res.Canaries))
	for i, c := range res.Canaries {
		cs[i] = oracle.Canary{ID: c.ID, Kind: c.Kind, Scenario: c.Scenario, Value: c.Value}
	}
	found := map[string]bool{}
	ids := map[string]bool{}
	_, err = oracle.ReadDump(bytes.NewReader(b), func(r oracle.Row) error {
		ids[r.Table+" "+r.Key] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/db.sql"
	if err := writeFile(path, b); err != nil {
		t.Fatal(err)
	}
	rep, err := oracle.Run(context.Background(), oracle.Options{Canaries: cs, Dump: path})
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range rep.Survivors {
		found[sv.ID] = true
	}
	for _, c := range cs {
		if !found[c.ID] {
			t.Errorf("the oracle misses %s (%s)", c.ID, c.Kind)
		}
	}
	// Every sampled serialized cell names a row that exists.
	if len(res.Serialized) < 10 {
		t.Fatalf("%d serialized cells sampled", len(res.Serialized))
	}
	for _, l := range res.Serialized {
		if !ids[l.Table+" "+l.Key] {
			t.Errorf("sampled cell %s %s isn't a row", l.Table, l.Key)
		}
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"200MB": 200_000_000, "2GB": 2_000_000_000, "20gb": 20_000_000_000, "1.5GiB": 3 << 29, "64KiB": 65536, "4096": 4096, "10B": 10} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%s: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "MB", "-2GB", "2TB", "two", "0"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func FuzzParseSize(f *testing.F) {
	for _, s := range []string{"200MB", "1.5GiB", "4096", "", "9e99GB"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := ParseSize(s)
		if err == nil && n <= 0 {
			t.Errorf("%q gave %d without an error", s, n)
		}
	})
}

func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o600)
}

// Every sampled serialized cell (and the big row) has PHP's framing right:
// each s:N: holds exactly N bytes. PHP itself checks the same cells in
// the testbed (the oracle's integrity half); this is the quick check.
func TestSerializedFraming(t *testing.T) {
	b, res := write(t, Options{Size: 3 << 20, Seed: 3, SerializedEvery: 1})
	want := map[string]bool{}
	for _, l := range res.Serialized {
		want[l.Table+" "+l.Key] = true
	}
	n := 0
	_, err := oracle.ReadDump(bytes.NewReader(b), func(r oracle.Row) error {
		if !want[r.Table+" "+r.Key] && r.Key != "option_id=3" {
			return nil
		}
		col := "meta_value"
		if r.Table == "wp_options" {
			col = "option_value"
		}
		v, _ := r.Get(col)
		if rest, ok := phpValue(v.S); !ok || rest != "" {
			t.Errorf("%s %s: not one serialized value", r.Table, r.Key)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(res.Serialized)+1 {
		t.Errorf("checked %d cells, sampled %d (+ the big row)", n, len(res.Serialized))
	}
	for _, bad := range []string{`s:5:"abc";`, `a:1:{s:1:"a";}`, `i:1`, `x:0;`} {
		if rest, ok := phpValue(bad); ok && rest == "" {
			t.Errorf("%q passes the framing check", bad)
		}
	}
}

// phpValue reads one serialized value (s, i, b, a) and returns the rest.
func phpValue(s string) (string, bool) {
	if len(s) < 2 || s[1] != ':' {
		return s, false
	}
	switch s[0] {
	case 'i', 'b':
		end := strings.IndexByte(s, ';')
		return s[end+1:], end > 2
	case 's':
		rest := s[2:]
		colon := strings.IndexByte(rest, ':')
		n, err := strconv.Atoi(rest[:max(colon, 0)])
		if colon < 0 || err != nil || len(rest) < colon+n+4 || rest[colon+1] != '"' || rest[colon+2+n:colon+4+n] != `";` {
			return s, false
		}
		return rest[colon+4+n:], true
	case 'a':
		rest := s[2:]
		colon := strings.IndexByte(rest, ':')
		n, err := strconv.Atoi(rest[:max(colon, 0)])
		if colon < 0 || err != nil || !strings.HasPrefix(rest[colon+1:], "{") {
			return s, false
		}
		rest = rest[colon+2:]
		for i := 0; i < 2*n; i++ {
			var ok bool
			if rest, ok = phpValue(rest); !ok {
				return s, false
			}
		}
		if !strings.HasPrefix(rest, "}") {
			return s, false
		}
		return rest[1:], true
	}
	return s, false
}
