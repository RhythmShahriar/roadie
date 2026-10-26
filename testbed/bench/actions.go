// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// act runs the case's command with what its action says around it and
// returns the run its gates judge.
func (h *Harness) act(ctx context.Context, cr *caseRun, args []string) (*Run, error) {
	a := cr.c.Action
	switch a.Name {
	case manifest.ActionInterrupt, manifest.ActionSSHCut:
		return h.interrupt(ctx, cr, args)
	case manifest.ActionSIGKILL:
		return h.sigkill(ctx, cr, args)
	case manifest.ActionWatch:
		return h.watch(ctx, cr, args)
	case manifest.ActionTwice:
		before, err := h.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		run, err := h.roadie(ctx, cr, args, nil)
		if err != nil {
			return nil, err
		}
		after, err := h.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		cr.res.add("twice", before == after, "the second run changed the staging database or files")
		return run, nil
	case manifest.ActionRekey:
		return h.rekey(ctx, cr, args)
	}
	before := targetFiles(cr, args)
	run, err := h.roadie(ctx, cr, args, nil)
	if err == nil {
		h.checkLeftovers(cr, args, before)
		h.checkUnchanged(ctx, cr)
	}
	return run, err
}

// targetOf returns the local path of a dump-file target (after --to), or
// "" for a site or database target.
func targetOf(cr *caseRun, args []string) string {
	for i, a := range cr.c.Command {
		if a == "--to" && i+1 < len(cr.c.Command) {
			p, err := manifest.Expand(cr.c.Command[i+1], cr.local)
			if err != nil || !strings.Contains(filepath.Base(p), ".sql") {
				return ""
			}
			return p
		}
	}
	return ""
}

// targetFiles lists the files in the target's folder and the cache,
// before a run, for the leftovers check.
func targetFiles(cr *caseRun, args []string) map[string]bool {
	out := map[string]bool{}
	for _, dir := range leftoverDirs(cr, args) {
		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				out[p] = true
			}
			return nil
		})
	}
	return out
}

// leftoverDirs are where a run might leave temp or spool files on this
// machine: the target's folder, the run's TMPDIR and its cache.
func leftoverDirs(cr *caseRun, args []string) []string {
	dirs := []string{filepath.Join(cr.work, "tmp"), filepath.Join(cr.work, "home", ".cache")}
	if t := targetOf(cr, args); t != "" {
		dirs = append(dirs, filepath.Dir(t))
	}
	return dirs
}

// checkLeftovers is the no_finished_output gate: the target is absent or
// doesn't end like a finished dump; no new file other than Roadie's
// reports and the run's logs is left in the target folder, TMPDIR or
// the cache.
func (h *Harness) checkLeftovers(cr *caseRun, args []string, before map[string]bool) {
	if !cr.c.Expect.NoFinishedOutput {
		return
	}
	var bad []string
	if t := targetOf(cr, args); t != "" && finishedDump(t) {
		bad = append(bad, filepath.Base(t)+" looks finished")
	}
	for p := range targetFiles(cr, args) {
		rel, _ := filepath.Rel(cr.work, p)
		if before[p] || strings.HasPrefix(rel, ".roadie"+string(filepath.Separator)) || strings.HasPrefix(rel, "home"+string(filepath.Separator)+".config") ||
			rel == "stdout.json" || rel == "stderr.log" || strings.HasPrefix(rel, "site"+string(filepath.Separator)) {
			continue
		}
		if t := targetOf(cr, args); p == t {
			continue // judged above
		}
		bad = append(bad, filepath.ToSlash(rel)+" left behind")
	}
	cr.res.add("no-finished-output", len(bad) == 0, "%s", orDash(strings.Join(sortedCopy(bad), ", ")))
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// finishedDump reports whether a dump file ends with mysqldump's or
// pg_dump's completion line (read through gzip when it is one).
func finishedDump(path string) bool {
	rc, err := oracle.OpenDump(path)
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	var tail []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := rc.Read(buf)
		tail = append(tail, buf[:n]...)
		if len(tail) > 8<<10 {
			tail = tail[len(tail)-8<<10:]
		}
		if err != nil {
			break
		}
	}
	s := string(tail)
	return strings.Contains(s, "-- Dump completed") || strings.Contains(s, "PostgreSQL database dump complete")
}

// checkUnchanged is the unchanged gate: staging hashes as it did at the
// snapshot.
func (h *Harness) checkUnchanged(ctx context.Context, cr *caseRun) {
	if !cr.c.Expect.Unchanged {
		return
	}
	now, err := h.snapshot(ctx)
	if err != nil {
		cr.res.add("unchanged", false, "%s", err)
		return
	}
	cr.res.add("unchanged", now == cr.st.snapshot && now != "", "the staging database or files changed")
}

