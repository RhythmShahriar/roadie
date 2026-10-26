// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// A dump that switches the client to a character set in which 0x5c can
// end a character is refused at the switch, before any row read under
// it reaches a sink: the server would read 0xbf5c as one character and
// end the string at the quote after it (CVE-2006-2753).
func TestOpenMultibyteCharset(t *testing.T) {
	const myHead = "-- MySQL dump 10.13\n/*!40101 SET NAMES utf8mb4 */;\nCREATE TABLE `t` (`a` int, `b` text);\n"
	const myTail = "-- Dump completed on 2026-10-26 10:00:00\n"
	const pgHead = "--\n-- PostgreSQL database dump\n--\nSET standard_conforming_strings = on;\nCREATE TABLE public.t (a integer, b text);\n"
	const pgTail = "--\n-- PostgreSQL database dump complete\n--\n"
	payload := "'); DROP DATABASE prod; -- ');\n"
	for _, c := range []struct{ name, in string }{
		{"gbk", myHead + "/*!40101 SET NAMES gbk */;\nINSERT INTO `t` VALUES (1,'\xbf\\'),(2,'b');\n" + myTail},
		{"gbk client", myHead + "SET character_set_client = gbk;\nINSERT INTO `t` VALUES (1,'\xbf\\" + payload + myTail},
		{"big5", myHead + "SET NAMES big5;\nINSERT INTO `t` VALUES (1,'\xa5\\" + payload + myTail},
		{"cp932", myHead + "SET NAMES cp932;\nINSERT INTO `t` VALUES (1,'\x83\\" + payload + myTail},
		{"gb18030", myHead + "SET NAMES gb18030;\nINSERT INTO `t` VALUES (1,'\x81\\" + payload + myTail},
		{"saved gbk", myHead + "SET @cs = 'gbk';\nSET character_set_client = @cs;\nINSERT INTO `t` VALUES (1,'\xbf\\" + payload + myTail},
		{"sjis E string", pgHead + "SET client_encoding = 'SJIS';\nINSERT INTO public.t VALUES (1, E'\x83\\" + payload + pgTail},
		{"sjis plain string", pgHead + "SET standard_conforming_strings = off;\nSET client_encoding = 'SJIS';\nINSERT INTO public.t VALUES (1, '\x83\\" + payload + pgTail},
		{"big5 set_config", pgHead + "SELECT pg_catalog.set_config('client_encoding', 'BIG5', false);\nINSERT INTO public.t VALUES (1, E'\xa5\\" + payload + pgTail},
	} {
		src, err := Open(t.Context(), strings.NewReader(c.in), Options{Name: "prod.sql"})
		var bs []*core.Batch
		if err == nil {
			bs, err = drainAll(t, src)
		}
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Line < 4 ||
			!strings.Contains(ce.Msg, "read a backslash in the rows after it as part of a character") {
			t.Errorf("%s: %v", c.name, err)
		}
		for _, b := range bs {
			if b.Pass == nil {
				t.Errorf("%s: rows read under the multibyte character set", c.name)
			}
		}
	}
}

// The same dumps under a single-byte or UTF-8 client character set read
// the backslash as an escape and keep the payload as data.
func TestOpenSafeCharset(t *testing.T) {
	in := "-- MySQL dump 10.13\n/*!40101 SET @saved_cs_client = @@character_set_client */;\n/*!40101 SET NAMES latin1 */;\n" +
		"CREATE TABLE `t` (`a` int, `b` text);\nINSERT INTO `t` VALUES (1,'\xbf\\'); DROP DATABASE prod; -- ');\n" +
		"/*!40101 SET character_set_client = @saved_cs_client */;\n-- Dump completed on 2026-10-26 10:00:00\n"
	bs, err := drainAll(t, mustOpen(t, in, Options{Name: "prod.sql"}))
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, b := range bs {
		rows += len(b.Rows)
		if b.Pass != nil && strings.Contains(string(b.Pass.Text), "DROP DATABASE") {
			t.Errorf("payload read as a statement: %q", b.Pass.Text)
		}
	}
	if rows != 1 {
		t.Errorf("%d rows", rows)
	}
}
