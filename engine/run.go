// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	echowp "github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/site/killswitch"
	"github.com/RhythmShahriar/roadie/verify"
)

// NotCovered is the PRD 19.1 sentence every report carries.
const NotCovered = "Not covered by this report: files and uploads (PDF invoices, form uploads, images with location data), " +
	"other databases, object caches (Redis, Memcached), search indexes (Algolia, ElasticPress), logs on disk outside wp-content, " +
	"copies held by third parties (Mailchimp lists, Stripe customers), names and street addresses written in free text that " +
	"Roadie didn't see in a structured column, encrypted columns, tables you told Roadie to keep."

// notCoveredLaravel is PRD 13.2's line for a Laravel copy.
const notCoveredLaravel = "Add an X-Robots-Tag: noindex middleware; Roadie does not edit Laravel code."

// The parts of prepare --only (PRD 6.5).
const (
	partData        = "data"
	partKeys        = "keys"
	partSideEffects = "side-effects"
	partURLs        = "urls"
	partNoindex     = "noindex"
)

// onlyParts lists the parts in help order.
var onlyParts = []string{partData, partKeys, partSideEffects, partURLs, partNoindex}

// sideEffectTables are the tables the option-level kill switch touches;
// prepare --only side-effects exports only these (PRD 6.5).
var sideEffectTables = []string{"options", "wc_webhooks", "automatewoo_queue"}

// maintenanceFile is WordPress's maintenance flag: while it exists and is
// under ten minutes old, visitors get the maintenance page.
const maintenanceFile = ".maintenance"

// rawExporter is a site that can hand over its database as a dump
// stream, so a two-pass run spools it once (PRD 10.3) instead of
// exporting twice. A site without it is exported once per pass.
type rawExporter interface {
	Export(ctx context.Context) (io.ReadCloser, error)
}

// runner is one Run: the job, the effective settings and the sites it
// opened.
type runner struct {
	j    Job
	mode Mode
	cfg  *roadiecfg.Config // config with the flags over it
	opts Options
	now  core.Clock
	only map[string]bool // prepare --only; nil means every part
	from core.Site       // nil for a dump file or stdin
	to   core.Site       // nil for a dump file, stdout or no target
	res  *core.Result
	// opened are the sites to close and ask for their remote commands.
	opened []core.Site
	// samePlace is a plan or prepare of one staging: source and target
	// are the same site.
	samePlace bool
	// maintenance is set while .maintenance is on the target.
	maintenance bool
	// stepped is set once a kill-switch or exposure step changed the
	// target: a run that stops after it left the target mixed, not
	// untouched.
	stepped bool
}

// Run runs the job.
func Run(ctx context.Context, j Job) (*core.Result, error) {
	r := &runner{j: j, mode: j.Mode, now: j.Clock}
	if r.now == nil {
		r.now = time.Now
	}
	r.res = &core.Result{Kind: string(j.Mode), Started: r.now(), TargetState: core.Untouched, FailOn: j.Opts.FailOn}
	err := r.run(ctx)
	r.endMaintenance(ctx)
	r.res.RemoteCommands = r.remoteCommands()
	for _, s := range r.opened {
		_ = s.Close()
	}
	if err != nil && r.stepped && r.res.TargetState == core.Untouched {
		r.res.TargetState = core.Mixed
	}
	r.res.Target.State = r.res.TargetState
	r.res.Duration = r.now().Sub(r.res.Started)
	if err != nil {
		var ce *core.Error
		if errors.As(err, &ce) {
			cp := *ce
			if cp.State == "" || r.res.TargetState == core.Mixed {
				cp.State = r.res.TargetState
			}
			r.res.Err = &cp
		}
		return r.res, err
	}
	return r.res, nil
}

