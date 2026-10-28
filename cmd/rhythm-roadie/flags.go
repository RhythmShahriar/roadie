// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/verify"
)

// Defaults of PRD 6.4 that aren't zero values.
const (
	defaultEchoURL     = "http://127.0.0.1:8025"
	defaultEmailDomain = "example.invalid"
	// defaultMaxMemory is no --max-memory: the engine then sets a 300 MiB
	// soft limit (PRD 22.3's peak), unless GOMEMLIMIT set one.
	defaultMaxMemory      = 0
	defaultMaxValue       = 256 << 20
	defaultMaxOutputRatio = 20
	maxJobs               = 8
)

// globals are the Output and Settings flags of PRD 6.3, on every command.
type globals struct {
	json, md, html, sarif, junit bool
	outputs                      cli.RepeatFlag
	open, quiet, plain, noColor  bool
	verbose, debug               bool
	config                       string
	noConfig, offline            bool
}

// runFlags holds every flag of every command as typed. Each command
// registers only its own; build turns them into engine, verify and report
// settings once the config is known.
type runFlags struct {
	g globals

	// Where (copy, prepare, plan).
	from, to, url  string
	urlMap         cli.ListFlag
	stack          string
	sites          cli.ListFlag
	schema         string
	dryRun         bool
	only           cli.ListFlag
	productionURL  string
	noBackup       bool
	schemaOnly     bool
	maxOutputRatio int

	// Pseudonymise.
	keepUsers        cli.ListFlag
	keepKey          bool
	emailDomain      string
	adminPasswordEnv string
	allowUncovered   bool
	drop, truncate   cli.ListFlag
	onePass          bool
	keepRoutines     bool
	keepAppKey       bool

	// Keys and side effects.
	keys         string
	noKillSwitch bool
	echoURL      string
	echoProxy    string
	noindex      onOff
	basicAuth    string

	// Target safety.
	confirmTarget   string
	targetIsStaging string
	yes             bool
	fixCollation    bool
	jobs            int
	maxMemory       string
	maxValue        string

	// After the copy; verify's CI block too.
	noVerify   bool
	failOn     string
	soundcheck bool
	sign       onOff
	key        string

	// verify.
	production  cli.ListFlag
	allowEmails cli.ListFlag
	allowPhones cli.ListFlag
	siteURL     string
	insecureTLS string
	echo        echoFlag
	sample      int
	skip        cli.ListFlag
	baseline    string

	// config, report, demo.
	force     bool
	brandName string
	brandLogo string
	input     string
	keep      bool
}

// onOff is a flag that reads as a bool alone (--sign) and takes on or off
// as a value (--sign=off), as PRD 6.4 and 6.6 write them.
type onOff struct{ on bool }

func (o *onOff) String() string {
	if o == nil || o.on {
		return "on"
	}
	return "off"
}

// Set takes on, off and the values strconv.ParseBool knows.
func (o *onOff) Set(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "yes":
		o.on = true
		return nil
	case "off", "no":
		o.on = false
		return nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return errors.New("want on or off")
	}
	o.on = b
	return nil
}

// IsBoolFlag lets --sign stand alone.
func (o *onOff) IsBoolFlag() bool { return true }

// echoFlag is verify's --echo[=<url>]: alone it asks Echo at the default
// address, with a value at that one.
type echoFlag struct {
	on  bool
	url string
}

func (e *echoFlag) String() string {
	if e == nil || !e.on {
		return "false"
	}
	return e.url
}

// Set takes true, false or Echo's URL.
func (e *echoFlag) Set(s string) error {
	if b, err := strconv.ParseBool(s); err == nil {
		e.on, e.url = b, ""
		return nil
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return errors.New("want Echo's URL, e.g. --echo=http://127.0.0.1:8025")
	}
	e.on, e.url = true, s
	return nil
}

// IsBoolFlag lets --echo stand alone.
func (e *echoFlag) IsBoolFlag() bool { return true }

// newFlags returns the flags with their defaults: sign on, noindex on.
func newFlags() *runFlags {
	return &runFlags{sign: onOff{on: true}, noindex: onOff{on: true}}
}

