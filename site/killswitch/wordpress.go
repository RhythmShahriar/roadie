// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/connectors/wordpress"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// ManualPrefix starts the What of a PlannedEdit Roadie can't make itself
// and prints for the user to do by hand, such as a php.ini line.
const ManualPrefix = "manual: "

// EchoPluginPath is where the Echo connector goes, relative to the site root.
const EchoPluginPath = "wp-content/" + wordpress.PluginPath

// echoMarker is in every connector file Echo writes.
const echoMarker = "rhythm-echo-connector"

// SendmailLine is the php.ini line that sends PHP's mail() through Echo.
const SendmailLine = `sendmail_path = "/usr/local/bin/rhythm-echo sendmail -t -i"`

// Salts are the eight wp-config.php secrets regenerated on every copy.
var Salts = []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"}

// step is one kill-switch step built from functions.
type step struct {
	id    string
	plan  func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error)
	apply func(ctx context.Context, s core.Site) error
	check func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error)
	// missing tells, from Options.Source, that the source doesn't hold
	// the side effect, with the read-back's words (see Absent); nil when
	// the step can't tell.
	missing func(ctx context.Context, s core.Site) (string, bool)
}

// keyFileStep is a step that changes the live keys of a config file and
// keeps what it did for core.KeyStep.
type keyFileStep struct {
	step
	applied []core.KeyHit
}

// AppliedKeys lists the live keys the last Apply found, each with what
// it did: swapped, blanked or manual.
func (st *keyFileStep) AppliedKeys() []core.KeyHit { return st.applied }

func (st *step) ID() string    { return st.id }
func (st *step) Phase() string { return "files" }

func (st *step) Plan(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
	return st.plan(ctx, s)
}

func (st *step) Apply(ctx context.Context, s core.Site) error {
	if st.apply == nil {
		return nil
	}
	return st.apply(ctx, s)
}

func (st *step) Check(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
	return st.check(ctx, s)
}

// killErr is the PRD 17 kill-switch error for a file Roadie couldn't
// write: what it means and how to finish by hand.
func killErr(id, file string, err error, means string, s core.Site) error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return err
	}
	if notBuilt(err) {
		return err
	}
	cause := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		cause = pe.Err.Error()
	}
	return core.Errorf(core.ErrKillSwitch, 1,
		"%s: couldn't write %s (%s), so %s. Fix it, then: rhythm-roadie prepare %s --only side-effects.",
		id, file, cause, means, s.Endpoint().Display)
}

// finding builds one finding at a file or an options row. Its title
// starts with a capital, as every other finding's does, unless it starts
// with a name such as wc_subscriptions_siteurl.
func finding(id string, sev core.Severity, title, fix string, at core.Location) core.Finding {
	return core.Finding{ID: id, Severity: sev, Title: capital(title), Count: 1, Fix: fix, Locations: []core.LocationCount{{Location: at, Count: 1}}}
}

