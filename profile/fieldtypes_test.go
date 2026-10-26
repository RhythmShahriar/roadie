// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// formSchema is a WordPress schema with the form plugins' tables on the
// main site and blog 2.
func formSchema() *core.Schema {
	s := &core.Schema{Dialect: core.MySQL, Prefix: "wp_"}
	add := func(name string, cols ...string) {
		t := &core.Table{Name: name}
		for _, c := range cols {
			t.Columns = append(t.Columns, core.Column{Name: c, Type: "longtext"})
		}
		s.Tables = append(s.Tables, t)
	}
	for _, b := range []string{"wp_", "wp_2_"} {
		add(b+"options", "option_name", "option_value")
		add(b+"posts", "ID", "post_type", "post_content")
		add(b+"postmeta", "meta_id", "post_id", "meta_key", "meta_value")
		add(b+"gf_form", "id", "title")
		add(b+"gf_form_meta", "form_id", "display_meta")
		add(b+"gf_entry_meta", "id", "form_id", "entry_id", "meta_key", "meta_value")
		add(b+"wpforms_entries", "entry_id", "form_id", "fields")
		add(b+"wpforms_entry_fields", "id", "form_id", "field_id", "value")
		add(b+"nf3_forms", "id")
		add(b+"nf3_fields", "id", "type", "parent_id")
	}
	return s
}

// row turns a map into the get func Observe and MatchWhere take.
func row(m map[string]string) func(string) ([]byte, bool) {
	return func(c string) ([]byte, bool) { v, ok := m[c]; return []byte(v), ok }
}

// ruleFor returns the rule the transform would pick for a value of
// column on row: the first in Rules order whose where clause holds.
func ruleFor(c *Compiled, table, column string, r map[string]string) core.Rule {
	for _, rule := range c.Rules(table) {
		if rule.Column == column && rule.Path == "" &&
			MatchWhere(rule, func(col string) (string, bool) { v, ok := r[col]; return v, ok }) {
			return rule
		}
	}
	return core.Rule{}
}

