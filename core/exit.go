// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "github.com/RhythmShahriar/rhythm/kit/cli"

// ExitFor maps a result to the kit exit codes (PRD 7): Err.Exit when set;
// 1 when any finding is at or above failOn; 3 when a verify scanned
// nothing (no table or no row); 0 otherwise.
func ExitFor(r *Result, failOn Severity) int {
	if r == nil {
		return cli.ExitUnavailable
	}
	if r.Err != nil {
		return r.Err.Exit
	}
	for _, f := range r.AllFindings() {
		if failOn.Fails(f.Severity) {
			return cli.ExitFindings
		}
	}
	if v := r.Verify; v != nil && (v.Coverage.TablesScanned == 0 || v.Coverage.RowsScanned == 0) {
		return cli.ExitUnavailable
	}
	return cli.ExitOK
}