// capital upper-cases the first letter of a sentence whose first word
// is a plain lower-case word; a name (a_b, a.b, wp-config.php) is kept.
func capital(s string) string {
	word, _, _ := strings.Cut(s, " ")
	if word == "" || word[0] < 'a' || word[0] > 'z' || strings.ContainsAny(word, "_.-/0123456789") {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// prepareFix is the fix of a finding a kill-switch step's check makes:
// prepare --only side-effects on the target, named as the run names it.
func prepareFix(s core.Site) string {
	where := "<staging>"
	if s != nil {
		if d := s.Endpoint().Display; d != "" {
			where = d
		}
	}
	return "rhythm-roadie prepare " + where + " --only side-effects"
}

func fileAt(line int) core.Location { return core.Location{File: "wp-config.php", Line: line} }

func optionAt(ctx context.Context, s core.Site, name, leaf string) core.Location {
	return core.Location{Table: wpdb.Prefix(ctx, s) + "options", Column: "option_value", Row: core.MaskRow("options", map[string]string{"option_name": name}), Path: leaf}
}

// configEdit is a step that changes wp-config.php defines.
func configEdit(id string, plan func(f *wpconfig.File) []string, edit func(f *wpconfig.File) error, means string) (func(context.Context, core.Site) ([]core.PlannedEdit, error), func(context.Context, core.Site) error) {
	p := func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
		f, err := readConfig(ctx, s)
		if err != nil {
			return []core.PlannedEdit{{Step: id, File: "wp-config.php", What: "can't read wp-config.php: " + err.Error()}}, nil
		}
		var out []core.PlannedEdit
		for _, w := range plan(f) {
			out = append(out, core.PlannedEdit{Step: id, File: "wp-config.php", What: w})
		}
		return out, nil
	}
	a := func(ctx context.Context, s core.Site) error {
		b, err := s.ReadFile(ctx, "wp-config.php")
		if err != nil {
			return killErr(id, "wp-config.php", err, means, s)
		}
		f, err := wpconfig.Parse(b)
		if err != nil {
			return killErr(id, "wp-config.php", err, means, s)
		}
		if err := edit(f); err != nil {
			return killErr(id, "wp-config.php", err, means, s)
		}
		if out := f.Bytes(); !bytes.Equal(out, b) {
			if err := s.WriteFile(ctx, "wp-config.php", out, 0o640); err != nil {
				return killErr(id, "wp-config.php", err, means, s)
			}
		}
		return nil
	}
	return p, a
}

// kept is the plan of a step the config leaves on.
func keptPlan(id, key string) func(context.Context, core.Site) ([]core.PlannedEdit, error) {
	return func(context.Context, core.Site) ([]core.PlannedEdit, error) {
		return []core.PlannedEdit{{Step: id, What: "left on (side_effects." + key + ": keep)"}}, nil
	}
}

// literal reads a define PHP keeps, with its line, when it is a literal
// before wp-settings.php.
func literal(f *wpconfig.File, name string) (wpconfig.Define, bool) {
	d, ok := f.Lookup(name)
	return d, ok && d.Literal && d.BeforeSettings
}

func wordpressSteps(o Options) []core.Step {
	steps := []core.Step{
		environmentStep(),
		cronStep(o),
		subscriptionsStep(o),
		keyStep(o),
		saltsStep(o),
		mailStep(o),
		schedulerStep(o),
		jetpackStep(o),
		webhooksStep(o),
		gatewaysStep(o),
		automatewooStep(o),
	}
	return steps
}

// environmentStep sets WP_ENVIRONMENT_TYPE to staging (PRD 13.1).
func environmentStep() core.Step {
	id := core.FindingConfigEnvironment
	plan, apply := configEdit(id,
		func(f *wpconfig.File) []string {
			if d, ok := literal(f, "WP_ENVIRONMENT_TYPE"); ok && d.Value == "staging" {
				return []string{"WP_ENVIRONMENT_TYPE is already 'staging'"}
			}
			return []string{"define( 'WP_ENVIRONMENT_TYPE', 'staging' );"}
		},
		func(f *wpconfig.File) error { return f.Set("WP_ENVIRONMENT_TYPE", "staging") },
		"WordPress may still call this copy production")
	return &step{id: id, plan: plan, apply: apply, check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "WP_ENVIRONMENT_TYPE is 'staging' in wp-config.php", Undo: "set WP_ENVIRONMENT_TYPE in wp-config.php"}
		f, err := readConfig(ctx, s)
		if err != nil {
			se.Status, se.How = "unknown", "wp-config.php can't be read"
			return se, nil, nil
		}
		d, ok := f.Lookup("WP_ENVIRONMENT_TYPE")
		switch {
		case ok && d.Literal && d.BeforeSettings && slices.Contains([]string{"staging", "local", "development"}, d.Value):
			se.How = "WP_ENVIRONMENT_TYPE is '" + d.Value + "' in wp-config.php"
			return se, nil, nil
		case !ok:
			se.Status, se.How = "on", "WP_ENVIRONMENT_TYPE isn't set, which WordPress reads as production"
		case !d.BeforeSettings:
			se.Status, se.How = "on", "WP_ENVIRONMENT_TYPE is defined after wp-settings.php, too late for WordPress to read it"
		default:
			se.Status, se.How = "on", "WP_ENVIRONMENT_TYPE isn't 'staging'"
		}
		return se, []core.Finding{finding(id, core.SeverityCritical, se.How, prepareFix(s), fileAt(d.Line))}, nil
	}}
}

// cronStep sets DISABLE_WP_CRON to true.
func cronStep(o Options) core.Step {
	id := core.FindingSideEffectCron
	st := &step{id: id, check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "DISABLE_WP_CRON is true; scheduled events are kept but WP-Cron doesn't run them", Undo: "remove DISABLE_WP_CRON from wp-config.php"}
		f, err := readConfig(ctx, s)
		if err != nil {
			se.Status, se.How = "unknown", "wp-config.php can't be read"
			return se, nil, nil
		}
		if d, ok := literal(f, "DISABLE_WP_CRON"); ok && (d.Value == "true" || d.Value == "1") {
			return se, nil, nil
		}
		d, _ := f.Lookup("DISABLE_WP_CRON")
		se.Status, se.How = "on", "DISABLE_WP_CRON isn't true, so WP-Cron runs production's scheduled events"
		return se, []core.Finding{finding(id, core.SeverityHigh, se.How, "define( 'DISABLE_WP_CRON', true ); in wp-config.php", fileAt(d.Line))}, nil
	}}
	if o.kept("cron") {
		st.plan = keptPlan(id, "cron")
		return st
	}
	st.plan, st.apply = configEdit(id,
		func(*wpconfig.File) []string { return []string{"define( 'DISABLE_WP_CRON', true );"} },
		func(f *wpconfig.File) error { return f.SetBool("DISABLE_WP_CRON", true) },
		"WP-Cron may run production's scheduled events")
	return st
}

