// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A named table the database lacks fails a source with NoTableError,
// unless the context says to leave missing tables out (prepare --only
// side-effects on a single site without WooCommerce webhooks).
func TestSourceMissingTable(t *testing.T) {
	c := openT(t, newSQLite(t, 3), ReadOnly)
	names := []string{"wp_options", "wp_blogs", "wp_wc_webhooks"}
	_, err := c.Source(context.Background(), names)
	var nt *NoTableError
	if !errors.As(err, &nt) || nt.Table != "wp_blogs" {
		t.Fatalf("missing table: %v", err)
	}
	src, err := c.Source(SkipMissing(context.Background()), names)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	var got []string
	for _, b := range drain(t, src) {
		if b.Table != nil && !slices.Contains(got, b.Table.Name) {
			got = append(got, b.Table.Name)
		}
	}
	if !slices.Equal(got, []string{"wp_options"}) {
		t.Errorf("read %v, want wp_options alone", got)
	}
}