// run is Run's body: PRD 10.1 step by step.
func (r *runner) run(ctx context.Context) error {
	cfg, err := effective(r.j.Config, r.j.Opts)
	if err != nil {
		return err
	}
	r.cfg, r.opts = cfg, r.j.Opts
	if r.opts.URL == "" {
		r.opts.URL = cfg.URL
	}
	if err := r.endpoints(); err != nil {
		return err
	}
	if r.mode == Prepare {
		if r.only, err = parseOnly(r.opts.Only); err != nil {
			return err
		}
	}
	r.res.URL = r.opts.URL
	r.res.Source = core.EndpointInfo{Kind: r.j.From.Kind, Display: r.j.From.Display}
	r.res.Target = core.EndpointInfo{Kind: r.j.To.Kind, Display: r.j.To.Display}
	defs, pack, err := loadProfiles(ctx, r.opts.Offline)
	if err != nil {
		return err
	}
	r.res.ProfilePack = pack
	r.res.ConfigSHA256 = configHash(cfg)

	// 1. The lock: one run per target. plan writes nothing, so it
	// doesn't wait for one.
	if r.mode != Plan && r.j.To.Kind != "" && r.j.To.Kind != core.Stdio {
		unlock, err := lockTarget(r.j.To)
		if err != nil {
			return lockError(r.j.To, err)
		}
		defer unlock()
	}
	if err := r.open(ctx); err != nil {
		return err
	}
	// 2. The guards, read-only; nothing is written if one refuses.
	if err := r.guard(ctx); err != nil {
		return err
	}
	// 5. The confirmation, before anything is written.
	if r.mode != Plan {
		if err := r.confirm(ctx); err != nil {
			return err
		}
	}
	dir, err := workDir()
	if err != nil {
		return err
	}
	key, _, stable, err := runKey(dir, cfg, r.opts.KeepKey, lookupEnv)
	if err != nil {
		return err
	}
	r.res.Consistency = core.ConsistencyInfo{Mode: "per-run"}
	if stable {
		r.res.Consistency = core.ConsistencyInfo{Mode: "stable", KeyFingerprint: key.Fingerprint()}
	}

	ko := r.killOptions()
	var stack core.Stack
	var applied, checks []core.Step
	if r.to != nil {
		stack = r.to.Stack()
		kill, exp := killSteps(stack, ko), exposureSteps(ko)
		checks = append(append(checks, kill...), exp...)
		if !r.opts.NoKillSwitch {
			if r.part(partSideEffects) {
				applied = append(applied, kill...)
			}
			if r.part(partNoindex) {
				applied = append(applied, exp...)
			}
		}
	}
	if r.mode == Plan {
		r.planSteps(ctx, applied)
	}

	p := &pipeline{mode: r.mode, cfg: cfg, opts: r.opts, defs: defs, site: r.from, key: key,
		progress: r.j.Progress, result: r.res, indexOnly: r.mode == Plan && r.opts.SchemaOnly}
	if !r.opts.NoKillSwitch && r.part(partSideEffects) {
		p.dataRules = func(st core.Stack) []core.Rule { return killDataRules(st, ko) }
	}
	p.parts = parts{noData: !r.part(partData), noKeys: !r.part(partKeys), noURLs: !r.part(partURLs)}
	dataPass := !p.parts.noData || !p.parts.noKeys || !p.parts.noURLs || p.dataRules != nil
	if p.parts.noData {
		// No profile or columns: rule runs; only the kill switch's data
		// rules, the keys and the URLs do.
		p.defs, p.cfg = nil, stripData(cfg)
	}
	limited := p.parts.noData && p.parts.noKeys && p.parts.noURLs

	if r.mode != Plan && dataPass && r.mode == Prepare && !r.opts.NoBackup {
		path, restore, err := r.to.Backup(ctx)
		if err != nil {
			return fmt.Errorf("backup of %s: %w. Fix it, or pass --no-backup to go on without one", r.j.To.Display, err)
		}
		r.res.Backup, r.res.Restore = path, restore
	}
	// 6. The file-level kill switch, the maintenance page and the sink
	// come once pass 1 has passed its checks: a stop there (an uncovered
	// column, a url-map error) leaves the target as it was.
	writeStart := func(ctx context.Context) error {
		if err := r.apply(ctx, applied, "files"); err != nil {
			return err
		}
		if !dataPass {
			return nil
		}
		if err := r.startMaintenance(ctx, stack); err != nil {
			return err
		}
		s, err := r.sink(ctx)
		if err != nil {
			return err
		}
		p.sink, p.importing = s, r.to != nil
		return nil
	}

	var out *output
	if dataPass {
		if p.in, err = r.input(ctx, limited); err != nil {
			return err
		}
		if r.mode != Plan {
			p.beforeWrite = writeStart
		}
		// 4, 7 and 8: both passes and the commit.
		out, err = p.run(ctx)
		r.endMaintenance(ctx)
		if err != nil {
			return err
		}
		r.res.Source.Bytes = inputBytes(r.res.Input)
		if r.j.To.Kind == core.DumpFile && r.mode != Plan {
			r.res.Output = fileFingerprint(r.j.To.Path)
		}
	} else if r.mode != Plan {
		if err := writeStart(ctx); err != nil {
			return err
		}
	}
	r.fillEndpoints(ctx)
	if r.res.Stack.Name == "" {
		// No pass ran (prepare --only noindex): the site says its stack.
		r.res.Stack.Name = stack
	}
	r.res.NotCovered = notCoveredLines(r.j.To, r.to != nil, r.res.Stack.Name)
	if r.mode == Plan {
		return nil
	}
	if err := r.apply(ctx, applied, "after"); err != nil {
		return err
	}
	if r.to != nil && stack == core.StackWordPress && !r.opts.NoKillSwitch && r.part(partSideEffects) {
		sum := sha256.Sum256(echowp.Plugin())
		r.res.EchoConnectorSHA256 = hex.EncodeToString(sum[:])
	}
	if err := r.basicAuth(dir); err != nil {
		return err
	}
	// 9. Verify what was written, read back from the target.
	if out != nil {
		return r.verify(ctx, out, checks)
	}
	return nil
}

