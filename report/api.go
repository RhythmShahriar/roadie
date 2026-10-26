// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package report builds the signed JSON envelope from a result and renders
// it as Markdown, HTML, SARIF and JUnit. Every view is a rendering of the
// signed JSON.
//
// The frozen cross-lane API (lanes.md 3.4) is Envelope and Canonical
// (envelope.go, canonical.go), Signer, LoadSigner and Check (sign.go,
// check.go), the renderers Markdown, HTML, SARIF and JUnit (one file
// each), Brand (here) and Save (save.go). Names are only added, never
// changed.
package report

// Brand puts a name and logo on the HTML report.
type Brand struct {
	Name string
	Logo []byte
}
