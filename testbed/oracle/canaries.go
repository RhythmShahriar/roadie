// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package oracle is the testbed's independent judge: it looks for every
// seeded canary in Roadie's output, in every encoding a value can hide
// in, checks that serialized PHP values still unserialize in real PHP and
// compares row counts.
//
// It must not import any package of the Roadie module (TestOracleImportsNoRoadie
// enforces it), so a bug in Roadie's own codecs or dump reader cannot make
// the score look good. It has its own canary file reader, encoders, a
// minimal dump reader and a matcher. It never prints a canary value: reports
// carry canary IDs, encodings and where they were found.
package oracle

import (
	"encoding/json"
	"fmt"
	"os"
)

// The canaries.json format the seeder writes (testbed/seed). The oracle
// reads it with its own types so it shares no code with the seeder.

// CanaryFile is runtime/canaries.json.
type CanaryFile struct {
	Version  int      `json:"version"`
	Seed     int64    `json:"seed"`
	Canaries []Canary `json:"canaries"`
}

// Canary is one seeded value. Kind "key" marks an API key; Live tells a
// live key (must not survive) from a test key (must survive unchanged).
type Canary struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Scenario  string   `json:"scenario"`
	Table     string   `json:"table,omitempty"`
	Column    string   `json:"column,omitempty"`
	Value     string   `json:"value"`
	Live      bool     `json:"live,omitempty"`
	Encodings []string `json:"encodings,omitempty"`
}

// CanaryFileVersion is the canaries.json version this oracle reads.
const CanaryFileVersion = 1

// LoadCanaries reads a canaries.json and keeps the canaries of scenario,
// or all of them when scenario is empty.
func LoadCanaries(path, scenario string) ([]Canary, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own runtime file, named by the operator
	if err != nil {
		return nil, err
	}
	var f CanaryFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != CanaryFileVersion {
		return nil, fmt.Errorf("%s: version %d, this oracle reads %d", path, f.Version, CanaryFileVersion)
	}
	var out []Canary
	for _, c := range f.Canaries {
		if scenario == "" || c.Scenario == scenario {
			out = append(out, c)
		}
	}
	return out, nil
}
