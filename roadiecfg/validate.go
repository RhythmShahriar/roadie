// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site"
)

// The values each enum setting takes, in the order help lists them.
var (
	stacks        = []string{"auto", "wordpress", "laravel", "sql"}
	phoneStyles   = []string{"echo", "country"}
	locales       = []string{"en", "de", "fr", "es", "nl", "it"}
	addressParts  = []string{"country", "state", "city", "postcode"}
	consistencies = []string{"per-run", "stable"}
	unparseables  = []string{"blank", "fail"}
	passwordModes = []string{"reset", "keep"}
	freeTexts     = []string{"scrub", "blank", "keep"}
	keyModes      = []string{"swap", "blank"}
	echoModes     = []string{"seal", "observe"}
	pauseKeep     = []string{"pause", "keep"}
	asModes       = []string{"pause", "cancel", "keep"}
	gatewayModes  = []string{"test-mode", "keep"}
	subsModes     = []string{"staging", "keep"}
	mailModes     = []string{"seal", "keep"}
	failOns       = []string{"none", "info", "low", "medium", "high", "critical"}
)

// profileID is the shape of a profile name in profiles:.
var profileID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// envName is the shape of a .env key in laravel.env.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validate checks every value Load decoded. Values that still hold an
// unset ${ROADIE_*} are left for the run that uses them.
func (l *loader) validate(c *Config) error {
	checks := []func(*Config) error{l.validateTop, l.validatePseudo, l.validateTables, l.validateKeys, l.validateSideEffects, l.validateVerify, l.validateLaravel}
	for _, check := range checks {
		if err := check(c); err != nil {
			return err
		}
	}
	return nil
}

// oneOf checks an enum value.
func (l *loader) oneOf(key, v string, options []string) error {
	if slices.Contains(options, v) || l.unset[key] != "" {
		return nil
	}
	list := humanList(options)
	if s := suggest(v, options); s != "" {
		return l.errAt(key, "%s is %q; did you mean %s? It takes %s.", key, v, s, list)
	}
	return l.errAt(key, "%s is %q; it takes %s.", key, v, list)
}

// humanList writes "a, b or c".
func humanList(options []string) string {
	if len(options) < 2 {
		return strings.Join(options, "")
	}
	return strings.Join(options[:len(options)-1], ", ") + " or " + options[len(options)-1]
}

// glob checks one path.Match pattern.
func (l *loader) glob(key, pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return l.errAt(key, "%s is empty; give a name or a glob such as wp_*_log", key)
	}
	if err := ValidGlob(pattern); err != nil {
		return l.errAt(key, "%s is %q, which isn't a valid glob (use *, ? and [abc] as in path.Match)", key, pattern)
	}
	return nil
}

// ValidGlob reports whether pattern is a valid path.Match pattern. Match
// stops at the first mismatch, so it is run against a name that walks
// the whole pattern.
func ValidGlob(pattern string) error {
	if _, err := path.Match(pattern, pattern); err != nil {
		return err
	}
	_, err := path.Match(pattern, "")
	return err
}