// part reports whether prepare --only leaves a part on.
func (r *runner) part(name string) bool { return r.only == nil || r.only[name] }

// endpoints checks that the mode has the endpoints it needs.
func (r *runner) endpoints() error {
	from, to := r.j.From, r.j.To
	switch r.mode {
	case Plan:
		if from.Kind == "" {
			return core.Errorf(core.ErrConfig, 2, "plan needs --from (or source: in .roadie/config.yaml)")
		}
	case Copy:
		if from.Kind == "" {
			return core.Errorf(core.ErrConfig, 2, "copy needs --from (or source: in .roadie/config.yaml)")
		}
		if to.Kind == "" {
			return core.Errorf(core.ErrConfig, 2, "copy needs --to (or target: in .roadie/config.yaml)")
		}
	case Prepare:
		if !isSite(to.Kind) {
			return core.Errorf(core.ErrConfig, 2,
				"prepare needs a staging site or database; %s is a %s. Use rhythm-roadie copy for dump files.", to.Display, kindName(to.Kind))
		}
		// prepare reads and writes one staging: a source: in the config
		// (production) is never read, so prepare can't become a copy
		// from production.
		r.j.From = to
	default:
		return core.Errorf(core.ErrConfig, 2, "engine: unknown mode %q", r.mode)
	}
	// Only prepare and plan may read and write one site: a copy whose
	// source is its target must reach the target-is-source guard.
	r.samePlace = (r.mode == Prepare || r.mode == Plan) &&
		r.j.From.Kind == r.j.To.Kind && r.j.From.Raw == r.j.To.Raw && isSite(r.j.To.Kind)
	return nil
}

// isSite reports whether an endpoint kind is a site or a database.
func isSite(k core.EndpointKind) bool {
	switch k {
	case core.LocalSite, core.SSHSite, core.WPAlias, core.DSN:
		return true
	}
	return false
}

// kindName is how a message names an endpoint kind.
func kindName(k core.EndpointKind) string {
	switch k {
	case core.DumpFile:
		return "dump file"
	case core.Stdio:
		return "stream (-)"
	case "":
		return "missing target"
	}
	return string(k)
}

