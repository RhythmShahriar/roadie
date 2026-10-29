// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
)

// EchoSMTPHost and EchoSMTPPort are Echo's SMTP sink, where a Laravel
// copy's MAIL_* settings point.
const (
	EchoSMTPHost = "127.0.0.1"
	EchoSMTPPort = "1025"
)

// ComposerLine is the command that adds Echo's Laravel connector. Roadie
// never runs Composer; the plan prints it.
const ComposerLine = "composer require rhythmsh/echo-laravel --dev"

// WorkersLine is the plan's reminder about queue workers already running.
const WorkersLine = "Stop queue workers and the scheduler on this host: jobs from production were cleared, new ones run in the request (QUEUE_CONNECTION=sync) through Echo."

// DatabaseOnlyLine is the plan's SIDE EFFECTS line for a target with no
// files (PRD 13.4).
const DatabaseOnlyLine = "This target is a database only: Roadie can't install the Echo seal or edit config files. Turn mail and jobs off where the app runs."

// ConfigCache is Laravel's cached config (php artisan config:cache).
// While it is there Laravel never reads .env, so every .env edit is
// ignored and the cache's own env, queue, mail and keys are what run.
const ConfigCache = "bootstrap/cache/config.php"

// laravelCaches are the caches prepare deletes: the config cache plus the
// route and event caches artisan optimize writes with it. Laravel reads
// the source files again without them.
var laravelCaches = []string{ConfigCache, "bootstrap/cache/routes-v7.php", "bootstrap/cache/events.php"}

// configCacheHow says why a check fails while the config cache is there.
const configCacheHow = ConfigCache + " exists, so Laravel ignores .env and runs on the cached config"

// configCacheFix is the fix of a check that fails on the config cache.
func configCacheFix(s core.Site) string {
	return "run php artisan config:clear on this copy, or " + prepareFix(s)
}

// hasConfigCache reports Laravel's cached config on s.
func hasConfigCache(ctx context.Context, s core.Site) (bool, error) {
	return s.Exists(ctx, ConfigCache)
}

// cachedOn is the side effect and finding of a check while the config
// cache is there: never off, since .env doesn't count.
func cachedOn(s core.Site, se core.SideEffect, sev core.Severity, what string) (core.SideEffect, []core.Finding, error) {
	se.Status, se.How = "on", configCacheHow+" ("+what+" from when it was cached)"
	return se, []core.Finding{finding(se.ID, sev, se.How, configCacheFix(s), core.Location{File: ConfigCache})}, nil
}

// cacheCheck wraps check: while the config cache is there the step is on
// whatever .env says.
func cacheCheck(sev core.Severity, what string, check func(context.Context, core.Site) (core.SideEffect, []core.Finding, error)) func(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
	return func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se, found, err := check(ctx, s)
		if err != nil {
			return se, found, err
		}
		cached, cerr := hasConfigCache(ctx, s)
		switch {
		case cerr != nil:
			se.Status, se.How = "unknown", ConfigCache+" can't be checked ("+cerr.Error()+"); while it is there Laravel ignores .env"
			return se, append(found, finding(se.ID, sev, se.How, configCacheFix(s), core.Location{File: ConfigCache})), nil
		case cached:
			return cachedOn(s, se, sev, what)
		}
		return se, found, nil
	}
}

// clearCaches deletes the Laravel caches that are there.
func clearCaches(ctx context.Context, s core.Site, id string) error {
	for _, rel := range laravelCaches {
		ok, err := s.Exists(ctx, rel)
		if err != nil {
			return killErr(id, rel, err, "Laravel ignores .env while "+ConfigCache+" is there", s)
		}
		if !ok {
			continue
		}
		if err := s.Remove(ctx, rel); err != nil {
			return killErr(id, rel, err, "Laravel ignores .env while "+ConfigCache+" is there; run php artisan config:clear on this copy", s)
		}
	}
	return nil
}

