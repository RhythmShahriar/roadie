// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// choice is one line of the picker.
type choice struct{ label, value string }

// asker asks the picker's questions. huhAsker asks on the terminal; tests
// answer from a script.
type asker interface {
	choose(title string, options []choice) (string, error)
	input(title, placeholder string, required bool) (string, error)
}

// pickerChoices is the picker of PRD 6.12, in its order.
var pickerChoices = []choice{
	{"Make a staging copy from production (copy)", "copy"},
	{"Make an existing staging site safe (prepare)", "prepare"},
	{"Check a staging copy (verify)", "verify"},
	{"See what a copy would change (plan)", "plan"},
	{"Try it on a built-in shop (demo)", "demo"},
	{"Write a config file to edit (config init)", "config init"},
}

// The placeholders of the questions.
const (
	productionPlaceholder = "ssh://deploy@shop.com/var/www/shop or prod.sql.gz"
	targetPlaceholder     = "/var/www/staging, ssh://deploy@staging.shop.test/var/www/staging or staging.sql.gz"
	urlPlaceholder        = "https://staging.shop.test"
	stagingPlaceholder    = "/var/www/staging, ssh://deploy@staging.shop.test/var/www/staging or @staging"
)

// pickRun runs one command line for the picker; tests replace it.
var pickRun = runArgs

// pick asks what to do and runs it (PRD 6.12). It is what rhythm-roadie
// with no arguments does on a terminal.
func pick(ctx context.Context, a asker, lr *lineReader, stdout, stderr io.Writer) int {
	action, err := a.choose("What should Roadie do?", pickerChoices)
	if err != nil {
		return askError(stderr, err)
	}
	switch action {
	case "copy":
		return pickCopy(ctx, a, lr, stdout, stderr)
	case "prepare", "verify":
		s, err := a.input("Which staging site?", stagingPlaceholder, true)
		if err != nil {
			return askError(stderr, err)
		}
		return pickRun(ctx, []string{action, s}, stdout, stderr)
	case "plan":
		src, tgt, err := askEndpoints(a, false)
		if err != nil {
			return askError(stderr, err)
		}
		args := []string{"plan", "--from", src}
		if tgt != "" {
			args = append(args, "--to", tgt)
		}
		return pickRun(ctx, args, stdout, stderr)
	case "demo":
		return pickRun(ctx, []string{"demo"}, stdout, stderr)
	case "config init":
		stack, err := a.choose("Which stack?", []choice{
			{"WordPress", "wordpress"}, {"WordPress with WooCommerce", "woocommerce"},
			{"Laravel", "laravel"}, {"Another app on MySQL, PostgreSQL or SQLite", "sql"},
		})
		if err != nil {
			return askError(stderr, err)
		}
		return pickRun(ctx, []string{"config", "init", "--stack", stack}, stdout, stderr)
	}
	return cli.ExitUsage
}

// askEndpoints asks where production is and where the copy goes.
func askEndpoints(a asker, targetRequired bool) (src, tgt string, err error) {
	if src, err = a.input("Where is production?", productionPlaceholder, true); err != nil {
		return "", "", err
	}
	tgt, err = a.input("Where should the copy go?", targetPlaceholder, targetRequired)
	return src, tgt, err
}

// pickCopy asks the three copy questions, shows the plan, asks before it
// copies and offers to save the answers (PRD 6.12).
func pickCopy(ctx context.Context, a asker, lr *lineReader, stdout, stderr io.Writer) int {
	src, tgt, err := askEndpoints(a, true)
	if err != nil {
		return askError(stderr, err)
	}
	url, err := a.input("The copy's URL?", urlPlaceholder, true)
	if err != nil {
		return askError(stderr, err)
	}
	where := []string{"--from", src, "--to", tgt, "--url", url}
	if code := pickRun(ctx, append([]string{"plan"}, where...), stdout, stderr); code != cli.ExitOK {
		return code
	}
	if !lr.yesNo(ctx, stderr, "Copy now? [y/N]", false) {
		return cli.ExitOK
	}
	// The answer above is the confirmation; --yes keeps copy from asking
	// the same thing again.
	code := pickRun(ctx, append(append([]string{"copy"}, where...), "--yes"), stdout, stderr)
	if ctx.Err() != nil {
		return code
	}
	if _, err := os.Stat(configPath); err == nil {
		return code // never overwrite a config the user already has
	}
	if lr.yesNo(ctx, stderr, "Save these answers to .roadie/config.yaml? [Y/n]", true) {
		// The config is committed (PRD 8.1): a DSN keeps its user, host
		// and database but never its password.
		src, srcPw := withoutPassword(src)
		tgt, tgtPw := withoutPassword(tgt)
		stack := detectedStack(lastRunStack)
		if stack == "" {
			stack = "wordpress"
		}
		if err := saveAnswers(configPath, dep.initTemplate(stack), src, tgt, url); err != nil {
			fmt.Fprintf(stderr, "%s: couldn't save the answers: %v\n", prog(), err)
			return max(code, cli.ExitUnavailable)
		}
		fmt.Fprintf(stderr, "Wrote %s for %s with source:, target: and url: set.\n", configPath, stackWords[stack])
		for _, pw := range []struct {
			left     bool
			key, env string
		}{{srcPw, "source", "ROADIE_FROM_PASSWORD"}, {tgtPw, "target", "ROADIE_TO_PASSWORD"}} {
			if pw.left {
				fmt.Fprintf(stderr, "Left the password out of %s: in %s; set %s when you run Roadie.\n", pw.key, configPath, pw.env)
			}
		}
		excludeReports(stderr, stderr)
	}
	return code
}