// parseOnly reads prepare --only: comma-separated parts, repeatable.
func parseOnly(list []string) (map[string]bool, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, item := range list {
		for _, p := range strings.Split(item, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if !slices.Contains(onlyParts, p) {
				return nil, core.Errorf(core.ErrConfig, 2, "--only %s isn't a part; use data, keys, side-effects, urls or noindex", p)
			}
			out[p] = true
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// effective is the config with the flags over it. The base config is
// never changed.
func effective(base *roadiecfg.Config, o Options) (*roadiecfg.Config, error) {
	if base == nil {
		base = roadiecfg.Default()
	}
	c := *base
	if o.URL != "" {
		c.URL = o.URL
	}
	if len(o.URLMap) > 0 {
		m := make(map[string]string, len(base.URLMap)+len(o.URLMap))
		for k, v := range base.URLMap {
			m[k] = v
		}
		for k, v := range o.URLMap {
			m[k] = v
		}
		c.URLMap = m
	}
	if o.Stack != "" {
		c.Stack = o.Stack
	}
	if len(o.KeepUsers) > 0 {
		c.Pseudonymise.KeepUsers = slices.Clone(o.KeepUsers)
	}
	if o.EmailDomain != "" {
		c.Pseudonymise.EmailDomain = o.EmailDomain
	}
	c.Drop = append(slices.Clone(base.Drop), o.Drop...)
	c.Truncate = append(slices.Clone(base.Truncate), o.Truncate...)
	for _, g := range append(slices.Clone(o.Drop), o.Truncate...) {
		if err := roadiecfg.ValidGlob(g); err != nil {
			return nil, core.Errorf(core.ErrConfig, 2, "--drop or --truncate %q isn't a valid glob: %v", g, err)
		}
	}
	if o.Keys != "" {
		c.Keys.Mode = o.Keys
	}
	c.Production = slices.Clone(base.Production)
	for _, p := range append(slices.Clone(o.Production), o.ProductionURL) {
		if p != "" && !slices.Contains(c.Production, p) {
			c.Production = append(c.Production, p)
		}
	}
	if o.NoIndex {
		c.Noindex = false
	}
	if o.EchoURL != "" {
		c.SideEffects.Echo.URL = o.EchoURL
	}
	if o.EchoProxy != "" {
		c.SideEffects.Echo.Proxy = o.EchoProxy
	}
	return &c, nil
}

// stripData is the config of a prepare that leaves the data alone: no
// columns:, drop, truncate or kept users, every profile off.
func stripData(c *roadiecfg.Config) *roadiecfg.Config {
	s := *c
	s.Columns, s.Drop, s.Truncate = nil, nil, nil
	s.Pseudonymise.KeepUsers = nil
	s.Profiles = []string{"auto"}
	return &s
}

// configHash is the SHA-256 of the effective config as config show
// --json prints it: secrets are masked there, so the hash carries none.
func configHash(c *roadiecfg.Config) string {
	b, err := c.Show(true)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// lockError gives a lock failure the lock code when the site lane didn't.
func lockError(e core.Endpoint, err error) error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return err
	}
	return core.Errorf(core.ErrLock, 3, "lock: couldn't take the run lock on %s (%v).", e.Display, err)
}

// siteOptions are the site.Options of an endpoint the run opens.
func (r *runner) siteOptions(readOnly bool) site.Options {
	return site.Options{ReadOnly: readOnly, WP: r.cfg.Remote.WP, BatchMode: r.j.Confirm == nil}
}

// open opens the source read-only and the target: read-only for plan,
// writable otherwise. A prepare (or a plan of one staging) opens one
// site and reads it through its read-only export.
func (r *runner) open(ctx context.Context) error {
	if isSite(r.j.To.Kind) {
		s, err := openSite(ctx, r.j.To, r.siteOptions(r.mode == Plan))
		if err != nil {
			return err
		}
		r.opened = append(r.opened, s)
		r.to = s
	}
	if r.samePlace {
		r.from = r.to
		return nil
	}
	if isSite(r.j.From.Kind) {
		s, err := openSite(ctx, r.j.From, r.siteOptions(true))
		if err != nil {
			return err
		}
		r.opened = append(r.opened, s)
		r.from = s
	}
	return nil
}

// heldURL matches the URL in a target-holds-production message.
var heldURL = regexp.MustCompile(`still calls itself (\S+?)\.(?:\s|$)`)

// guard runs the target guards. When the staging still holds a URL that
// may be production's, a terminal is asked once whether it is the live
// site; yes takes it as --production-url and the guards run again.
func (r *runner) guard(ctx context.Context) error {
	if r.j.To.Kind == "" {
		return nil
	}
	mode := string(r.mode)
	if r.samePlace {
		mode = string(Prepare)
	}
	asked := false
	for {
		err := guardTarget(ctx, site.GuardInput{From: r.j.From, To: r.j.To, FromSite: r.from, ToSite: r.to,
			Production: r.cfg.Production, ProductionURL: r.opts.ProductionURL,
			TargetIsStaging: r.opts.TargetIsStaging, Mode: mode})
		if err == nil {
			return nil
		}
		var ce *core.Error
		if asked || r.j.Confirm == nil || !errors.As(err, &ce) || ce.Code != core.ErrTargetHoldsProduction {
			return err
		}
		u := r.heldURL(ctx, ce.Msg)
		if u == "" {
			return err
		}
		q := fmt.Sprintf("%s still calls itself %s. Is %s the live site? Type yes to go on:", r.j.To.Display, u, u)
		ok, cerr := r.j.Confirm(q, "yes")
		if cerr != nil {
			return cerr
		}
		if !ok {
			return err
		}
		asked = true
		r.opts.ProductionURL = u
		if !slices.Contains(r.cfg.Production, u) {
			r.cfg.Production = append(r.cfg.Production, u)
		}
	}
}

// heldURL reads the URL a staging still calls itself: from the guard's
// message, else from WP-CLI.
func (r *runner) heldURL(ctx context.Context, msg string) string {
	if m := heldURL.FindStringSubmatch(msg); m != nil {
		return printable(m[1])
	}
	if r.to == nil {
		return ""
	}
	b, err := r.to.WP(ctx, "option", "get", "siteurl", "--skip-plugins", "--skip-themes")
	if err != nil {
		return ""
	}
	return printable(strings.TrimSpace(string(b)))
}

// printable drops control and format characters (escape sequences,
// bidi overrides) and anything after the first space from a URL read
// from the target's own data, so a crafted siteurl can't drive the
// terminal when the question shows it. It is cut to 512 bytes.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s, _, _ = strings.Cut(strings.TrimSpace(s), " ")
	for len(s) > 512 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}

// confirmNames are the names the target may be confirmed by, the one to
// show first: a database's name, else the site's host (from --url), its
// ssh host or its alias.
func (r *runner) confirmNames(ctx context.Context) (names []string, what string) {
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, s) }) {
			names = append(names, s)
		}
	}
	to := r.j.To
	if to.Kind == core.DSN {
		what = "the database name"
		if id, err := r.to.DBIdentity(ctx); err == nil {
			add(id.Name)
		}
	} else {
		what = "the site's host"
		if u, err := url.Parse(r.opts.URL); err == nil {
			add(u.Hostname())
		}
		add(to.Host)
		add(strings.TrimPrefix(to.Alias, "@"))
	}
	if len(names) == 0 {
		add(to.Display)
	}
	return names, what
}