// cachePlan lists the caches prepare deletes.
func cachePlan(ctx context.Context, s core.Site, id string) []core.PlannedEdit {
	var out []core.PlannedEdit
	for _, rel := range laravelCaches {
		if ok, _ := s.Exists(ctx, rel); ok {
			what := "deleted, so Laravel reads the source files again"
			if rel == ConfigCache {
				what = "deleted: while it is there Laravel ignores .env and runs production's cached env, queue, mail and keys (run php artisan config:cache again only after checking .env)"
			}
			out = append(out, core.PlannedEdit{Step: id, File: rel, What: what})
		}
	}
	return out
}

// envFile is the Laravel .env file, relative to the site root.
func (o Options) envFile() string {
	if o.Cfg != nil {
		return cmp.Or(o.Cfg.Laravel.EnvFile, ".env")
	}
	return ".env"
}

// envExtra is laravel.env from the config: keys the user sets on every
// copy. They win over Roadie's own values.
func (o Options) envExtra() map[string]string {
	if o.Cfg == nil {
		return nil
	}
	return o.Cfg.Laravel.Env
}

func (o Options) appKey() (string, error) {
	f := o.AppKey
	if f == nil {
		f = pseudo.AppKey
	}
	k := f()
	if k == "" {
		return "", fmt.Errorf("config.salts: %w", core.ErrNotBuilt)
	}
	return k, nil
}

// envSet sets name unless laravel.env sets it, which the environment
// step applies instead.
func (o Options) envSet(f *dotenv.File, name, value string) error {
	if _, ok := o.envExtra()[name]; ok {
		return nil
	}
	return f.Set(name, value)
}

func readEnv(ctx context.Context, s core.Site, rel string) (*dotenv.File, error) {
	b, err := s.ReadFile(ctx, rel)
	if err != nil {
		return nil, err
	}
	return dotenv.Parse(b)
}

// envEdit is a step that changes .env keys: plan lists the edits, edit
// makes them.
func envEdit(o Options, id string, plan func(f *dotenv.File) []string, edit func(f *dotenv.File) error, means string) (func(context.Context, core.Site) ([]core.PlannedEdit, error), func(context.Context, core.Site) error) {
	rel := o.envFile()
	p := func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
		f, err := readEnv(ctx, s, rel)
		if err != nil {
			return []core.PlannedEdit{{Step: id, File: rel, What: "can't read " + rel + ": " + err.Error()}}, nil
		}
		var out []core.PlannedEdit
		for _, w := range plan(f) {
			out = append(out, core.PlannedEdit{Step: id, File: rel, What: w})
		}
		return out, nil
	}
	a := func(ctx context.Context, s core.Site) error {
		b, err := s.ReadFile(ctx, rel)
		if err != nil {
			return killErr(id, rel, err, means, s)
		}
		f, err := dotenv.Parse(b)
		if err != nil {
			return killErr(id, rel, err, means, s)
		}
		if err := edit(f); err != nil {
			return killErr(id, rel, err, means, s)
		}
		if out := f.Bytes(); !bytes.Equal(out, b) {
			if err := s.WriteFile(ctx, rel, out, 0o640); err != nil {
				return killErr(id, rel, err, means, s)
			}
		}
		return nil
	}
	return p, a
}

// setLine is a plan line for KEY=value, showing laravel.env's value when
// it overrides Roadie's.
func (o Options) setLine(name, value string) string {
	if v, ok := o.envExtra()[name]; ok {
		return name + "=" + v + " (laravel.env)"
	}
	return name + "=" + value
}

func laravelSteps(o Options) []core.Step {
	return []core.Step{
		laravelEnvironmentStep(o),
		laravelSaltsStep(o),
		laravelMailStep(o),
		laravelQueueStep(o),
		laravelKeyStep(o),
	}
}