// urlDefines are rewritten to the staging URL when wp-config.php sets them.
var urlDefines = []string{"WP_HOME", "WP_SITEURL"}

// stagingURL is o.URL without a trailing slash, as WordPress keeps it.
func (o Options) stagingURL() string { return strings.TrimSuffix(o.URL, "/") }

// networkDefines are a multisite network's DOMAIN_CURRENT_SITE and
// PATH_CURRENT_SITE for the staging URL: its host (with any port) and its
// path with a slash at both ends, as wp-config.php holds them. A network
// whose defines still name production loads production's network, or
// sends visitors there.
func (o Options) networkDefines() [][2]string {
	u, err := url.Parse(o.stagingURL())
	if err != nil || u.Host == "" {
		return nil
	}
	p := "/" + strings.Trim(u.Path, "/") + "/"
	if p == "//" {
		p = "/"
	}
	return [][2]string{{"DOMAIN_CURRENT_SITE", u.Host}, {"PATH_CURRENT_SITE", p}}
}

// subscriptionsStep rewrites WP_HOME and WP_SITEURL; the mu-plugin forces
// Subscriptions' staging mode and wc_subscriptions_siteurl is never touched.
func subscriptionsStep(o Options) core.Step {
	id := core.FindingSideEffectSubscriptions
	st := &step{id: id}
	st.check = func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "roadie-staging.php marks the site a duplicate, so Subscriptions charges no renewals", Undo: "delete wp-content/mu-plugins/roadie-staging.php"}
		opts, err := wpdb.Options(ctx, s, "wc_subscriptions_siteurl", "siteurl")
		if err != nil {
			se.Status, se.How = "unknown", "the options table can't be read"
			return se, nil, nil
		}
		stored, present := opts["wc_subscriptions_siteurl"]
		if !present {
			if ok, _ := s.Exists(ctx, "wp-content/plugins/woocommerce-subscriptions"); !ok {
				se.Status, se.How = "not-present", "WooCommerce Subscriptions isn't on this copy"
				return se, nil, nil
			}
		}
		var out []core.Finding
		if !hasHooks(ctx, s, hookDuplicateSite) {
			se.Status, se.How = "on", "Subscriptions is present and nothing forces its staging mode"
			out = append(out, finding(id, core.SeverityCritical, se.How, prepareFix(s), core.Location{File: MuPluginPath}))
		}
		here := o.stagingURL()
		if here == "" {
			here = strings.TrimSuffix(string(opts["siteurl"]), "/")
		}
		if present && here != "" && sameSite(strings.Replace(string(stored), "_[wc_subscriptions_siteurl]_", "", 1), here) {
			se.Status, se.How = "on", "wc_subscriptions_siteurl matches this copy's URL, so Subscriptions treats it as the live site"
			out = append(out, finding(id, core.SeverityCritical, se.How, "restore wc_subscriptions_siteurl from production", optionAt(ctx, s, "wc_subscriptions_siteurl", "")))
		}
		if f, err := readConfig(ctx, s); err == nil && o.URL != "" {
			for _, n := range urlDefines {
				if d, ok := literal(f, n); ok && !sameSite(d.Value, here) {
					se.Status, se.How = "on", n+" in wp-config.php isn't the staging URL"
					out = append(out, finding(id, core.SeverityCritical, se.How, "set "+n+" to "+here, fileAt(d.Line)))
				}
			}
		}
		return se, out, nil
	}
	if o.Source != nil {
		st.missing = func(ctx context.Context, s core.Site) (string, bool) {
			if o.Source.option("wc_subscriptions_siteurl") || pluginOnDisk(ctx, s, "woocommerce-subscriptions") {
				return "", false
			}
			return "WooCommerce Subscriptions isn't on this copy", true
		}
	}
	if o.kept("subscriptions") {
		st.plan = keptPlan(id, "subscriptions")
		return withData(st, onlyData(subscriptionsData(o)))
	}
	if o.URL == "" {
		st.plan = func(context.Context, core.Site) ([]core.PlannedEdit, error) {
			return []core.PlannedEdit{{Step: id, File: MuPluginPath, What: "woocommerce_subscriptions_is_duplicate_site returns true; wc_subscriptions_siteurl left as it is"}}, nil
		}
		return withData(st, onlyData(subscriptionsData(o)))
	}
	plan, apply := configEdit(id,
		func(f *wpconfig.File) []string {
			var out []string
			for _, n := range urlDefines {
				if _, ok := f.Lookup(n); ok {
					out = append(out, n+" to "+o.stagingURL())
				}
			}
			for _, nv := range o.networkDefines() {
				if _, ok := f.Lookup(nv[0]); ok {
					out = append(out, nv[0]+" to "+nv[1])
				}
			}
			return out
		},
		func(f *wpconfig.File) error {
			for _, n := range urlDefines {
				if d, ok := f.Lookup(n); ok && d.Literal {
					if err := f.Set(n, o.stagingURL()); err != nil {
						return err
					}
				}
			}
			for _, nv := range o.networkDefines() {
				if d, ok := f.Lookup(nv[0]); ok && d.Literal && d.Value != nv[1] {
					if err := f.Set(nv[0], nv[1]); err != nil {
						return err
					}
				}
			}
			return nil
		},
		"WP_HOME, WP_SITEURL or the network's DOMAIN_CURRENT_SITE may still name production")
	st.apply = apply
	st.plan = func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
		edits, err := plan(ctx, s)
		return append([]core.PlannedEdit{{Step: id, File: MuPluginPath, What: "woocommerce_subscriptions_is_duplicate_site returns true; wc_subscriptions_siteurl left as it is"}}, edits...), err
	}
	return withData(st, onlyData(subscriptionsData(o)))
}

