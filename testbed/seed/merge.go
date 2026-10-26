// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seed

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
)

// Values makes canary values for one scenario outside a template, for a
// fixture a program writes (testbed/gen). The same seed and scenario give
// the same values in the same order.
type Values struct {
	scenario string
	gen      *generator
	counts   map[string]int
	seen     map[string]bool
}

// NewValues returns a value maker for scenario.
func NewValues(seedValue int64, scenario string) *Values {
	return &Values{scenario: scenario, gen: newGenerator(seedValue, scenario), counts: map[string]int{}, seen: map[string]bool{}}
}

// Canary makes a fresh canary of kind, recorded at table.column, with its
// ID in the template's <scenario>-<kind>-<nnnn> form.
func (v *Values) Canary(kind, table, column string) (Canary, error) {
	for {
		val, err := v.gen.value(kind)
		if err != nil {
			return Canary{}, err
		}
		if v.seen[val] {
			continue
		}
		v.seen[val] = true
		v.counts[kind]++
		return Canary{
			ID: fmt.Sprintf("%s-%s-%04d", v.scenario, kind, v.counts[kind]), Kind: kind, Scenario: v.scenario,
			Table: table, Column: column, Value: val, Encodings: []string{"raw"},
		}, nil
	}
}

// MergeCanaries replaces scenario's canaries in out/canaries.json with cs
// and keeps every other scenario's, so a generated fixture joins the
// seeded ones. The file is written 0600, as Run writes it.
func MergeCanaries(out string, seedValue int64, scenario string, cs []Canary) error {
	path := filepath.Join(out, "canaries.json")
	kept, err := keepCanaries(path, func(s string) bool { return s != scenario })
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.Scenario != scenario {
			return fmt.Errorf("canary %s belongs to %s, not %s", c.ID, c.Scenario, scenario)
		}
	}
	all := append(kept, cs...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Scenario < all[j].Scenario })
	b, err := json.MarshalIndent(canaryFile{Version: CanaryFileVersion, Seed: seedValue, Canaries: all}, "", " ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'))
}
