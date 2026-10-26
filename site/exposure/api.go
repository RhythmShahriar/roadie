// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package exposure keeps a staging copy out of search engines and away
// from visitors: noindex, robots.txt and a basic auth snippet.
//
// The exported names in this file are the frozen cross-lane API; new
// names are added, never changed.
package exposure

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// Steps returns the exposure steps: one search.indexable step that
// replaces a physical robots.txt (WordPress and Laravel) and checks that
// the copy tells crawlers to stay out. The noindex hooks themselves are in
// the Roadie mu-plugin, which the kill switch writes.
func Steps(o killswitch.Options) []core.Step {
	return []core.Step{robotsStep(o)}
}

// AllSteps returns every prepare step for the stack in the order they
// apply: killswitch.Steps, then, for WordPress and Laravel, the exposure
// Steps. The kill switch can't add them itself, since this package
// imports it for Options; the engine calls AllSteps so robots.txt is
// never left out.
func AllSteps(stack core.Stack, o killswitch.Options) []core.Step {
	steps := killswitch.Steps(stack, o)
	if stack == core.StackWordPress || stack == core.StackLaravel {
		steps = append(steps, Steps(o)...)
	}
	return steps
}

// BasicAuth returns the files of a basic auth setup for user and the
// password it chose: apache.htaccess, .htpasswd, nginx.conf and Caddyfile,
// to go under BasicAuthDir. The password is in no file; the caller prints
// it once.
func BasicAuth(user string) (files map[string][]byte, password string, err error) {
	return basicAuth(user, PasswdFile)
}
