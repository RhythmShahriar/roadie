// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	rdb "github.com/RhythmShahriar/roadie/db"
)

// A database without AutomateWoo's queue table reports AutomateWoo as
// not present, not unknown: the screen would list it as still on. A
// missing wc_webhooks table is no webhooks.
func TestAutomateWooNotPresent(t *testing.T) {
	d := prodDB()
	delete(d, "wp_automatewoo_queue")
	delete(d, "wp_wc_webhooks")
	s := fixtureSite(prodFiles(), d)
	inner := s.SourceFunc
	s.SourceFunc = func(tables []string) (core.Source, error) {
		for _, n := range tables {
			if _, ok := d[n]; !ok {
				return nil, &rdb.NoTableError{DB: "staging", Table: n}
			}
		}
		return inner(tables)
	}
	status, ids := check(t, s, fixtureOptions())
	if status[core.FindingSideEffectAutomateWoo] != "not-present" || len(ids[core.FindingSideEffectAutomateWoo]) != 0 {
		t.Errorf("automatewoo: %q %v", status[core.FindingSideEffectAutomateWoo], ids[core.FindingSideEffectAutomateWoo])
	}
	if status[core.FindingSideEffectWebhooks] != "not-present" {
		t.Errorf("webhooks: %q", status[core.FindingSideEffectWebhooks])
	}
}