// laravelEnv lists the values a staging copy's APP_ENV may hold.
var laravelEnv = []string{"staging", "local", "development", "testing"}

// laravelEnvironmentStep sets APP_ENV and APP_URL, then the laravel.env
// keys of the config.
func laravelEnvironmentStep(o Options) core.Step {
	id := core.FindingConfigEnvironment
	rel := o.envFile()
	extra := sortedNames(o.envExtra())
	plan, apply := envEdit(o, id,
		func(*dotenv.File) []string {
			out := []string{o.setLine("APP_ENV", "staging")}
			if o.URL != "" {
				out = append(out, o.setLine("APP_URL", o.stagingURL()))
			}
			for _, n := range extra {
				if n != "APP_ENV" && n != "APP_URL" {
					out = append(out, n+"="+o.envExtra()[n]+" (laravel.env)")
				}
			}
			return out
		},
		func(f *dotenv.File) error {
			if err := o.envSet(f, "APP_ENV", "staging"); err != nil {
				return err
			}
			if o.URL != "" {
				if err := o.envSet(f, "APP_URL", o.stagingURL()); err != nil {
					return err
				}
			}
			for _, n := range extra {
				if err := f.Set(n, o.envExtra()[n]); err != nil {
					return err
				}
			}
			return nil
		},
		"Laravel may still call this copy production")
	return &step{id: id, plan: func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
		edits, err := plan(ctx, s)
		return append(edits, cachePlan(ctx, s, id)...), err
	}, apply: func(ctx context.Context, s core.Site) error {
		if err := apply(ctx, s); err != nil {
			return err
		}
		return clearCaches(ctx, s, id)
	}, check: cacheCheck(core.SeverityCritical, "its APP_ENV", func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", Undo: "set APP_ENV in " + rel}
		f, err := readEnv(ctx, s, rel)
		if err != nil {
			se.Status, se.How = "unknown", rel+" can't be read"
			return se, nil, nil
		}
		p, ok := f.Get("APP_ENV")
		switch {
		case ok && slices.Contains(laravelEnv, p.Value) && !slices.Contains(f.Unresolved(), "APP_ENV"):
			se.How = "APP_ENV is " + p.Value + " in " + rel
			return se, nil, nil
		case !ok || p.Value == "":
			se.Status, se.How = "on", "APP_ENV isn't set, which Laravel reads as production"
		default:
			se.Status, se.How = "on", "APP_ENV isn't staging"
		}
		return se, []core.Finding{finding(id, core.SeverityCritical, se.How, prepareFix(s), core.Location{File: rel, Line: p.Line})}, nil
	})}
}

// laravelSaltsStep rotates APP_KEY unless --keep-app-key. Its Check gives
// status only: telling the key from production's needs production's
// value, which verify has.
func laravelSaltsStep(o Options) core.Step {
	id := core.FindingConfigSalts
	rel := o.envFile()
	plan, apply := envEdit(o, id,
		func(*dotenv.File) []string {
			if o.KeepAppKey {
				return []string{"APP_KEY kept (--keep-app-key): it is production's key, so encrypted columns and old sessions stay readable only with it; this copy can also read what production encrypts"}
			}
			return []string{o.setLine("APP_KEY", "base64: and 32 random bytes") + ": encrypted columns and old sessions from production become unreadable (pass --keep-app-key to keep the key)"}
		},
		func(f *dotenv.File) error {
			if o.KeepAppKey {
				return nil
			}
			k, err := o.appKey()
			if err != nil {
				return err
			}
			return o.envSet(f, "APP_KEY", k)
		},
		"this copy keeps production's APP_KEY")
	return &step{id: id, plan: plan, apply: apply, check: func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "APP_KEY is set", Undo: "copy APP_KEY back from production (not recommended)"}
		f, err := readEnv(ctx, s, rel)
		if err != nil {
			se.Status, se.How = "unknown", rel+" can't be read"
			return se, nil, nil
		}
		if p, ok := f.Get("APP_KEY"); !ok || p.Value == "" {
			se.Status, se.How = "on", "APP_KEY is missing"
		} else if o.KeepAppKey {
			se.Status, se.How = "on", "APP_KEY was kept from production (--keep-app-key)"
		}
		return se, nil, nil
	}}
}

