// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

const wantCharset = "holds a SET of the client character set to big5, cp932, sjis, gbk, gb18030 or a value Roadie can't read, " +
	"which could make the staging server read a backslash in the rows after it as part of a character"

// A client character set in which 0x5c can end a character is refused,
// however the SET spells it.
func TestCharsetRefused(t *testing.T) {
	for _, text := range []string{
		"SET NAMES gbk;",
		"/*!40101 SET NAMES big5 */;",
		"SET NAMES cp932;",
		"SET NAMES 'sjis';",
		"SET NAMES `GB18030`;",
		"SET NAMES \"gbk\" COLLATE gbk_bin;",
		"SET CHARACTER SET gbk;",
		"SET CHARSET big5;",
		"SET character_set_client = gbk;",
		"/*!40101 SET character_set_client = gbk */;",
		"SET SESSION character_set_connection = cp932;",
		"SET @@session.character_set_client = 'big5';",
		"SET @@character_set_results := sjis;",
		"SET collation_connection = gbk_chinese_ci;",
		"SET time_zone = '+00:00', character_set_client = gbk;",
		"SET character_set_client = 'g\\bk';",
		"SET character_set_client = _utf8mb4'gbk';",
		"SET character_set_client = X'67626b';",
		"SET character_set_client = CONCAT('gb', 'k');",
		"SET character_set_client = @@character_set_server;",
		"SET character_set_client = @@global.character_set_client;",
		"SET character_set_client = @`a\\b`;",
		"SET NAMES utf8mb4, character_set_client = gbk;",
	} {
		d, err := check(text, Options{Name: "prod.sql"})
		var ce *core.Error
		if d.Verdict != Refuse || !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused {
			t.Errorf("%q: %v %v", text, d.Verdict, err)
			continue
		}
		if !strings.Contains(ce.Msg, wantCharset) || !strings.HasSuffix(ce.Msg, "Export the dump again with --default-character-set=utf8mb4.") {
			t.Errorf("%q: %s", text, ce.Msg)
		}
	}
}

// The character sets dump tools write are kept.
func TestCharsetKept(t *testing.T) {
	for _, text := range []string{
		"SET NAMES utf8mb4;", "SET NAMES 'utf8';", "SET NAMES latin1 COLLATE latin1_swedish_ci;",
		"SET NAMES DEFAULT;", "SET CHARACTER SET binary;", "SET character_set_client = ujis;",
		"SET character_set_client = @@character_set_client;",
		"SET character_set_results = @@session.character_set_results;",
		"SET collation_connection = utf8mb4_0900_ai_ci;",
		"SET character_set_client = @never_set;",
	} {
		d, err := check(text, Options{Name: "prod.sql"})
		if err != nil || d.Verdict != Keep {
			t.Errorf("%q: %+v %v", text, d, err)
		}
	}
}

// runFilter feeds statements to one Filter and returns the first error.
func runFilter(t *testing.T, stmts []string) error {
	t.Helper()
	bs, modes := statements(t, []byte(strings.Join(stmts, "\n")+"\n"))
	f := NewFilter(Options{Name: "prod.sql"})
	for i, b := range bs {
		if _, err := f.Add(b, modes[i]); err != nil {
			return err
		}
	}
	return f.Finish()
}

// A user variable restores a client character set only when Roadie knows
// it holds one it judged.
func TestCharsetVars(t *testing.T) {
	kept := [][]string{
		{"/*!40101 SET @saved_cs_client = @@character_set_client */;", "/*!50503 SET character_set_client = utf8mb4 */;",
			"/*!40101 SET character_set_client = @saved_cs_client */;"},
		{"SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION;", "SET NAMES utf8mb4;", "SET COLLATION_CONNECTION=@old_collation_connection;"},
		{"SET @x = 'latin1';", "SET character_set_client = @X;"},
		{"SET @x = 'gbk';", "SET @x = @@character_set_client;", "SET character_set_client = @x;"},
		{"SET character_set_client = @never_set;"},
	}
	for _, c := range kept {
		if err := runFilter(t, c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	refused := [][]string{
		{"SET @x = 'gbk';", "SET NAMES @x;"},
		{"SET @x = 'gbk';", "SET character_set_client = @x;"},
		{"SET @x = @@character_set_client;", "SET @x = CONCAT('gb', 'k');", "SET character_set_client = @x;"},
		{"SET @x = @@character_set_server;", "SET character_set_client = @x;"},
		// Not every supported server runs a gated save.
		{"SET @x = 'gbk';", "/*!80099 SET @x = @@character_set_client */;", "SET character_set_client = @x;"},
		{"SET @x = 'gbk';", "/*M!100616 SET @x = @@character_set_client */;", "SET character_set_client = @x;"},
		// A SET that fails on one clause assigns none of them.
		{"SET @x = 'gbk';", "SET @x = @@character_set_client, time_zone = 'nowhere';", "SET character_set_client = @x;"},
		{"SET @x = @@character_set_client, character_set_client = @x;"},
		// A variable Roadie can't name may be any of them.
		{"SET @'a\\'b' = 'gbk';", "SET character_set_client = @never_set;"},
	}
	for _, c := range refused {
		err := runFilter(t, c)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || !strings.Contains(ce.Msg, wantCharset) {
			t.Errorf("%q: %v", c, err)
		}
	}
}

// PostgreSQL client encodings in which 0x5c can end a character are
// refused in SET and set_config, under any alias.
func TestPGEncodingRefused(t *testing.T) {
	for _, text := range []string{
		"SET client_encoding = 'SJIS';",
		"SET client_encoding TO 'Shift_JIS';",
		"SET client_encoding = sjis;",
		"SET SESSION client_encoding = 'BIG5';",
		"SET LOCAL client_encoding = 'GBK';",
		"SET client_encoding = 'win936';",
		"SET client_encoding = 'UHC';",
		"SET client_encoding = 'Windows-949';",
		"SET client_encoding = 'GB18030';",
		"SET client_encoding = 'JOHAB';",
		"SET client_encoding = 'SHIFT_JIS_2004';",
		"SET client_encoding = \"MSKANJI\";",
		"SET client_encoding = E'SJIS';",
		"SET client_encoding = $$SJIS$$;",
		"SET client_encoding = 'UTF8', 'SJIS';",
		"SET client_encoding = 'S\\JIS';",
		"SELECT pg_catalog.set_config('client_encoding', 'SJIS', false);",
		"SELECT set_config('client_encoding', 'big5', true);",
		"SELECT set_config('client_encoding', E'GBK', false);",
	} {
		d, err := checkPGText(text, Options{Name: "prod.sql"})
		if d.Verdict != Refuse || err == nil || !strings.Contains(err.Error(),
			"holds a change of client_encoding to SJIS, BIG5, GBK, UHC, GB18030, JOHAB or a value Roadie can't read, which could make the staging server read a backslash") {
			t.Errorf("%q: %v %v", text, d.Verdict, err)
		}
	}
	for _, text := range []string{
		"SET client_encoding = 'UTF8';", "SET client_encoding TO 'LATIN1';", "SET client_encoding = DEFAULT;",
		"SET client_encoding = 'EUC_JP';", "SELECT pg_catalog.set_config('client_encoding', 'UTF8', false);",
	} {
		if d, err := checkPGText(text, Options{Name: "prod.sql"}); err != nil || d.Verdict != Keep {
			t.Errorf("%q: %+v %v", text, d, err)
		}
	}
}
