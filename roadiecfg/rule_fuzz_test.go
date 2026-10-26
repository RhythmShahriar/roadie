// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// FuzzParseRule feeds random rule names to ParseRule: it never panics, a
// path rule always names its format and never deletes a row; only
// fixed: carries a value.
func FuzzParseRule(f *testing.F) {
	for _, s := range append(RuleNames(), "json:billing.email=email", "serialized:*=username",
		"json:a=delete-row", "json:=email", "fixed:", "fixed:paused", "serialized:a=json:b=email", " email ") {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		spec, err := ParseRule(s)
		if err != nil {
			return
		}
		if spec.Path != "" {
			if !strings.HasPrefix(spec.Path, "json:") && !strings.HasPrefix(spec.Path, "serialized:") {
				t.Fatalf("%q: path %q", s, spec.Path)
			}
			if spec.Action == core.DeleteRow {
				t.Fatalf("%q: delete-row on a path", s)
			}
		}
		if spec.Value != "" && spec.Action != core.Fixed {
			t.Fatalf("%q: value %q on %v", s, spec.Value, spec.Action)
		}
	})
}
