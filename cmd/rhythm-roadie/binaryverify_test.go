// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// binaryEnv runs in a temp project with the real deps and no network.
func binaryEnv(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	testutil.NoNetwork(t)
}

// binarySecrets are a production email and a live Stripe key, built at
// run time.
func binarySecrets() (email, key string) {
	return testutil.J("fiona.mcleod", "@gmail.com"), testutil.J("sk_", "live_", "Ab3Cd4Ef5Gh6Ij7Kl8Mn9Op0Qr1St2")
}

// pgByteaDump is a pg_dump -Fp dump whose settings.val is bytea holding
// email and key, as COPY or (inserts) as INSERT statements.
func pgByteaDump(email, key string, inserts bool) string {
	h := func(s string) string { return hex.EncodeToString([]byte(s)) }
	var b strings.Builder
	b.WriteString("--\n-- PostgreSQL database dump\n--\n\nSET standard_conforming_strings = on;\n\n" +
		"CREATE TABLE public.customers (\n    id integer NOT NULL,\n    email text,\n    name text\n);\n\n" +
		"CREATE TABLE public.settings (\n    id integer NOT NULL,\n    name text,\n    val bytea,\n    note text\n);\n\n")
	if inserts {
		b.WriteString("INSERT INTO public.customers VALUES (1, '" + email + "', 'Fiona McLeod');\n\n" +
			"INSERT INTO public.settings VALUES (1, 'alert', '\\x" + h(email) + "', 'x');\n" +
			"INSERT INTO public.settings VALUES (2, 'stripe', '\\x" + h(key) + "', 'y');\n\n")
	} else {
		b.WriteString("COPY public.customers (id, email, name) FROM stdin;\n1\t" + email + "\tFiona McLeod\n\\.\n\n" +
			"COPY public.settings (id, name, val, note) FROM stdin;\n" +
			"1\talert\t\\\\x" + h(email) + "\tx\n2\tstripe\t\\\\x" + h(key) + "\ty\n\\.\n\n")
	}
	b.WriteString("--\n-- PostgreSQL database dump complete\n--\n\n")
	return b.String()
}

const byteaConfig = `stack: sql
columns:
  - table: customers
    column: email
    rule: email
  - table: customers
    column: name
    rule: full_name
`

// A PostgreSQL bytea column holding a production email and a live key:
// verify of the dump fails it, copy takes both out of the bytea, and an
// exact verify of a copy that still holds them fails, never a false PASS.
func TestPGByteaCopyAndVerify(t *testing.T) {
	binaryEnv(t)
	email, key := binarySecrets()
	if err := os.WriteFile("cfg.yaml", []byte(byteaConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, inserts := range []bool{false, true} {
		in := pgByteaDump(email, key, inserts)
		if err := os.WriteFile("pg2.sql", []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := runCmd(t, "verify", "pg2.sql", "--production", "shop.example.com", "--offline", "--json")
		if ids := findingIDs(mustData(t, out)); code != 1 || !ids["key.stripe-live"] || !ids["pii.email"] {
			t.Errorf("inserts=%v: verify of the input: exit %d %v\n%s", inserts, code, ids, errOut)
		}
		out, errOut, code = runCmd(t, "copy", "--from", "pg2.sql", "--to", "out.sql", "--config", "cfg.yaml",
			"--url", "https://staging.example.test", "--yes", "--offline", "--json")
		if code != 0 {
			t.Fatalf("inserts=%v: copy: exit %d\n%s%s", inserts, code, out, errOut)
		}
		b, err := os.ReadFile("out.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{email, key} {
			if h := hex.EncodeToString([]byte(s)); strings.Contains(string(b), h) || strings.Contains(string(b), s) {
				t.Errorf("inserts=%v: the copy keeps %q:\n%s", inserts, s[:6], b)
			}
		}
		// The input as the "copy": exact mode must fail it.
		out, errOut, code = runCmd(t, "verify", "pg2.sql", "--from", "pg2.sql", "--production", "shop.example.com", "--offline", "--json")
		if ids := findingIDs(mustData(t, out)); code != 1 || !ids["key.stripe-live"] {
			t.Errorf("inserts=%v: exact verify of a leaking copy: exit %d %v\n%s", inserts, code, ids, errOut)
		}
	}
}

// mysqldump --hex-blob writes a longblob as 0x...: a standalone verify
// and an exact verify read its bytes and fail an email and a live key.
func TestVerifyHexBlobDump(t *testing.T) {
	binaryEnv(t)
	email, key := binarySecrets()
	h := func(s string) string { return "0x" + strings.ToUpper(hex.EncodeToString([]byte(s))) }
	dump := "-- MySQL dump 10.13  Distrib 8.4.0, for Linux (x86_64)\n-- Server version\t8.4.0\n" +
		"/*!40101 SET NAMES utf8mb4 */;\nDROP TABLE IF EXISTS `wp_wfconfig`;\n" +
		"CREATE TABLE `wp_wfconfig` (\n  `name` varchar(100) NOT NULL,\n  `val` longblob,\n  PRIMARY KEY (`name`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n" +
		"LOCK TABLES `wp_wfconfig` WRITE;\n" +
		"INSERT INTO `wp_wfconfig` VALUES ('alertEmails'," + h(email) + "),('apiKey'," + h(key) + ");\n" +
		"UNLOCK TABLES;\n-- Dump completed on 2026-10-26 10:00:00\n"
	if err := os.WriteFile("real-hexblob.sql", []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"real-hexblob.sql", "--production", "shop.example.com", "--offline", "--json"},
		{"real-hexblob.sql", "--from", "real-hexblob.sql", "--production", "shop.example.com", "--offline", "--json"},
	} {
		out, errOut, code := runCmd(t, "verify", args...)
		data := mustData(t, out)
		if ids := findingIDs(data); code != 1 || !ids["key.stripe-live"] || !ids["pii.email"] || data["complete"] == true && data["result"] == "pass" {
			t.Errorf("%v: exit %d %v\n%s", args, code, ids, errOut)
		}
	}
}

func mustData(t *testing.T, out string) map[string]any {
	t.Helper()
	data, _ := jsonData(t, out)
	return data
}