func compileForms(t *testing.T, cfg *roadiecfg.Config) *Compiled {
	t.Helper()
	if cfg == nil {
		cfg = roadiecfg.Default()
	}
	c, err := Compile(nil, mustBuiltin(t), formSchema(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"gravity-forms", "wpforms", "ninja-forms"} {
		if !slices.Contains(c.Profiles(), p) {
			t.Fatalf("%s not detected: %v", p, c.Profiles())
		}
	}
	return c
}

// Two Gravity Forms forms keep the email in different fields: field 3 of
// form 1 and field 5 of form 2. Each entry value gets the rule of its own
// form's field; a value of a field the form doesn't type keeps the
// fallback scrubber.
func TestGravityFormsFieldTypes(t *testing.T) {
	c := compileForms(t, nil)
	ft := NewFieldTypes(c)
	if !ft.Wants("wp_gf_form_meta") || !ft.Wants("wp_2_gf_form_meta") || ft.Wants("wp_12_gf_form_meta") || ft.Wants("wp_gf_entry_meta") {
		t.Fatal("Wants picks the wrong tables")
	}
	ft.Observe("wp_gf_form_meta", row(map[string]string{"form_id": "1", "display_meta": `{"fields":[
		{"id":3,"type":"email"},{"id":"4","type":"phone"},
		{"id":1,"type":"name","inputs":[{"id":"1.2"},{"id":"1.3"},{"id":"1.6"}]},
		{"id":7,"type":"address","inputs":[{"id":"7.1"},{"id":"7.3"},{"id":"7.5"},{"id":"7.6"}]},
		{"id":8,"type":"name"},{"id":9,"type":"checkbox"},{"id":"1*","type":"email"},{"id":10,"type":"date"}]}`}))
	ft.Observe("wp_gf_form_meta", row(map[string]string{"form_id": "2", "display_meta": `{"fields":[{"id":5,"type":"email"},{"id":3,"type":"textarea"}]}`}))
	ft.Observe("wp_2_gf_form_meta", row(map[string]string{"form_id": "1", "display_meta": `{"fields":[{"id":2,"type":"email"}]}`}))
	ft.Observe("wp_gf_form_meta", row(map[string]string{"form_id": "3", "display_meta": `not json`}))
	if ft.Errors != 1 {
		t.Errorf("errors %d, want 1", ft.Errors)
	}
	c = c.WithRules(ft.Rules())
	cases := []struct {
		table, form, key string
		action           core.Action
		kind             core.FakeKind
	}{
		{"wp_gf_entry_meta", "1", "3", core.Fake, core.KEmail},
		{"wp_gf_entry_meta", "2", "5", core.Fake, core.KEmail},
		{"wp_gf_entry_meta", "2", "3", core.Scrub, core.KText}, // a textarea in form 2
		{"wp_gf_entry_meta", "1", "5", core.Scrub, core.KText}, // form 1 has no field 5: fallback
		{"wp_gf_entry_meta", "1", "4", core.Fake, core.KPhone},
		{"wp_gf_entry_meta", "1", "1.3", core.Fake, core.KFirstName},
		{"wp_gf_entry_meta", "1", "1.6", core.Fake, core.KLastName},
		{"wp_gf_entry_meta", "1", "1.2", core.Scrub, core.KText}, // the name prefix
		{"wp_gf_entry_meta", "1", "7.1", core.Fake, core.KStreet},
		{"wp_gf_entry_meta", "1", "7.6", core.Keep, ""}, // country, kept by default
		{"wp_gf_entry_meta", "1", "8", core.Fake, core.KFullName},
		{"wp_gf_entry_meta", "1", "9", core.Scrub, core.KText},
		{"wp_gf_entry_meta", "1", "10", core.Fake, core.KDOB},
		{"wp_gf_entry_meta", "2", "10", core.Scrub, core.KText}, // form 2 has no date field
		{"wp_2_gf_entry_meta", "1", "2", core.Fake, core.KEmail},
		{"wp_2_gf_entry_meta", "1", "3", core.Scrub, core.KText}, // blog 2's form 1 differs
	}
	for _, tc := range cases {
		r := ruleFor(c, tc.table, "meta_value", map[string]string{"form_id": tc.form, "meta_key": tc.key, "meta_value": "x"})
		if r.Action != tc.action || r.Fake != tc.kind {
			t.Errorf("%s form %s field %s: %s %s (%s), want %s %s", tc.table, tc.form, tc.key, r.Action, r.Fake, r.ID, tc.action, tc.kind)
		}
	}
	// The rules are in the hash; adding them twice changes nothing.
	again := c.WithRules(ft.Rules())
	if again.SHA256() != c.SHA256() || compileForms(t, nil).SHA256() == c.SHA256() {
		t.Error("SHA256 doesn't follow the field rules")
	}
}

// A columns: rule on gf_entry_meta.meta_value wins over the field rules,
// and free_text: blank turns the text fields into blanks.
func TestFieldTypesPolicies(t *testing.T) {
	cfg := roadiecfg.Default()
	cfg.Columns = []roadiecfg.ColumnRule{{Table: "wp_gf_entry_meta", Column: "meta_value", Rule: "blank"}}
	cfg.Pseudonymise.FreeText = "blank"
	c := compileForms(t, cfg)
	ft := NewFieldTypes(c)
	ft.Observe("wp_gf_form_meta", row(map[string]string{"form_id": "1", "display_meta": `{"fields":[{"id":3,"type":"email"}]}`}))
	ft.Observe("wp_nf3_fields", row(map[string]string{"id": "4", "type": "textarea", "parent_id": "1"}))
	c = c.WithRules(ft.Rules())
	if r := ruleFor(c, "wp_gf_entry_meta", "meta_value", map[string]string{"form_id": "1", "meta_key": "3"}); r.ID != "config.wp_gf_entry_meta.meta_value" {
		t.Errorf("config rule lost: %s", r.ID)
	}
	if r := ruleFor(c, "wp_postmeta", "meta_value", map[string]string{"meta_key": "_field_4"}); r.Action != core.Blank || r.Profile != "ninja-forms" {
		t.Errorf("free_text blank: %+v", r)
	}
}

// WPForms fields come from the form post's JSON: entry_fields rows by
// form and field ID, the entries JSON by path.
func TestWPFormsFieldTypes(t *testing.T) {
	c := compileForms(t, nil)
	ft := NewFieldTypes(c)
	ft.Observe("wp_posts", row(map[string]string{"ID": "12", "post_type": "wpforms", "post_content": `{"fields":{
		"1":{"id":"1","type":"name"},"2":{"id":"2","type":"email"},"3":{"id":3,"type":"address"},"4":{"id":"4","type":"select"}}}`}))
	ft.Observe("wp_posts", row(map[string]string{"ID": "13", "post_type": "page", "post_content": `{"fields":{"2":{"id":"2","type":"email"}}}`}))
	ft.Observe("wp_posts", row(map[string]string{"ID": "14", "post_type": "wpforms", "post_content": `{"fields":[{"id":"6","type":"phone"}]}`}))
	c = c.WithRules(ft.Rules())
	for _, tc := range []struct {
		form, field string
		kind        core.FakeKind
	}{{"12", "2", core.KEmail}, {"12", "1", core.KFullName}, {"12", "3", core.KAddress}, {"14", "6", core.KPhone}, {"13", "2", core.KText}, {"12", "4", core.KText}} {
		r := ruleFor(c, "wp_wpforms_entry_fields", "value", map[string]string{"form_id": tc.form, "field_id": tc.field})
		if r.Fake != tc.kind {
			t.Errorf("form %s field %s: %s (%s), want %s", tc.form, tc.field, r.Fake, r.ID, tc.kind)
		}
	}
	paths := map[string]core.FakeKind{}
	for _, r := range c.Rules("wp_wpforms_entries") {
		if r.Column == "fields" && r.Path != "" && r.Where["form_id"] == "12" {
			paths[r.Path] = r.Fake
		}
	}
	for p, k := range map[string]core.FakeKind{"json:2.value": core.KEmail, "json:1.value": core.KFullName, "json:1.first": core.KFirstName,
		"json:1.last": core.KLastName, "json:3.city": core.KCity, "json:3.postal": core.KPostcode, "json:3.value": core.KText} {
		if paths[p] != k {
			t.Errorf("path %s: %q, want %s", p, paths[p], k)
		}
	}
	if _, ok := paths["json:4.value"]; ok {
		t.Error("an untyped select field got a rule")
	}
}

// Ninja Forms fields are numbered across forms; postmeta _field_<id>
// gets the field's rule ahead of the _field_* fallback.
func TestNinjaFieldTypes(t *testing.T) {
	c := compileForms(t, nil)
	ft := NewFieldTypes(c)
	for _, f := range []map[string]string{{"id": "5", "type": "email"}, {"id": "6", "type": "firstname"}, {"id": "7", "type": "submit"}, {"id": "x", "type": "email"}} {
		ft.Observe("wp_2_nf3_fields", row(f))
	}
	c = c.WithRules(ft.Rules())
	for key, kind := range map[string]core.FakeKind{"_field_5": core.KEmail, "_field_6": core.KFirstName, "_field_7": core.KText} {
		if r := ruleFor(c, "wp_2_postmeta", "meta_value", map[string]string{"meta_key": key}); r.Fake != kind {
			t.Errorf("%s: %s (%s), want %s", key, r.Fake, r.ID, kind)
		}
	}
	// Blog 2's fields don't type the main site's submissions.
	if r := ruleFor(c, "wp_postmeta", "meta_value", map[string]string{"meta_key": "_field_5"}); r.Fake != core.KText {
		t.Errorf("main site _field_5: %s", r.ID)
	}
}

// Every rule name in the type maps parses, so add never drops a field.
func TestFieldTypeRulesParse(t *testing.T) {
	for _, m := range []map[string]fieldKind{gfTypes, wpfTypes, nfTypes, gfInputs["name"], gfInputs["address"], wpfParts["name"], wpfParts["address"]} {
		for typ, k := range m {
			if _, err := roadiecfg.ParseRule(k.rule); err != nil {
				t.Errorf("%s: %v", typ, err)
			}
			if k.part != "" && !slices.Contains(AddressParts, k.part) {
				t.Errorf("%s: part %s", typ, k.part)
			}
		}
	}
}

// A form definition is untrusted data: odd JSON never panics and never
// makes a rule with a glob in its where clause.
func FuzzFieldTypes(f *testing.F) {
	f.Add(`{"fields":[{"id":3,"type":"email"}]}`, "1")
	f.Add(`{"fields":{"1":{"id":"1","type":"name"}}}`, "12")
	f.Add(`{"fields":[{"id":"1*","type":"email","inputs":[{"id":"1.3"}]}]}`, "[1]")
	schema := formSchema()
	defs, err := Builtin()
	if err != nil {
		f.Fatal(err)
	}
	c, err := Compile(nil, defs, schema, roadiecfg.Default())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, content, id string) {
		ft := NewFieldTypes(c)
		ft.Observe("wp_gf_form_meta", row(map[string]string{"form_id": id, "display_meta": content}))
		ft.Observe("wp_posts", row(map[string]string{"ID": id, "post_type": "wpforms", "post_content": content}))
		ft.Observe("wp_nf3_fields", row(map[string]string{"id": id, "type": content}))
		for _, r := range ft.Rules() {
			for _, v := range r.Where {
				if hasGlob(v) {
					t.Fatalf("glob in where: %+v", r)
				}
			}
		}
	})
}

