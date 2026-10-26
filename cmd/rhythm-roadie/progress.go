// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// progress shows the engine's progress (core.Progress). On a terminal it
// redraws two lines, the PRD 10.2 bar and the current table, at most ten
// times a second; elsewhere it prints one line per phase. It also keeps
// each phase's duration for --debug.
type progress struct {
	mu       sync.Mutex
	w        io.Writer
	terminal bool
	quiet    bool
	now      func() time.Time

	phase       string
	total, done int64
	table       string
	tableRows   int64
	drawn       bool      // the two lines are on screen
	last        time.Time // last redraw
	samples     []sample  // for the moving average of the speed
	tm          timings
}

// sample is how far a phase was at one moment.
type sample struct {
	at   time.Time
	done int64
}

// redrawEvery and speedWindow are PRD 10.2's limits: ten redraws a
// second, the ETA from a 5 s moving average. sampleEvery is the moving
// average's resolution.
const (
	redrawEvery = 100 * time.Millisecond
	sampleEvery = 100 * time.Millisecond
	speedWindow = 5 * time.Second
	barWidth    = 32
)

// phaseNames are the engine's phases as the screen says them.
var phaseNames = map[string]string{"index": "Indexing", "transform": "Streaming", "import": "Importing", "verify": "Verifying"}

func newProgress(w io.Writer, terminal, quiet bool, now func() time.Time) *progress {
	return &progress{w: w, terminal: terminal, quiet: quiet, now: now}
}

var _ core.Progress = (*progress)(nil)

// Phase starts a phase; total is its size in bytes (0 when unknown).
func (p *progress) Phase(name string, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == p.phase && total == p.total {
		// The engine and verify both announce verify: one line, one timing.
		return
	}
	now := p.now()
	p.endPhase(now)
	p.phase, p.total, p.done, p.table, p.tableRows = name, total, 0, "", 0
	p.samples = []sample{{now, 0}}
	p.tm.start(name, now)
	if p.quiet {
		return
	}
	if !p.terminal {
		line := phaseName(name)
		if total > 0 {
			line += " " + size(total)
		}
		fmt.Fprintln(p.w, line)
		return
	}
	p.draw(now)
}

// Advance adds n bytes to the phase.
func (p *progress) Advance(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	now := p.now()
	// The engine may call Advance once a row, so samples closer together
	// than sampleEvery share one slot: the window then holds about 50
	// samples however fast the rows come.
	if k := len(p.samples); k >= 2 && now.Sub(p.samples[k-2].at) < sampleEvery {
		p.samples[k-1] = sample{now, p.done}
	} else {
		p.samples = append(p.samples, sample{now, p.done})
	}
	for len(p.samples) > 2 && now.Sub(p.samples[1].at) >= speedWindow {
		p.samples = p.samples[1:]
	}
	if p.terminal && !p.quiet && now.Sub(p.last) >= redrawEvery {
		p.draw(now)
	}
}

// Table says which table the phase is in and how many rows it has done.
func (p *progress) Table(name string, rows int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.table, p.tableRows = name, rows
	if p.terminal && !p.quiet && p.now().Sub(p.last) >= redrawEvery {
		p.draw(p.now())
	}
}

// Note prints a message between the progress lines and the rest.
func (p *progress) Note(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.quiet {
		return
	}
	if p.terminal {
		p.clear()
		fmt.Fprintf(p.w, "  · %s\n", safe(msg))
		if p.phase != "" {
			p.draw(p.now())
		}
		return
	}
	fmt.Fprintf(p.w, "%s\n", safe(msg))
}

// Done ends the last phase and takes the progress lines off the screen.
func (p *progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endPhase(p.now())
	p.clear()
	p.phase = ""
}

// timings returns the phases' durations.
func (p *progress) timings() *timings {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.tm
	return &t
}

func (p *progress) endPhase(now time.Time) {
	if p.phase != "" {
		p.tm.end(now)
	}
}

// clear removes the two progress lines.
func (p *progress) clear() {
	if p.drawn {
		fmt.Fprint(p.w, "\x1b[1A\r\x1b[2K\x1b[1B\r\x1b[2K\x1b[1A\r")
		p.drawn = false
	}
}

func (p *progress) draw(now time.Time) {
	p.last = now
	if p.drawn {
		fmt.Fprint(p.w, "\x1b[1A\r")
	}
	fmt.Fprintf(p.w, "\x1b[2K  %s\n\x1b[2K  %s", p.line(), p.tableLine())
	p.drawn = true
}

// line is "Streaming   ████░░  1.9 GB of 2.4 GB · 31 MB/s · 0:14 left".
func (p *progress) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-11s ", phaseName(p.phase))
	if p.total > 0 {
		done := min(p.done, p.total)
		filled := int(float64(done) / float64(p.total) * barWidth) // no int64 overflow on huge sizes
		b.WriteString(strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled))
		fmt.Fprintf(&b, "  %s of %s", size(done), size(p.total))
	} else {
		b.WriteString(size(p.done))
	}
	if rate := p.rate(); rate > 0 {
		fmt.Fprintf(&b, " · %s/s", size(int64(rate)))
		if p.total > p.done {
			left := time.Duration(float64(p.total-p.done) / rate * float64(time.Second))
			fmt.Fprintf(&b, " · %s left", clock(left))
		}
	}
	return b.String()
}

// tableLine is "wp_wc_orders_meta  811,903 rows".
func (p *progress) tableLine() string {
	if p.table == "" {
		return ""
	}
	return safe(p.table) + "  " + num(p.tableRows) + " rows"
}

// rate is bytes a second over the last speedWindow.
func (p *progress) rate() float64 {
	if len(p.samples) < 2 {
		return 0
	}
	first, last := p.samples[0], p.samples[len(p.samples)-1]
	secs := last.at.Sub(first.at).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(last.done-first.done) / secs
}

func phaseName(name string) string {
	if n, ok := phaseNames[name]; ok {
		return n
	}
	return name
}

// clock writes a duration as 0:14 or 1:02:03.
func clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// timings are the phases' durations, in order.
type timings struct {
	names []string
	durs  []time.Duration
	begin time.Time
}

func (t *timings) start(name string, at time.Time) {
	t.names = append(t.names, name)
	t.durs = append(t.durs, 0)
	t.begin = at
}

func (t *timings) end(at time.Time) {
	if n := len(t.durs); n > 0 && t.durs[n-1] == 0 {
		t.durs[n-1] = at.Sub(t.begin)
	}
}

// String is "index 1.2s · transform 3.4s".
func (t *timings) String() string {
	var parts []string
	for i, n := range t.names {
		parts = append(parts, n+" "+duration(t.durs[i]))
	}
	return strings.Join(parts, " · ")
}