// flagGroups are the groups of flags a command registers, by the help
// block they appear in.
type flagGroup int

const (
	groupCopyWhere flagGroup = iota
	groupPrepareWhere
	groupDryRun
	groupPseudonymise
	groupKeys
	groupSafety
	groupAfter
	groupPlan
	groupVerify
	groupConfig
	groupReport
	groupDemo
)

// commandGroups lists each command's flag groups; the globals come on top.
var commandGroups = map[string][]flagGroup{
	"copy":    {groupCopyWhere, groupDryRun, groupPseudonymise, groupKeys, groupSafety, groupAfter},
	"prepare": {groupPrepareWhere, groupDryRun, groupPseudonymise, groupKeys, groupSafety, groupAfter},
	// plan takes copy's and prepare's flags (PRD 6.7) and its own.
	"plan":     {groupCopyWhere, groupPrepareWhere, groupPseudonymise, groupKeys, groupSafety, groupAfter, groupPlan},
	"verify":   {groupVerify},
	"config":   {groupConfig},
	"report":   {groupReport},
	"profiles": nil,
	"explain":  nil,
	"demo":     {groupDemo},
}

// flagSet returns a fresh flag set for cmd with its flags bound to f.
// Errors are reported by parse, so the set itself stays silent.
func (f *runFlags) flagSet(cmd string) *flag.FlagSet {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	seen := map[string]bool{}
	str := func(p *string, name, usage string) {
		if !seen[name] {
			seen[name] = true
			fs.StringVar(p, name, *p, usage)
		}
	}
	boolean := func(p *bool, name, usage string) {
		if !seen[name] {
			seen[name] = true
			fs.BoolVar(p, name, *p, usage)
		}
	}
	value := func(v flag.Value, name, usage string) {
		if !seen[name] {
			seen[name] = true
			fs.Var(v, name, usage)
		}
	}
	integer := func(p *int, name, usage string) {
		if !seen[name] {
			seen[name] = true
			fs.IntVar(p, name, *p, usage)
		}
	}

	g := &f.g
	boolean(&g.json, "json", "JSON")
	boolean(&g.md, "md", "Markdown")
	boolean(&g.html, "html", "HTML report")
	boolean(&g.sarif, "sarif", "SARIF 2.1.0")
	boolean(&g.junit, "junit", "JUnit XML")
	value(&g.outputs, "o", "write the report to a file")
	value(&g.outputs, "output", "write the report to a file")
	boolean(&g.open, "open", "open the HTML report")
	boolean(&g.quiet, "q", "results only")
	boolean(&g.quiet, "quiet", "results only")
	boolean(&g.plain, "plain", "no color, boxes or glyphs")
	boolean(&g.noColor, "no-color", "no color")
	boolean(&g.verbose, "v", "every table, rule and file edit")
	boolean(&g.verbose, "verbose", "every table, rule and file edit")
	boolean(&g.debug, "debug", "timings and counts")
	str(&g.config, "config", "settings file")
	boolean(&g.noConfig, "no-config", "don't read .roadie/config.yaml")
	boolean(&g.offline, "offline", "built-in profiles only")

	for _, grp := range commandGroups[cmd] {
		switch grp {
		case groupCopyWhere:
			str(&f.from, "from", "source")
			str(&f.to, "to", "target")
			str(&f.url, "url", "the staging site's URL")
			value(&f.urlMap, "url-map", "extra URL pairs")
			str(&f.stack, "stack", "wordpress, laravel or sql")
			value(&f.sites, "site", "multisite blog IDs")
			str(&f.schema, "schema", "CREATE TABLE statements")
		case groupPrepareWhere:
			str(&f.url, "url", "the staging site's URL")
			value(&f.urlMap, "url-map", "extra URL pairs")
			str(&f.stack, "stack", "wordpress, laravel or sql")
			value(&f.sites, "site", "multisite blog IDs")
			value(&f.only, "only", "parts to prepare")
			str(&f.productionURL, "production-url", "the live site's URL")
			boolean(&f.noBackup, "no-backup", "don't back up first")
		case groupDryRun:
			boolean(&f.dryRun, "dry-run", "run plan instead")
		case groupPseudonymise:
			value(&f.keepUsers, "keep-users", "users kept as they are")
			boolean(&f.keepKey, "keep-key", "keep the consistency key")
			str(&f.emailDomain, "email-domain", "domain for fake emails")
			str(&f.adminPasswordEnv, "admin-password-env", "ROADIE_ variable with the kept users' password")
			boolean(&f.allowUncovered, "allow-uncovered", "copy despite uncovered columns")
			value(&f.drop, "drop", "tables left out")
			value(&f.truncate, "truncate", "tables copied without rows")
			boolean(&f.onePass, "one-pass", "skip the name index pass")
			boolean(&f.keepRoutines, "keep-routines", "keep triggers, procedures and views")
			boolean(&f.keepAppKey, "keep-app-key", "keep Laravel's APP_KEY")
		case groupKeys:
			str(&f.keys, "keys", "swap or blank")
			boolean(&f.noKillSwitch, "no-kill-switch", "copy the data only")
			str(&f.echoURL, "echo-url", "where the Echo connector reports")
			str(&f.echoProxy, "echo-proxy", "Echo's proxy address")
			value(&f.noindex, "noindex", "noindex the copy")
			str(&f.basicAuth, "basic-auth", "basic-auth user")
		case groupSafety:
			value(&f.production, "production", "production hosts")
			str(&f.confirmTarget, "confirm-target", "the target's host, typed out")
			str(&f.targetIsStaging, "target-is-staging", "the target's host when it says production")
			boolean(&f.yes, "y", "don't ask")
			boolean(&f.yes, "yes", "don't ask")
			boolean(&f.fixCollation, "fix-collation", "rewrite MySQL 8 collations")
			integer(&f.jobs, "jobs", "transform workers")
			str(&f.maxMemory, "max-memory", "soft memory limit")
			str(&f.maxValue, "max-value", "largest single value")
			integer(&f.maxOutputRatio, "max-output-ratio", "largest growth of a compressed dump")
		case groupAfter:
			boolean(&f.noVerify, "no-verify", "skip the verify")
			str(&f.failOn, "fail-on", "exit 1 at or above this level")
			boolean(&f.soundcheck, "soundcheck", "also run rhythm-soundcheck")
			value(&f.sign, "sign", "sign the JSON report")
			str(&f.key, "key", "signing key file")
		case groupPlan:
			boolean(&f.schemaOnly, "schema-only", "read only the schema")
			str(&f.failOn, "fail-on", "exit 1 at or above this level")
		case groupVerify:
			value(&f.production, "production", "production hosts")
			str(&f.from, "from", "production's dump or database")
			value(&f.allowEmails, "allow-emails", "emails that may stay real")
			value(&f.allowPhones, "allow-phones", "phones that may stay real")
			str(&f.siteURL, "site", "the staging site's URL")
			str(&f.insecureTLS, "insecure-tls", "a staging host with a self-signed certificate")
			value(&f.echo, "echo", "ask Echo")
			integer(&f.sample, "sample", "rows per table")
			str(&f.maxValue, "max-value", "largest single value")
			integer(&f.maxOutputRatio, "max-output-ratio", "largest growth of a compressed dump")
			value(&f.skip, "skip", "checks left out")
			str(&f.failOn, "fail-on", "exit 1 at or above this level")
			str(&f.baseline, "baseline", "accepted findings")
			value(&f.sign, "sign", "sign the JSON report")
			str(&f.key, "key", "signing key file")
		case groupConfig:
			str(&f.stack, "stack", "stack for init")
			str(&f.from, "from", "source to read for init")
			boolean(&f.force, "force", "replace the file")
		case groupReport:
			str(&f.brandName, "brand-name", "name on the HTML report")
			str(&f.brandLogo, "brand-logo", "logo file")
			str(&f.key, "key", "public key to pin")
			str(&f.input, "input", "dump the report describes")
		case groupDemo:
			boolean(&f.keep, "keep", "keep the demo's files")
		}
	}
	return fs
}

