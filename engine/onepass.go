// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// rereadable reports whether an input can be read again at no more cost
// than a second read: a dump file, a database or prepare's spool. A
// stream (stdin, an ssh export) under --one-pass is read once.
func rereadable(in input) bool {
	switch v := in.(type) {
	case *fileInput, *siteInput, *tablesInput, *selfInput:
		return true
	case *streamInput:
		return !v.onePass
	case interface{ readsAgain() bool }:
		return v.readsAgain()
	}
	return false
}

// streamURLs is the check of a --one-pass over a stream, which learns
// the source's own site URLs (siteurl, home, the blogs) only as it
// writes. With --url, a site production: didn't name kept its URLs, so
// the copy is taken back. Without --url nothing is rewritten in either
// mode: the hosts join the plan's production list, so verify looks for
// them. A two-pass run or a pre-read rewrites them. It returns the hosts
// that stayed; the caller takes the copy back and words the stop with
// streamURLError for the state the sink is left in.
func (p *pipeline) streamURLs(idx *indexer, plan urlPlan) (urlPlan, []string, error) {
	if p.parts.noURLs {
		return plan, nil, nil
	}
	src := idx.siteURLList()
	p.sourceURLs = slices.Clone(src)
	if p.samePlace {
		src = ownURLs(src, p.opts.URL, p.cfg.Production)
	}
	full, err := buildURLPlan(urlSources{SiteURLs: src, Blogs: idx.blogs, Production: p.cfg.Production}, p.opts.URL, p.cfg.URLMap)
	if err != nil {
		return plan, nil, err
	}
	var missed []string
	for _, h := range full.Production {
		if !slices.Contains(plan.Production, h) {
			missed = append(missed, h)
		}
	}
	if len(missed) == 0 {
		return plan, nil, nil
	}
	if p.opts.URL == "" {
		plan.Production = append(plan.Production, missed...)
		return plan, nil, nil
	}
	return plan, missed, nil
}

// streamURLError is the stop of a --one-pass stream whose own site
// production: didn't name. A dump file is removed, so its copy was taken
// back. A database or site target already holds the rows pass 2 wrote,
// production's site URL among them, so the message says the target is
// mixed and never claims it was taken back.
func streamURLError(missed []string, st core.TargetState) *core.Error {
	site := strings.Join(missed, ", ")
	if st == core.Untouched {
		return &core.Error{Code: core.ErrConfig, Exit: 2, State: st, Msg: fmt.Sprintf(
			"--one-pass read a stream whose own site is %s, which production: doesn't name, so its URLs would have stayed; the copy was taken back. Add it to production: in .roadie/config.yaml (or pass --production %s), or drop --one-pass.",
			site, missed[0])}
	}
	return &core.Error{Code: core.ErrConfig, Exit: 2, State: st, Msg: fmt.Sprintf(
		"--one-pass read a stream whose own site is %s, which production: doesn't name, so its URLs stayed in the rows already written. The target is in a %s state and holds production's site URL: run the same command again with --production %s, or without --one-pass.",
		site, st, missed[0])}
}

// onePassFinding is the coverage note of a --one-pass run: no name
// index was built, so a name the detectors don't see in free text stays.
func onePassFinding() core.Finding {
	return core.Finding{ID: core.FindingInfoOnePass, Severity: core.SeverityInfo,
		Title: "--one-pass skipped the name index: names in free text that no detector sees stay as they are",
		Fix:   "Copy again without --one-pass to look the source's names up in free text too."}
}