// lastRunStack is the stack the last copy, prepare or plan in this
// process found; the picker saves the config template for it.
var lastRunStack core.StackInfo

// withoutPassword drops the password from a URL-style endpoint
// (mysql://user:secret@host/db gives mysql://user@host/db) and reports
// whether there was one. The password ends at the last @ of the address,
// since a password may hold / or @ unescaped; a user part with / or @ in
// it isn't one (ssh://deploy@host:22/path has no password).
func withoutPassword(s string) (string, bool) {
	i := strings.Index(s, "://")
	if i < 0 {
		return s, false
	}
	rest := s[i+3:]
	colon := strings.Index(rest, ":")
	if colon < 0 || strings.ContainsAny(rest[:colon], "/@?#") {
		return s, false
	}
	at := strings.LastIndex(rest, "@")
	if at < colon {
		return s, false
	}
	return s[:i+3] + rest[:colon] + rest[at:], true
}

// inlinePassword is the password withoutPassword would drop, "" when
// there is none.
func inlinePassword(s string) string {
	stripped, ok := withoutPassword(s)
	if !ok {
		return ""
	}
	// stripped is s with the password and its colon taken out.
	i := strings.Index(s, "://") + 3
	colon := i + strings.Index(s[i:], ":")
	return s[colon+1 : colon+1+len(s)-len(stripped)-1]
}

// configPath is where config init and the picker write the settings.
var configPath = filepath.Join(".roadie", "config.yaml")

// saveAnswers writes the config template with only source:, target: and
// url: filled in, each a quoted YAML string. A template without those
// lines (none given) becomes just the three keys.
func saveAnswers(path string, tmpl []byte, src, tgt, url string) error {
	values := map[string]string{"source": src, "target": tgt, "url": url}
	out := fillTemplate(tmpl, values)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// fillTemplate sets top-level keys in the template text, keeping the line
// comments; keys the template lacks are added at the top.
func fillTemplate(tmpl []byte, values map[string]string) []byte {
	lines := strings.SplitAfter(string(tmpl), "\n")
	done := map[string]bool{}
	for i, line := range lines {
		for _, key := range []string{"source", "target", "url"} {
			rest, ok := strings.CutPrefix(line, key+":")
			if !ok || done[key] {
				continue
			}
			comment := ""
			if j := strings.Index(rest, "#"); j >= 0 {
				// Keep the comment in its column when the value fits.
				val := " " + strconv.Quote(values[key])
				pad := max(1, j-len(val))
				comment = strings.Repeat(" ", pad) + strings.TrimRight(rest[j:], "\n")
			}
			lines[i] = key + ": " + strconv.Quote(values[key]) + comment + "\n"
			done[key] = true
		}
	}
	var head bytes.Buffer
	for _, key := range []string{"source", "target", "url"} {
		if !done[key] {
			fmt.Fprintf(&head, "%s: %s\n", key, strconv.Quote(values[key]))
		}
	}
	return append(head.Bytes(), strings.Join(lines, "")...)
}

// askError turns a question that ended into an exit code: Ctrl-C or Esc
// is 130, anything else couldn't run.
func askError(stderr io.Writer, err error) int {
	if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, context.Canceled) {
		return cli.ExitInterrupted
	}
	fmt.Fprintf(stderr, "%s: %v\n", prog(), err)
	return cli.ExitUnavailable
}

// huhAsker asks on the terminal with Huh, as Soundcheck's picker does.
type huhAsker struct{}

func (huhAsker) choose(title string, options []choice) (string, error) {
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o.label, o.value)
	}
	v := options[0].value
	err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title(title).Options(opts...).Value(&v))).
		WithTheme(huh.ThemeCharm()).Run()
	return v, err
}

func (huhAsker) input(title, placeholder string, required bool) (string, error) {
	var v string
	in := huh.NewInput().Title(title).Placeholder(placeholder).Value(&v)
	if required {
		in = in.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("this one is needed")
			}
			return nil
		})
	}
	err := huh.NewForm(huh.NewGroup(in)).WithTheme(huh.ThemeCharm()).Run()
	return strings.TrimSpace(v), err
}

// runPicker is rhythm-roadie with no arguments on a terminal.
func runPicker(stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	showFirstRun(&parsed{f: newFlags()}, stderr)
	return pick(ctx, huhAsker{}, newLineReader(dep.stdin), stdout, stderr)
}

// runArgs runs one command line in this process the way cli.Main would
// and returns its exit code.
func runArgs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	for _, c := range tool().Commands {
		if c.Name == args[0] {
			err := c.Run(ctx, args[1:], stdout, stderr)
			if ctx.Err() != nil {
				fmt.Fprintf(stderr, "%s: interrupted\n", prog())
				return cli.ExitInterrupted
			}
			return exitFor(stderr, err)
		}
	}
	return cli.ExitUsage
}

// exitFor prints err as cli.Main does and returns its exit code.
func exitFor(stderr io.Writer, err error) int {
	var ee *cli.ExitError
	var ue cli.UsageError
	switch {
	case err == nil, errors.Is(err, cli.ErrHelp):
		return cli.ExitOK
	case errors.As(err, &ee):
		if ee.Err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", prog(), ee.Err)
		}
		return ee.Code
	case errors.As(err, &ue):
		if !ue.Reported {
			fmt.Fprintf(stderr, "%s: %s\n", prog(), ue.Msg)
		}
		return cli.ExitUsage
	}
	fmt.Fprintf(stderr, "%s: %v\n", prog(), err)
	return cli.ExitUnavailable
}