// parsed is a command line after parsing: the flags, which of them were
// given and the positional arguments.
type parsed struct {
	cmd  string
	f    *runFlags
	set  map[string]bool
	args []string
}

// given reports whether any of names was on the command line.
func (p *parsed) given(names ...string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return p.set[n] })
}

// parse parses argv for cmd. --help prints the command's help to stdout
// and returns cli.ErrHelp. Flag errors are printed to stderr with a "did
// you mean" for a mistyped name and come back as a reported UsageError.
func parse(cmd string, argv []string, stdout, stderr io.Writer) (*parsed, error) {
	if helpAsked(argv) {
		fmt.Fprint(stdout, helpFor(cmd))
		return nil, cli.ErrHelp
	}
	f := newFlags()
	fs := f.flagSet(cmd)
	args, err := cli.ParseInterspersed(fs, argv)
	if err != nil {
		return nil, flagError(stderr, cmd, fs, err)
	}
	p := &parsed{cmd: cmd, f: f, set: map[string]bool{}, args: args}
	fs.Visit(func(fl *flag.Flag) { p.set[fl.Name] = true })
	if err := p.check(stderr); err != nil {
		return nil, err
	}
	return p, nil
}

// helpAsked reports whether argv holds -h or --help before any "--".
func helpAsked(argv []string) bool { return cli.HasFlag(argv, "h") || cli.HasFlag(argv, "help") }