func (l *loader) validateTop(c *Config) error {
	for _, k := range []struct{ key, v string }{{"source", c.Source}, {"target", c.Target}} {
		if k.v == "" || l.unset[k.key] != "" {
			continue
		}
		if _, err := site.ParseEndpoint(k.v); err != nil && !errors.Is(err, core.ErrNotBuilt) {
			return l.errAt(k.key, "%s: %s", k.key, strings.TrimSuffix(err.Error(), "."))
		}
	}
	if c.URL != "" && l.unset["url"] == "" {
		if err := checkSiteURL(c.URL); err != nil {
			return l.errAt("url", "url is %q, which isn't a URL like https://staging.shop.test", c.URL)
		}
	}
	for i, p := range c.Production {
		key := fmt.Sprintf("production[%d]", i)
		if l.unset[key] != "" {
			continue
		}
		if err := CheckProduction(p); err != nil {
			return l.errAt(key, "%s is %q, which isn't a site like shop.com, *.shop.com or https://shop.com/shop/", key, p)
		}
	}
	for _, from := range sortedKeys(c.URLMap) {
		key := "url_map." + from
		if err := checkHostOrURL(from); err != nil {
			return l.errAt(key, "url_map has %q, which isn't a host like cdn.shop.com", from)
		}
		if to := c.URLMap[from]; l.unset[key] == "" {
			if err := checkHostOrURL(to); err != nil {
				return l.errAt(key, "%s is %q, which isn't a host like cdn.staging.shop.test", key, to)
			}
		}
	}
	if err := l.oneOf("stack", c.Stack, stacks); err != nil {
		return err
	}
	for i, p := range c.Profiles {
		key := fmt.Sprintf("profiles[%d]", i)
		if !profileID.MatchString(p) {
			return l.errAt(key, "%s is %q; profiles are names like woocommerce or gravity-forms (rhythm-roadie profiles lists them)", key, p)
		}
	}
	if wp := c.Remote.WP; wp == "" || strings.HasPrefix(wp, "-") || strings.ContainsAny(wp, "\n\r\x00") {
		return l.errAt("remote.wp", "remote.wp is %q; give the path of wp on the remote host, e.g. /usr/local/bin/wp", wp)
	}
	return nil
}

// checkSiteURL accepts an http or https URL with a host.
func checkSiteURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("not a URL")
	}
	return nil
}

// checkHostOrURL accepts a bare host (with an optional port and path) or
// an http or https URL.
func checkHostOrURL(s string) error {
	if strings.Contains(s, "://") {
		return checkSiteURL(s)
	}
	if s == "" || strings.ContainsAny(s, " \t\n@") {
		return errors.New("not a host")
	}
	u, err := url.Parse("//" + s)
	if err != nil || u.Hostname() == "" {
		return errors.New("not a host")
	}
	return nil
}

// CheckProduction checks one production: entry with Soundcheck's syntax:
// a host, *.host or an http or https URL with an optional path.
func CheckProduction(entry string) error {
	raw := strings.TrimSpace(entry)
	if raw == "" || strings.ContainsAny(raw, " \t\n") {
		return errors.New("not a site")
	}
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("not a site")
	}
	host := strings.TrimPrefix(u.Hostname(), "*.")
	if host == "" || strings.Contains(host, "*") {
		return errors.New("not a site")
	}
	return nil
}

func (l *loader) validatePseudo(c *Config) error {
	p := &c.Pseudonymise
	if l.unset["pseudonymise.email_domain"] == "" && !core.ReservedEmailDomain(p.EmailDomain) {
		return l.errAt("pseudonymise.email_domain", "pseudonymise.email_domain is %q, which can receive real mail. Use a reserved domain: example.invalid, example.com or a name under .test, .example, .invalid or .localhost.", p.EmailDomain)
	}
	for _, e := range []struct {
		key, v string
		opts   []string
	}{
		{"pseudonymise.phone_style", p.PhoneStyle, phoneStyles},
		{"pseudonymise.locale", p.Locale, locales},
		{"pseudonymise.consistency", p.Consistency, consistencies},
		{"pseudonymise.on_unparseable", p.OnUnparseable, unparseables},
		{"pseudonymise.passwords", p.Passwords, passwordModes},
		{"pseudonymise.free_text", p.FreeText, freeTexts},
	} {
		if err := l.oneOf(e.key, e.v, e.opts); err != nil {
			return err
		}
	}
	for i, k := range p.Keep {
		if err := l.oneOf(fmt.Sprintf("pseudonymise.keep[%d]", i), k, addressParts); err != nil {
			return err
		}
	}
	for i, g := range p.KeepUsers {
		if err := l.glob(fmt.Sprintf("pseudonymise.keep_users[%d]", i), g); err != nil {
			return err
		}
	}
	return nil
}