// confirm asks before a site or database target is changed (PRD 6.4,
// 6.5, 16.1). Without a terminal --confirm-target must name the target;
// --yes skips only copy's replace question.
func (r *runner) confirm(ctx context.Context) error {
	if r.to == nil {
		return nil
	}
	names, what := r.confirmNames(ctx)
	host := names[0]
	ct := strings.TrimSpace(r.opts.ConfirmTarget)
	if ct != "" && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, ct) }) {
		return core.Errorf(core.ErrPrepareConfirm, 2,
			"prepare-confirm: --confirm-target %s doesn't name this target, which is %s. Pass --confirm-target %s (%s) to go on.", ct, host, host, what)
	}
	if r.j.Confirm == nil {
		if ct == "" {
			return core.Errorf(core.ErrPrepareConfirm, 2,
				"prepare-confirm: there's no terminal to confirm on. Pass --confirm-target %s (%s) to go on.", host, what)
		}
		return nil
	}
	if r.mode == Prepare {
		q := fmt.Sprintf("prepare rewrites this site's database while it runs. Visitors see a maintenance page for about a minute. Type the site's host to go on (%s):", host)
		ok, err := r.j.Confirm(q, host)
		if err != nil {
			return err
		}
		if !ok {
			return core.Errorf(core.ErrPrepareConfirm, 2, "prepare-confirm: the answer wasn't %s, so nothing was changed.", host)
		}
		return nil
	}
	if r.opts.Yes {
		return nil
	}
	ok, err := r.j.Confirm(fmt.Sprintf("Replace the database of %s? [y/N]", r.j.To.Display), "")
	if err != nil {
		return err
	}
	if !ok {
		return core.Errorf(core.ErrPrepareConfirm, 2, "prepare-confirm: %s wasn't replaced; nothing was changed.", r.j.To.Display)
	}
	return nil
}

// killOptions are the kill switch's settings for this run.
func (r *runner) killOptions() killswitch.Options {
	return killswitch.Options{EchoURL: r.cfg.SideEffects.Echo.URL, EchoProxy: r.cfg.SideEffects.Echo.Proxy,
		URL: r.opts.URL, Production: r.cfg.Production, KeepAppKey: r.opts.KeepAppKey, Noindex: r.cfg.Noindex, Cfg: r.cfg}
}