// flagError rewrites the flag package's error in the double-dash form
// users type, adds a suggestion for an unknown flag and prints it.
func flagError(stderr io.Writer, cmd string, fs *flag.FlagSet, err error) error {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		var names []string
		fs.VisitAll(func(fl *flag.Flag) { names = append(names, fl.Name) })
		msg = "unknown flag " + dashed(name)
		if s := cli.Suggest(name, names); s != "" {
			msg += "\n  did you mean: " + prog() + " " + cmd + " " + dashed(s)
		}
		return usageError(stderr, cmd, "%s", msg)
	}
	// "invalid value "x" for flag -jobs: ..." and "flag needs an argument: -from".
	for _, f := range []string{"flag -", "argument: -"} {
		if i := strings.Index(msg, f); i >= 0 {
			rest := msg[i+len(f):]
			name, tail, _ := strings.Cut(rest, ":")
			msg = msg[:i+len(f)-1] + dashed(name) + strings.TrimPrefix(":"+tail, ":")
			if tail == "" {
				msg = msg[:i+len(f)-1] + dashed(name)
			}
			break
		}
	}
	return usageError(stderr, cmd, "%s", msg)
}

// dashed is a flag name as users type it: -o for one letter, --name otherwise.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// prog is the command name for messages: "rhythm-roadie", or what the
// router says the user typed ("rhythm roadie").
func prog() string {
	if n := cli.Name(); n != "" && n != "rhythm-roadie.test" && !strings.HasSuffix(n, ".test") {
		return n
	}
	return "rhythm-roadie"
}

// usageError prints "rhythm-roadie <cmd>: <msg>" and the help hint to
// stderr and returns a reported UsageError (exit 2).
func usageError(stderr io.Writer, cmd, format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(stderr, "%s %s: %s\n  run '%s help %s' for usage\n", prog(), cmd, msg, prog(), cmd)
	return cli.UsageError{Msg: msg, Reported: true}
}

