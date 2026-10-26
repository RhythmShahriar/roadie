// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// codeFile splits lines without copying the rest of the file, so a big
// file of short lines stays linear.
func TestCodeFileLinear(t *testing.T) {
	b := bytes.Repeat([]byte("<?php echo $a;\n"), (2<<20)/15)
	r := &run{j: Job{}, skip: newSkipper(nil), keySeen: map[string]bool{}, checked: map[string]int64{}, why: map[string]string{},
		res: &core.VerifyResult{}}
	start := time.Now()
	r.codeFile("wp-content/plugins/big.php", b)
	bound := time.Second
	if raceEnabled {
		bound *= 15
	}
	if d := time.Since(start); d > bound {
		t.Errorf("2 MiB of short lines took %v, want under %v", d, bound)
	}
	if len(r.res.Findings) != 0 {
		t.Errorf("findings in plain code: %+v", r.res.Findings)
	}
}