// mailCredentialKeys are the .env keys of Laravel's API mailers, by
// MAIL_MAILER.
var mailCredentialKeys = map[string][]string{
	"mailgun":  {"MAILGUN_SECRET"},
	"postmark": {"POSTMARK_TOKEN", "POSTMARK_API_KEY"},
	"ses":      {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
	"ses-v2":   {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
	"resend":   {"RESEND_KEY", "RESEND_API_KEY"},
	"sendgrid": {"SENDGRID_API_KEY"},
}

// laravelMailStep points MAIL_* at Echo's SMTP sink. MAIL_URL, MAIL_SCHEME
// and MAIL_ENCRYPTION are blanked when the file has them: a MAIL_URL wins
// over MAIL_HOST; TLS to the sink would fail.
func laravelMailStep(o Options) core.Step {
	id := core.FindingSideEffectMail
	rel := o.envFile()
	echoURL := fallback(o.EchoURL, "http://127.0.0.1:8025")
	set := []struct{ name, value string }{
		{"MAIL_MAILER", "smtp"}, {"MAIL_HOST", EchoSMTPHost}, {"MAIL_PORT", EchoSMTPPort},
		{"MAIL_USERNAME", ""}, {"MAIL_PASSWORD", ""},
		{"RHYTHM_ECHO_URL", echoURL}, {"RHYTHM_ECHO_MODE", "seal"},
	}
	blankIfSet := []string{"MAIL_URL", "MAIL_SCHEME", "MAIL_ENCRYPTION"}
	st := &step{id: id}
	if o.kept("mail") {
		st.plan = keptPlan(id, "mail")
	} else {
		plan, apply := envEdit(o, id,
			func(f *dotenv.File) []string {
				var out []string
				for _, kv := range set {
					out = append(out, o.setLine(kv.name, kv.value))
				}
				for _, n := range blankIfSet {
					if _, ok := f.Get(n); ok {
						out = append(out, o.setLine(n, ""))
					}
				}
				return out
			},
			func(f *dotenv.File) error {
				for _, kv := range set {
					if err := o.envSet(f, kv.name, kv.value); err != nil {
						return err
					}
				}
				for _, n := range blankIfSet {
					if _, ok := f.Get(n); ok {
						if err := o.envSet(f, n, ""); err != nil {
							return err
						}
					}
				}
				return nil
			},
			"mail from this copy isn't sealed")
		st.apply = apply
		st.plan = func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
			edits, err := plan(ctx, s)
			return append(edits, core.PlannedEdit{Step: id, File: "composer.json",
				What: ManualPrefix + ComposerLine + " (Roadie never runs Composer), so Echo seals mail, SMS and webhooks in the app too"}), err
		}
	}
	st.check = cacheCheck(core.SeverityCritical, "its mailer", func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", Undo: "set MAIL_* in " + rel + " back to your mail server"}
		f, err := readEnv(ctx, s, rel)
		if err != nil {
			se.Status, se.How = "unknown", rel+" can't be read"
			return se, nil, nil
		}
		get := func(n string) (core.Pair, bool) {
			p, ok := f.Get(n)
			return p, ok && p.Value != ""
		}
		on := func(how string, p core.Pair) (core.SideEffect, []core.Finding, error) {
			se.Status, se.How = "on", how
			return se, []core.Finding{finding(id, core.SeverityCritical, how, prepareFix(s), core.Location{File: rel, Line: p.Line})}, nil
		}
		if p, ok := get("RHYTHM_ECHO_MODE"); ok && p.Value != "seal" {
			return on("RHYTHM_ECHO_MODE is "+p.Value+", so Echo lets messages out", p)
		}
		if p, ok := get("MAIL_URL"); ok {
			if u, err := url.Parse(p.Value); err != nil || !loopback(u.Hostname()) {
				return on("MAIL_URL names a mail server other than Echo's sink", p)
			}
		}
		mailer, _ := f.Get("MAIL_MAILER")
		switch m := cmp.Or(mailer.Value, "smtp"); m {
		case "smtp":
			host, _ := f.Get("MAIL_HOST")
			if loopback(host.Value) {
				se.How = "MAIL_HOST is " + host.Value + " (Echo's SMTP sink when it runs there)"
				return se, nil, nil
			}
			user, u := get("MAIL_USERNAME")
			pass, pw := get("MAIL_PASSWORD")
			if u || pw {
				at := pass
				if !pw {
					at = user
				}
				return on("MAIL_HOST is "+cmp.Or(host.Value, "unset")+" with credentials set, so mail from this copy reaches real people", at)
			}
			se.Status, se.How = "unknown", "MAIL_HOST is "+cmp.Or(host.Value, "unset")+" without credentials; a relay that needs none would still send"
		case "log", "array":
			se.How = "MAIL_MAILER is " + m + ", which sends nothing"
		default:
			for _, k := range mailCredentialKeys[m] {
				if p, ok := get(k); ok {
					return on("MAIL_MAILER is "+m+" and "+k+" is set, so mail from this copy reaches real people", p)
				}
			}
			se.Status, se.How = "unknown", "MAIL_MAILER is "+m+", which Roadie can't check"
		}
		return se, nil, nil
	})
	return st
}

