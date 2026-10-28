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
