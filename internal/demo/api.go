// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package demo makes the small WooCommerce shop rhythm-roadie demo runs
// the whole loop on, with fake customers only.
//
// The exported names in this file are the frozen cross-lane API; the
// testbed lane replaces the bodies, which return core.ErrNotBuilt until then.
package demo

import (
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// Options configures Shop.
type Options struct {
	Seed      int64
	Customers int
	Leaky     bool // leave PII and live-looking keys where a careless copy would
}

// Shop writes a MySQL dump of a small WooCommerce shop to w.
func Shop(w io.Writer, o Options) error {
	return core.ErrNotBuilt
}

// Production returns the demo's production hosts.
func Production() []string {
	return nil
}