// check validates what can be checked without the config.
func (p *parsed) check(stderr io.Writer) error {
	f, g := p.f, &p.f.g
	if p.cmd != "verify" {
		for _, name := range []string{"sarif", "junit"} {
			if p.set[name] {
				return usageError(stderr, p.cmd, "--%s is for verify only", name)
			}
		}
	}
	formats := 0
	for _, on := range []bool{g.json, g.md, g.html, g.sarif, g.junit} {
		if on {
			formats++
		}
	}
	if formats > 1 {
		return usageError(stderr, p.cmd, "pick one of --json, --md, --html, --sarif and --junit for stdout; write the others with -o")
	}
	for _, o := range g.outputs {
		format, ok := formatOf(o)
		switch {
		case !ok:
			return usageError(stderr, p.cmd, "-o %s: the extension picks the format; use .json, .md, .html, .sarif or .xml", o)
		case (format == "sarif" || format == "junit") && p.cmd != "verify" && p.cmd != "report":
			return usageError(stderr, p.cmd, "-o %s: %s is for verify only", o, strings.ToUpper(format))
		}
	}
	if g.noConfig && p.set["config"] {
		return usageError(stderr, p.cmd, "--config and --no-config can't go together")
	}
	if p.set["fail-on"] {
		if _, err := cli.ParseFailOn(f.failOn); err != nil {
			return usageError(stderr, p.cmd, "%s", err.Error())
		}
	}
	if p.set["stack"] {
		allowed := []string{"wordpress", "laravel", "sql"}
		if p.cmd == "config" {
			allowed = []string{"wordpress", "woocommerce", "laravel", "sql"}
		}
		if !slices.Contains(allowed, f.stack) {
			return usageError(stderr, p.cmd, "--stack %q: want %s", f.stack, orList(allowed))
		}
	}
	if p.set["keys"] && f.keys != "swap" && f.keys != "blank" {
		return usageError(stderr, p.cmd, "--keys %q: want swap or blank", f.keys)
	}
	if p.set["admin-password-env"] && !strings.HasPrefix(f.adminPasswordEnv, "ROADIE_") {
		return usageError(stderr, p.cmd, "--admin-password-env %s: only ROADIE_ variables are read; rename it ROADIE_%s", f.adminPasswordEnv, f.adminPasswordEnv)
	}
	if p.set["jobs"] && (f.jobs < 1 || f.jobs > maxJobs) {
		return usageError(stderr, p.cmd, "--jobs %d: want 1 to %d", f.jobs, maxJobs)
	}
	if p.set["max-output-ratio"] && f.maxOutputRatio < 1 {
		return usageError(stderr, p.cmd, "--max-output-ratio %d: want 1 or more", f.maxOutputRatio)
	}
	if p.set["sample"] && f.sample < 0 {
		return usageError(stderr, p.cmd, "--sample %d: want 0 (every row) or more", f.sample)
	}
	for _, name := range []string{"max-memory", "max-value"} {
		v := map[string]string{"max-memory": f.maxMemory, "max-value": f.maxValue}[name]
		if p.set[name] {
			if _, err := parseSize(v); err != nil {
				return usageError(stderr, p.cmd, "--%s %q: %v", name, v, err)
			}
		}
	}
	if _, err := parseURLMap(f.urlMap); err != nil {
		return usageError(stderr, p.cmd, "--url-map: %v", err)
	}
	if p.cmd != "verify" {
		if _, err := parseSites(f.sites); err != nil {
			return usageError(stderr, p.cmd, "--site: %v", err)
		}
	}
	for _, part := range f.only {
		if !slices.Contains(onlyParts, part) {
			return usageError(stderr, p.cmd, "--only %q: want %s", part, orList(onlyParts))
		}
	}
	for _, c := range f.skip {
		if !slices.Contains(skipChecks, c) {
			msg := fmt.Sprintf("--skip %q: want %s", c, orList(skipChecks))
			if s := cli.Suggest(c, skipChecks); s != "" {
				msg += "\n  did you mean: --skip " + s
			}
			return usageError(stderr, p.cmd, "%s", msg)
		}
	}
	return nil
}

// onlyParts are prepare --only's parts (PRD 6.5).
var onlyParts = []string{"data", "keys", "side-effects", "urls", "noindex"}

// skipChecks are verify --skip's checks (PRD 6.6).
var skipChecks = []string{"emails", "phones", "names", "addresses", "ips", "cards", "keys", "urls", "side-effects", "config", "uploads"}