// planSteps asks each step what it would change, for plan.
func (r *runner) planSteps(ctx context.Context, steps []core.Step) {
	for _, st := range steps {
		edits, err := st.Plan(ctx, r.to)
		if err != nil {
			r.res.PlannedEdits = append(r.res.PlannedEdits, core.PlannedEdit{Step: st.ID(),
				What: fmt.Sprintf("manual: couldn't plan this step (%v)", err)})
			continue
		}
		r.res.PlannedEdits = append(r.res.PlannedEdits, edits...)
	}
}

// apply applies the steps of one phase. A step that fails stops the run
// with kill-switch (exit 1): the copy must not run with that side effect
// on.
func (r *runner) apply(ctx context.Context, steps []core.Step, phase string) error {
	for _, st := range steps {
		if st.Phase() != phase {
			continue
		}
		err := st.Apply(ctx, r.to)
		if err == nil {
			r.stepped = true
			continue
		}
		var ce *core.Error
		if errors.As(err, &ce) {
			return err
		}
		return core.Errorf(core.ErrKillSwitch, 1,
			"%s: couldn't turn it off on %s (%v). Fix that, then: rhythm-roadie prepare %s --only side-effects.",
			st.ID(), r.j.To.Display, err, r.j.To.Display)
	}
	return nil
}

// startMaintenance writes WordPress's .maintenance before a prepare
// imports, so visitors see the maintenance page instead of a half
// imported shop.
func (r *runner) startMaintenance(ctx context.Context, stack core.Stack) error {
	if r.mode != Prepare || stack != core.StackWordPress || r.j.To.Kind == core.DSN {
		return nil
	}
	b := fmt.Appendf(nil, "<?php $upgrading = %d; ?>\n", r.now().Unix())
	if err := r.to.WriteFile(ctx, maintenanceFile, b, 0o644); err != nil {
		return fmt.Errorf("couldn't write %s on %s: %w", maintenanceFile, r.j.To.Display, err)
	}
	r.maintenance = true
	return nil
}

// endMaintenance removes .maintenance again, also after an error.
func (r *runner) endMaintenance(ctx context.Context) {
	if !r.maintenance {
		return
	}
	r.maintenance = false
	_ = r.to.Remove(context.WithoutCancel(ctx), maintenanceFile)
}

// dumpOptions are the dump reader's options for a file or stream.
func (r *runner) dumpOptions(ctx context.Context, name string) (dump.Options, error) {
	o := dump.Options{Name: name, MaxValue: r.opts.MaxValue, MaxRatio: r.opts.MaxOutputRatio, KeepRoutines: r.opts.KeepRoutines}
	if r.opts.SchemaFile != "" {
		s, err := readSchemaFile(ctx, r.opts.SchemaFile)
		if err != nil {
			return o, err
		}
		o.Schema = s
	}
	return o, nil
}

// input is the source both passes read. limited exports only the kill
// switch's tables (prepare --only side-effects).
func (r *runner) input(ctx context.Context, limited bool) (input, error) {
	return r.inputFor(ctx, r.j.From, r.from, limited, r.opts.OnePass)
}

// inputFor opens an endpoint as a pass input.
func (r *runner) inputFor(ctx context.Context, e core.Endpoint, s core.Site, limited, onePass bool) (input, error) {
	switch e.Kind {
	case core.DumpFile:
		o, err := r.dumpOptions(ctx, filepath.Base(e.Path))
		if err != nil {
			return nil, err
		}
		return &fileInput{path: e.Path, o: o}, nil
	case core.Stdio:
		o, err := r.dumpOptions(ctx, "stdin")
		if err != nil {
			return nil, err
		}
		in := &streamInput{r: stdin, o: o, onePass: onePass}
		if !onePass {
			if in.dir, err = spoolDir(); err != nil {
				return nil, err
			}
		}
		return in, nil
	}
	if s == nil {
		return nil, core.Errorf(core.ErrConfig, 2, "engine: %s isn't open", e.Display)
	}
	if limited {
		id, err := s.DBIdentity(ctx)
		if err == nil && s.Stack() == core.StackWordPress {
			blogs, err := blogPrefixes(ctx, s, id.Prefix)
			if err != nil {
				return nil, err
			}
			tables := make([]string, 0, len(sideEffectTables)*len(blogs))
			for _, b := range blogs {
				for _, t := range sideEffectTables {
					tables = append(tables, b+t)
				}
			}
			return &tablesInput{s: s, tables: tables}, nil
		}
	}
	if ex, ok := s.(rawExporter); ok {
		rc, err := ex.Export(ctx)
		if err != nil {
			return nil, err
		}
		o, err := r.dumpOptions(ctx, e.Display)
		if err != nil {
			_ = rc.Close()
			return nil, err
		}
		in := &streamInput{r: rc, rc: rc, o: o, onePass: onePass}
		if !onePass {
			if in.dir, err = spoolDir(); err != nil {
				_ = rc.Close()
				return nil, err
			}
		}
		return in, nil
	}
	return &siteInput{s: s}, nil
}