// sameSite compares two site URLs by host and path.
func sameSite(a, b string) bool {
	norm := func(s string) string {
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil || u.Host == "" {
			return strings.ToLower(strings.TrimSuffix(s, "/"))
		}
		return strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
	}
	return norm(a) == norm(b)
}

// keyStep swaps or blanks every live key wp-config.php defines. Its Check
// lists the live keys still there as key.<provider>-live findings.
func keyStep(o Options) core.Step {
	id := core.FindingKeyGeneric
	live := func(f *wpconfig.File) []core.KeyHit {
		var out []core.KeyHit
		for _, h := range o.scan(f.Pairs()) {
			if h.Env == "live" {
				out = append(out, h)
			}
		}
		return out
	}
	pairAt := func(f *wpconfig.File, line int) (core.Pair, bool) {
		for _, p := range f.Pairs() {
			if p.Line == line {
				return p, true
			}
		}
		return core.Pair{}, false
	}
	ks := &keyFileStep{step: step{id: id}}
	plan, apply := configEdit(id,
		func(f *wpconfig.File) []string {
			var out []string
			for _, h := range live(f) {
				out = append(out, fmt.Sprintf("%s at line %d (%s): swapped or blanked", h.ID, h.At.Line, h.Masked))
			}
			return out
		},
		func(f *wpconfig.File) error {
			ks.applied = nil
			for _, h := range live(f) {
				p, ok := pairAt(f, h.At.Line)
				if !ok {
					continue
				}
				v, action, err := o.replace(h, []byte(p.Value))
				if err != nil {
					return err
				}
				h.Action = action
				ks.applied = append(ks.applied, h)
				if action == "manual" || action == "kept" {
					continue
				}
				if err := f.Set(p.Name, string(v)); err != nil {
					return err
				}
			}
			return nil
		},
		"live keys stay in wp-config.php")
	ks.plan, ks.apply = plan, apply
	ks.check = func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "no live key defined in wp-config.php"}
		f, err := readConfig(ctx, s)
		if err != nil {
			se.Status, se.How = "unknown", "wp-config.php can't be read"
			return se, nil, nil
		}
		var out []core.Finding
		for _, h := range live(f) {
			se.Status, se.How = "on", "live keys are still defined in wp-config.php"
			fd := finding(h.ID, h.Severity, "a live key is defined in wp-config.php", prepareFix(s), fileAt(h.At.Line))
			fd.Examples = []string{h.Masked}
			out = append(out, fd)
		}
		return se, out, nil
	}
	return ks
}

// saltsStep regenerates the eight salts. Its Check gives status only:
// telling them from production's needs production's values (verify).
func saltsStep(o Options) core.Step {
	id := core.FindingConfigSalts
	plan, apply := configEdit(id,
		func(*wpconfig.File) []string {
			return []string{"the eight salts (AUTH_KEY to NONCE_SALT) regenerated, 64 random characters each"}
		},
		func(f *wpconfig.File) error {
			for _, n := range Salts {
				v, err := o.salt()
				if err != nil {
					return err
				}
				if err := f.Set(n, v); err != nil {
					return err
				}
			}
			return nil
		},
		"logins from production stay valid here")
	return &step{id: id, plan: plan, apply: apply, check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "the eight salts are set", Undo: "copy the salts back from production (not recommended)"}
		f, err := readConfig(ctx, s)
		if err != nil {
			se.Status, se.How = "unknown", "wp-config.php can't be read"
			return se, nil, nil
		}
		for _, n := range Salts {
			d, ok := f.Lookup(n)
			if !ok || d.Literal && (d.Value == "" || strings.Contains(d.Value, "put your unique phrase here")) {
				se.Status, se.How = "on", n+" is missing or the sample phrase"
				break
			}
		}
		return se, nil, nil
	}}
}

// evalFiler is a site reached through WP-CLI (ssh, alias), where the
// connector goes in through wp eval-file.
type evalFiler interface {
	EvalFile(ctx context.Context, script []byte) error
}

