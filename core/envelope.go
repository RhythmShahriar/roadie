// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "encoding/json"

// EnvelopeSchema is the schema of every --json document and saved report.
// (It can't be called Schema: that name is the database schema type.)
const EnvelopeSchema = "rhythm-roadie/v1"

// Summary is the envelope's counts (PRD 16.2).
type Summary struct {
	Critical            int64 `json:"critical"`
	High                int64 `json:"high"`
	Medium              int64 `json:"medium"`
	Low                 int64 `json:"low"`
	Info                int64 `json:"info"`
	Tables              int   `json:"tables"`
	TablesPseudonymised int   `json:"tables_pseudonymised"`
	Rows                int64 `json:"rows"`
	ValuesPseudonymised int64 `json:"values_pseudonymised"`
	URLsRewritten       int64 `json:"urls_rewritten"`
	KeysFound           int   `json:"keys_found"`
	KeysSwapped         int   `json:"keys_swapped"`
	KeysBlanked         int   `json:"keys_blanked"`
	SideEffectsOff      int   `json:"side_effects_off"`
	SideEffectsTotal    int   `json:"side_effects_total"`
	UncoveredColumns    int   `json:"uncovered_columns"`
}

// Envelope is the JSON document every command writes with --json, in
// Echo's envelope shape.
type Envelope struct {
	Schema   string          `json:"schema"`
	Kind     string          `json:"kind"`
	Tool     string          `json:"tool"`
	Version  string          `json:"version"`
	Complete bool            `json:"complete"`
	Summary  Summary         `json:"summary"`
	Verdict  string          `json:"verdict"` // "ok", "findings", "error"
	Data     json.RawMessage `json:"data"`    // built by report from Result; field list in PRD 16.2
}