// loopback reports a host name or address on this machine.
func loopback(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// idleQueues are the QUEUE_CONNECTION values that never leave a job for
// a worker.
var idleQueues = []string{"sync", "null", "log"}

// laravelQueueStep runs jobs in the request (QUEUE_CONNECTION=sync), so
// no worker picks up production's jobs. The plan reminds the user of
// workers already running.
func laravelQueueStep(o Options) core.Step {
	id := core.FindingSideEffectQueue
	rel := o.envFile()
	plan, apply := envEdit(o, id,
		func(*dotenv.File) []string { return []string{o.setLine("QUEUE_CONNECTION", "sync")} },
		func(f *dotenv.File) error { return o.envSet(f, "QUEUE_CONNECTION", "sync") },
		"queued jobs may run here")
	return &step{id: id,
		plan: func(ctx context.Context, s core.Site) ([]core.PlannedEdit, error) {
			edits, err := plan(ctx, s)
			return append(edits, core.PlannedEdit{Step: id, File: "queue workers", What: ManualPrefix + WorkersLine}), err
		},
		apply: apply,
		check: cacheCheck(core.SeverityHigh, "its queue connection", func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "off", Undo: "set QUEUE_CONNECTION in " + rel + " and start a worker"}
			f, err := readEnv(ctx, s, rel)
			if err != nil {
				se.Status, se.How = "unknown", rel+" can't be read"
				return se, nil, nil
			}
			p, set := f.Get("QUEUE_CONNECTION")
			// Laravel 11 and later default to the database queue.
			conn := cmp.Or(p.Value, "database")
			if slices.Contains(idleQueues, conn) {
				se.How = "QUEUE_CONNECTION is " + conn + ", so no worker runs queued jobs"
				return se, nil, nil
			}
			if conn != "database" {
				se.Status, se.How = "unknown", "QUEUE_CONNECTION is "+conn+"; that queue isn't in the database, so Roadie can't count its jobs"
				return se, nil, nil
			}
			prefix, _ := f.Get("DB_PREFIX")
			table := prefix.Value + "jobs"
			n, err := wpdb.Count(ctx, s, table, nil, func([][]byte) bool { return true })
			if err != nil {
				se.Status, se.How = "unknown", table+" can't be read"
				return se, nil, nil
			}
			se.Status, se.How = "on", "QUEUE_CONNECTION is database, so a worker runs every queued job"
			if !set {
				se.How = "QUEUE_CONNECTION isn't set, so Laravel uses the database queue"
			}
			if n == 0 {
				return se, nil, nil
			}
			fd := finding(id, core.SeverityHigh, strconv.FormatInt(n, 10)+" queued jobs from production wait for a worker on this copy", prepareFix(s), core.Location{Table: table})
			fd.Count, fd.Locations[0].Count = n, n
			fd.Detail = se.How
			return se, []core.Finding{fd}, nil
		})}
}

