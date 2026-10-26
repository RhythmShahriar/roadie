// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package demo makes the small WooCommerce shop rhythm-roadie demo runs
// the whole loop on, with fake customers only.
//
// The exported names in this file are the frozen cross-lane API. The shop
// itself is built in shop.go and the wp-config.php fixture in wpconfig.go.
package demo

import (
	"bufio"
	"io"
)

// Options configures Shop.
type Options struct {
	Seed      int64
	Customers int
	Leaky     bool // leave PII and live-looking keys where a careless copy would
}

// DefaultCustomers is how many customers Shop makes when Options.Customers
// is zero or less.
const DefaultCustomers = 25

// Shop writes a MySQL dump of a small WooCommerce shop to w. The same
// Options always give the same bytes. Every customer is invented from the
// seed, the Stripe key is assembled at run time and every URL points at
// the hosts Production returns.
//
// Leaky asks for the data a careless copy would leave behind. That is the
// production data itself, so the bytes are the same either way: demo
// verifies this dump untouched for its FAIL half and Roadie's copy of it
// for its PASS half.
func Shop(w io.Writer, o Options) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	newShop(o).write(bw)
	return bw.Flush()
}

// Production returns the demo's production hosts.
func Production() []string {
	return []string{productionHost}
}
