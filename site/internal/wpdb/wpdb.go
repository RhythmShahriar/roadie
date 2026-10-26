// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wpdb reads a few rows of a site's own database through
// core.Site.Source, read-only: the options the target guards and the
// kill-switch checks look at plus rows such as pending actions.
package wpdb

import (
	"context"
	"errors"
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// Prefix is the site's WordPress table prefix, "wp_" when unknown.
func Prefix(ctx context.Context, s core.Site) string {
	if id, err := s.DBIdentity(ctx); err == nil && id.Prefix != "" {
		return id.Prefix
	}
	return "wp_"
}

// Rows calls fn for every row of table with the values of cols, in that
// order; a column the table lacks, or a NULL, is nil. A table that isn't
// there yields no rows.
func Rows(ctx context.Context, s core.Site, table string, cols []string, fn func(vals [][]byte) error) error {
	src, err := s.Source(ctx, []string{table})
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if b.Table == nil || b.Table.Name != table {
			continue
		}
		pos := positions(b, cols)
		vals := make([][]byte, len(cols))
		for _, r := range b.Rows {
			for i, p := range pos {
				vals[i] = nil
				if p >= 0 && p < len(r.Values) && r.Values[p].Kind != core.Null {
					vals[i] = r.Values[p].Raw
				}
			}
			if err := fn(vals); err != nil {
				return err
			}
		}
	}
}

// positions maps each wanted column to its value position in the batch.
// Batch.Columns may be empty, meaning every column in table order.
func positions(b *core.Batch, cols []string) []int {
	out := make([]int, len(cols))
	for i, c := range cols {
		out[i] = -1
		ti := b.Table.Col(c)
		if ti < 0 {
			continue
		}
		if len(b.Columns) == 0 {
			out[i] = ti
			continue
		}
		for p, x := range b.Columns {
			if x == ti {
				out[i] = p
				break
			}
		}
	}
	return out
}

// Options reads the named options from <prefix>options. A missing option
// is absent from the map.
func Options(ctx context.Context, s core.Site, names ...string) (map[string][]byte, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	return OptionsMatch(ctx, s, func(n string) bool { return want[n] })
}

// OptionsMatch reads every option whose name match accepts, in one pass.
func OptionsMatch(ctx context.Context, s core.Site, match func(name string) bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := Rows(ctx, s, Prefix(ctx, s)+"options", []string{"option_name", "option_value"}, func(v [][]byte) error {
		if v[0] != nil && match(string(v[0])) {
			out[string(v[0])] = append([]byte(nil), v[1]...)
		}
		return nil
	})
	return out, err
}

// Count counts the rows of table for which keep accepts the values of cols.
func Count(ctx context.Context, s core.Site, table string, cols []string, keep func(vals [][]byte) bool) (int64, error) {
	var n int64
	err := Rows(ctx, s, table, cols, func(v [][]byte) error {
		if keep(v) {
			n++
		}
		return nil
	})
	return n, err
}