// orList writes "a, b or c".
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// formatOf returns the report format an -o file's extension names.
func formatOf(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json", true
	case ".md":
		return "md", true
	case ".html", ".htm":
		return "html", true
	case ".sarif":
		return "sarif", true
	case ".xml":
		return "junit", true
	}
	return "", false
}

// parseSize reads 1GiB, 256MiB, 512KiB, 2GB, 10MB or a byte count.
func parseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   int64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
		{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"B", 1},
	}
	mult := int64(1)
	for _, u := range units {
		if n, ok := strings.CutSuffix(t, u.suffix); ok {
			t, mult = strings.TrimSpace(n), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("want a size such as 1GiB, 256MiB or 512KiB")
	}
	if n > (1<<62)/mult {
		return 0, errors.New("too large")
	}
	return n * mult, nil
}

// parseURLMap reads --url-map's a=b pairs.
func parseURLMap(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, p := range pairs {
		a, b, ok := strings.Cut(p, "=")
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		switch {
		case !ok || a == "" || b == "":
			return nil, fmt.Errorf("%q isn't production=staging, e.g. fr.shop.com=fr.staging.shop.test", p)
		case m[a] != "" && m[a] != b:
			return nil, fmt.Errorf("%s is mapped twice", a)
		}
		m[a] = b
	}
	return m, nil
}

