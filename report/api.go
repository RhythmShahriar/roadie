// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package report builds the signed JSON envelope from a result and renders
// it as Markdown, HTML, SARIF and JUnit. Every view is a rendering of the
// signed JSON.
//
// The frozen cross-lane API (lanes.md 3.4) is Envelope and Canonical
// (envelope.go, canonical.go), Signer, LoadSigner and Check (sign.go,
// check.go), the renderers Markdown, HTML, SARIF and JUnit, Brand and
// Save. Names are only added, never changed.
package report

import (
	"github.com/RhythmShahriar/roadie/core"
)

// Markdown renders e for a pull request comment.
func Markdown(e *core.Envelope) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// HTML renders one or more envelopes as one self-contained page.
func HTML(e []*core.Envelope, brand Brand) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// SARIF renders e as SARIF 2.1.0.
func SARIF(e *core.Envelope) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// JUnit renders e as a JUnit suite.
func JUnit(e *core.Envelope) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// Brand puts a name and logo on the HTML report.
type Brand struct {
	Name string
	Logo []byte
}

// Save writes report.json and report.html under dir's .roadie/reports/<ts>/
// and returns the folder.
func Save(dir string, e *core.Envelope, html []byte) (string, error) {
	return "", core.ErrNotBuilt
}
