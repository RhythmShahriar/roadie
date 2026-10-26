// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// soundcheckTimeout bounds one Soundcheck run; tests shorten it.
var soundcheckTimeout = 10 * time.Minute

// soundcheckMaxOutput caps the JSON read from Soundcheck.
const soundcheckMaxOutput = 16 << 20

// SoundcheckNotRun is the note of a verify --soundcheck without Soundcheck.
const SoundcheckNotRun = "Soundcheck not run: install it with rhythm install soundcheck"

// soundcheck is Soundcheck.
func soundcheck(ctx context.Context, url, echoURL string) *core.SoundcheckResult {
	bin, err := exec.LookPath("rhythm-soundcheck")
	if err != nil {
		return &core.SoundcheckResult{Note: SoundcheckNotRun}
	}
	ctx, cancel := context.WithTimeout(ctx, soundcheckTimeout)
	defer cancel()
	args := []string{"run", url, "--json"}
	if echoURL != "" {
		args = append(args, "--echo="+echoURL)
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- our own tool, arguments passed without a shell
	var out limitedBuffer
	out.max = soundcheckMaxOutput
	cmd.Stdout = &out
	err = cmd.Run()
	res := &core.SoundcheckResult{Ran: true}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	case err != nil:
		res.ExitCode = -1
		res.Note = "Soundcheck didn't finish: " + err.Error()
		if ctx.Err() != nil {
			res.Note = "Soundcheck didn't finish within " + soundcheckTimeout.String()
		}
	}
	var doc struct {
		Schema  string         `json:"schema"`
		Summary map[string]any `json:"summary"`
	}
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &doc) == nil {
		res.Schema = doc.Schema
		for k, v := range doc.Summary {
			if n, ok := v.(float64); ok {
				if res.Counts == nil {
					res.Counts = map[string]int64{}
				}
				res.Counts[k] = int64(n)
			}
		}
	}
	return res
}

// SoundcheckFinding returns soundcheck.failed (medium) for a run that
// exited 1, or nil.
func SoundcheckFinding(r *core.SoundcheckResult) *core.Finding {
	if r == nil || !r.Ran || r.ExitCode != 1 {
		return nil
	}
	return &core.Finding{ID: core.FindingSoundcheckFailed, Severity: core.SeverityMedium,
		Title: "Soundcheck found the staging site broken", Count: 1,
		Fix: "Open Soundcheck's report (rhythm-soundcheck report) and fix what it lists."}
}

// limitedBuffer keeps the first max bytes written and drops the rest, so
// a runaway child can't fill memory.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
