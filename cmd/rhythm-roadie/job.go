// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/report"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/verify"
)

// deps are the functions of other lanes the commands call plus the
// process's clock, environment and terminal. Tests replace them, so the
// views and exit codes are tested with canned results whatever state the
// other lanes are in.
type deps struct {
	parseEndpoint func(string) (core.Endpoint, error)
	findConfig    func(dir string) (string, error)
	loadConfig    func(path string, env func(string) (string, bool)) (*roadiecfg.Config, error)
	defaultConfig func() *roadiecfg.Config
	run           func(context.Context, engine.Job) (*core.Result, error)
	verifyJob     func(context.Context, engine.Job, core.Endpoint, *core.Endpoint) (verify.Job, error)
	verify        func(context.Context, verify.Job) (*core.VerifyResult, error)
	loadSigner    func(keyFile string, env func(string) (string, bool)) (signer, bool, error)
	envelope      func(r *core.Result, tool, version string) (*core.Envelope, error)
	markdown      func(*core.Envelope) ([]byte, error)
	html          func([]*core.Envelope, report.Brand) ([]byte, error)
	sarif         func(*core.Envelope) ([]byte, error)
	junit         func(*core.Envelope) ([]byte, error)
	save          func(dir string, e *core.Envelope, html []byte) (string, error)
	open          func(path string) error

	initTemplate   func(stack string) []byte
	excludeFromGit func(dir string, paths []string) (bool, error)
	loadKey        func(dir string, env func(string) (string, bool)) (pseudo.Key, string, error)
	saveKey        func(dir string, k pseudo.Key) error
	forgetKey      func(dir string) error
	newKey         func() (pseudo.Key, error)
	loadProfiles   func(ctx context.Context, offline bool) ([]profile.Def, string, error)
	demoShop       func(w io.Writer, o demo.Options) error
	demoProduction func() []string
	now            func() time.Time
	env            envFunc
	stdin          io.Reader
	// terminal reports whether stdin and stdout are both terminals: the
	// picker, the questions and Confirm need one.
	terminal func() bool
	// stdoutTerminal reports whether stdout alone is a terminal: the
	// live view, color and the progress line need one.
	stdoutTerminal func() bool
	// stderrTerminal reports whether stderr is a terminal: the release
	// notice is only for a person reading one.
	stderrTerminal func() bool
}

// dep is what the commands use; tests swap it.
var dep = realDeps()

func realDeps() deps {
	return deps{
		parseEndpoint:  site.ParseEndpoint,
		findConfig:     roadiecfg.Find,
		loadConfig:     roadiecfg.Load,
		defaultConfig:  roadiecfg.Default,
		run:            engine.Run,
		verifyJob:      engine.VerifyJob,
		verify:         verify.Run,
		loadSigner:     realLoadSigner,
		envelope:       report.Envelope,
		markdown:       report.Markdown,
		html:           report.HTML,
		sarif:          report.SARIF,
		junit:          report.JUnit,
		save:           report.Save,
		open:           openFile,
		initTemplate:   roadiecfg.InitTemplate,
		excludeFromGit: roadiecfg.ExcludeFromGit,
		loadKey:        roadiecfg.LoadKey,
		saveKey:        roadiecfg.SaveKey,
		forgetKey:      roadiecfg.ForgetKey,
		newKey:         pseudo.NewKey,
		loadProfiles:   profile.Load,
		demoShop:       demo.Shop,
		demoProduction: demo.Production,
		now:            time.Now,
		env:            osEnv,
		stdin:          os.Stdin,
		terminal:       func() bool { return cli.IsTerminal(os.Stdout) && isTerminalReader(os.Stdin) },
		stdoutTerminal: func() bool { return cli.IsTerminal(os.Stdout) },
		stderrTerminal: func() bool { return cli.IsTerminal(os.Stderr) },
	}
}

// isTerminalReader reports whether r is a terminal.
func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && cli.IsTerminal(f)
}

// loadConfig reads the settings file the flags name: none with
// --no-config, the --config file, else the nearest .roadie/config.yaml.
// A config that doesn't exist gives the defaults; the path is "" then.
// The result is nil only when there is no file and no defaults either.
func (p *parsed) loadConfig() (*roadiecfg.Config, string, error) {
	if p.f.g.noConfig {
		return dep.defaultConfig(), "", nil
	}
	path := p.f.g.config
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		found, err := dep.findConfig(wd)
		switch {
		case errors.Is(err, fs.ErrNotExist), err == nil && found == "":
			// Without a .roadie/config.yaml, the production: lists of
			// .soundcheck/ and .echo/ in this folder still count (PRD 8.2).
			cfg := dep.defaultConfig()
			if cfg != nil {
				if err := cfg.MergeProduction(wd); err != nil {
					return nil, "", err
				}
			}
			return cfg, "", nil
		case err != nil:
			return nil, "", err
		}
		path = found
	}
	cfg, err := dep.loadConfig(path, dep.env)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

// endpoints are the raw --from and --to of a run, after the config.
type endpoints struct{ from, to string }

