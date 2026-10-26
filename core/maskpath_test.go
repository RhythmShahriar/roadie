// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"strings"
	"testing"
)

// Every key that came from the data is masked; a program's key stays.
func TestMaskPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                                                 "",
		"serialized:billing.email":                         "serialized:billing.email",
		"json:items[2].email":                              "json:items[2].email",
		"serialized:address_1":                             "serialized:address_1",
		"base64:serialized:login":                          "base64:serialized:login",
		"serialized:data.json:email":                       "serialized:data.json:email",
		"serialized:12":                                    "serialized:12",
		`json:ada\.kowalczyk@gmail\.com.plan`:              "json:" + MaskEmail("ada.kowalczyk@gmail.com") + ".plan",
		"json:Margarethe Kowalczyk.phone":                  "json:<key>.phone",
		"json:+44 7911 654321.name":                        "json:<key>.name",
		"json:447911654321.name":                           "json:<key>.name",
		"serialized:" + strings.Repeat("ab12", 16) + ".ip": "serialized:<key>.ip",
		"json:Zoë.city":                                    "json:<key>.city",
		"json:a1b2c3d4e5.x":                                "json:<key>.x",
		"json:k[3]":                                        "json:k[3]",
		"json:Jane Doe[1]":                                 "json:<key>[1]",
	} {
		if got := MaskPath(in); got != want {
			t.Errorf("MaskPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzMaskPath(f *testing.F) {
	for _, s := range []string{"json:a.b", `json:a\.b@c\.de.x`, "serialized:x[1].json:Jane Doe", "\\", "[", "]["} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		got := MaskPath(p)
		if strings.Count(got, ".") > strings.Count(p, ".")+strings.Count(p, "@")*2 {
			t.Errorf("MaskPath(%q) = %q added separators", p, got)
		}
		if strings.Contains(p, " ") && strings.Contains(got, " ") {
			t.Errorf("MaskPath(%q) = %q kept a key with a space", p, got)
		}
	})
}