// baseline runs the command once to time it, then removes its output.
func (h *Harness) baseline(ctx context.Context, cr *caseRun, args []string) (time.Duration, error) {
	run, err := h.roadie(ctx, cr, args, nil)
	if err != nil {
		return 0, err
	}
	if t := targetOf(cr, args); t != "" {
		_ = os.Remove(t)
	}
	if run.Exit != 0 {
		return 0, fmt.Errorf("the baseline run exited %d", run.Exit)
	}
	return run.Wall, nil
}

// interrupt times a baseline run, then runs again and at At of that time
// sends SIGINT (r20 interrupt) or kills the remote export (r20 ssh-cut).
func (h *Harness) interrupt(ctx context.Context, cr *caseRun, args []string) (*Run, error) {
	base, err := h.baseline(ctx, cr, args)
	if err != nil {
		return nil, err
	}
	delay := time.Duration(float64(base) * cr.c.Action.At)
	before := targetFiles(cr, args)
	var mu sync.Mutex
	fired := ""
	run, err := h.roadie(ctx, cr, args, func(signal func(os.Signal) error) {
		go func() {
			time.Sleep(delay)
			mu.Lock()
			defer mu.Unlock()
			if cr.c.Action.Name == manifest.ActionSSHCut {
				_, _ = h.Ex.Exec(context.Background(), ctrSSHD, "root", nil, nil, "pkill", "-KILL", "-f", "mariadb-dump|mysqldump")
				fired = "the ssh stream was cut"
				return
			}
			if signal(os.Interrupt) == nil {
				fired = "SIGINT"
			}
		}()
	})
	if err != nil {
		return nil, err
	}
	mu.Lock()
	run.Signaled = fired
	mu.Unlock()
	cr.res.add("action "+cr.c.Action.Name, run.Signaled != "", "the run finished before the harness could act at %.0f%%", cr.c.Action.At*100)
	h.checkLeftovers(cr, args, before)
	return run, nil
}

// sigkill kills the run at Runs random points (seeded, so two benchmark
// runs pick the same points) and checks after each that verify never
// passes a partial output: a target that exists without its completion
// line must fail verify.
func (h *Harness) sigkill(ctx context.Context, cr *caseRun, args []string) (*Run, error) {
	base, err := h.baseline(ctx, cr, args)
	if err != nil {
		return nil, err
	}
	target := targetOf(cr, args)
	if target == "" {
		return nil, fmt.Errorf("sigkill needs a dump-file target")
	}
	verifyArgs, err := manifest.ExpandAll(append([]string{"verify"}, cr.c.Verify...), cr.vars)
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewPCG(uint64(h.Seed), 26)) // #nosec G404 G115 -- reproducible kill points from the seed, not a secret
	partial, passed, killed := 0, 0, 0
	for i := 0; i < cr.c.Action.Runs; i++ {
		_ = os.Remove(target)
		delay := time.Duration(float64(base) * (0.05 + 0.9*rng.Float64()))
		run, err := h.roadie(ctx, cr, args, func(signal func(os.Signal) error) {
			go func() { time.Sleep(delay); _ = signal(os.Kill) }()
		})
		if err != nil {
			return nil, err
		}
		if run.Exit == 128+9 {
			killed++
		}
		if !fileExists(target) || finishedDump(target) {
			continue
		}
		partial++
		v, err := h.roadie(ctx, cr, verifyArgs, nil)
		if err != nil {
			return nil, err
		}
		if v.Exit == 0 {
			passed++
		}
	}
	cr.res.add("action sigkill", passed == 0, "%d runs, %d partial outputs left, %d passed verify", cr.c.Action.Runs, partial, passed)
	cr.run.Notes = append(cr.run.Notes, fmt.Sprintf("sigkill: %d of %d runs were killed before they finished", killed, cr.c.Action.Runs))
	_ = os.Remove(target)
	return h.roadie(ctx, cr, args, nil)
}

// watch runs the command while scanning the run's folders on this
// machine for canaries every 250 ms: raw production data must never land
// on the local disk (r19), not even for a moment.
func (h *Harness) watch(ctx context.Context, cr *caseRun, args []string) (*Run, error) {
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	seen := map[string]bool{}
	var mu sync.Mutex
	scans := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for wctx.Err() == nil {
			rep, err := oracle.Run(wctx, oracle.Options{Canaries: cr.st.canaries, Tree: cr.work})
			mu.Lock()
			if err == nil {
				scans++
				for _, s := range rep.Survivors {
					seen[s.ID] = true
				}
			}
			mu.Unlock()
			select {
			case <-wctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
		}
	}()
	run, err := h.roadie(ctx, cr, args, nil)
	stop()
	<-done
	// One more scan of what the run left, however short it was.
	if rep, serr := oracle.Run(ctx, oracle.Options{Canaries: cr.st.canaries, Tree: cr.work}); serr == nil {
		scans++
		for _, s := range rep.Survivors {
			seen[s.ID] = true
		}
	}
	mu.Lock()
	defer mu.Unlock()
	cr.res.add("watch", len(seen) == 0 && scans > 0, "%d canaries seen on the local disk during the run", len(seen))
	return run, err
}

