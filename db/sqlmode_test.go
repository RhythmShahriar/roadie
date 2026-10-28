// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// A live MySQL or MariaDB source sets sql_mode as mysqldump does, so the
// zero-date defaults of WordPress tables load under MySQL 8's default
// mode. It restores the mode before its end line.
func TestLiveSourceSetsSQLMode(t *testing.T) {
	for _, d := range []core.Dialect{core.MySQL, core.MariaDB} {
		s := &source{c: &Conn{engine: d}}
		s.header()
		s.trailer()
		var all []string
		for _, b := range s.queue {
			all = append(all, string(b.Pass.Text))
		}
		text := strings.Join(all, "")
		set := strings.Index(text, "SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO'")
		back := strings.Index(text, "SET SQL_MODE=@OLD_SQL_MODE")
		end := strings.Index(text, "-- Dump completed")
		if set < 0 || back < set || end < back {
			t.Fatalf("%s: header and trailer\n%s", d, text)
		}
	}
	s := &source{c: &Conn{engine: core.Postgres}}
	s.header()
	for _, b := range s.queue {
		if strings.Contains(string(b.Pass.Text), "SQL_MODE") {
			t.Fatal("a PostgreSQL dump sets SQL_MODE")
		}
	}
}

func TestRelaxedSQLMode(t *testing.T) {
	for _, c := range []struct {
		in, want string
		changed  bool
	}{
		{"ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION",
			"ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION", true},
		{"STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION", "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION", false},
		{"", "", false},
		{"NO_ZERO_DATE", "", true},
		{"NO_ZERO_DATE'; DROP", "NO_ZERO_DATE'; DROP", false},
	} {
		got, changed := relaxedSQLMode(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("relaxedSQLMode(%q) = %q %v, want %q %v", c.in, got, changed, c.want, c.changed)
		}
	}
}