func (o Options) echoSettings() wordpress.Settings {
	return wordpress.Settings{Mode: "seal", URL: o.EchoURL, Proxy: o.EchoProxy}
}

// mailStep installs Echo's connector in seal mode. The mailer plugins'
// settings are DataRules.
func mailStep(o Options) core.Step {
	id := core.FindingSideEffectMail
	means := "mail from this copy isn't sealed"
	st := &step{id: id}
	defer func() {
		// With mail: keep nothing is installed or blocked. The
		// mu-plugin leaves wp_mail alone; Check still reports what it finds.
		if o.kept("mail") {
			st.plan, st.apply = keptPlan(id, "mail"), nil
		}
	}()
	st.plan = func(context.Context, core.Site) ([]core.PlannedEdit, error) {
		out := []core.PlannedEdit{
			{Step: id, File: EchoPluginPath, What: "Echo connector in seal mode (" + fallback(o.EchoURL, "http://127.0.0.1:8025") + ")"},
			{Step: id, File: "wp-content/" + wordpress.SettingsPath, What: "Echo connector settings"},
		}
		if !o.echoInstall() {
			out = []core.PlannedEdit{{Step: id, What: "Echo connector not installed (side_effects.echo.install: false); roadie-staging.php blocks wp_mail instead"}}
		}
		for _, m := range o.mailers() {
			if !o.Source.option(m.Option) {
				continue
			}
			to := m.MailerPath + " to " + m.DefaultMailer
			if m.DefaultMailer == "" {
				to = m.MailerPath + " cleared (the default PHP mailer)"
			}
			out = append(out, core.PlannedEdit{Step: id, File: "options " + m.Option, What: to + ", credentials blanked"})
		}
		out = append(out, core.PlannedEdit{Step: id, File: "php.ini", What: ManualPrefix + SendmailLine + " (the path of rhythm-echo on this host), so PHP's mail() reaches Echo too"})
		return out, nil
	}
	st.apply = func(ctx context.Context, s core.Site) error {
		if !o.echoInstall() {
			return nil
		}
		if old, err := s.ReadFile(ctx, EchoPluginPath); err == nil && !bytes.Contains(old, []byte(echoMarker)) {
			return core.Errorf(core.ErrKillSwitch, 1,
				"side-effect.mail: %s exists and wasn't written by Echo, so %s. Remove it, then: rhythm-roadie prepare %s --only side-effects.",
				EchoPluginPath, means, s.Endpoint().Display)
		}
		if ef, ok := s.(evalFiler); ok {
			script, err := wordpress.Script(o.echoSettings())
			if err != nil {
				return killErr(id, EchoPluginPath, err, means, s)
			}
			if err := ef.EvalFile(ctx, []byte(script)); err != nil {
				return killErr(id, EchoPluginPath, err, means, s)
			}
			return nil
		}
		files, err := wordpress.Files(o.echoSettings())
		if err != nil {
			return killErr(id, EchoPluginPath, err, means, s)
		}
		for _, rel := range sortedKeys(files) {
			if err := s.WriteFile(ctx, path.Join("wp-content", rel), files[rel], 0o644); err != nil {
				return killErr(id, path.Join("wp-content", rel), err, means, s)
			}
		}
		return nil
	}
	st.check = func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "Echo's connector seals wp_mail", Undo: "delete " + EchoPluginPath + " (roadie-staging.php then blocks wp_mail)"}
		var out []core.Finding
		b, err := s.ReadFile(ctx, EchoPluginPath)
		switch {
		case err == nil:
			if sha256.Sum256(b) != sha256.Sum256(wordpress.Plugin()) {
				out = append(out, finding(core.FindingSideEffectConnectorChange, core.SeverityHigh,
					"the installed Echo connector differs from the one Roadie ships", prepareFix(s), core.Location{File: EchoPluginPath}))
			}
		case hasHooks(ctx, s, hookPreMail):
			se.How, se.Undo = "no Echo connector; roadie-staging.php blocks wp_mail", "delete "+MuPluginPath
		default:
			se.Status, se.How = "on", "no Echo connector and nothing blocks wp_mail"
			out = append(out, finding(id, core.SeverityCritical, se.How, prepareFix(s), core.Location{File: EchoPluginPath}))
		}
		ms := o.mailers()
		if len(ms) == 0 {
			return se, out, nil
		}
		names := map[string]bool{}
		for _, m := range ms {
			names[m.Option] = true
		}
		opts, err := wpdb.OptionsMatch(ctx, s, func(n string) bool { return names[n] })
		if err != nil {
			return se, out, nil
		}
		for _, m := range ms {
			raw, ok := opts[m.Option]
			if !ok {
				continue
			}
			v, err := settings(raw)
			if err != nil {
				continue
			}
			if v[m.MailerPath] == m.DefaultMailer || v[m.MailerPath] == "" {
				continue
			}
			for _, p := range m.SecretPaths {
				if v[p] != "" {
					se.Status, se.How = "on", "an SMTP or mail API plugin still sends with credentials"
					out = append(out, finding(id, core.SeverityCritical, "the "+m.ID+" mailer still has credentials and sends past Echo",
						prepareFix(s), optionAt(ctx, s, m.Option, p)))
					break
				}
			}
		}
		return se, out, nil
	}
	return st
}

