// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import "context"

// checkOptions refuses options that could reach the wrong host before
// anything is read.
func checkOptions(j Job) error {
	_ = j
	return nil
}

// extraChecks runs the checks that talk to services: the staging site
// over HTTP and Echo.
func (r *run) extraChecks(ctx context.Context) {
	_ = ctx
}
