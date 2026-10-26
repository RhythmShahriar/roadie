// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"strings"
	"testing"
)

// formTables is a dump in mysqldump's order: Gravity Forms entries come
// before the form that says which field is a name; WPForms and Ninja
// Forms definitions come before their entries.
func formTables() []tableData {
	gfEntry := table("wp_gf_entry", "id:int(10)", "form_id:int(10)")
	gfMeta := table("wp_gf_entry_meta", "id:bigint(20)", "form_id:int(10)", "entry_id:int(10)", "meta_key", "meta_value")
	gfForm := table("wp_gf_form_meta", "form_id:int(10)", "display_meta")
	nfFields := table("wp_nf3_fields", "id:int(11)", "type")
	posts := table("wp_posts", "ID:bigint(20)", "post_type", "post_content")
	pm := table("wp_postmeta", "meta_id:bigint(20)", "post_id:bigint(20)", "meta_key", "meta_value")
	wpf := table("wp_wpforms_entries", "entry_id:bigint(20)", "form_id:bigint(20)", "fields")
	return []tableData{wpBase(),
		{gfEntry, rows{{1, 1}}},
		{gfMeta, rows{{1, 1, 1, "1.3", "Cleo"}, {2, 1, 1, "1.6", "Marsh"}, {3, 1, 1, "2", "hello there"}, {4, 1, 1, "payment_status", "Paid"}}},
		{gfForm, rows{{1, `{"fields":[{"id":1,"type":"name","inputs":[{"id":"1.3"},{"id":"1.6"}]},{"id":2,"type":"textarea"}]}`}}},
		{nfFields, rows{{5, "firstname"}, {6, "textarea"}}},
		{posts, rows{{9, "wpforms", `{"fields":{"1":{"id":"1","type":"name"}}}`}}},
		{pm, rows{{1, 20, "_field_5", "Dora"}, {2, 20, "_field_6", "long note"}}},
		{wpf, rows{{1, 9, `{"1":{"first":"Ezra","last":"Quill","value":"Ezra Quill"}}`}}},
	}
}

// The values the field-type rules fake reach Known before it is sealed,
// whether the form definition came before its entries or after; values
// of text fields and of other meta don't.
func TestFormEntriesReachKnown(t *testing.T) {
	f := useFakes(t)
	p, _, _ := fixture(t, Copy, "wp_", formTables, nil, Options{})
	run(t, p)
	for _, v := range []string{"Cleo", "Marsh", "Dora", "Ezra", "Quill"} {
		if _, _, ok := f.known.Contains([]byte(v)); !ok {
			t.Errorf("%s isn't in Known", v)
		}
	}
	for _, v := range []string{"hello there", "Paid", "long note"} {
		if _, _, ok := f.known.Contains([]byte(v)); ok {
			t.Errorf("%s is in Known", v)
		}
	}
}

// --one-pass seals Known before the field-type rules exist: it keeps no
// entry values at all.
func TestFormEntriesOnePass(t *testing.T) {
	useFakes(t)
	p, _, _ := fixture(t, Copy, "wp_", formTables, nil, Options{OnePass: true})
	idx := newIndexer(t.Context(), p.cfg, p.defs, nil, nil)
	if idx.entries != nil {
		t.Error("an indexer without Known keeps entries")
	}
	run(t, p)
}

// The kept values stop at the budget; the rest are counted.
func TestFormEntriesBudget(t *testing.T) {
	e := &formEntries{}
	big := strings.Repeat("x", entryMaxValue-1) // plus one letter: the longest value kept
	isBlog := func(table, suffix string) bool { return table == "wp_"+suffix }
	n := entryBudget/(entryMaxValue+entryOverhead) + 3
	for i := range n {
		e.observe("wp_wpforms_entry_fields", []string{"wpforms-field-types"}, isBlog, getter(map[string]string{
			"form_id": "1", "field_id": "2", "value": big + string(rune('a'+i%26))}))
	}
	if e.bytes > entryBudget || e.skipped == 0 || len(e.list)+int(e.skipped) != n {
		t.Errorf("bytes %d, kept %d, skipped %d of %d", e.bytes, len(e.list), e.skipped, n)
	}
	// Too long, empty, the wrong table or no fix: nothing kept.
	e = &formEntries{}
	for _, c := range []struct {
		table string
		fixes []string
		vals  map[string]string
	}{
		{"wp_wpforms_entry_fields", []string{"wpforms-field-types"}, map[string]string{"form_id": "1", "field_id": "2", "value": big + "yz"}},
		{"wp_wpforms_entry_fields", []string{"wpforms-field-types"}, map[string]string{"form_id": "1", "field_id": "2", "value": " "}},
		{"wp_wpforms_entry_fields", []string{"wpforms-field-types"}, map[string]string{"form_id": "1", "value": "a"}},
		{"wp_wpforms_entry_fields", []string{"gf-field-types"}, map[string]string{"form_id": "1", "field_id": "2", "value": "a"}},
		{"wp_gf_entry_meta", []string{"gf-field-types"}, map[string]string{"form_id": "1", "meta_key": "payment_status", "meta_value": "a"}},
		{"wp_postmeta", []string{"ninja-field-types"}, map[string]string{"meta_key": "_field_x", "meta_value": "a"}},
		{"wp_postmeta", []string{"ninja-field-types"}, map[string]string{"meta_key": "_field_5.1", "meta_value": "a"}},
		{"wp_12_gf_entry_meta", []string{"gf-field-types"}, map[string]string{"form_id": "1", "meta_key": "3", "meta_value": "a"}},
	} {
		e.observe(c.table, c.fixes, isBlog, getter(c.vals))
	}
	if len(e.list) != 0 {
		t.Errorf("kept %+v", e.list)
	}
}

// getter turns a map into a row reader.
func getter(m map[string]string) func(string) ([]byte, bool) {
	return func(c string) ([]byte, bool) { v, ok := m[c]; return []byte(v), ok }
}

func TestFieldNumber(t *testing.T) {
	for _, c := range []struct {
		k, prefix string
		want      bool
	}{
		{"3", "", true}, {"3.6", "", true}, {"3.", "", false}, {".6", "", false}, {"a", "", false}, {"", "", false},
		{"_field_5", "_field_", true}, {"_field_5.1", "_field_", false}, {"_field_", "_field_", false}, {"_field_x", "_field_", false},
	} {
		if got := fieldNumber([]byte(c.k), c.prefix); got != c.want {
			t.Errorf("fieldNumber(%q, %q) = %v", c.k, c.prefix, got)
		}
	}
}
