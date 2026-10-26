// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// The config init, key and git parts land in the next commit.

func initTemplate(string) []byte { return nil }

func loadKey(string, func(string) (string, bool)) (pseudo.Key, string, error) {
	return pseudo.Key{}, "", core.ErrNotBuilt
}

func saveKey(string, pseudo.Key) error { return core.ErrNotBuilt }

func forgetKey(string) error { return core.ErrNotBuilt }

func excludeFromGit(string, []string) (bool, error) { return false, core.ErrNotBuilt }