// parseSites reads --site's blog IDs.
func parseSites(ids []string) ([]int, error) {
	var out []int
	for _, s := range ids {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%q isn't a blog ID (1, 2, ...)", s)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// envFunc reads the environment, as roadiecfg and report take it.
type envFunc func(string) (string, bool)

// defaultJobs is the number of CPUs, at most 8.
func defaultJobs() int { return min(runtime.NumCPU(), maxJobs) }

// engineOptions turns the flags over the config into engine.Options:
// a flag wins, then the config, then the default. mode picks plan's
// --fail-on default (none) over copy's and prepare's (high).
func (p *parsed) engineOptions(cfg *roadiecfg.Config, mode engine.Mode, env envFunc) (engine.Options, error) {
	f := p.f
	o := engine.Options{
		Version:          version,
		URL:              f.url,
		Stack:            f.stack,
		SchemaFile:       f.schema,
		KeepUsers:        []string(f.keepUsers),
		KeepKey:          f.keepKey,
		EmailDomain:      f.emailDomain,
		AdminPasswordEnv: f.adminPasswordEnv,
		AllowUncovered:   f.allowUncovered,
		Drop:             []string(f.drop),
		Truncate:         []string(f.truncate),
		OnePass:          f.onePass,
		KeepRoutines:     f.keepRoutines,
		KeepAppKey:       f.keepAppKey,
		Keys:             f.keys,
		NoKillSwitch:     f.noKillSwitch,
		EchoURL:          f.echoURL,
		EchoProxy:        f.echoProxy,
		NoIndex:          !f.noindex.on,
		BasicAuth:        f.basicAuth,
		ConfirmTarget:    f.confirmTarget,
		TargetIsStaging:  f.targetIsStaging,
		Yes:              f.yes,
		FixCollation:     f.fixCollation,
		Jobs:             f.jobs,
		MaxMemory:        defaultMaxMemory,
		MaxValue:         defaultMaxValue,
		MaxOutputRatio:   f.maxOutputRatio,
		NoVerify:         f.noVerify,
		Soundcheck:       f.soundcheck,
		Only:             []string(f.only),
		ProductionURL:    f.productionURL,
		NoBackup:         f.noBackup,
		SchemaOnly:       f.schemaOnly,
		Offline:          p.offline(env),
	}
	o.URLMap, _ = parseURLMap(f.urlMap) // checked by parse
	o.Sites, _ = parseSites(f.sites)
	if p.set["max-memory"] {
		o.MaxMemory, _ = parseSize(f.maxMemory)
	}
	if p.set["max-value"] {
		o.MaxValue, _ = parseSize(f.maxValue)
	}

	if cfg != nil {
		if o.URL == "" {
			o.URL = cfg.URL
		}
		if len(cfg.URLMap) > 0 {
			m := map[string]string{}
			for k, v := range cfg.URLMap {
				m[k] = v
			}
			for k, v := range o.URLMap { // a flag wins pair by pair
				m[k] = v
			}
			o.URLMap = m
		}
		if o.Stack == "" && cfg.Stack != "auto" {
			o.Stack = cfg.Stack
		}
		if !p.given("keep-users") {
			o.KeepUsers = cfg.Pseudonymise.KeepUsers
		}
		if !o.KeepKey && cfg.Pseudonymise.Consistency == "stable" {
			o.KeepKey = true
		}
		if o.EmailDomain == "" {
			o.EmailDomain = cfg.Pseudonymise.EmailDomain
		}
		if !p.given("drop") {
			o.Drop = cfg.Drop
		}
		if !p.given("truncate") {
			o.Truncate = cfg.Truncate
		}
		if o.Keys == "" {
			o.Keys = cfg.Keys.Mode
		}
		// RHYTHM_ECHO_URL stands in for the default URL, not for one the
		// config names.
		if o.EchoURL == "" && cfg.SideEffects.Echo.URL != defaultEchoURL {
			o.EchoURL = cfg.SideEffects.Echo.URL
		}
		if o.EchoProxy == "" {
			o.EchoProxy = cfg.SideEffects.Echo.Proxy
		}
		if !p.given("noindex") && !cfg.Noindex {
			o.NoIndex = true
		}
		if o.BasicAuth == "" && cfg.BasicAuth {
			o.BasicAuth = "staging"
		}
		o.Production = slices.Clone(cfg.Production)
		if o.ProductionURL == "" && mode == engine.Prepare && len(cfg.Production) == 1 && strings.Contains(cfg.Production[0], "://") {
			o.ProductionURL = cfg.Production[0]
		}
	}
	// --production adds to the config's list, as on verify: a CI job can
	// name production without a config file.
	if len(f.production) > 0 {
		o.Production = dedupe(append(o.Production, f.production...))
	}

	if o.EmailDomain == "" {
		o.EmailDomain = defaultEmailDomain
	}
	if o.Keys == "" {
		o.Keys = "swap"
	}
	urlFrom := "--echo-url"
	if f.echoURL == "" {
		urlFrom = "side_effects.echo.url"
	}
	if o.EchoURL == "" {
		if v, ok := env("RHYTHM_ECHO_URL"); ok && v != "" {
			o.EchoURL, urlFrom = v, "RHYTHM_ECHO_URL"
		} else {
			o.EchoURL = defaultEchoURL
		}
	}
	// The connector sends captured mail to these URLs: plain http only
	// on this machine or a private network (security N6), whichever of
	// the flag, the variable or the config named them.
	if err := roadiecfg.ValidateEchoURL(o.EchoURL); err != nil {
		return o, core.Errorf(core.ErrConfig, cli.ExitUsage, "%s %v", urlFrom, err)
	}
	if o.EchoProxy != "" {
		proxyFrom := "--echo-proxy"
		if f.echoProxy == "" {
			proxyFrom = "side_effects.echo.proxy"
		}
		// cURL takes a proxy as host:port too (the help's example).
		proxy := o.EchoProxy
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		if err := roadiecfg.ValidateEchoURL(proxy); err != nil {
			return o, core.Errorf(core.ErrConfig, cli.ExitUsage, "%s %v", proxyFrom, err)
		}
	}
	if o.Jobs == 0 {
		o.Jobs = defaultJobs()
	}
	if o.MaxOutputRatio == 0 {
		o.MaxOutputRatio = defaultMaxOutputRatio
	}
	sev, err := p.failOnLevel(cfg, mode == engine.Plan)
	if err != nil {
		return o, err
	}
	o.FailOn = sev
	return o, nil
}

// failOnLevel is --fail-on, else verify.fail_on in the config, else high;
// plan's default is none and only its own flag changes it.
func (p *parsed) failOnLevel(cfg *roadiecfg.Config, plan bool) (core.Severity, error) {
	if p.set["fail-on"] {
		return cli.ParseFailOn(p.f.failOn)
	}
	if plan {
		return core.SeverityNone, nil
	}
	if cfg != nil && cfg.Verify.FailOn != "" {
		sev, err := cli.ParseFailOn(cfg.Verify.FailOn)
		if err != nil {
			return 0, core.Errorf(core.ErrConfig, cli.ExitUsage, ".roadie/config.yaml: verify.fail_on: %v", err)
		}
		return sev, nil
	}
	return core.SeverityHigh, nil
}

// offline is --offline or RHYTHM_OFFLINE set to anything but "" or "0".
func (p *parsed) offline(env envFunc) bool {
	if p.f.g.offline {
		return true
	}
	v, _ := env(cli.OfflineEnv)
	return v != "" && v != "0"
}

// signing is whether to sign and with which key file: --sign=off and
// verify.sign: false turn it off.
func (p *parsed) signing(cfg *roadiecfg.Config) (sign bool, keyFile string) {
	sign = p.f.sign.on
	if !p.given("sign") && cfg != nil && !cfg.Verify.Sign {
		sign = false
	}
	return sign, p.f.key
}

// verifyOptions turns verify's flags over the config into verify.Options.
func (p *parsed) verifyOptions(cfg *roadiecfg.Config, env envFunc, now time.Time) (verify.Options, []string, error) {
	f := p.f
	o := verify.Options{
		Sample:      f.sample,
		Skip:        []string(f.skip),
		SiteURL:     f.siteURL,
		InsecureTLS: f.insecureTLS,
		Echo:        f.echo.on,
		EchoURL:     f.echo.url,
		Baseline:    f.baseline,
		Jobs:        defaultJobs(),
		Allow:       detect.Allow{Emails: []string(f.allowEmails), Phones: []string(f.allowPhones)},
	}
	production := []string(f.production)
	if cfg != nil {
		production = append(slices.Clone(cfg.Production), production...)
		o.Allow.Emails = append(slices.Clone(cfg.Verify.AllowEmails), o.Allow.Emails...)
		o.Allow.Phones = append(slices.Clone(cfg.Verify.AllowPhones), o.Allow.Phones...)
		o.Allow.KeptEmails = slices.Clone(cfg.Pseudonymise.KeepUsers)
		if !p.given("skip") {
			o.Skip = slices.Clone(cfg.Verify.Skip)
		}
		if o.EchoURL == "" {
			o.EchoURL = cfg.SideEffects.Echo.URL
		}
		ign, err := ignores(cfg)
		if err != nil {
			return o, nil, err
		}
		o.Ignore = ign
	}
	if o.EchoURL == "" {
		o.EchoURL = defaultEchoURL
	}
	// Echo's own tools read its token from the same variable.
	o.EchoToken, _ = env("RHYTHM_ECHO_TOKEN")
	sev, err := p.failOnLevel(cfg, false)
	if err != nil {
		return o, nil, err
	}
	o.FailOn = sev
	o.Clock = func() time.Time { return now }
	return o, dedupe(production), nil
}

// ignores turns keys.ignore and verify.ignore into verify.Ignore, with
// until read as that date at 00:00 UTC.
func ignores(cfg *roadiecfg.Config) ([]verify.Ignore, error) {
	var out []verify.Ignore
	for _, list := range [][]roadiecfg.Ignore{cfg.Keys.Ignore, cfg.Verify.Ignore} {
		for _, i := range list {
			v := verify.Ignore{ID: i.ID, Location: i.Location, Reason: i.Reason}
			if i.Until != "" {
				t, err := time.ParseInLocation("2006-01-02", i.Until, time.UTC)
				if err != nil {
					return nil, core.Errorf(core.ErrConfig, cli.ExitUsage,
						".roadie/config.yaml: ignore entry %s until %q isn't a date; write it as 2027-01-31", i.ID, i.Until)
				}
				v.Until = t
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// dedupe drops repeated entries, keeping the first.
func dedupe(list []string) []string {
	var out []string
	for _, s := range list {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// osEnv reads the process environment.
func osEnv(name string) (string, bool) { return os.LookupEnv(name) }
