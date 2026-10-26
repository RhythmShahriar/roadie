// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const mysqlDump = "-- MySQL dump 10.13  Distrib 8.4.3\n" +
	"/*!40101 SET NAMES utf8mb4 */;\n" +
	"DROP TABLE IF EXISTS `wp_options`;\n" +
	"CREATE TABLE `wp_options` (\n" +
	"  `option_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
	"  `option_name` varchar(191) NOT NULL DEFAULT '',\n" +
	"  `option_value` longtext NOT NULL,\n" +
	"  PRIMARY KEY (`option_id`),\n" +
	"  UNIQUE KEY `option_name` (`option_name`)\n" +
	") ENGINE=InnoDB;\n" +
	"LOCK TABLES `wp_options` WRITE;\n" +
	"INSERT INTO `wp_options` VALUES (1,'siteurl','https://shop.example.com'),(2,'it\\'s','a;b\\\\c\\nd'),(3,'nul','x\\0y'),(4,'n',NULL);\n" +
	"INSERT INTO `wp_options` VALUES (5,'hex',0x616263),(6,'bin',_binary 'q\\'r'),(7,'num',-1.5e3);\n" +
	"UNLOCK TABLES;\n" +
	"CREATE TABLE `wp_usermeta` (\n" +
	"  `umeta_id` bigint NOT NULL,\n" +
	"  `user_id` bigint NOT NULL,\n" +
	"  `meta_key` varchar(255) DEFAULT NULL,\n" +
	"  `meta_value` longtext,\n" +
	"  PRIMARY KEY (`umeta_id`,`user_id`),\n" +
	"  KEY `meta_key` (`meta_key`(191))\n" +
	");\n" +
	"INSERT INTO `wp_usermeta` (`umeta_id`, `user_id`, `meta_key`, `meta_value`) VALUES\n(1, 2, 'k', 'v'),\n(2, 2, 'k2', 'v2');\n" +
	"INSERT IGNORE INTO `wp_usermeta` VALUES (3,2,'k3','v3') ON DUPLICATE KEY UPDATE meta_value='x;y';\n" +
	"CREATE TABLE `nokey` (\n  `a` int\n);\n" +
	"INSERT INTO nokey VALUES (1),(2);\n" +
	"DELIMITER ;;\n" +
	"/*!50003 CREATE*/ /*!50003 TRIGGER `t` BEFORE INSERT ON `nokey` FOR EACH ROW BEGIN SET NEW.a = 1; INSERT INTO nokey VALUES (9); END */;;\n" +
	"DELIMITER ;\n" +
	"REPLACE INTO `nokey` VALUES (3);\n" +
	"-- Dump completed on 2026-10-01 12:00:00\n"

func TestReadMySQLDump(t *testing.T) {
	var rows []Row
	st, err := ReadDump(strings.NewReader(mysqlDump), func(r Row) error {
		rows = append(rows, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(st.Rows); got != "map[nokey:3 wp_options:7 wp_usermeta:3]" {
		t.Errorf("rows %s", got)
	}
	if got := strings.Join(st.Tables, ","); got != "wp_options,wp_usermeta,nokey" {
		t.Errorf("tables %s", got)
	}
	want := map[string]string{
		"wp_options/option_id=2/option_value":         "a;b\\c\nd",
		"wp_options/option_id=2/option_name":          "it's",
		"wp_options/option_id=3/option_value":         "x\x00y",
		"wp_options/option_id=5/option_value":         "abc",
		"wp_options/option_id=6/option_value":         "q'r",
		"wp_options/option_id=7/option_value":         "-1.5e3",
		"wp_usermeta/umeta_id=2,user_id=2/meta_value": "v2",
		"wp_usermeta/umeta_id=3,user_id=2/meta_key":   "k3",
		"nokey/#2/a": "2",
		"nokey/#3/a": "3",
	}
	got := map[string]string{}
	for _, r := range rows {
		for i, c := range r.Cols {
			got[r.Table+"/"+r.Key+"/"+c] = r.Vals[i].S
		}
		if r.Key == "option_id=4" {
			if v, _ := r.Get("option_value"); !v.Null {
				t.Error("NULL read as a string")
			}
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestReadPostgresDump(t *testing.T) {
	dump := "--\n-- PostgreSQL database dump\n--\n\nSET statement_timeout = 0;\n" +
		"CREATE FUNCTION public.f() RETURNS trigger LANGUAGE plpgsql AS $_$ BEGIN RAISE 'a;b'; END; $_$;\n" +
		"CREATE TABLE public.\"User\" (\n    id integer NOT NULL,\n    email text\n);\n" +
		"COPY public.\"User\" (id, email) FROM stdin;\n1\tada@example.com\n2\t\\N\n3\ttab\\there\n\\.\n\n" +
		"INSERT INTO public.notes (id, body) VALUES (1, 'it''s; fine'), (2, E'x');\n"
	var rows []Row
	st, err := ReadDump(strings.NewReader(dump), func(r Row) error { rows = append(rows, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(st.Rows); got != "map[public.User:3 public.notes:2]" {
		t.Errorf("rows %s", got)
	}
	if v, _ := rows[1].Get("email"); !v.Null {
		t.Error(`\N is not NULL`)
	}
	if v, _ := rows[2].Get("email"); v.S != "tab\there" {
		t.Errorf("COPY escape: %q", v.S)
	}
	if v, _ := rows[3].Get("body"); v.S != "it's; fine" {
		t.Errorf("doubled quote: %q", v.S)
	}
}

func TestReadDumpErrors(t *testing.T) {
	for _, bad := range []string{
		"INSERT INTO `t` VALUES (1,'unterminated",
		"INSERT INTO `t` VALUES (1,2",
		"INSERT INTO `t` VALUES 1,2);",
		"INSERT INTO `t` (a VALUES (1);",
		"/* never closed",
		"INSERT INTO `t` VALUES (0xZZ);",
	} {
		if _, err := ReadDump(strings.NewReader(bad), nil); !errors.Is(err, errSyntax) {
			t.Errorf("%q: %v, want a syntax error", bad, err)
		}
	}
	stop := errors.New("stop")
	if _, err := ReadDump(strings.NewReader(mysqlDump), func(Row) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("callback error: %v", err)
	}
}

// The reader never panics or loops on any input; a dump it writes
// itself reads back with the same values.
func FuzzReadDump(f *testing.F) {
	f.Add([]byte(mysqlDump), "x")
	f.Add([]byte("COPY a (b) FROM stdin;\n1\n\\.\n"), "it's")
	f.Add([]byte("-- PostgreSQL database dump\nCOPY a (b) FROM stdin;\n\\N\n\\.\n"), "a\\b")
	f.Add([]byte("DELIMITER ;;\nSELECT 1;;\nDELIMITER ;\n"), "\x00\n")
	f.Fuzz(func(t *testing.T, data []byte, value string) {
		_, _ = ReadDump(bytes.NewReader(data), func(Row) error { return nil })
		dump := "CREATE TABLE `t` (\n  `id` int,\n  `v` text,\n  PRIMARY KEY (`id`)\n);\n" +
			"INSERT INTO `t` VALUES (1,'" + sqlEscape(value) + "');\n"
		var got []Row
		if _, err := ReadDump(strings.NewReader(dump), func(r Row) error { got = append(got, r); return nil }); err != nil {
			t.Fatalf("own dump: %v", err)
		}
		if len(got) != 1 || len(got[0].Vals) != 2 || got[0].Vals[1].S != value {
			t.Fatalf("round trip of %q: %+v", value, got)
		}
	})
}
