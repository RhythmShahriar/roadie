// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// unserializeScript reads values framed as an 8-digit byte length and the
// bytes from stdin and prints one line per value: 1 when PHP unserializes
// it (with allowed_classes false, as Roadie's oracle contract says), 0 when
// it doesn't.
//
//go:embed php/unserialize.php
var unserializeScript string

// Location names one cell: a table, the row's key (Row.Key) and a column.
type Location struct {
	Table  string `json:"table"`
	Key    string `json:"key"`
	Column string `json:"column"`
}

// SerializedFile is the list of cells that held valid serialized PHP in a
// scenario's input, written at seed time.
type SerializedFile struct {
	Version int        `json:"version"`
	Cells   []Location `json:"cells"`
}

// PHP runs the unserialize check. Cmd is the command and its first
// arguments, such as ["php"] or ["docker", "run", "--rm", "-i", image, "php"];
// the script is passed with -r, the values on stdin.
type PHP struct {
	Cmd []string
}

// looksSerialized is a cheap filter before PHP sees a value: every
// serialized value starts with a type letter and a colon or is N;.
func looksSerialized(s string) bool {
	if s == "N;" {
		return true
	}
	if len(s) < 4 || s[1] != ':' {
		return false
	}
	return strings.IndexByte("asiObdCE", s[0]) >= 0
}

// Check returns, for each value, whether PHP unserializes it.
func (p PHP) Check(ctx context.Context, values []string) ([]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(p.Cmd) == 0 {
		return nil, errors.New("oracle: no PHP command")
	}
	script := strings.TrimPrefix(unserializeScript, "<?php")
	args := append(append([]string{}, p.Cmd[1:]...), "-r", script)
	cmd := exec.CommandContext(ctx, p.Cmd[0], args...) // #nosec G204 -- the operator's PHP command
	var in bytes.Buffer
	for _, v := range values {
		fmt.Fprintf(&in, "%08d", len(v))
		in.WriteString(v)
	}
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("oracle: php: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(values) {
		return nil, fmt.Errorf("oracle: php answered %d of %d values", len(lines), len(values))
	}
	res := make([]bool, len(values))
	for i, l := range lines {
		res[i] = l == "1"
	}
	return res, nil
}

// phpBatch bounds how many bytes go to one PHP process.
const phpBatch = 64 << 20

// checkAll runs Check in batches of at most phpBatch bytes.
func (p PHP) checkAll(ctx context.Context, values []string) ([]bool, error) {
	var out []bool
	for start := 0; start < len(values); {
		end, size := start, 0
		for end < len(values) && (end == start || size+len(values[end]) < phpBatch) {
			size += len(values[end])
			end++
		}
		ok, err := p.Check(ctx, values[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, ok...)
		start = end
	}
	return out, nil
}

// SerializedCells reads a dump and returns every cell that holds a value
// PHP unserializes, sorted: the list the integrity check replays on the
// output. The seeder calls it on each scenario's input.
func SerializedCells(ctx context.Context, dump io.Reader, p PHP) ([]Location, error) {
	var locs []Location
	var vals []string
	_, err := ReadDump(dump, func(r Row) error {
		for i, v := range r.Vals {
			if !v.Null && i < len(r.Cols) && looksSerialized(v.S) {
				locs = append(locs, Location{r.Table, r.Key, r.Cols[i]})
				vals = append(vals, v.S)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ok, err := p.checkAll(ctx, vals)
	if err != nil {
		return nil, err
	}
	var out []Location
	for i, l := range locs {
		if ok[i] {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessLoc(out[i], out[j]) })
	return out, nil
}

func lessLoc(a, b Location) bool {
	if a.Table != b.Table {
		return a.Table < b.Table
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Column < b.Column
}

// LoadSerialized reads a serialized.json.
func LoadSerialized(path string) ([]Location, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own runtime file
	if err != nil {
		return nil, err
	}
	var f SerializedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Cells, nil
}

// Integrity is the PHP half of gate 2.
type Integrity struct {
	Checked  int        `json:"checked"`
	Failures int        `json:"failures"`
	Missing  int        `json:"missing"` // cells whose row is there and whose value or column is gone
	// Gone counts, by table, the cells whose whole row is gone from the
	// output: a row the copy deleted on purpose (a session) or a
	// truncated table. The row counts say whether that was meant.
	Gone   map[string]int `json:"gone,omitempty"`
	Failed []Location     `json:"failed,omitempty"`
}

// CheckIntegrity unserializes, in the output dump, every cell that held a
// valid serialized value in the input.
func CheckIntegrity(ctx context.Context, output io.Reader, cells []Location, p PHP) (Integrity, error) {
	want := map[Location]bool{}
	for _, c := range cells {
		want[c] = true
	}
	found := map[Location]string{}
	rows := map[[2]string]bool{}
	_, err := ReadDump(output, func(r Row) error {
		rows[[2]string{r.Table, r.Key}] = true
		for i, v := range r.Vals {
			if i >= len(r.Cols) {
				break
			}
			l := Location{r.Table, r.Key, r.Cols[i]}
			if want[l] && !v.Null {
				found[l] = v.S
			}
		}
		return nil
	})
	if err != nil {
		return Integrity{}, err
	}
	var res Integrity
	var locs []Location
	var vals []string
	for _, c := range cells {
		v, ok := found[c]
		if !ok && !rows[[2]string{c.Table, c.Key}] {
			if res.Gone == nil {
				res.Gone = map[string]int{}
			}
			res.Gone[c.Table]++
			continue
		}
		if !ok {
			res.Missing++
			continue
		}
		locs = append(locs, c)
		vals = append(vals, v)
	}
	ok, err := p.checkAll(ctx, vals)
	if err != nil {
		return Integrity{}, err
	}
	res.Checked = len(vals)
	for i, good := range ok {
		if !good {
			res.Failures++
			res.Failed = append(res.Failed, locs[i])
		}
	}
	return res, nil
}