func fallback(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// hasHooks reports a Roadie mu-plugin on the site holding every hook.
func hasHooks(ctx context.Context, s core.Site, hooks ...string) bool {
	b, err := s.ReadFile(ctx, MuPluginPath)
	if err != nil || !bytes.Contains(b, []byte(MuPluginMarker)) {
		return false
	}
	for _, h := range hooks {
		if !bytes.Contains(b, []byte("'"+h+"'")) {
			return false
		}
	}
	return true
}

// schedulerStep writes the Roadie mu-plugin; its Check counts the
// pending actions Roadie kept.
func schedulerStep(o Options) core.Step {
	id := core.FindingSideEffectActionScheduler
	means := "Action Scheduler, Subscriptions renewals and Jetpack aren't paused"
	st := &step{id: id}
	st.plan = func(context.Context, core.Site) ([]core.PlannedEdit, error) {
		out := []core.PlannedEdit{{Step: id, File: MuPluginPath, What: "Roadie mu-plugin: " + strings.Join(o.muParts(), ", ")}}
		if o.setting("action_scheduler") == "cancel" {
			out = append(out, core.PlannedEdit{Step: id, File: "actionscheduler_actions", What: "pending actions marked canceled (side_effects.action_scheduler: cancel)"})
		}
		return out, nil
	}
	st.apply = func(ctx context.Context, s core.Site) error {
		if old, err := s.ReadFile(ctx, MuPluginPath); err == nil && !bytes.Contains(old, []byte(MuPluginMarker)) {
			return core.Errorf(core.ErrKillSwitch, 1,
				"side-effect.action-scheduler: %s exists and wasn't written by Roadie, so %s. Remove it, then: rhythm-roadie prepare %s --only side-effects.",
				MuPluginPath, means, s.Endpoint().Display)
		}
		if err := s.WriteFile(ctx, MuPluginPath, MuPlugin(o), 0o644); err != nil {
			return killErr(id, MuPluginPath, err, means, s)
		}
		return nil
	}
	st.check = func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "roadie-staging.php pauses Action Scheduler", Undo: "delete " + MuPluginPath}
		pending, err := wpdb.Count(ctx, s, wpdb.Prefix(ctx, s)+"actionscheduler_actions", []string{"status"}, func(v [][]byte) bool {
			return string(v[0]) == "pending"
		})
		known := err == nil
		if known {
			se.How += "; " + strconv.FormatInt(pending, 10) + " pending actions kept, never run"
		}
		if hasHooks(ctx, s, hookAsyncRunner, hookBatches, hookRunQueue) {
			return se, nil, nil
		}
		se.Status, se.How = "on", "nothing pauses Action Scheduler"
		if !known {
			se.Status = "unknown"
			return se, nil, nil
		}
		if pending == 0 {
			return se, nil, nil
		}
		fd := finding(id, core.SeverityHigh, "Action Scheduler isn't paused and has pending actions", prepareFix(s), core.Location{Table: wpdb.Prefix(ctx, s) + "actionscheduler_actions", Column: "status"})
		fd.Count, fd.Locations[0].Count = pending, pending
		fd.Detail = strconv.FormatInt(pending, 10) + " pending actions"
		return se, []core.Finding{fd}, nil
	}
	return withData(st, schedulerData)
}

// jetpackStep is check only: the mu-plugin puts Jetpack offline and a
// DataRule blanks its tokens.
func jetpackStep(o Options) core.Step {
	id := core.FindingSideEffectJetpack
	st := &step{id: id,
		plan: func(context.Context, core.Site) ([]core.PlannedEdit, error) {
			out := []core.PlannedEdit{{Step: id, File: MuPluginPath, What: "jetpack_offline_mode returns true"}}
			if o.Source.option("jetpack_private_options") {
				out = append(out, core.PlannedEdit{Step: id, File: "options jetpack_private_options", What: "blog and user tokens blanked"})
			}
			return out, nil
		},
		check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "off", How: "roadie-staging.php puts Jetpack in offline mode", Undo: "delete " + MuPluginPath + " and reconnect Jetpack"}
			opts, err := wpdb.Options(ctx, s, "jetpack_options")
			_, inDB := opts["jetpack_options"]
			onDisk, _ := s.Exists(ctx, "wp-content/plugins/jetpack")
			if err == nil && !inDB && !onDisk {
				se.Status, se.How = "not-present", "Jetpack isn't on this copy"
				return se, nil, nil
			}
			if hasHooks(ctx, s, hookJetpack) {
				return se, nil, nil
			}
			se.Status, se.How = "on", "Jetpack is present and not in offline mode"
			return se, []core.Finding{finding(id, core.SeverityMedium, se.How, prepareFix(s), core.Location{File: MuPluginPath})}, nil
		}}
	if o.Source != nil {
		st.missing = func(ctx context.Context, s core.Site) (string, bool) {
			if o.Source.option("jetpack_options") || pluginOnDisk(ctx, s, "jetpack") {
				return "", false
			}
			return "Jetpack isn't on this copy", true
		}
	}
	return st
}