// laravelKeyStep swaps or blanks every live key in .env. Its Check lists
// the live keys still there.
func laravelKeyStep(o Options) core.Step {
	id := core.FindingKeyGeneric
	rel := o.envFile()
	live := func(f *dotenv.File) []core.KeyHit {
		var out []core.KeyHit
		for _, h := range o.scanIn(rel, f.Pairs()) {
			if h.Env == "live" {
				out = append(out, h)
			}
		}
		return out
	}
	pairAt := func(f *dotenv.File, line int) (core.Pair, bool) {
		for _, p := range f.Pairs() {
			if p.Line == line {
				return p, true
			}
		}
		return core.Pair{}, false
	}
	ks := &keyFileStep{step: step{id: id}}
	plan, apply := envEdit(o, id,
		func(f *dotenv.File) []string {
			var out []string
			for _, h := range live(f) {
				out = append(out, fmt.Sprintf("%s at line %d (%s): swapped or blanked", h.ID, h.At.Line, h.Masked))
			}
			return out
		},
		func(f *dotenv.File) error {
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
				if err := o.envSet(f, p.Name, string(v)); err != nil {
					return err
				}
			}
			return nil
		},
		"live keys stay in "+rel)
	ks.plan, ks.apply = plan, apply
	ks.check = func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se := core.SideEffect{ID: id, Status: "off", How: "no live key in " + rel}
		f, err := readEnv(ctx, s, rel)
		if err != nil {
			se.Status, se.How = "unknown", rel+" can't be read"
			return se, nil, nil
		}
		var out []core.Finding
		for _, h := range live(f) {
			se.Status, se.How = "on", "live keys are still in "+rel
			fd := finding(h.ID, h.Severity, "a live key is set in "+rel, prepareFix(s), core.Location{File: rel, Line: h.At.Line})
			fd.Examples = []string{h.Masked}
			out = append(out, fd)
		}
		return se, out, nil
	}
	return ks
}

// laravelRules clear production's queued jobs, so no worker on the copy
// runs them, whatever the profiles say. Laravel tables carry no prefix
// placeholder: DB_PREFIX is almost never set.
func laravelRules(Options) []core.Rule {
	return []core.Rule{
		{ID: Profile + ".jobs", Profile: Profile, Table: "jobs", Action: core.Truncate, Explain: "production's queued jobs dropped"},
		{ID: Profile + ".job_batches", Profile: Profile, Table: "job_batches", Action: core.Truncate, Explain: "production's job batches dropped"},
	}
}

// databaseOnlyStep is the one step of a target with no files: it changes
// nothing and reports side-effect.not-applicable (PRD 13.4).
func databaseOnlyStep() core.Step {
	id := core.FindingSideEffectNotApplicable
	return &step{id: id,
		plan: func(context.Context, core.Site) ([]core.PlannedEdit, error) {
			return []core.PlannedEdit{{Step: id, What: DatabaseOnlyLine}}, nil
		},
		check: func(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
			se := core.SideEffect{ID: id, Status: "unknown", How: DatabaseOnlyLine}
			return se, []core.Finding{{ID: id, Severity: core.SeverityInfo, Title: DatabaseOnlyLine, Count: 1}}, nil
		}}
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
