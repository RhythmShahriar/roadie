// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// emails returns n made-up personal addresses and m blank-ish values.
func samplesOf(n, emails int) [][]byte {
	var out [][]byte
	for i := range n {
		if i < emails {
			out = append(out, fmt.Appendf(nil, "member%d.kowalczyk@gmail.com", i))
		} else {
			out = append(out, fmt.Appendf(nil, "note %d about the order", i))
		}
	}
	return out
}

func TestUncovered(t *testing.T) {
	tbl := &core.Table{Name: "wp_loyalty_members", Columns: []core.Column{
		{Name: "id", Type: "bigint unsigned"},
		{Name: "email", Type: "varchar(191)"},
		{Name: "notes", Type: "text"},
		{Name: "phone", Type: "varchar(40)"},
		{Name: "contact", Type: "text"},
		{Name: "covered_mail", Type: "varchar(100)"},
		{Name: "description", Type: "text"},
		{Name: "zip", Type: "int"},
	}}
	samples := map[string][][]byte{
		"email":        append(samplesOf(980, 980), samplesOf(20, 0)...),
		"notes":        samplesOf(100, 5),
		"contact":      samplesOf(100, 30),
		"covered_mail": samplesOf(10, 10),
		"description":  samplesOf(10, 0),
	}
	got := Uncovered(tbl, map[string]bool{"covered_mail": true}, samples)
	want := []core.Uncovered{
		{Table: "wp_loyalty_members", Column: "email", Rate: 98},
		{Table: "wp_loyalty_members", Column: "phone", Rate: 0},
		{Table: "wp_loyalty_members", Column: "contact", Rate: 30},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	title := UncoveredTitle(got[0], samples["email"])
	if w := "wp_loyalty_members.email looks personal (98% of 1,000 sampled values are emails) and no rule covers it"; title != w {
		t.Errorf("title\n got %s\nwant %s", title, w)
	}
	if title := UncoveredTitle(got[1], nil); title != "wp_loyalty_members.phone looks personal (by its name) and no rule covers it" {
		t.Errorf("name title %s", title)
	}
	if Uncovered(nil, nil, nil) != nil {
		t.Error("nil table")
	}
}

// Reserved and role addresses don't count as real; the sample stops at 1,000.
func TestUncoveredCountsOnlyRealValues(t *testing.T) {
	tbl := &core.Table{Name: "t", Columns: []core.Column{{Name: "c", Type: "text"}}}
	var s [][]byte
	for i := range 2000 {
		s = append(s, fmt.Appendf(nil, "user%d@example.com", i))
	}
	if got := Uncovered(tbl, nil, map[string][][]byte{"c": s}); len(got) != 0 {
		t.Errorf("reserved: %+v", got)
	}
	s = append(samplesOf(1000, 0), samplesOf(1000, 1000)...)
	if got := Uncovered(tbl, nil, map[string][][]byte{"c": s}); len(got) != 0 {
		t.Errorf("past the first 1,000: %+v", got)
	}
}

func TestPersonalName(t *testing.T) {
	for _, n := range []string{"email", "user_email", "billingPhone", "first_name", "firstName", "last_name", "surname",
		"full_name", "address_1", "street", "city", "postcode", "zip", "ip", "ip_address", "author_IP", "dob",
		"date_of_birth", "ssn", "iban", "passport_no", "mobile", "fax", "tel", "e_mail"} {
		if !PersonalName(n) {
			t.Errorf("%s is personal", n)
		}
	}
	for _, n := range []string{"description", "id", "status", "zipper_color", "option_value", "telemetry", "shipping_method",
		"address_type", "email_verified_at", "billing_address_id", "phone_status", "MAIL_DRIVER", "mail_host"} {
		if PersonalName(n) {
			t.Errorf("%s is not personal", n)
		}
	}
}
