// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// SARIF 2.1.0, the subset GitHub code scanning reads.
// https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html

// WikiBase is the wiki page every finding's helpUri points into, with
// the finding ID as its anchor.
const WikiBase = "https://github.com/RhythmShahriar/rhythm/wiki/Roadie-Verify-And-Reports"

const sarifSchema = "https://json.schemastore.org/sarif-2.1.0.json"

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
	// Properties carry PRD 19.1's footer and Not covered sentence, which
	// every report format holds.
	Properties map[string]any `json:"properties"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string           `json:"id"`
	ShortDescription sarifText        `json:"shortDescription"`
	HelpURI          string           `json:"helpUri"`
	Properties       map[string]any   `json:"properties,omitempty"`
	DefaultConfig    *sarifRuleConfig `json:"defaultConfiguration,omitempty"`
}

type sarifRuleConfig struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifInvocation struct {
	ExecutionSuccessful        bool                `json:"executionSuccessful"`
	ToolExecutionNotifications []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string    `json:"level"`
	Message sarifText `json:"message"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation *sarifPhysical `json:"physicalLocation,omitempty"`
	LogicalLocations []sarifLogical `json:"logicalLocations,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

type sarifLogical struct {
	Name               string `json:"name"`
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind"`
}

// SARIF renders e as SARIF 2.1.0 (PRD 16.3): one rule per finding ID
// with its wiki page, one result per finding location. A database
// location is the dump file, or db://<target>/<table>, with the column
// as a logical location; a file finding keeps its path and line. Every
// message holds masked values only, as the envelope does.
func SARIF(e *core.Envelope) ([]byte, error) {
	r, err := readReport(e)
	if err != nil {
		return nil, err
	}
	d := r.data
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{Name: e.Tool, Version: e.Version,
			InformationURI: "https://github.com/RhythmShahriar/roadie", Rules: []sarifRule{}}},
		Invocations: []sarifInvocation{{ExecutionSuccessful: d.Error == nil && d.Result != ResultError}},
		Results:     []sarifResult{},
		Properties: map[string]any{
			"verdict":     e.Verdict,
			"result":      d.Result,
			"verify_mode": d.VerifyMode,
			"not_covered": NotCoveredLine(d.NotCovered),
			"notice":      strings.Join(Footer(r.keyID()), " "),
		},
	}
	if d.Error != nil {
		run.Invocations[0].ToolExecutionNotifications = []sarifNotification{{Level: "error", Message: sarifText{d.Error.Msg}}}
	}
	if c := d.Coverage; c != nil {
		for _, s := range c.Skipped {
			run.Invocations[0].ToolExecutionNotifications = append(run.Invocations[0].ToolExecutionNotifications,
				sarifNotification{Level: "note", Message: sarifText{"skipped " + s.Table + ": " + s.Reason}})
		}
	}
	rules := map[string]bool{}
	for _, f := range d.Findings {
		if !rules[f.ID] {
			rules[f.ID] = true
			run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
				ID:               f.ID,
				ShortDescription: sarifText{cmp.Or(f.Title, f.ID)},
				HelpURI:          WikiBase + "#" + url.PathEscape(f.ID),
				DefaultConfig:    &sarifRuleConfig{Level: sarifLevel(f.Severity)},
				Properties:       map[string]any{"security-severity": securitySeverity(f.Severity)},
			})
		}
		locs := f.Locations
		if len(locs) == 0 {
			locs = []core.LocationCount{{Count: f.Count}}
		}
		for _, l := range locs {
			msg := findingTitle(f)
			if w := l.String(); w != "" {
				msg += ": " + w
			}
			if len(f.Examples) > 0 {
				msg += " (e.g. " + strings.Join(f.Examples, ", ") + ")"
			}
			if f.Fix != "" {
				msg += ". Fix: " + f.Fix
			}
			run.Results = append(run.Results, sarifResult{
				RuleID:              f.ID,
				Level:               sarifLevel(f.Severity),
				Message:             sarifText{msg},
				Locations:           []sarifLocation{r.sarifLocation(l.Location)},
				PartialFingerprints: map[string]string{"roadieFinding/v1": fingerprintOf(f.ID, l.Location)},
				Properties:          map[string]any{"severity": f.Severity.String(), "count": max(l.Count, 1)},
			})
		}
	}
	slices.SortStableFunc(run.Tool.Driver.Rules, func(a, b sarifRule) int { return cmp.Compare(a.ID, b.ID) })
	b, err := marshalIndent(sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{run}})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// sarifLocation places a finding: a file with its line, or a table in
// the dump file or the database with the column as a logical location.
func (r *report) sarifLocation(l core.Location) sarifLocation {
	if l.File != "" {
		p := &sarifPhysical{ArtifactLocation: sarifArtifact{URI: fileURI(l.File)}}
		if l.Line > 0 {
			p.Region = &sarifRegion{StartLine: l.Line}
		}
		return sarifLocation{PhysicalLocation: p}
	}
	t := r.data.Target
	uri := fileURI(t.Display)
	if core.EndpointKind(t.Kind) != core.DumpFile {
		uri = "db://" + url.PathEscape(r.name())
		if l.Table != "" {
			uri += "/" + url.PathEscape(l.Table)
		}
	}
	loc := sarifLocation{PhysicalLocation: &sarifPhysical{ArtifactLocation: sarifArtifact{URI: uri}}}
	if l.Table != "" {
		name := cmp.Or(l.Column, l.Table)
		fq := l.Table
		kind := "resource"
		if l.Column != "" {
			fq, kind = l.Table+"."+l.Column, "member"
		}
		loc.LogicalLocations = []sarifLogical{{Name: name, FullyQualifiedName: fq, Kind: kind}}
	}
	return loc
}

// fileURI writes a path as a URI with forward slashes: a relative path
// stays a relative reference, which code scanning resolves against the
// repository; an absolute one (/srv/x.sql, C:\x.sql, \\host\share) becomes
// a file: URI, since "C:/x.sql" would read as a URI with the scheme "c".
func fileURI(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	u := strings.Join(parts, "/")
	switch {
	case strings.HasPrefix(p, "//"):
		return "file:" + u
	case strings.HasPrefix(p, "/"):
		return "file://" + u
	case len(p) >= 2 && p[1] == ':' && ('a' <= p[0]|0x20 && p[0]|0x20 <= 'z'):
		return "file:///" + u
	case strings.Contains(parts[0], ":"):
		return "./" + u // a colon in the first segment would read as a scheme
	}
	return u
}

// sarifLevel maps a severity to SARIF's levels.
func sarifLevel(s core.Severity) string {
	switch {
	case s >= core.SeverityHigh:
		return "error"
	case s == core.SeverityMedium:
		return "warning"
	}
	return "note"
}

// securitySeverity is the score GitHub sorts code scanning alerts by.
func securitySeverity(s core.Severity) string {
	switch s {
	case core.SeverityCritical:
		return "9.5"
	case core.SeverityHigh:
		return "8.0"
	case core.SeverityMedium:
		return "5.0"
	case core.SeverityLow:
		return "3.0"
	}
	return "0.0"
}

// fingerprintOf identifies a finding across runs: its ID and where it is,
// never a value.
func fingerprintOf(id string, l core.Location) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{id, l.File, l.Table, l.Column, l.Row, l.Path}, "\x00")))
	return hex.EncodeToString(sum[:8])
}
