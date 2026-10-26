// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/report"
)

// firstRunKey names the once-per-machine marker of the note (PRD 6.11).
const firstRunKey = "roadie-authorised-use"

// firstRunNote is printed once per machine before the first command's
// output, on stderr, never with --json (PRD 6.11).
const firstRunNote = `Note: use Roadie on sites you own or are authorised to copy. It reads production
data to pseudonymise it and never writes to production. Pseudonymised data is still
personal data: keep copies as protected as the live site. This notice is shown once.
`

// showFirstRun prints the first-run note unless the run asked for JSON,
// which must stay alone on stdout and must not use up the note either.
// kit's own FirstRun hook can't tell a --json run, so Roadie does it here.
func showFirstRun(p *parsed, stderr io.Writer) {
	if p.f.g.json {
		return
	}
	if config.FirstRun(firstRunKey) {
		fmt.Fprint(stderr, firstRunNote)
	}
}

// prepareQuestion is the PRD 16.1 confirmation prepare asks on a terminal.
func prepareQuestion(host string) string {
	return "prepare rewrites this site's database while it runs. Visitors see a maintenance page for about a minute. Type the site's host to go on (" + host + "):"
}

// replaceQuestion is the copy confirmation of product.md 5.3.
func replaceQuestion(target string) string {
	return "Replace the database of " + target + "? [y/N]"
}

// lineReader reads answers one line at a time from stdin, giving up when
// ctx ends, so Ctrl-C at a question exits at once. One reader serves the
// whole run, so text typed ahead isn't lost between questions.
type lineReader struct {
	once  sync.Once
	lines chan string
	in    io.Reader
}

func newLineReader(in io.Reader) *lineReader { return &lineReader{in: in} }

// read returns the next line without its newline; ok is false at EOF or
// when ctx ends first.
func (l *lineReader) read(ctx context.Context) (string, bool) {
	l.once.Do(func() {
		l.lines = make(chan string)
		go func() {
			// The goroutine outlives a canceled question only until the
			// process exits, which is what follows a Ctrl-C.
			defer close(l.lines)
			s := bufio.NewScanner(io.LimitReader(l.in, 1<<20))
			for s.Scan() {
				l.lines <- s.Text()
			}
		}()
	})
	select {
	case line, ok := <-l.lines:
		return strings.TrimSpace(line), ok
	case <-ctx.Done():
		return "", false
	}
}

// yesNo asks a [y/N] or [Y/n] question on out; def is the answer to an
// empty line. EOF and Ctrl-C answer no.
func (l *lineReader) yesNo(ctx context.Context, out io.Writer, question string, def bool) bool {
	fmt.Fprint(out, question+" ")
	ans, ok := l.read(ctx)
	if !ok {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(ans) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// confirmer returns engine.Job.Confirm for a terminal: it prints the
// question and reads a line. With an expected answer (the target's host
// for prepare) the line must equal it, compared case-insensitively;
// without one it is a [y/N] question. An empty question gets the PRD 16.1
// wording for the mode. EOF and Ctrl-C answer no. Without a terminal
// there is no confirmer: the job is nil-Confirm and the flags decide.
func confirmer(ctx context.Context, lr *lineReader, out io.Writer, mode, target string) func(question, expect string) (bool, error) {
	return func(question, expect string) (bool, error) {
		if question == "" {
			question = replaceQuestion(target)
			if mode == "prepare" && expect != "" {
				question = prepareQuestion(expect)
			}
		}
		if expect == "" {
			return lr.yesNo(ctx, out, question, false), nil
		}
		fmt.Fprint(out, question+" ")
		ans, ok := lr.read(ctx)
		if !ok {
			fmt.Fprintln(out)
			return false, nil
		}
		return strings.EqualFold(ans, strings.TrimSpace(expect)), nil
	}
}

// signer is what a run needs of report.Signer.
type signer interface {
	Sign(e *core.Envelope) error
	KeyID() string
}

// loadReportSigner loads the signing key and prints the PRD 16.4 line
// when it was just created, which happens once per machine.
func loadReportSigner(keyFile string, stderr io.Writer) (signer, error) {
	s, created, err := dep.loadSigner(keyFile, dep.env)
	if err != nil {
		return nil, err
	}
	if created {
		path := "the signing key"
		if dir, err := config.Dir(); err == nil {
			path = filepath.Join(dir, "roadie", "signing.key")
		}
		fmt.Fprintf(stderr, "Created a signing key for Roadie reports: %s (keep %s private; set ROADIE_SIGNING_KEY in CI to sign with one identity).\n",
			shortKeyID(s.KeyID()), path)
	}
	return s, nil
}

// realLoadSigner adapts report.LoadSigner, never returning a typed nil.
func realLoadSigner(keyFile string, env func(string) (string, bool)) (signer, bool, error) {
	s, created, err := report.LoadSigner(keyFile, env)
	if err != nil || s == nil {
		if err == nil {
			err = core.ErrNotBuilt
		}
		return nil, created, err
	}
	return s, created, nil
}

// shortKeyID is a key ID as the screen shows it: "roadie:" and 7
// characters, then "…" (PRD 16.4).
func shortKeyID(id string) string {
	rest, ok := strings.CutPrefix(id, "roadie:")
	if !ok || len(rest) <= 7 {
		return id
	}
	return "roadie:" + rest[:7] + "…"
}