// webhooksStep is check only: a DataRule pauses wc_webhooks.
func webhooksStep(o Options) core.Step {
	id := core.FindingSideEffectWebhooks
	st := &step{id: id,
		plan: func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
			if o.kept("webhooks") {
				return keptPlan(id, "webhooks")(ctx, s)
			}
			if n, _ := o.Source.table("wc_webhooks"); o.Source != nil && n == 0 {
				return nil, nil
			}
			return []core.PlannedEdit{{Step: id, File: "wc_webhooks", What: "status to paused, secret blanked"}}, nil
		},
		check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "off", How: "every WooCommerce webhook is paused", Undo: "WooCommerce > Settings > Advanced > Webhooks"}
			table := wpdb.Prefix(ctx, s) + "wc_webhooks"
			var total, active int64
			err := wpdb.Rows(ctx, s, table, []string{"status"}, func(v [][]byte) error {
				total++
				if string(v[0]) == "active" {
					active++
				}
				return nil
			})
			switch {
			case err != nil:
				se.Status, se.How = "unknown", "wc_webhooks can't be read"
				return se, nil, nil
			case total == 0:
				se.Status, se.How = "not-present", "no WooCommerce webhooks"
				return se, nil, nil
			case active == 0:
				return se, nil, nil
			}
			se.Status, se.How = "on", strconv.FormatInt(active, 10)+" WooCommerce webhooks are active"
			fd := finding(id, core.SeverityHigh, "active WooCommerce webhooks send this copy's orders to their endpoints", prepareFix(s), core.Location{Table: table, Column: "status"})
			fd.Count, fd.Locations[0].Count = active, active
			return se, []core.Finding{fd}, nil
		}}
	if o.Source != nil {
		st.missing = func(context.Context, core.Site) (string, bool) {
			if n, _ := o.Source.table("wc_webhooks"); n > 0 {
				return "", false
			}
			return "no WooCommerce webhooks", true
		}
	}
	return withData(st, onlyData(st.check))
}

// wcpayID is WooPayments' gateway ID.
const wcpayID = "woocommerce_payments"

// wcpayDevMode reports whether wp-config.php puts WooPayments in dev
// (sandbox) mode, as verify reads it: WCPAY_DEV_MODE on,
// WP_ENVIRONMENT_TYPE development or staging, or any WP_DEVELOPMENT_MODE.
// WooPayments then only uses a test account, which can't take live
// payments, so its test_mode setting doesn't matter.
func wcpayDevMode(f *wpconfig.File) bool {
	if d, ok := literal(f, "WCPAY_DEV_MODE"); ok {
		if v := strings.ToLower(strings.TrimSpace(d.Value)); v == "true" || v == "1" {
			return true
		}
	}
	if d, ok := literal(f, "WP_ENVIRONMENT_TYPE"); ok {
		if v := strings.ToLower(strings.TrimSpace(d.Value)); v == "development" || v == "staging" {
			return true
		}
	}
	if d, ok := literal(f, "WP_DEVELOPMENT_MODE"); ok {
		if v := strings.ToLower(strings.TrimSpace(d.Value)); v != "" && v != "false" && v != "0" {
			return true
		}
	}
	return false
}

// gatewayOption is the settings option of a WooCommerce gateway ID.
func gatewayOption(id string) string { return "woocommerce_" + id + "_settings" }

