// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// A table the copy truncated (Laravel's sessions) is left out of the
// uncovered scan (PRD 15.2): its ip_address column is no finding.
func TestUncoveredSkipsTruncatedTables(t *testing.T) {
	sessions := table("sessions", "id", "user_id", "ip_address", "payload")
	jobs := table("jobs", "id", "queue")
	job := func(scan func(string) bool) *core.VerifyResult {
		return runJob(t, Job{Source: source(batch(sessions), batch(jobs, []string{"1", "default"})), ScanTable: scan})
	}
	if res := job(nil); len(res.Uncovered) != 1 || res.Uncovered[0].Column != "ip_address" {
		t.Fatalf("without ScanTable: %+v", res.Uncovered)
	}
	res := job(func(tb string) bool { return tb != "sessions" })
	if len(res.Uncovered) != 0 {
		t.Errorf("truncated table scanned: %+v", res.Uncovered)
	}
	for _, f := range res.Findings {
		if f.ID == core.FindingUncoveredColumn {
			t.Errorf("finding %s", f.Title)
		}
	}
}

// A production email in a column DEFAULT is found like one in a row,
// under the table's "(schema)" column; it isn't counted as a row.
func TestDDLLiteralsScanned(t *testing.T) {
	users := table("wp_users", "ID", "user_email")
	ddl := &core.Batch{Pass: &core.Statement{Kind: "alter-table",
		Text: []byte("ALTER TABLE `wp_users` ADD COLUMN `zz_note` varchar(100) DEFAULT 'maria.kowalska@gmail.com';")}}
	res := runJob(t, Job{Source: source(batch(users, []string{"1", "u1@example.invalid"}), ddl)})
	found := false
	for _, f := range res.Findings {
		for _, l := range f.Locations {
			if f.ID == core.FindingPIIEmail && l.Table == "wp_users" && l.Column == "(schema)" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("findings %+v", res.Findings)
	}
	if res.Coverage.RowsScanned != 1 {
		t.Errorf("rows scanned %d", res.Coverage.RowsScanned)
	}
}
