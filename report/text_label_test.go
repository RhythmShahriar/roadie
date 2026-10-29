// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The environment check and side effect cover WP_ENVIRONMENT_TYPE and
// Laravel's APP_ENV, so their labels name neither.
func TestEnvironmentLabelsFitEveryStack(t *testing.T) {
	for _, l := range []string{CheckLabel(core.FindingConfigEnvironment), checkNames[core.FindingConfigEnvironment],
		EffectLabel(core.FindingConfigEnvironment, false), EffectLabel(core.FindingConfigEnvironment, true)} {
		if l == "" || strings.Contains(l, "WP_ENVIRONMENT_TYPE") || strings.Contains(l, "APP_ENV") {
			t.Errorf("label %q", l)
		}
	}
	if got := CheckLabel(core.FindingConfigEnvironment); got != "The site says it is staging" {
		t.Errorf("check label %q", got)
	}
}

// The mail check passes when Echo seals mail or when roadie-staging.php
// blocks it with no connector there, so its label names neither; the
// line's detail says which.
func TestMailCheckLabel(t *testing.T) {
	if got := CheckTitle(core.FindingSideEffectMail, "pass"); got != "Off: Mail" {
		t.Errorf("check label %q", got)
	}
}