// blogPrefixes returns the table prefix of every blog of a WordPress
// site: "wp_" first, then "wp_2_" and so on for each other blog of a
// multisite's blogs table. A single site has no blogs table, so the
// list is the main prefix alone. Each blog keeps its own options,
// webhooks and AutomateWoo queue, so the kill switch has to reach them
// all. The site skips a listed table it doesn't have.
func blogPrefixes(ctx context.Context, s core.Site, prefix string) ([]string, error) {
	out := []string{prefix}
	src, err := s.Source(ctx, []string{prefix + "blogs"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	seen := map[uint64]bool{}
	var ids []uint64
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if b == nil || b.Table == nil || b.Table.Name != prefix+"blogs" {
			continue
		}
		pos := positions(b)
		for ri := range b.Rows {
			v, ok := rowGet(b, pos, &b.Rows[ri])("blog_id")
			if !ok {
				continue
			}
			// Blog 1 is the main site, which uses the bare prefix.
			n, err := strconv.ParseUint(strings.Trim(string(v), "'\""), 10, 64)
			if err != nil || n <= 1 || seen[n] {
				continue
			}
			seen[n] = true
			ids = append(ids, n)
		}
	}
	slices.Sort(ids)
	for _, n := range ids {
		out = append(out, prefix+strconv.FormatUint(n, 10)+"_")
	}
	return out, nil
}

// tablesInput exports only some tables of a site, on every pass.
type tablesInput struct {
	s      core.Site
	tables []string
}

func (t *tablesInput) open(ctx context.Context, _ int) (core.Source, error) {
	return t.s.Source(ctx, t.tables)
}
func (t *tablesInput) close() error { return nil }

// sink is where pass 2 writes: the dump file, stdout or the target's
// database.
func (r *runner) sink(ctx context.Context) (core.Sink, error) {
	wo := dump.WriterOptions{Compression: r.j.To.Compression, FixCollation: r.opts.FixCollation, KeepRoutines: r.opts.KeepRoutines}
	switch r.j.To.Kind {
	case core.DumpFile:
		return newFileSink(r.j.To.Path, wo)
	case core.Stdio:
		s := newStreamSink(stdout, wo)
		if s == nil {
			return nil, core.ErrNotBuilt
		}
		return s, nil
	}
	return r.to.Sink(ctx)
}

// inputBytes is the input's size, 0 when unknown.
func inputBytes(fp *core.Fingerprint) int64 {
	if fp == nil {
		return 0
	}
	return fp.Bytes
}

// fileFingerprint is the SHA-256 of a written dump file; nil when it
// can't be read.
func fileFingerprint(path string) *core.Fingerprint {
	f, err := os.Open(path) // #nosec G304 -- the dump this run wrote
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil
	}
	return &core.Fingerprint{Kind: "sha256-file", Value: hex.EncodeToString(h.Sum(nil)), Bytes: n}
}

// fillEndpoints adds each site's engine and server to the result.
func (r *runner) fillEndpoints(ctx context.Context) {
	if r.from != nil {
		if id, err := r.from.DBIdentity(ctx); err == nil {
			r.res.Source.Engine, r.res.Source.Server = string(id.Engine), id.Server
		}
	}
	if r.to != nil {
		if id, err := r.to.DBIdentity(ctx); err == nil {
			r.res.Target.Engine, r.res.Target.Server = string(id.Engine), id.Server
		}
	}
}

// notCoveredLines is the result's Not covered section: PRD 19.1's
// sentence, the Tuner hint for a site with files (PRD 21) and the
// Laravel noindex hint (PRD 13.2).
func notCoveredLines(to core.Endpoint, siteTarget bool, stack core.Stack) []string {
	out := []string{NotCovered}
	if siteTarget && to.Kind != core.DSN {
		dir := to.Path
		if dir == "" {
			dir = "<staging dir>"
		}
		out = append(out, fmt.Sprintf("Run rhythm tuner scan %s for file-level leaks.", dir))
	}
	if stack == core.StackLaravel {
		out = append(out, notCoveredLaravel)
	}
	return out
}

