// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpdb

import (
	"context"
	"errors"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func optionsTable() *core.Table {
	return &core.Table{Name: "shop_options", Columns: []core.Column{{Name: "option_id"}, {Name: "option_name"}, {Name: "option_value"}, {Name: "autoload"}}}
}

func str(s string) core.Value { return core.Value{Kind: core.String, Raw: []byte(s)} }

func TestOptions(t *testing.T) {
	s := testutil.FakeSite(nil)
	s.Identity.Prefix = "shop_"
	var asked []string
	s.SourceFunc = func(tables []string) (core.Source, error) {
		asked = tables
		tb := optionsTable()
		return testutil.FakeSource(nil, []*core.Batch{
			{Seq: 1, Pass: &core.Statement{Kind: core.StmtCreateTable}},
			{Seq: 2, Table: tb, Rows: []core.Row{
				{Values: []core.Value{{Kind: core.Number, Raw: []byte("1")}, str("siteurl"), str("https://staging.shop.test"), str("yes")}},
				{Values: []core.Value{{Kind: core.Number, Raw: []byte("2")}, str("blogname"), str("Shop"), str("yes")}},
			}},
			// A batch naming its columns: only name and value, swapped.
			{Seq: 3, Table: tb, Columns: []int{2, 1}, Rows: []core.Row{
				{Values: []core.Value{{Kind: core.Null}, str("home")}},
			}},
		}), nil
	}
	got, err := Options(context.Background(), s, "siteurl", "home", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "shop_options" {
		t.Fatalf("asked for %v", asked)
	}
	if string(got["siteurl"]) != "https://staging.shop.test" || got["home"] != nil || len(got) != 2 {
		t.Fatalf("%q", got)
	}
	if _, ok := got["blogname"]; ok {
		t.Fatal("an option nobody asked for")
	}
}

func TestPrefixDefault(t *testing.T) {
	s := testutil.FakeSite(nil)
	s.Identity.Prefix = ""
	if Prefix(context.Background(), s) != "wp_" {
		t.Fatal("default prefix")
	}
}

func TestRowsError(t *testing.T) {
	s := testutil.FakeSite(nil)
	boom := errors.New("boom")
	s.SourceFunc = func([]string) (core.Source, error) { return nil, boom }
	if err := Rows(context.Background(), s, "x", nil, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	s.SourceFunc = func([]string) (core.Source, error) {
		src := testutil.FakeSource(nil, nil)
		src.Err = boom
		return src, nil
	}
	if err := Rows(context.Background(), s, "x", nil, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

// A table the database doesn't have yields no rows and Has says so; any
// other error still stops.
func TestMissingTable(t *testing.T) {
	s := testutil.FakeSite(nil)
	s.SourceFunc = func(tables []string) (core.Source, error) {
		return nil, &db.NoTableError{DB: "staging", Table: tables[0]}
	}
	ctx := context.Background()
	if err := Rows(ctx, s, "wp_wc_webhooks", nil, func([][]byte) error { t.Error("a row"); return nil }); err != nil {
		t.Errorf("rows: %v", err)
	}
	if has, err := Has(ctx, s, "wp_automatewoo_queue"); has || err != nil {
		t.Errorf("has: %v %v", has, err)
	}
	s.SourceFunc = func([]string) (core.Source, error) { return testutil.FakeSource(nil, nil), nil }
	if has, err := Has(ctx, s, "wp_options"); !has || err != nil {
		t.Errorf("has wp_options: %v %v", has, err)
	}
	if !Missing(errors.New("remote wp db export exited 2: mysqldump: Couldn't find table: \"wp_blogs\"")) || Missing(errors.New("boom")) || Missing(nil) {
		t.Error("Missing")
	}
}
