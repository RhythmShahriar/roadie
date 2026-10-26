// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package exposure keeps a staging copy out of search engines and away
// from visitors: noindex, robots.txt and a basic auth snippet.
//
// The exported names in this file are the frozen cross-lane API; the site
// lane replaces the bodies, which return core.ErrNotBuilt until then.
package exposure

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// Steps returns the exposure steps.
func Steps(o killswitch.Options) []core.Step {
	return nil
}

// BasicAuth returns the files of a basic auth setup for user and the
// password it chose.
func BasicAuth(user string) (files map[string][]byte, password string, err error) {
	return nil, "", core.ErrNotBuilt
}
