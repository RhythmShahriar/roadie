// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/site"
)

// A --to Roadie can read but not write (.sql.bz2) is refused while the
// arguments are read, with dump-format and exit 2, before the source is
// read; .sql, .sql.gz and .sql.zst go through.
func TestUnwritableDumpTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	fx := useFixture(t, planResult(), nil, func(d *deps) { d.parseEndpoint = site.ParseEndpoint })
	for _, cmd := range []string{"copy", "plan"} {
		out, errOut, code := runCmd(t, cmd, "--from", "prod.sql", "--to", "out.sql.bz2", "--url", "https://staging.shop.test", "--yes")
		want := "dump-format: --to out.sql.bz2: Roadie reads bzip2 dumps but doesn't write them. Name the copy .sql, .sql.gz or .sql.zst."
		if code != 2 || !strings.Contains(out+errOut, want) {
			t.Errorf("%s: exit %d\n%s%s", cmd, code, out, errOut)
		}
	}
	if len(fx.jobs) != 0 {
		t.Fatalf("the source was read: %d jobs", len(fx.jobs))
	}
	for _, name := range []string{"out.sql", "out.sql.gz", "out.sql.zst"} {
		if _, errOut, code := runCmd(t, "plan", "--from", "prod.sql", "--to", name); code == 2 {
			t.Errorf("%s refused: %s", name, errOut)
		}
	}
}