// A Laravel app's own tables come from laravel.tables in its settings
// file; they join the laravel profile's fixed tables; a laravel.tables
// entry for a fixed column replaces the profile's rule.
func TestLaravelConfigRules(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	yml := "stack: laravel\nlaravel:\n  tables:\n    customers: {email: email, phone: phone, notes: text}\n    users: {name: keep}\n"
	if err := os.WriteFile(file, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := roadiecfg.Load(file, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	s := &core.Schema{Dialect: core.MySQL}
	for name, cols := range map[string][]string{"migrations": {"id", "migration"}, "users": {"id", "name", "email", "password"},
		"customers": {"id", "email", "phone", "notes"}, "sessions": {"id", "payload"}} {
		tb := &core.Table{Name: name}
		for _, c := range cols {
			tb.Columns = append(tb.Columns, core.Column{Name: c, Type: "varchar(255)"})
		}
		s.Tables = append(s.Tables, tb)
	}
	c, err := Compile(nil, mustBuiltin(t), s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(c.Rules("customers")); !slices.Equal(got, []string{"laravel.customers.email", "laravel.customers.notes", "laravel.customers.phone"}) {
		t.Errorf("customers: %v", got)
	}
	for _, r := range c.Rules("users") {
		if r.Column == "name" && (r.Action != core.Keep || r.Explain != "laravel.tables in .roadie/config.yaml") {
			t.Errorf("users.name: %+v", r)
		}
	}
	if c.TableAction("sessions") != core.Truncate || ruleFor(c, "users", "email", nil).Fake != core.KEmail {
		t.Error("the laravel profile's own rules are lost")
	}
}

// A profile that only an option name gives away (Contact Form 7's wpcf7)
// is picked when pass 1 passes the option names it saw.
func TestCompileWithOptions(t *testing.T) {
	defs := mustBuiltin(t)
	s := formSchema()
	plain, err := Compile(nil, defs, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	hinted, err := CompileWith(nil, defs, s, nil, Hints{Options: map[string]bool{"wpcf7": true}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain.Profiles(), "contact-form-7-flamingo") || !slices.Contains(hinted.Profiles(), "contact-form-7-flamingo") {
		t.Errorf("plain %v, hinted %v", plain.Profiles(), hinted.Profiles())
	}
	if r := ruleFor(hinted, "wp_postmeta", "meta_value", map[string]string{"meta_key": "_from_email"}); r.Fake != core.KEmail {
		t.Errorf("_from_email: %+v", r)
	}
}