func (l *loader) validateTables(c *Config) error {
	for _, list := range []struct {
		key string
		v   []string
	}{{"drop", c.Drop}, {"truncate", c.Truncate}} {
		for i, g := range list.v {
			if err := l.glob(fmt.Sprintf("%s[%d]", list.key, i), g); err != nil {
				return err
			}
		}
	}
	for i, r := range c.Columns {
		key := fmt.Sprintf("columns[%d]", i)
		if err := l.glob(key+".table", r.Table); err != nil {
			return err
		}
		if strings.TrimSpace(r.Column) == "" {
			return l.errAt(key, "%s needs a column: the column the rule changes", key)
		}
		for _, wc := range sortedKeys(r.Where) {
			if err := l.glob(key+".where."+wc, r.Where[wc]); err != nil {
				return err
			}
		}
		if _, err := ParseRule(r.Rule); err != nil && l.unset[key+".rule"] == "" {
			return l.errAt(key+".rule", "%s.rule: %v", key, err)
		}
	}
	return nil
}

// testKeyProviders are the names test_keys takes: every provider with a
// key.<provider>-live ID, written with - or _.
func testKeyProviders() []string {
	var out []string
	for _, p := range core.KeyProviders() {
		out = append(out, p)
		if u := strings.ReplaceAll(p, "-", "_"); u != p {
			out = append(out, u)
		}
	}
	return out
}

func (l *loader) validateKeys(c *Config) error {
	if err := l.oneOf("keys.mode", c.Keys.Mode, keyModes); err != nil {
		return err
	}
	providers := testKeyProviders()
	for _, p := range sortedKeys(c.Keys.TestKeys) {
		key := "keys.test_keys." + p
		if !slices.Contains(providers, p) {
			hint := ""
			if s := suggest(p, providers); s != "" {
				hint = "; did you mean " + s + "?"
			}
			return l.errAt(key, "%s names %q, which isn't a provider Roadie swaps keys for%s", key, p, orDot(hint))
		}
		v := c.Keys.TestKeys[p]
		if v == "" || l.unset[key] != "" {
			continue
		}
		err := keys.ValidateTestKey(strings.ReplaceAll(p, "_", "-"), v)
		if err == nil || errors.Is(err, core.ErrNotBuilt) {
			continue
		}
		var ce *core.Error
		if errors.As(err, &ce) {
			out := *ce
			out.Msg = l.file + l.lineOf(key) + ": " + l.redact(key, ce.Msg)
			return &out
		}
		return l.errAt(key, "%s: %v", key, err)
	}
	return l.validateIgnores("keys.ignore", c.Keys.Ignore)
}

// validateIgnores checks keys.ignore or verify.ignore entries.
func (l *loader) validateIgnores(list string, entries []Ignore) error {
	for i, e := range entries {
		key := fmt.Sprintf("%s[%d]", list, i)
		switch {
		case e.ID == "":
			return l.errAt(key, "%s needs an id: the finding ID to hide, e.g. pii.phone", key)
		case !core.KnownID(e.ID):
			ids := append(core.FindingIDs(), knownKeyIDs()...)
			if s := suggest(e.ID, ids); s != "" {
				return l.errAt(key+".id", "%s.id is %q, which isn't a finding ID; did you mean %s? rhythm-roadie explain lists them.", key, e.ID, s)
			}
			return l.errAt(key+".id", "%s.id is %q, which isn't a finding ID; rhythm-roadie explain lists them.", key, e.ID)
		case strings.TrimSpace(e.Location) == "":
			return l.errAt(key, "%s needs a location: the table and row or file it hides, e.g. \"wp_options shop_phone\"", key)
		case strings.TrimSpace(e.Reason) == "":
			return l.errAt(key, "%s needs a reason: say why this value may stay", key)
		}
		if e.Until != "" {
			if _, err := time.Parse(time.DateOnly, e.Until); err != nil {
				return l.errAt(key+".until", "%s.until is %q; write a date like 2027-01-31", key, e.Until)
			}
		}
	}
	return nil
}

// knownKeyIDs lists every key.<provider>-live ID, for hints.
func knownKeyIDs() []string {
	var out []string
	for _, p := range core.KeyProviders() {
		out = append(out, core.KeyLiveID(p))
	}
	return out
}

