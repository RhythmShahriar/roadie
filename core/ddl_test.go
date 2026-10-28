// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"testing"
)

func TestDDLLiterals(t *testing.T) {
	text := []byte("CREATE TABLE `it's` (\n  `a` varchar(9) DEFAULT 'pub''lish',\n  -- a 'comment'\n  `b` text COMMENT 'ask o\\'neil@x.com' /* 'no' */,\n" +
		"  `c` int /*!40101 DEFAULT '7' */\n) COMMENT=\"x\" ENGINE=InnoDB;")
	var got []string
	for _, l := range DDLLiterals(text) {
		got = append(got, string(text[l[0]:l[1]]))
	}
	want := []string{"pub''lish", `ask o\'neil@x.com`, "7"}
	if len(got) != len(want) {
		t.Fatalf("literals %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("literal %d: %q, want %q", i, got[i], want[i])
		}
	}
	if l := DDLLiterals([]byte("CREATE TABLE t (a text DEFAULT 'cut")); len(l) != 0 {
		t.Errorf("unclosed literal: %v", l)
	}
}

func TestDDLTable(t *testing.T) {
	for in, want := range map[string]string{
		"CREATE TABLE `wp_users` (`a` int)":                    "wp_users",
		"CREATE TABLE IF NOT EXISTS shop.`wp_users` (a int)":   "wp_users",
		"ALTER TABLE `wp_users` ADD COLUMN `z` varchar(9)":     "wp_users",
		`ALTER TABLE ONLY public."customers" ADD CONSTRAINT x`: "customers",
		"SELECT 1": "",
	} {
		if got := DDLTable([]byte(in)); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	if !IsDDL(StmtCreateTable) || !IsDDL("alter-table") || IsDDL(StmtAlterKeys) {
		t.Error("IsDDL")
	}
}