// basicAuth writes the --basic-auth snippets under .roadie/basic-auth/
// (folder 0700, files 0600) and keeps the password for the screen.
func (r *runner) basicAuth(dir string) error {
	user := r.opts.BasicAuth
	if user == "" && r.cfg.BasicAuth {
		user = "staging"
	}
	if user == "" || !r.part(partNoindex) {
		return nil
	}
	files, pw, err := basicAuthFiles(user)
	if err != nil {
		return err
	}
	folder := filepath.Join(dir, roadiecfg.Dir, "basic-auth")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(folder, 0o700); err != nil { // #nosec G302 -- a folder needs its x bit; 0700 keeps it to the owner
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(folder, filepath.Base(n)), files[n], 0o600); err != nil {
			return err
		}
	}
	r.res.BasicAuthPassword = pw
	return nil
}

// productionPairs reads production's wp-config.php or .env, for verify's
// config.salts and config.points-at-production in exact mode.
func productionPairs(ctx context.Context, s core.Site, cfg *roadiecfg.Config) []core.Pair {
	if s == nil {
		return nil
	}
	var out []core.Pair
	if b, err := s.ReadFile(ctx, "wp-config.php"); err == nil {
		if ps, err := wpConfigPairs(b); err == nil {
			out = append(out, ps...)
		}
	}
	env := cfg.Laravel.EnvFile
	if env == "" {
		env = ".env"
	}
	if b, err := s.ReadFile(ctx, env); err == nil {
		if ps, err := dotenvPairs(b); err == nil {
			out = append(out, ps...)
		}
	}
	return out
}

// production is what verify treats as production: the settings and the
// URLs pass 1 found.
func production(cfg *roadiecfg.Config, found []string) []string {
	out := slices.Clone(cfg.Production)
	for _, p := range found {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// verifyOptions are verify's settings from the config and the flags.
func (r *runner) verifyOptions() verify.Options {
	return verify.Options{FailOn: r.opts.FailOn, Skip: slices.Clone(r.cfg.Verify.Skip),
		Allow:   detect.Allow{Emails: slices.Clone(r.cfg.Verify.AllowEmails), Phones: slices.Clone(r.cfg.Verify.AllowPhones)},
		SiteURL: r.opts.URL, EchoURL: r.cfg.SideEffects.Echo.URL, Jobs: r.opts.Jobs, Clock: r.j.Clock}
}

// verify scans what was written in exact mode, read back from the target
// (PRD 10.1 step 9), never from the engine's counts.
func (r *runner) verify(ctx context.Context, out *output, checks []core.Step) error {
	if r.opts.NoVerify {
		return nil
	}
	if r.j.To.Kind == core.Stdio {
		// A stream can't be read back; the result says verify didn't run.
		return nil
	}
	vj := verify.Job{Target: r.j.To, Known: out.Known, Production: production(r.cfg, r.res.Production),
		Rules: compiledRules(out.Compiled), Steps: checks, Opts: r.verifyOptions(), Progress: r.j.Progress}
	if r.mode == Copy {
		vj.ProductionPairs = productionPairs(ctx, r.from, r.cfg)
	}
	if isSite(r.j.To.Kind) {
		s, err := openSite(ctx, r.j.To, r.siteOptions(true))
		if err != nil {
			return err
		}
		r.opened = append(r.opened, s)
		vj.Site = s
	}
	if r.j.Progress != nil {
		r.j.Progress.Phase("verify", 0)
	}
	v, err := runVerify(ctx, vj)
	if err != nil {
		return err
	}
	r.res.Verify = v
	if v != nil && r.opts.Soundcheck && r.opts.URL != "" {
		v.Soundcheck = runSoundcheck(ctx, r.opts.URL, r.cfg.SideEffects.Echo.URL)
	}
	return nil
}

// compiledRules is every rule of a set, nil for none.
func compiledRules(c *profile.Compiled) []core.Rule {
	if c == nil {
		return nil
	}
	return c.All()
}

// remoteCommands lists every remote command the run's sites ran, once
// each, in order.
func (r *runner) remoteCommands() []string {
	var out []string
	for i, s := range r.opened {
		if slices.Contains(r.opened[:i], s) {
			continue
		}
		for _, c := range s.RemoteCommands() {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}
