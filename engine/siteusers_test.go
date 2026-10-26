// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// networkUsers is blogTables with users: root is a super admin on no
// blog, fr belongs to blog 2, de to blog 3, main to blog 1 and left had
// blog 2's roles taken away. adminsLast puts sitemeta after the users.
func networkUsers(adminsLast bool) func() []tableData {
	return func() []tableData {
		sitemeta := tableData{table("wp_sitemeta", "meta_id:bigint(20)", "site_id:bigint(20)", "meta_key", "meta_value"),
			rows{{1, 1, "site_admins", `a:1:{i:0;s:4:"root";}`}}}
		users := tableData{table("wp_users", "ID:bigint(20)", "user_login", "user_email"), rows{
			{1, "root", "root@example.com"}, {2, "fr", "fr@example.com"}, {3, "de", "de@example.com"},
			{4, "main", "main@example.com"}, {5, "left", "left@example.com"}}}
		meta := tableData{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value"), rows{
			{1, 1, "nickname", "root"},
			{2, 2, "wp_2_capabilities", `a:1:{s:8:"customer";b:1;}`}, {3, 2, "nickname", "fr"},
			{4, 3, "wp_3_capabilities", `a:1:{s:8:"customer";b:1;}`}, {5, 3, "nickname", "de"},
			{6, 4, "wp_capabilities", `a:1:{s:6:"editor";b:1;}`},
			{7, 5, "wp_2_capabilities", "a:0:{}"}, {8, 5, "nickname", "left"}}}
		out := blogTables()
		if adminsLast {
			return append(out, meta, users, sitemeta)
		}
		return append(out, sitemeta, meta, users)
	}
}

// written returns the IDs of the rows of table the sink got and didn't
// drop, by the named column.
func written(sink *testutil.Sink, table, col string) []string {
	var ids []string
	for _, b := range sink.Batches() {
		if b.Table == nil || b.Table.Name != table {
			continue
		}
		ci := b.Table.Col(col)
		for _, r := range b.Rows {
			for i, c := range b.Columns {
				if c == ci && !r.Drop {
					ids = append(ids, string(r.Values[i].Raw))
				}
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// copy --site keeps the members of the chosen blogs (and of the main
// site) plus the super admins and drops every other user with their
// meta, whichever order the dump has the tables in.
func TestPipelineSiteUsers(t *testing.T) {
	useFakes(t)
	for _, last := range []bool{false, true} {
		p, sink, _ := fixture(t, Copy, "wp_", networkUsers(last), nil, Options{URL: "https://staging.shop.test", Sites: []int{2}})
		out := run(t, p)
		if got := written(sink, "wp_users", "ID"); !slices.Equal(got, []string{"1", "2", "4"}) {
			t.Errorf("adminsLast %v: users %v", last, got)
		}
		if got := written(sink, "wp_usermeta", "user_id"); !slices.Equal(got, []string{"1", "2", "4"}) {
			t.Errorf("adminsLast %v: usermeta of %v", last, got)
		}
		var users, meta int64
		for _, c := range out.Result.Changes {
			if c.Rule == siteUserRule && c.Action == core.DeleteRow {
				switch c.Table {
				case "wp_users":
					users = c.Rows
				case "wp_usermeta":
					meta = c.Rows
				}
			}
		}
		if users != 2 || meta != 4 {
			t.Errorf("adminsLast %v: changes %d users, %d meta: %+v", last, users, meta, out.Result.Changes)
		}
	}
	// Without --site every user stays.
	p, sink, _ := fixture(t, Copy, "wp_", networkUsers(false), nil, Options{URL: "https://staging.shop.test"})
	run(t, p)
	if got := written(sink, "wp_users", "ID"); len(got) != 5 {
		t.Errorf("no --site: users %v", got)
	}
	// Without a usermeta table membership is unknown: nobody is left out.
	noMeta := func() []tableData {
		return slices.DeleteFunc(networkUsers(false)(), func(td tableData) bool { return td.t.Name == "wp_usermeta" })
	}
	p, sink, _ = fixture(t, Copy, "wp_", noMeta, nil, Options{URL: "https://staging.shop.test", Sites: []int{2}})
	run(t, p)
	if got := written(sink, "wp_users", "ID"); len(got) != 5 {
		t.Errorf("no usermeta: users %v", got)
	}
}

func TestSerializedStrings(t *testing.T) {
	for in, want := range map[string][]string{
		`a:2:{i:0;s:4:"root";i:1;s:6:"ad"m"n";}`: {"root", `ad"m"n`},
		`a:0:{}`:                                 nil,
		``:                                       nil,
		`s:9:"short";s:2:"ok";`:                  {"ok"},
		`s:-1:"x";s:x:"y";s:`:                    nil,
	} {
		if got := serializedStrings([]byte(in)); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func FuzzSerializedStrings(f *testing.F) {
	for _, s := range []string{`a:1:{i:0;s:4:"root";}`, `s:`, `s:3:"ab`, `s:0:"";`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, v []byte) {
		for _, s := range serializedStrings(v) {
			if !strings.Contains(string(v), s) {
				t.Fatalf("%q isn't in the input", s)
			}
		}
	})
}
