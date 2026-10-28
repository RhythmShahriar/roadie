// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// CheckKeyLive is the check ID that stands for every key.<provider>-live
// finding, so the checks list has one line for live keys whatever the
// provider.
const CheckKeyLive = "key.live"

// dataChecks run over every scanned row; Checked is the rows scanned.
var dataChecks = []string{
	core.FindingPIIEmail, core.FindingPIIPhone, core.FindingPIIName, core.FindingPIIAddress,
	core.FindingPIIIP, core.FindingPIICard, core.FindingPIIIBAN, core.FindingExactSurvivors,
	CheckKeyLive, core.FindingKeyLiveMode, core.FindingKeyGeneric, core.FindingKeyUnknownEnv,
	core.FindingURLProduction, core.FindingURLProductionSiteURL, core.FindingDataEncrypted,
}

// siteFileChecks read a site's files; a database or dump target skips them.
var siteFileChecks = []string{core.FindingConfigSalts, core.FindingConfigPointsAtProduction, core.FindingUploadsPersonal}

// httpChecks need --site.
var httpChecks = []string{core.FindingSearchIndexable, core.FindingAuthNone}

// CheckIDs are the check IDs verify lists in data.checks beyond the
// finding IDs: key.live stands for every key.<provider>-live.
func CheckIDs() []string {
	return slices.Concat(dataChecks, siteFileChecks, httpChecks)
}

// count adds n to what check id looked at, which also marks it as run.
func (r *run) count(id string, n int64) {
	r.checked[id] += n
}

// skipCheck records why check id didn't run, unless it already has a reason.
func (r *run) skipCheck(id, why string) {
	if _, ok := r.why[id]; !ok {
		r.why[id] = why
	}
}

// skipReason names the --skip entry that leaves id out, or "".
func (r *run) skipReason(id string) string {
	probe := id
	if id == CheckKeyLive {
		probe = core.KeyLiveID("stripe")
	}
	for name, s := range skipNames {
		if r.skip[name] {
			for _, x := range s {
				if probe == x || strings.HasSuffix(x, ".") && strings.HasPrefix(probe, x) {
					return "left out with --skip " + name
				}
			}
		}
	}
	return ""
}

// checks lists one core.Check per detector and site check, sorted by ID:
// fail when a finding of the check is at or above --fail-on (low when
// --fail-on is none), warn when it has findings below that, pass when
// it ran without any, skip with the reason when it didn't run or ran and
// couldn't tell the state. Checked counts the rows, files, pages or calls it
// looked at. Hidden findings don't fail a check.
func (r *run) checks() []core.Check {
	listed := map[string]bool{}
	rows := r.res.Coverage.RowsScanned
	for _, id := range dataChecks {
		listed[id] = true
		switch {
		case r.skipReason(id) != "":
			r.skipCheck(id, r.skipReason(id))
		case id == core.FindingExactSurvivors && r.j.Known == nil:
			r.skipCheck(id, "heuristic mode: no record of what Roadie replaced")
		case (id == core.FindingURLProduction || id == core.FindingURLProductionSiteURL) && !r.urls && len(productionHosts(r.j.Production)) == 0:
			r.skipCheck(id, "no production hosts to look for; set production: in .roadie/config.yaml")
		case id == core.FindingURLProduction && !r.urls:
			r.skipCheck(id, "production: lists only globs, which the URL matcher can't search for")
		default:
			r.count(id, rows)
		}
	}
	listed[core.FindingUncoveredColumn] = true
	for _, id := range siteFileChecks {
		listed[id] = true
		switch {
		case r.skipReason(id) != "":
			r.skipCheck(id, r.skipReason(id))
		case r.site == nil:
			r.skipCheck(id, "a database or dump target has no files to read")
		case id == core.FindingConfigSalts && len(r.j.ProductionPairs) == 0:
			r.skipCheck(id, "production's wp-config.php or .env wasn't read in this run")
		}
	}
	for _, id := range httpChecks {
		listed[id] = true
		if r.j.Opts.SiteURL == "" {
			r.skipCheck(id, "--site not given")
		}
	}
	for _, st := range r.j.Steps {
		listed[st.ID()] = true
	}
	if r.j.Opts.Echo {
		listed[core.FindingSideEffectMail] = true
	}
	ids := make([]string, 0, len(listed))
	for id := range listed {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]core.Check, 0, len(ids))
	for _, id := range ids {
		n, ran := r.checked[id]
		if !ran {
			why := cmp.Or(r.why[id], r.skipReason(id))
			if why == "" {
				why = "nothing to check on this target"
			}
			out = append(out, core.Check{ID: id, Status: "skip", Detail: why})
			continue
		}
		c := core.Check{ID: id, Status: "pass", Checked: n, Detail: r.note[id]}
		if hits, all := r.hits(id); hits > 0 {
			c.Status = "fail"
			c.Detail = fmt.Sprintf("%d %s (%s)", hits, plural(hits, "finding", "findings"), id)
		} else if all > 0 {
			c.Status = "warn"
			c.Detail = fmt.Sprintf("%d %s below --fail-on %s (%s)", all, plural(all, "finding", "findings"), r.j.Opts.FailOn, id)
		} else if r.unknown[id] {
			// It ran and couldn't tell the state: not a pass.
			c.Status = "skip"
			c.Detail = cmp.Or(c.Detail, "status unknown")
		}
		out = append(out, c)
	}
	return out
}

// hits counts the hits of check id's findings that fail it and all of
// its hits of low severity or above (an info finding is a note, which
// warns of nothing; it still fails at --fail-on info).
func (r *run) hits(id string) (failing, all int64) {
	for _, f := range r.res.Findings {
		if checkOf(f.ID) != id {
			continue
		}
		if fo := r.j.Opts.FailOn; fo.Fails(f.Severity) || fo == core.SeverityNone && f.Severity >= core.SeverityLow {
			failing += f.Count
		}
		if f.Severity >= core.SeverityLow {
			all += f.Count
		}
	}
	return failing, all
}

// checkOf returns the check a finding ID belongs to.
func checkOf(id string) string {
	if p, ok := strings.CutPrefix(id, "key."); ok && strings.HasSuffix(p, "-live") {
		return CheckKeyLive
	}
	return id
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
