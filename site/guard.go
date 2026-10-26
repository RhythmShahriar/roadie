// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"

	"github.com/RhythmShahriar/roadie/core"
)

func guard(context.Context, GuardInput) error { return core.ErrNotBuilt }
