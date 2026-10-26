// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package exposure

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// RobotsBackupSuffix is added to robots.txt for the copy Roadie keeps of
// the file it replaced.
const RobotsBackupSuffix = ".roadie-backup"

// fixNoindex is the command that puts the exposure guard back.
const fixNoindex = "rhythm-roadie prepare <staging> --only noindex"

// robotsPath is the physical robots.txt of a stack's web root, "" for a
// stack without one.
func robotsPath(stack core.Stack) string {
	switch stack {
	case core.StackWordPress:
		return "robots.txt"
	case core.StackLaravel:
		return "public/robots.txt"
	}
	return ""
}

// robots is the search.indexable step.
type robots struct {
	o killswitch.Options
}

func robotsStep(o killswitch.Options) core.Step { return &robots{o: o} }

func (st *robots) ID() string    { return core.FindingSearchIndexable }
func (st *robots) Phase() string { return "files" }

// Plan says what Apply will write.
func (st *robots) Plan(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
	id := st.ID()
	rel := robotsPath(s.Stack())
	if rel == "" {
		return nil, nil
	}
	if !st.o.Noindex {
		return []core.PlannedEdit{{Step: id, What: "left on (--noindex=off): no noindex, robots.txt or crawler block"}}, nil
	}
	var out []core.PlannedEdit
	if s.Stack() == core.StackWordPress {
		out = append(out, core.PlannedEdit{Step: id, File: killswitch.MuPluginPath,
			What: "noindex and nofollow (wp_robots and X-Robots-Tag), robots.txt from the robots_txt filter, sitemaps off; blog_public in the database left as it is"})
	}
	old, err := s.ReadFile(ctx, rel)
	switch {
	case err == nil && string(old) == killswitch.RobotsTxt():
		out = append(out, core.PlannedEdit{Step: id, File: rel, What: "already blocks every crawler"})
	case err == nil:
		what := "backed up to " + rel + RobotsBackupSuffix + " and replaced: everything disallowed for every agent and the named AI crawlers"
		if ok, _ := s.Exists(ctx, rel+RobotsBackupSuffix); ok {
			what = "replaced (the backup " + rel + RobotsBackupSuffix + " from an earlier run is kept): everything disallowed for every agent and the named AI crawlers"
		}
		out = append(out, core.PlannedEdit{Step: id, File: rel, What: what})
	case s.Stack() == core.StackLaravel:
		out = append(out, core.PlannedEdit{Step: id, File: rel, What: "written: everything disallowed for every agent and the named AI crawlers"})
	}
	return out, nil
}

