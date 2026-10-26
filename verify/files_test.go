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

// A host that means a different server on each machine never points at
// production just because both sides spell it the same.
func TestPointsAtProductionMachineLocal(t *testing.T) {
	cases := []struct {
		name, value, prod string
		want              bool
	}{
		{"DB_HOST", "localhost", "localhost", false},
		{"DB_HOST", "127.0.0.1", "127.0.0.1", false},
		{"DB_HOST", "127.0.0.1:3306", "127.0.0.1:3306", false},
		{"DB_HOST", "localhost:/var/run/mysqld.sock", "localhost:/var/run/mysqld.sock", false},
		{"DB_HOST", "/var/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", false},
		{"DB_HOST", "db", "db", false},
		{"DB_HOST", "db:3306", "db:3306", false},
		{"DB_HOST", "[::1]:3306", "[::1]:3306", false},
		{"DB_HOST", "169.254.10.2", "169.254.10.2", false},
		{"WP_REDIS_HOST", "127.0.0.1", "127.0.0.1", false},
		{"REDIS_HOST", "redis", "redis", false},
		{"DB_HOST", "shop-prod.abc123.eu-west-1.rds.amazonaws.com", "shop-prod.abc123.eu-west-1.rds.amazonaws.com", true},
		{"DB_HOST", "10.0.4.12", "10.0.4.12", true},
		{"WP_REDIS_HOST", "cache.shop.example.net", "cache.shop.example.net", true},
		{"AWS_BUCKET", "shop", "shop", true},
		{"DB_HOST", "staging-db.internal.example.net", "shop-prod.abc123.eu-west-1.rds.amazonaws.com", false},
	}
	for _, c := range cases {
		got := pointsAtProduction(core.Pair{Name: c.name, Value: c.value}, c.prod, true, nil)
		if got != c.want {
			t.Errorf("%s=%q vs production %q: got %v, want %v", c.name, c.value, c.prod, got, c.want)
		}
	}
	// A machine-local value still matches a listed production host.
	if !pointsAtProduction(core.Pair{Name: "DB_HOST", Value: "db"}, "db", true, []string{"db"}) {
		t.Error("db listed in production: not matched")
	}
}

// A machine-local DB_HOST equal on both sides is production's database
// when the database name is production's too: a staging copy on
// production's own server.
func TestSameLocalDatabase(t *testing.T) {
	prod := map[string]string{"DB_HOST": "localhost", "DB_NAME": "shop_wp", "DB_USER": "shop"}
	pairs := func(kv ...string) []core.Pair {
		var out []core.Pair
		for i := 0; i < len(kv); i += 2 {
			out = append(out, core.Pair{Name: kv[i], Value: kv[i+1], Line: i/2 + 1})
		}
		return out
	}
	cases := []struct {
		name  string
		pairs []core.Pair
		prod  map[string]string
		want  string
	}{
		{"same name and user", pairs("DB_HOST", "localhost", "DB_NAME", "shop_wp", "DB_USER", "shop"), prod, "DB_NAME"},
		{"same name, no user here", pairs("DB_HOST", "localhost", "DB_NAME", "shop_wp"), prod, "DB_NAME"},
		{"socket", pairs("DB_HOST", "localhost:/run/mysqld.sock", "DB_NAME", "shop_wp"),
			map[string]string{"DB_HOST": "localhost:/run/mysqld.sock", "DB_NAME": "shop_wp"}, "DB_NAME"},
		{"laravel", pairs("DB_HOST", "127.0.0.1", "DB_DATABASE", "shop", "DB_USERNAME", "shop"),
			map[string]string{"DB_HOST": "127.0.0.1", "DB_DATABASE": "shop", "DB_USERNAME": "shop"}, "DB_DATABASE"},
		{"own database", pairs("DB_HOST", "localhost", "DB_NAME", "shop_staging", "DB_USER", "shop"), prod, ""},
		{"own user", pairs("DB_HOST", "localhost", "DB_NAME", "shop_wp", "DB_USER", "staging"), prod, ""},
		{"other local host", pairs("DB_HOST", "127.0.0.1", "DB_NAME", "shop_wp"), prod, ""},
		{"remote host", pairs("DB_HOST", "db.shop.com", "DB_NAME", "shop_wp"),
			map[string]string{"DB_HOST": "db.shop.com", "DB_NAME": "shop_wp"}, ""},
		{"no name in production", pairs("DB_HOST", "localhost", "DB_NAME", "shop_wp"), map[string]string{"DB_HOST": "localhost"}, ""},
		{"empty name", pairs("DB_HOST", "localhost", "DB_NAME", ""), map[string]string{"DB_HOST": "localhost", "DB_NAME": ""}, ""},
		{"no production pairs", pairs("DB_HOST", "localhost", "DB_NAME", "shop_wp"), nil, ""},
	}
	for _, c := range cases {
		p, ok := sameLocalDatabase(c.pairs, c.prod)
		if got := map[bool]string{true: p.Name}[ok]; got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
