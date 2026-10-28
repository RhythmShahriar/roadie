// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The strings inside a define built from an expression come back as
// pairs named by the define and their array key, each on its own line;
// a literal define and the array keys themselves don't. SetMember
// changes one of them alone.
func TestMemberPairs(t *testing.T) {
	src := "<?php\n" +
		"define( 'PLAIN', 'x' );\n" +
		"define( 'AS3CF_SETTINGS', serialize( array(\n" +
		"\t'provider' => 'aws',\n" +
		"\t'secret-access-key' => \"s3cr\\\"et\",\n" +
		"\t0 => 'zero',\n" +
		") ) );\n" +
		"define( 'FROM_ENV', getenv( 'X' ) ?: 'fallback' );\n"
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := f.MemberPairs()
	want := []core.Pair{
		{Name: "AS3CF_SETTINGS.provider", Value: "aws", Line: 4, Quoted: true},
		{Name: "AS3CF_SETTINGS.secret-access-key", Value: "s3cr\"et", Line: 5, Quoted: true},
		{Name: "AS3CF_SETTINGS.0", Value: "zero", Line: 6, Quoted: true},
		{Name: "FROM_ENV", Value: "X", Line: 8, Quoted: true},
		{Name: "FROM_ENV", Value: "fallback", Line: 8, Quoted: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if err := f.SetMember("AS3CF_SETTINGS.secret-access-key", 5, ""); err != nil {
		t.Fatal(err)
	}
	out := string(f.Bytes())
	if out != strings.Replace(src, `"s3cr\"et"`, `""`, 1) {
		t.Errorf("edited:\n%s", out)
	}
	if err := f.SetMember("AS3CF_SETTINGS.nope", 5, ""); err == nil {
		t.Error("an unknown member was set")
	}
	if p, ok := f.Get("PLAIN"); !ok || p.Value != "x" {
		t.Errorf("PLAIN %v", p)
	}
}