func (l *loader) validateSideEffects(c *Config) error {
	s := &c.SideEffects
	if l.unset["side_effects.echo.url"] == "" {
		if err := ValidateEchoURL(s.Echo.URL); err != nil {
			return l.errAt("side_effects.echo.url", "side_effects.echo.url %v", err)
		}
	}
	if s.Echo.Proxy != "" && l.unset["side_effects.echo.proxy"] == "" {
		if err := ValidateEchoURL(s.Echo.Proxy); err != nil {
			return l.errAt("side_effects.echo.proxy", "side_effects.echo.proxy %v", err)
		}
	}
	for _, e := range []struct {
		key, v string
		opts   []string
	}{
		{"side_effects.echo.mode", s.Echo.Mode, echoModes},
		{"side_effects.cron", s.Cron, pauseKeep},
		{"side_effects.action_scheduler", s.ActionScheduler, asModes},
		{"side_effects.webhooks", s.Webhooks, pauseKeep},
		{"side_effects.gateways", s.Gateways, gatewayModes},
		{"side_effects.subscriptions", s.Subscriptions, subsModes},
		{"side_effects.mail", s.Mail, mailModes},
	} {
		if err := l.oneOf(e.key, e.v, e.opts); err != nil {
			return err
		}
	}
	return nil
}

// ValidateEchoURL checks an Echo or Echo proxy URL (security N6): Echo's
// token travels with every request, so plain http is allowed only to
// this machine or a private network. https may go anywhere. The error
// reads after the setting's name: "side_effects.echo.url <error>" or
// "--echo-url <error>".
func ValidateEchoURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("is %q, which isn't a URL like http://127.0.0.1:8025", stripPassword(raw))
	}
	if u.User != nil {
		return errors.New("holds a user name or password; Echo reads its token from RHYTHM_ECHO_TOKEN, never from the URL")
	}
	if u.Scheme == "https" || privateHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("is %s over plain http to a public host; use https or an address on this machine or a private network", stripPassword(u.Redacted()))
}

// privateHost reports whether host is loopback, private or link-local,
// or a name that only resolves on a local network: localhost, a single
// label (a Docker or LAN host) or a name under .local, .localhost,
// .internal, .lan or .home.arpa.
func privateHost(host string) bool {
	if a, err := netip.ParseAddr(host); err == nil {
		a = a.Unmap()
		return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

func (l *loader) validateVerify(c *Config) error {
	if _, err := cli.ParseFailOn(c.Verify.FailOn); err != nil || c.Verify.FailOn == "" {
		return l.oneOf("verify.fail_on", c.Verify.FailOn, failOns)
	}
	for i, e := range c.Verify.AllowEmails {
		key := fmt.Sprintf("verify.allow_emails[%d]", i)
		if !strings.Contains(e, "@") && l.unset[key] == "" {
			return l.errAt(key, "%s is %q, which isn't an email address or a glob like *@shop.com", key, e)
		}
	}
	for i, s := range c.Verify.Skip {
		if strings.TrimSpace(s) == "" {
			key := fmt.Sprintf("verify.skip[%d]", i)
			return l.errAt(key, "%s is empty", key)
		}
	}
	return l.validateIgnores("verify.ignore", c.Verify.Ignore)
}

func (l *loader) validateLaravel(c *Config) error {
	f := c.Laravel.EnvFile
	if f == "" || strings.HasPrefix(f, "/") || slices.Contains(strings.Split(f, "/"), "..") {
		return l.errAt("laravel.env_file", "laravel.env_file is %q; give a path inside the project, e.g. .env", f)
	}
	for _, t := range sortedKeys(c.Laravel.Tables) {
		for _, col := range sortedKeys(c.Laravel.Tables[t]) {
			key := "laravel.tables." + t + "." + col
			if _, err := ParseRule(c.Laravel.Tables[t][col]); err != nil {
				return l.errAt(key, "%s: %v", key, err)
			}
		}
	}
	for _, k := range sortedKeys(c.Laravel.Env) {
		if !envName.MatchString(k) {
			return l.errAt("laravel.env."+k, "laravel.env has %q, which isn't a .env name like QUEUE_CONNECTION", k)
		}
	}
	return nil
}