// fakeEmail matches the fake addresses Roadie makes (example.invalid by
// default).
var fakeEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]*example\.invalid`)

// rekey runs prepare again with the other key (r21): either it refuses
// and staging is unchanged, or it re-maps every person, so no fake of the
// first key is left.
func (h *Harness) rekey(ctx context.Context, cr *caseRun, args []string) (*Run, error) {
	a := filepath.Join(cr.work, "before.sql")
	if err := h.dumpDB(ctx, manifest.OutStagingDB, a); err != nil {
		return nil, err
	}
	cr.key = h.keys[1]
	run, err := h.roadie(ctx, cr, args, nil)
	cr.key = h.keys[0]
	if err != nil {
		return nil, err
	}
	c := filepath.Join(cr.work, "after.sql")
	if err := h.dumpDB(ctx, manifest.OutStagingDB, c); err != nil {
		return nil, err
	}
	if run.Exit != 0 {
		cr.res.add("rekey", hashFile(a) == hashFile(c), "the refused run changed staging")
		return run, nil
	}
	ab, _ := os.ReadFile(a) // #nosec G304 -- the harness's own dump
	cb, _ := os.ReadFile(c) // #nosec G304 -- the harness's own dump
	first := map[string]bool{}
	for _, m := range fakeEmail.FindAll(ab, -1) {
		first[string(m)] = true
	}
	mixed := 0
	for _, m := range fakeEmail.FindAll(cb, -1) {
		if first[string(m)] {
			mixed++
		}
	}
	cr.res.add("rekey", len(first) > 0 && mixed == 0, "%d fakes of the first key, %d still there after the new key", len(first), mixed)
	return run, nil
}

// verifyAll is g05: every kept r-scenario output verified again in a
// fresh process, its report checked and the control rounds counted.
func (h *Harness) verifyAll(ctx context.Context, cr *caseRun) {
	if len(h.kept) == 0 {
		cr.res.Pending = "no r-scenario output was kept in this run (run the copy scenarios first)"
		return
	}
	cr.env = &hostEnv{bin: h.Bin, work: cr.work, path: os.Getenv("PATH")}
	for _, k := range h.kept {
		vars := cr.env.Vars()
		vars["output"] = k.Path
		args, err := manifest.ExpandAll(cr.c.Command, vars)
		if err != nil {
			cr.res.add("verify "+k.ID, false, "%s", err)
			continue
		}
		before := reports(cr.work)
		for round := 0; round < h.Rounds; round++ {
			run, err := h.roadie(ctx, cr, args, nil)
			if err != nil {
				cr.res.add("verify "+k.ID, false, "%s", err)
				break
			}
			env, _ := ParseEnvelope(run.Stdout)
			h.countControl("g05 "+k.ID, env)
			if round == 0 {
				cr.res.add("verify "+k.ID, run.Exit == 0, "%s", describe(run, env))
				h.reportCheck(ctx, cr, before)
			}
		}
	}
}

// controlRounds runs a good input's command Rounds-1 more times (the
// case's own run is the first) and counts the false alarms.
func (h *Harness) controlRounds(ctx context.Context, cr *caseRun, args []string, first *Run, env *Envelope) {
	if env == nil {
		h.control.Skipped = append(h.control.Skipped, cr.st.s.ID+": no envelope from rhythm-roadie")
		return
	}
	h.countControl(cr.st.s.ID, env)
	clean := first.Exit == 0
	for round := 1; round < h.Rounds; round++ {
		run, err := h.roadie(ctx, cr, args, nil)
		if err != nil {
			cr.res.add("control", false, "%s", err)
			return
		}
		e, _ := ParseEnvelope(run.Stdout)
		h.countControl(cr.st.s.ID, e)
		clean = clean && run.Exit == 0
	}
	cr.res.add("control", clean && h.control.ByInput[cr.st.s.ID] == 0, "%d rounds, %d false alarms", h.Rounds, h.control.ByInput[cr.st.s.ID])
}

// countControl adds one control check.
func (h *Harness) countControl(input string, env *Envelope) {
	h.control.Checks++
	n := 0
	if env != nil {
		n = env.AtOrAbove(core.SeverityLow)
		h.control.IDs = append(h.control.IDs, env.FindingsAtOrAbove(core.SeverityLow)...)
	}
	h.control.FalseAlarms += n
	h.control.ByInput[input] += n
}
