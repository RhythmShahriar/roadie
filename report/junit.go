// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/xml"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Name    string       `xml:"name,attr"`
	Tests   int          `xml:"tests,attr"`
	Fails   int          `xml:"failures,attr"`
	Errors  int          `xml:"errors,attr"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Fails      int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Timestamp  string          `xml:"timestamp,attr,omitempty"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
	SystemOut  string          `xml:"system-out"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitProblem `xml:"failure"`
	Error     *junitProblem `xml:"error"`
	Skipped   *junitSkipped `xml:"skipped"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// junitSuiteName is the suite PRD 16.3 names.
const junitSuiteName = "roadie.verify"

// JUnit renders e as a JUnit suite (PRD 16.3): suite roadie.verify, one
// case per check; a failed case's message is its findings' titles and
// counts. A run that stopped is one case with an error. The footer and
// the Not covered sentence go in system-out, as in every format.
func JUnit(e *core.Envelope) ([]byte, error) {
	r, err := readReport(e)
	if err != nil {
		return nil, err
	}
	d := r.data
	s := junitSuite{
		Name:      junitSuiteName,
		Timestamp: d.Started,
		Properties: []junitProperty{
			{"tool", e.Tool}, {"version", e.Version}, {"kind", e.Kind}, {"verdict", e.Verdict},
			{"verify_mode", d.VerifyMode}, {"fail_on", d.FailOn},
		},
		SystemOut: NotCoveredLine(d.NotCovered) + "\n" + strings.Join(Footer(r.keyID()), "\n") + "\n",
	}
	if d.Error != nil {
		s.Cases = append(s.Cases, junitCase{Name: "run", Classname: junitSuiteName,
			Error: &junitProblem{Message: d.Error.Msg, Type: d.Error.Code}})
		s.Errors++
	}
	for _, c := range d.Checks {
		jc := junitCase{Name: CheckLabel(c.ID) + " (" + c.ID + ")", Classname: junitSuiteName}
		switch c.Status {
		case "fail":
			var titles, details []string
			for _, f := range r.checkFindings(c.ID) {
				titles = append(titles, findingTitle(f))
				line := f.ID + " " + f.Severity.String() + ": " + findingTitle(f)
				if w := locationLine(f); w != "" {
					line += " at " + w
				}
				if len(f.Examples) > 0 {
					line += " (e.g. " + strings.Join(f.Examples, ", ") + ")"
				}
				details = append(details, line)
			}
			msg := strings.Join(titles, "; ")
			if msg == "" {
				msg = checkResult(c)
			}
			jc.Failure = &junitProblem{Message: msg, Type: c.ID, Text: strings.Join(details, "\n")}
			s.Fails++
		case "skip":
			jc.Skipped = &junitSkipped{Message: checkResult(c)}
			s.Skipped++
		}
		s.Cases = append(s.Cases, jc)
	}
	s.Tests = len(s.Cases)
	out := junitSuites{Name: junitSuiteName, Tests: s.Tests, Fails: s.Fails, Errors: s.Errors, Suites: []junitSuite{s}}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}
