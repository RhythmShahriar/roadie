// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"testing"
)

// FuzzSQLMode checks the sql_mode follower against what it can't get
// wrong without noticing: a flag turned on must be named in the
// statement as a plain word, a change behind a version gate that only
// some servers pass is unknown; the same statement from the same
// state gives the same mode.
func FuzzSQLMode(f *testing.F) {
	for _, s := range []string{
		"SET sql_mode='NO_BACKSLASH_ESCAPES'",
		"/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;",
		"SET sql_mode=CONCAT('NO_BACKSLASH','_ESCAPES')",
		"SET sql_mode='NO_BACKSLASH' '_ESCAPES'",
		"SET sql_mode=0x4E4F5F4241434B534C4153485F45534341504553",
		"/*!99999 SET sql_mode='ANSI_QUOTES' */",
		"SET SESSION sql_mode = IFNULL(@m, 'ANSI')",
		"SET @m='NO_BACKSLASH_ESCAPES', sql_mode=@m",
	} {
		f.Add([]byte(s), false, false)
	}
	f.Fuzz(func(t *testing.T, in []byte, nb, aq bool) {
		cur := Mode{NoBackslashEscapes: nb, ANSIQuotes: aq}
		got := SQLMode(in, cur, nil)
		if again := SQLMode(in, cur, nil); again != got {
			t.Fatalf("%q: %+v then %+v", in, got, again)
		}
		if got.Unknown || quoting(got) == quoting(cur) {
			return
		}
		if gated(in, cur) {
			t.Fatalf("%q: a gated change was followed: %+v", in, got)
		}
		body := bytes.ToLower(Body(in, cur))
		if got.NoBackslashEscapes && !nb && !bytes.Contains(body, []byte("no_backslash_escapes")) {
			t.Fatalf("%q: NO_BACKSLASH_ESCAPES on without its name", in)
		}
		if got.ANSIQuotes && !aq && !bytes.Contains(body, []byte("ansi")) {
			t.Fatalf("%q: ANSI_QUOTES on without its name", in)
		}
	})
}
