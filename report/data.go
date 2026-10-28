// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"github.com/RhythmShahriar/roadie/core"
)

// Data is the data object of a copy, prepare, verify or plan envelope,
// field for field as PRD 16.2 lists it. Envelope builds it from a result
// and every renderer reads it back, so a view can only show what the
// signed JSON holds. Fields are only added in v1.
type Data struct {
	Result              string          `json:"result"`      // pass, fail, error
	VerifyMode          string          `json:"verify_mode"` // exact, heuristic, none
	FailOn              string          `json:"fail_on"`
	Started             string          `json:"started"` // RFC 3339 UTC
	DurationMS          int64           `json:"duration_ms"`
	Source              EndpointData    `json:"source"`
	Target              TargetData      `json:"target"`
	TargetState         string          `json:"target_state"`
	Production          []string        `json:"production"`
	URL                 string          `json:"url"`
	Stack               StackData       `json:"stack"`
	Consistency         ConsistencyData `json:"consistency"`
	Input               *Fingerprint    `json:"input"`
	Output              *Fingerprint    `json:"output"`
	RulesetSHA256       string          `json:"ruleset_sha256"`
	ConfigSHA256        string          `json:"config_sha256"`
	ProfilePack         string          `json:"profile_pack"`
	EchoConnectorSHA256 string          `json:"echo_connector_sha256"`
	Tables              []TableData     `json:"tables"`
	Covered             []ColumnRate    `json:"covered"`
	Uncovered           []ColumnRate    `json:"uncovered"`
	Keys                []KeyData       `json:"keys"`
	SideEffects         []SideEffect    `json:"side_effects"`
	Findings            []core.Finding  `json:"findings"`
	Checks              []CheckData     `json:"checks"`
	Coverage            *CoverageData   `json:"coverage"`
	RemoteCommands      []string        `json:"remote_commands"`
	NotCovered          []string        `json:"not_covered"`
	Soundcheck          *SoundcheckData `json:"soundcheck"`
	Error               *ErrorData      `json:"error,omitempty"`
	Signature           *Signature      `json:"signature,omitempty"`
}

// EndpointData is data.source; data.target adds its state to it.
type EndpointData struct {
	Kind    string `json:"kind"`
	Display string `json:"display"`
	Engine  string `json:"engine"`
	Server  string `json:"server"`
	Bytes   int64  `json:"bytes"`
}

// TargetData is data.target: the endpoint and what the run left it in.
type TargetData struct {
	EndpointData
	State string `json:"state"`
}

// StackData is data.stack.
type StackData struct {
	Name      string   `json:"name"`
	Profiles  []string `json:"profiles"`
	Multisite bool     `json:"multisite"`
	HPOS      bool     `json:"hpos"`
	Sites     int      `json:"sites"`
}

// ConsistencyData is data.consistency. A per-run key has no fingerprint,
// which the JSON writes as null.
type ConsistencyData struct {
	Mode           string  `json:"mode"`
	KeyFingerprint *string `json:"key_fingerprint"`
}

// Fingerprint is data.input or data.output.
type Fingerprint struct {
	Kind  string `json:"kind"` // sha256-file or db-tables
	Value string `json:"value"`
	Bytes int64  `json:"bytes"`
}

// TableData is one entry of data.tables.
type TableData struct {
	Name    string       `json:"name"`
	Rows    int64        `json:"rows"`
	Action  string       `json:"action"`
	Profile string       `json:"profile"`
	Columns []ColumnData `json:"columns"`
}

// ColumnData is one column of a table entry.
type ColumnData struct {
	Name    string `json:"name"`
	Rule    string `json:"rule"`
	Changed int64  `json:"changed"`
	Kept    int64  `json:"kept"`
}

// ColumnRate is one entry of data.covered or data.uncovered.
type ColumnRate struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Rate   int    `json:"rate"`
}

// KeyData is one entry of data.keys; Masked is the only form of the key.
type KeyData struct {
	ID       string        `json:"id"`
	Severity string        `json:"severity"`
	Provider string        `json:"provider"`
	Env      string        `json:"env"`
	Location core.Location `json:"location"`
	Masked   string        `json:"masked"`
	Action   string        `json:"action"`
}

// SideEffect is one entry of data.side_effects.
type SideEffect struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	How    string `json:"how"`
	Undo   string `json:"undo"`
}

// CheckData is one entry of data.checks.
type CheckData struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // pass, warn, fail, skip
	Checked int64  `json:"checked"`
	Detail  string `json:"detail,omitempty"`
}

// CoverageData is data.coverage; null when the run had no verify.
type CoverageData struct {
	TablesScanned int           `json:"tables_scanned"`
	TablesTotal   int           `json:"tables_total"`
	RowsScanned   int64         `json:"rows_scanned"`
	BytesScanned  int64         `json:"bytes_scanned"`
	Skipped       []SkippedData `json:"skipped"`
	Sample        int           `json:"sample"`
	// SkippedChecks are the data checks --skip left out; any makes the
	// run partial and complete false.
	SkippedChecks []string `json:"skipped_checks"`
}

// SkippedData is a table verify didn't read, with why.
type SkippedData struct {
	Table  string `json:"table"`
	Reason string `json:"reason"`
}

// SoundcheckData is data.soundcheck; null unless verify --soundcheck ran.
type SoundcheckData struct {
	Ran      bool             `json:"ran"`
	ExitCode int              `json:"exit_code"`
	Schema   string           `json:"schema"`
	Counts   map[string]int64 `json:"counts"`
	Note     string           `json:"note,omitempty"`
}

// Signature is data.signature (PRD 16.4).
type Signature struct {
	Alg       string `json:"alg"` // ed25519
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"` // base64 of the raw 32-byte key
	Sig       string `json:"sig"`        // base64 of the 64-byte signature
}

// ErrorData is data.error: the stop's code, exit and message. A prepare
// that stopped after its backup adds the backup's path and the command
// that restores it, so a script can undo a target left mixed.
type ErrorData struct {
	*core.Error
	Backup  string `json:"backup,omitempty"`
	Restore string `json:"restore,omitempty"`
}
