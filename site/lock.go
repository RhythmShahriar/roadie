// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import "github.com/RhythmShahriar/roadie/core"

// LockNotify is Lock with a callback for the stale-lock note.
func LockNotify(core.Endpoint, func(string)) (func(), error) { return nil, core.ErrNotBuilt }