// Apply backs up a physical robots.txt once and replaces it. A WordPress
// copy without one gets nothing: the mu-plugin's robots_txt filter serves
// the same text. Laravel has no such filter, so the file is written.
func (st *robots) Apply(ctx context.Context, s core.Site) error {
	rel := robotsPath(s.Stack())
	if rel == "" || !st.o.Noindex {
		return nil
	}
	want := []byte(killswitch.RobotsTxt())
	old, err := s.ReadFile(ctx, rel)
	switch {
	case err == nil && bytes.Equal(old, want):
		return nil
	case err == nil:
		// The first run's backup holds the site's own file; a later run
		// would only back up Roadie's text over it.
		if ok, _ := s.Exists(ctx, rel+RobotsBackupSuffix); !ok {
			if err := s.WriteFile(ctx, rel+RobotsBackupSuffix, old, 0o644); err != nil {
				return writeErr(rel+RobotsBackupSuffix, err, s)
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return writeErr(rel, err, s)
	case s.Stack() == core.StackWordPress:
		return nil
	}
	if err := s.WriteFile(ctx, rel, want, 0o644); err != nil {
		return writeErr(rel, err, s)
	}
	return nil
}

// writeErr is the PRD 17 kill-switch error for a robots.txt Roadie
// couldn't back up or write.
func writeErr(file string, err error, s core.Site) error {
	var ce *core.Error
	if errors.As(err, &ce) || errors.Is(err, core.ErrReadOnly) {
		return err
	}
	cause := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		cause = pe.Err.Error()
	}
	return core.Errorf(core.ErrKillSwitch, 1,
		"search.indexable: couldn't write %s (%s), so search engines may still crawl this copy. Fix it, then: rhythm-roadie prepare %s --only noindex.",
		file, cause, s.Endpoint().Display)
}

// Check reports search.indexable when nothing on the copy tells crawlers
// to stay out: no mu-plugin noindex (or blog_public 1) and no blocking
// physical robots.txt, or a physical robots.txt that lets them in.
func (st *robots) Check(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
	id := st.ID()
	se := core.SideEffect{ID: id, Status: "off"}
	rel := robotsPath(s.Stack())
	if rel == "" {
		se.Status, se.How = "not-present", "this target is a database only; Roadie can't serve noindex for it"
		return se, nil, nil
	}
	b, err := s.ReadFile(ctx, rel)
	physical := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		se.Status, se.How = "unknown", rel+" can't be read"
		return se, nil, nil
	}
	if physical && !Blocks(b) {
		se.Status, se.How = "on", rel+" lets crawlers in; a physical robots.txt wins over WordPress's own"
		if s.Stack() == core.StackLaravel {
			se.How = rel + " lets crawlers in"
		}
		return se, []core.Finding{indexable(se.How, core.Location{File: rel})}, nil
	}
	if s.Stack() == core.StackLaravel {
		if !physical {
			se.Status, se.How = "on", "no "+rel+" and nothing else tells crawlers to stay out"
			return se, []core.Finding{indexable(se.How, core.Location{File: rel})}, nil
		}
		se.How, se.Undo = rel+" disallows every crawler", restoreUndo(rel)
		return se, nil, nil
	}
	if noindexHook(ctx, s) {
		se.How, se.Undo = "roadie-staging.php sends noindex and serves a robots.txt that disallows every crawler", "delete "+killswitch.MuPluginPath
		if physical {
			se.How = "roadie-staging.php sends noindex and " + rel + " disallows every crawler"
			se.Undo += "; " + restoreUndo(rel)
		}
		return se, nil, nil
	}
	if physical {
		se.How, se.Undo = rel+" disallows every crawler", restoreUndo(rel)
		return se, nil, nil
	}
	opts, err := wpdb.Options(ctx, s, "blog_public")
	if err == nil && string(opts["blog_public"]) == "0" {
		se.How, se.Undo = "blog_public is 0, so WordPress asks search engines not to index", "Settings > Reading > Search engine visibility"
		return se, nil, nil
	}
	se.Status, se.How = "on", "no noindex from roadie-staging.php, blog_public is 1 and no robots.txt blocks crawlers"
	return se, []core.Finding{indexable(se.How, core.Location{File: killswitch.MuPluginPath})}, nil
}

func restoreUndo(rel string) string {
	return "restore " + rel + " from " + rel + RobotsBackupSuffix
}

func indexable(how string, at core.Location) core.Finding {
	return core.Finding{ID: core.FindingSearchIndexable, Severity: core.SeverityMedium, Title: "search engines may index this copy: " + how,
		Count: 1, Fix: fixNoindex, Locations: []core.LocationCount{{Location: at, Count: 1}}}
}

// noindexHook reports a Roadie mu-plugin on the site that adds noindex.
func noindexHook(ctx context.Context, s core.Site) bool {
	b, err := s.ReadFile(ctx, killswitch.MuPluginPath)
	return err == nil && bytes.Contains(b, []byte(killswitch.MuPluginMarker)) && bytes.Contains(b, []byte("'wp_robots'"))
}

// Blocks reports whether a robots.txt disallows the whole site for every
// agent: the group for "*" holds "Disallow: /" and no Allow rule that
// opens a path again. Crawlers read the first matching group, so a later
// "*" group doesn't count.
func Blocks(robots []byte) bool {
	var (
		inStar, rules, seen, disallowAll, allows bool
	)
	for _, line := range strings.Split(string(robots), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if rules {
				// A user-agent line after rules starts a new group.
				if inStar {
					return disallowAll && !allows
				}
				inStar, rules = false, false
			}
			if val == "*" && !seen {
				inStar, seen = true, true
			}
		case "disallow", "allow":
			rules = true
			if !inStar {
				continue
			}
			if key == "disallow" && val == "/" {
				disallowAll = true
			}
			if key == "allow" && val != "" {
				allows = true
			}
		}
	}
	return inStar && disallowAll && !allows
}