// gatewaysStep is check only: DataRules put known gateways in test mode
// and disable unknown ones.
func gatewaysStep(o Options) core.Step {
	id := core.FindingSideEffectGateways
	st := &step{id: id,
		plan: func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
			if o.kept("gateways") {
				return keptPlan(id, "gateways")(ctx, s)
			}
			var out []core.PlannedEdit
			for _, g := range o.gateways() {
				if !o.Source.option(g.Option) {
					continue
				}
				out = append(out, core.PlannedEdit{Step: id, File: "options " + g.Option, What: g.Name + " to test mode, live keys swapped or blanked"})
			}
			for _, u := range unknownGateways(o) {
				out = append(out, core.PlannedEdit{Step: id, File: "options " + gatewayOption(u), What: "unknown gateway " + u + " disabled (enabled = no)"})
			}
			return out, nil
		},
		check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "off", How: "every enabled gateway is in test mode", Undo: "WooCommerce > Settings > Payments"}
			known := map[string]bool{}
			for _, g := range o.gateways() {
				known[g.Option] = true
			}
			opts, err := wpdb.OptionsMatch(ctx, s, func(n string) bool {
				return known[n] || GatewayOptionsMatch(n)
			})
			if err != nil {
				se.Status, se.How = "unknown", "the options table can't be read"
				return se, nil, nil
			}
			var out []core.Finding
			bad := func(opt, leaf, title string) {
				se.Status, se.How = "on", "a payment gateway can take real payments"
				out = append(out, finding(id, core.SeverityCritical, title, prepareFix(s), optionAt(ctx, s, opt, leaf)))
			}
			dev := false
			if f, err := readConfig(ctx, s); err == nil {
				dev = wcpayDevMode(f)
			}
			for _, g := range o.gateways() {
				raw, ok := opts[g.Option]
				if !ok || g.ID == wcpayID && dev {
					continue
				}
				v, err := settings(raw)
				if err != nil {
					continue
				}
				if g.EnabledPath != "" && v[g.EnabledPath] != "yes" {
					continue
				}
				if g.ModePath != "" && v[g.ModePath] != g.TestValue {
					bad(g.Option, g.ModePath, g.Name+" is enabled and not in test mode")
				}
			}
			// Without woocommerce_gateway_order the settings options are
			// the only list, so an unknown gateway is still checked.
			for _, gid := range SourceGatewayIDs(opts) {
				opt := gatewayOption(gid)
				raw, ok := opts[opt]
				if knownGateway(o, gid) || !ok {
					continue
				}
				if v, err := settings(raw); err == nil && v["enabled"] == "yes" {
					bad(opt, "enabled", "the gateway "+gid+" is enabled and Roadie can't tell its test mode")
				}
			}
			if len(out) == 0 && len(opts) == 0 {
				se.Status, se.How = "not-present", "no WooCommerce gateways"
			}
			return se, out, nil
		}}
	if o.Source != nil {
		st.missing = func(context.Context, core.Site) (string, bool) {
			for name := range o.Source.Options {
				if GatewayOptionsMatch(name) || slices.ContainsFunc(o.gateways(), func(g keys.Gateway) bool { return g.Option == name }) {
					return "", false
				}
			}
			return "no WooCommerce gateways", true
		}
	}
	// No data check: on a dump or a database, key.live-mode in the
	// data scan reads the gateways, as Gateway.Mode does.
	return st
}

// knownGateway reports a gateway ID the list has, by its ID or by its
// woocommerce_<id>_settings option.
func knownGateway(o Options, id string) bool {
	for _, g := range o.gateways() {
		if g.ID == id || g.Option == gatewayOption(id) {
			return true
		}
	}
	return false
}

// unknownGateways are the source's gateway IDs the list doesn't know.
func unknownGateways(o Options) []string {
	var out []string
	for _, id := range o.GatewayIDs {
		if !knownGateway(o, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// automatewooStep is check only: a DataRule truncates the queue.
func automatewooStep(o Options) core.Step {
	id := core.FindingSideEffectAutomateWoo
	st := &step{id: id,
		plan: func(context.Context, core.Site) ([]core.PlannedEdit, error) {
			if _, ok := o.Source.table("automatewoo_queue"); !ok {
				return nil, nil
			}
			return []core.PlannedEdit{{Step: id, File: "automatewoo_queue", What: "queue truncated; workflows run through Action Scheduler, which is paused"}}, nil
		},
		check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "off", How: "the AutomateWoo queue is empty", Undo: "none needed: new workflows queue as usual"}
			table := wpdb.Prefix(ctx, s) + "automatewoo_queue"
			if has, err := wpdb.Has(ctx, s, table); err == nil && !has {
				se.Status, se.How, se.Undo = "not-present", "AutomateWoo isn't on this copy", ""
				return se, nil, nil
			}
			n, err := wpdb.Count(ctx, s, table, nil, func([][]byte) bool { return true })
			if err != nil {
				se.Status, se.How = "unknown", "automatewoo_queue can't be read"
				return se, nil, nil
			}
			if n == 0 {
				return se, nil, nil
			}
			se.Status, se.How = "on", strconv.FormatInt(n, 10)+" AutomateWoo workflows are queued"
			fd := finding(id, core.SeverityHigh, "Queued AutomateWoo workflows from production would send to real customers", prepareFix(s), core.Location{Table: table})
			fd.Count, fd.Locations[0].Count = n, n
			return se, []core.Finding{fd}, nil
		}}
	if o.Source != nil {
		st.missing = func(context.Context, core.Site) (string, bool) {
			if _, ok := o.Source.table("automatewoo_queue"); ok {
				return "", false
			}
			return "AutomateWoo isn't on this copy", true
		}
	}
	return withData(st, onlyData(st.check))
}