// runEndpoints picks the source and target strings: copy and a plan with
// --from read --from and --to (or source: and target:); prepare and a
// plan of <staging> use the staging for both. A plan needs no target.
func (p *parsed) runEndpoints(mode engine.Mode, cfg *roadiecfg.Config, stderr io.Writer) (endpoints, bool, error) {
	var src, tgt string
	if cfg != nil {
		src, tgt = cfg.Source, cfg.Target
	}
	// prepare, prepare --dry-run and plan <staging> name one site; a plan
	// with no source at all but a target in the config plans a prepare.
	staging := p.cmd == "prepare" || (p.cmd == "plan" && (len(p.args) > 0 || (p.f.from == "" && src == "" && tgt != "")))
	if staging {
		if p.given("from", "to") && p.cmd == "plan" {
			return endpoints{}, false, usageError(stderr, p.cmd, "give either <staging> or --from and --to, not both")
		}
		s := tgt
		switch len(p.args) {
		case 0:
		case 1:
			s = p.args[0]
		default:
			return endpoints{}, false, usageError(stderr, p.cmd, "one staging at a time; got %d", len(p.args))
		}
		if s == "" {
			return endpoints{}, false, usageError(stderr, p.cmd,
				"%s needs the staging copy: a site path, ssh://..., @alias or DSN (or target: in .roadie/config.yaml; rhythm-roadie config init writes one)", p.cmd)
		}
		return endpoints{from: s, to: s}, true, nil
	}
	if len(p.args) > 0 {
		return endpoints{}, false, usageError(stderr, p.cmd, "unexpected argument %q; name the source with --from and the target with --to", p.args[0])
	}
	if p.f.from != "" {
		src = p.f.from
	}
	if p.f.to != "" {
		tgt = p.f.to
	}
	switch {
	case p.cmd == "plan" && src == "":
		return endpoints{}, false, usageError(stderr, p.cmd,
			"plan needs --from <source> or a <staging> (or source: in .roadie/config.yaml; rhythm-roadie config init writes one)")
	case src == "" || (tgt == "" && mode == engine.Copy):
		return endpoints{}, false, usageError(stderr, p.cmd,
			"%s needs --from and --to (or source: and target: in .roadie/config.yaml; rhythm-roadie config init writes one)", p.cmd)
	}
	return endpoints{from: src, to: tgt}, false, nil
}

// parseEndpoint parses one endpoint and fills its password from the
// environment. An inline DSN password is accepted with the PRD 6.2
// warning, printed once per run by warn.
func parseEndpoint(raw, flagName, envName string, warn func(string)) (core.Endpoint, error) {
	e, err := dep.parseEndpoint(raw)
	if err != nil {
		return e, scrubPassword(err, raw)
	}
	if e.Kind == core.DSN {
		if e.Password != "" {
			warn(fmt.Sprintf("the password in %s is visible to other users of this machine; use %s", flagName, envName))
		} else if v, ok := dep.env(envName); ok {
			e.Password = v
		}
	}
	return e, nil
}

// scrubPassword takes an inline DSN password out of err's message, raw
// or URL-decoded, in case the parser quoted the endpoint back: the
// message reaches the terminal and CI logs. A *core.Error keeps its code
// and exit.
func scrubPassword(err error, raw string) error {
	pw := inlinePassword(raw)
	if pw == "" {
		return err
	}
	msg := err.Error()
	stripped, _ := withoutPassword(raw)
	msg = strings.ReplaceAll(msg, raw, stripped)
	forms := []string{pw}
	if dec, derr := url.PathUnescape(pw); derr == nil && dec != pw {
		forms = append(forms, dec)
	}
	for _, f := range forms {
		msg = strings.ReplaceAll(msg, f, "•••")
	}
	if msg == err.Error() {
		return err
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		c := *ce
		c.Msg = msg
		return &c
	}
	return errors.New(msg)
}

// buildJob turns a parsed copy, prepare or plan command line into the
// engine job: endpoints, config and options. Confirm and Progress are set
// by the caller, which knows about the terminal.
func (p *parsed) buildJob(mode engine.Mode, cfg *roadiecfg.Config, stderr io.Writer) (engine.Job, error) {
	eps, staging, err := p.runEndpoints(mode, cfg, stderr)
	if err != nil {
		return engine.Job{}, err
	}
	opts, err := p.engineOptions(cfg, mode, dep.env)
	if err != nil {
		return engine.Job{}, err
	}
	warned := map[string]bool{}
	warn := func(msg string) {
		if !warned[msg] {
			warned[msg] = true
			fmt.Fprintf(stderr, "%s %s: %s\n", prog(), p.cmd, msg)
		}
	}
	j := engine.Job{Mode: mode, Config: cfg, Opts: opts, Clock: dep.now}
	fromFlag, fromEnv := "--from", "ROADIE_FROM_PASSWORD"
	if staging {
		// The staging is the target that prepare writes to.
		fromFlag, fromEnv = "the staging", "ROADIE_TO_PASSWORD"
	}
	if j.From, err = parseEndpoint(eps.from, fromFlag, fromEnv, warn); err != nil {
		return j, endpointError(stderr, p.cmd, err)
	}
	switch {
	case staging:
		j.To = j.From
	case eps.to != "":
		if j.To, err = parseEndpoint(eps.to, "--to", "ROADIE_TO_PASSWORD", warn); err != nil {
			return j, endpointError(stderr, p.cmd, err)
		}
	}
	return j, nil
}

// endpointError reports an endpoint that didn't parse. A *core.Error
// keeps its code and exit; anything else is a usage error.
// The raw value is never in the message: it may hold a password.
func endpointError(stderr io.Writer, cmd string, err error) error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return &cli.ExitError{Code: ce.Exit, Err: ce}
	}
	return usageError(stderr, cmd, "%v", err)
}

// mode is the engine mode a command line runs: copy and prepare become a
// plan with --dry-run (PRD 6.1), with the same arguments.
func (p *parsed) mode() engine.Mode {
	switch {
	case p.f.dryRun, p.cmd == "plan":
		return engine.Plan
	case p.cmd == "prepare":
		return engine.Prepare
	}
	return engine.Copy
}
