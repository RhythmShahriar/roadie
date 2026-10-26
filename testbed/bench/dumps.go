// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// siteSetups are the setup steps a dump-level run can leave out: they
// only prepare the staging site the scenario writes to, which a dump
// target replaces.
var siteSetups = []string{manifest.SetupStagingSite, manifest.SetupSnapshot}

// siteOnlyIDs are finding ID prefixes only a site target can have: files,
// side effects, the site's config and its pages. A dump has none of them.
var siteOnlyIDs = []string{"uploads.", "side-effect.", "config.", "search.", "auth."}

// dumpTarget is where a dump-level copy writes, in place of the site.
const dumpTarget = "${work}/out.sql"

// DumpScenarios returns the scenarios of f that read a seeded dump and
// can run on this machine without the testbed: copies to a dump file
// (a copy to the staging site is turned into one), and verify of a dump.
// The gates that need the testbed (the sinkhole, the boot check, WP-CLI
// checks, imports into its databases) are left out; the exit and error
// codes, the oracle (gates 1 and 2), the joins and the fresh verify
// (gate 4) stay. withPHP keeps gate 2's serialized check, which needs
// PHP; without it the row counts are compared.
func DumpScenarios(f *manifest.File, withPHP bool) []manifest.Scenario {
	var out []manifest.Scenario
	for _, s := range f.Scenarios {
		if s.Pending != "" || s.Input == "" || s.Perf || s.Good || !dumpLevel(s) {
			continue
		}
		if s.Kind != manifest.KindVerify && s.Kind != manifest.KindCopy && s.ID != "r24" {
			continue
		}
		d := s
		d.Run, d.Setup, d.Multisite = manifest.RunHost, nil, ""
		d.Boot, d.Drill, d.ImportInto, d.Absent, d.WPChecks = false, false, nil, nil, nil
		d.Command, d.Verify, d.Output = nil, nil, manifest.Output{}
		d.Expect, d.Forbid = manifest.Expect{}, nil
		d.Cases = nil
		for _, c := range s.AllCases() {
			if c.Pending != "" || toolDump(c.Command) {
				continue
			}
			if len(s.Cases) == 0 {
				c.Expect = s.Expect
			}
			d.Cases = append(d.Cases, dumpCase(c, withPHP))
		}
		if len(d.Cases) == 0 {
			continue
		}
		if len(s.Cases) == 0 {
			// One unnamed case, as the manifest had it.
			c := d.Cases[0]
			d.Command, d.Verify, d.Output, d.Expect, d.Forbid = c.Command, c.Verify, c.Output, c.Expect, c.Forbid
			d.Cases = nil
		}
		out = append(out, d)
	}
	return out
}

// dumpLevel reports whether every case of s reads a dump of its input
// and needs nothing of the testbed but the staging site it would write.
func dumpLevel(s manifest.Scenario) bool {
	if s.Run != manifest.RunHost && s.Run != manifest.RunStagingWP {
		return false
	}
	for _, st := range s.Setup {
		if !slices.Contains(siteSetups, st) {
			return false
		}
	}
	for _, c := range s.AllCases() {
		if c.Action.Name != "" {
			return false
		}
		for _, st := range c.Setup {
			if !slices.Contains(siteSetups, st) {
				return false
			}
		}
		if !readsDump(c.Command) {
			return false
		}
	}
	return true
}

// readsDump reports whether a command reads a dump of the seeded input:
// copy --from ${in}/..., or verify ${in}/... (with --from ${in}/... if
// any), and names no host or database of the testbed.
func readsDump(args []string) bool {
	if len(args) < 2 {
		return false
	}
	for _, a := range args {
		if strings.Contains(a, "${ssh_source}") || strings.Contains(a, "${staging_dsn}") || strings.Contains(a, "${output}") {
			return false
		}
	}
	switch args[0] {
	case "copy":
		i := slices.Index(args, "--from")
		return i >= 0 && i+1 < len(args) && strings.HasPrefix(args[i+1], "${in}/")
	case "verify":
		return strings.HasPrefix(args[1], "${in}/")
	}
	return false
}

// toolDump reports whether a command reads a dump the real tools wrote
// (make formats runs them in the testbed's containers).
func toolDump(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "${in}/tool-") })
}

// dumpCase turns one case into its dump-level form.
func dumpCase(c manifest.Case, withPHP bool) manifest.Case {
	c.Setup, c.WPChecks = nil, nil
	c.Command = toDump(c.Command)
	c.Verify = toDump(c.Verify)
	if c.Output.DB != "" || c.Output.Tree != "" {
		c.Output = manifest.Output{DB: dumpTarget}
	}
	e := c.Expect
	e.Sinkhole, e.TargetState = nil, ""
	if e.Unchanged {
		// A run that stops leaves the dump target unwritten.
		e.Unchanged, e.NoFinishedOutput = false, true
	}
	if e.Integrity && !withPHP {
		e.Integrity, e.RowsOnly = false, true
	}
	if e.NoFinishedOutput || e.ExitCode != nil && *e.ExitCode != 0 && e.VerifyExit == nil {
		// A run that stops writes nothing to judge.
		c.Output, c.Verify, e.Canaries, e.Integrity, e.RowsOnly = manifest.Output{}, nil, nil, false, false
	}
	e.FindingIDs = dataIDs(e.FindingIDs)
	e.Locations = slices.DeleteFunc(slices.Clone(e.Locations), func(l string) bool { return strings.HasPrefix(l, "file:") })
	c.Expect = e
	c.Forbid = dataIDs(c.Forbid)
	return c
}

// toDump points a command's site target at the dump target and drops
// the flags only a site target takes.
func toDump(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--confirm-target", "--target-is-staging":
			i++
			continue
		case "--yes":
			continue
		case "${site}":
			out = append(out, dumpTarget)
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// dataIDs leaves out the finding IDs only a site target can have.
func dataIDs(ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
		return slices.ContainsFunc(siteOnlyIDs, func(p string) bool { return strings.HasPrefix(id, p) })
	})
}
