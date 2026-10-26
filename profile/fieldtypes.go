// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// The form plugins whose field values sit in one column for every field
// type. Which value is an email depends on the form, so these row fixes
// read each form's field list in pass 1 and add a rule per typed field.
const (
	FixGravityForms = "gf-field-types"
	FixWPForms      = "wpforms-field-types"
	FixNinjaForms   = "ninja-field-types"
)

// fieldKind is the rule a typed field gets: a rule name of config
// columns: and the address part it holds, if any.
type fieldKind struct {
	rule, part string
}

// gfTypes maps a Gravity Forms field type to its rule; name and address
// fields are split by input below.
// A date field is most often a date of birth; the dob rule shifts it by
// days within its year, which leaves an appointment date usable too.
var gfTypes = map[string]fieldKind{
	"email": {rule: "email"}, "phone": {rule: "phone"}, "website": {rule: "url"}, "date": {rule: "dob"},
	"text": {rule: "text"}, "textarea": {rule: "text"}, "hidden": {rule: "text"},
}

// gfInputs maps the input number of a name or address field (the part
// after the dot of "3.6") to its rule. Name inputs 2 (prefix) and 8
// (suffix) aren't personal and stay with the fallback scrubber.
var gfInputs = map[string]map[string]fieldKind{
	"name": {"3": {rule: "first_name"}, "4": {rule: "first_name"}, "6": {rule: "last_name"}},
	"address": {"1": {rule: "street"}, "2": {rule: "street"}, "3": {rule: "city", part: "city"},
		"4": {rule: "blank", part: "state"}, "5": {rule: "postcode", part: "postcode"}, "6": {rule: "blank", part: "country"}},
}

// wpfTypes maps a WPForms field type to its rule for the one-value column
// (wpforms_entry_fields.value, which holds a name or an address whole).
var wpfTypes = map[string]fieldKind{
	"email": {rule: "email"}, "phone": {rule: "phone"}, "name": {rule: "full_name"}, "url": {rule: "url"},
	"address": {rule: "address"}, "text": {rule: "text"}, "textarea": {rule: "text"}, "hidden": {rule: "text"},
}

// wpfParts are the keys WPForms keeps beside "value" in the entry JSON
// for a name or an address field.
var wpfParts = map[string]map[string]fieldKind{
	"name": {"first": {rule: "first_name"}, "middle": {rule: "first_name"}, "last": {rule: "last_name"}},
	"address": {"address1": {rule: "street"}, "address2": {rule: "street"}, "city": {rule: "city", part: "city"},
		"state": {rule: "blank", part: "state"}, "postal": {rule: "postcode", part: "postcode"}, "country": {rule: "blank", part: "country"}},
}

// nfTypes maps a Ninja Forms field type to its rule.
var nfTypes = map[string]fieldKind{
	"email": {rule: "email"}, "phone": {rule: "phone"}, "firstname": {rule: "first_name"},
	"lastname": {rule: "last_name"}, "address": {rule: "street"}, "address2": {rule: "street"},
	"city": {rule: "city", part: "city"}, "zip": {rule: "postcode", part: "postcode"},
	"liststate": {rule: "blank", part: "state"}, "listcountry": {rule: "blank", part: "country"},
	"textbox": {rule: "text"}, "textarea": {rule: "text"}, "hidden": {rule: "text"},
}

// FieldTypes builds the per-form rules of the field-type row fixes from
// the rows pass 1 shows it: gf_form_meta.display_meta for Gravity Forms,
// the post_content of wpforms posts for WPForms and nf3_fields for Ninja
// Forms. Pass 1 is one goroutine, so FieldTypes isn't safe for
// concurrent use.
type FieldTypes struct {
	c     *compiled
	rules []core.Rule
	// Errors counts form definitions that didn't parse; their fields keep
	// the fallback rule.
	Errors int
}

// NewFieldTypes returns an empty collector for the set's row fixes.
func NewFieldTypes(c *Compiled) *FieldTypes { return &FieldTypes{c: c.c} }

// blog returns the "<prefix>[<N>_]" part of a table name ending in
// suffix, when it is the main site or a blog of the schema.
func (f *FieldTypes) blog(table, suffix string) (string, bool) {
	p, ok := strings.CutSuffix(table, suffix)
	if !ok {
		return "", false
	}
	if p == f.c.prefix {
		return p, true
	}
	for _, s := range f.c.sites {
		if p == f.c.prefix+s {
			return p, true
		}
	}
	return "", false
}

// Wants reports whether pass 1 should show Observe the rows of table.
func (f *FieldTypes) Wants(table string) bool {
	fixes := f.c.rowFixes
	if _, ok := f.blog(table, "gf_form_meta"); ok && slices.Contains(fixes, FixGravityForms) {
		return true
	}
	if _, ok := f.blog(table, "posts"); ok && slices.Contains(fixes, FixWPForms) {
		return true
	}
	_, ok := f.blog(table, "nf3_fields")
	return ok && slices.Contains(fixes, FixNinjaForms)
}

// Observe reads one row of a table Wants named. get returns a column's
// value on the row, false for NULL or a missing column.
func (f *FieldTypes) Observe(table string, get func(column string) ([]byte, bool)) {
	if !f.Wants(table) {
		return
	}
	if blog, ok := f.blog(table, "gf_form_meta"); ok {
		f.gravity(blog, get)
		return
	}
	if blog, ok := f.blog(table, "posts"); ok {
		if t, ok := get("post_type"); ok && string(t) == "wpforms" {
			f.wpforms(blog, get)
		}
		return
	}
	if blog, ok := f.blog(table, "nf3_fields"); ok {
		f.ninja(blog, get)
	}
}

// Rules returns the rules built so far, sorted by table and ID.
func (f *FieldTypes) Rules() []core.Rule {
	out := slices.Clone(f.rules)
	slices.SortFunc(out, func(a, b core.Rule) int {
		if c := strings.Compare(a.Table, b.Table); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return slices.CompactFunc(out, func(a, b core.Rule) bool { return a.Table == b.Table && a.ID == b.ID })
}

// add builds one rule and applies the settings' policies to it.
func (f *FieldTypes) add(profile, blog, table, column string, where map[string]string, path string, k fieldKind, explain string) {
	spec, err := roadiecfg.ParseRule(k.rule)
	if err != nil {
		return // the maps above hold only valid rule names; a test holds them to it
	}
	r := core.Rule{ID: profile + "." + ruleSuffix(table, column, where, path), Profile: profile, Table: blog + table,
		Column: column, Where: where, Path: path, Action: spec.Action, Fake: spec.Fake, Explain: explain}
	cfg := f.c.cfg
	if cfg == nil {
		cfg = roadiecfg.Default()
	}
	f.rules = append(f.rules, applyPolicies(r, k.part, cfg))
}

// gravity reads one gf_form_meta row: display_meta holds the form as
// JSON with a fields list of {id, type, inputs}.
func (f *FieldTypes) gravity(blog string, get func(string) ([]byte, bool)) {
	formID, ok1 := get("form_id")
	meta, ok2 := get("display_meta")
	if !ok1 || !ok2 || !isNumber(string(formID)) {
		return
	}
	var form struct {
		Fields []struct {
			ID     json.RawMessage `json:"id"`
			Type   string          `json:"type"`
			Inputs []struct {
				ID json.RawMessage `json:"id"`
			} `json:"inputs"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(meta, &form); err != nil {
		f.Errors++
		return
	}
	for _, fd := range form.Fields {
		id := jsonID(fd.ID)
		if !isNumber(id) {
			continue
		}
		where := func(key string) map[string]string {
			return map[string]string{"form_id": string(formID), "meta_key": key}
		}
		explain := func(what string) string {
			return fmt.Sprintf("field %s of form %s is %s (gf_form_meta.display_meta)", id, formID, what)
		}
		if subs, ok := gfInputs[fd.Type]; ok && len(fd.Inputs) > 0 {
			for _, in := range fd.Inputs {
				full := jsonID(in.ID)
				field, sub, ok := strings.Cut(full, ".")
				k, known := subs[sub]
				if !ok || field != id || !known {
					continue
				}
				f.add("gravity-forms", blog, "gf_entry_meta", "meta_value", where(full), "", k, explain("a "+fd.Type+" field"))
			}
			continue
		}
		if fd.Type == "name" {
			f.add("gravity-forms", blog, "gf_entry_meta", "meta_value", where(id), "", fieldKind{rule: "full_name"}, explain("a name field"))
			continue
		}
		if k, ok := gfTypes[fd.Type]; ok {
			f.add("gravity-forms", blog, "gf_entry_meta", "meta_value", where(id), "", k, explain("a "+fd.Type+" field"))
		}
	}
}

// wpforms reads one wpforms post: post_content holds the form as JSON
// with fields keyed by field ID (an object, or a list in exports).
func (f *FieldTypes) wpforms(blog string, get func(string) ([]byte, bool)) {
	formID, ok1 := get("ID")
	content, ok2 := get("post_content")
	if !ok1 || !ok2 || !isNumber(string(formID)) {
		return
	}
	var form struct {
		Fields json.RawMessage `json:"fields"`
	}
	type field struct {
		ID   json.RawMessage `json:"id"`
		Type string          `json:"type"`
	}
	if err := json.Unmarshal(content, &form); err != nil {
		f.Errors++
		return
	}
	var fields []field
	var byID map[string]field
	if err := json.Unmarshal(form.Fields, &byID); err == nil {
		for _, fd := range byID {
			fields = append(fields, fd)
		}
	} else if err := json.Unmarshal(form.Fields, &fields); err != nil && len(form.Fields) > 0 {
		f.Errors++
		return
	}
	fid := string(formID)
	for _, fd := range fields {
		id := jsonID(fd.ID)
		k, ok := wpfTypes[fd.Type]
		if !isNumber(id) || !ok {
			continue
		}
		explain := fmt.Sprintf("field %s of form %s is a %s field (the form's post_content)", id, fid, fd.Type)
		f.add("wpforms", blog, "wpforms_entry_fields", "value", map[string]string{"form_id": fid, "field_id": id}, "", k, explain)
		entries := map[string]string{"form_id": fid}
		if parts, ok := wpfParts[fd.Type]; ok {
			for key, pk := range parts {
				f.add("wpforms", blog, "wpforms_entries", "fields", entries, "json:"+id+"."+key, pk, explain)
			}
			if fd.Type == "address" {
				k = fieldKind{rule: "text"} // the formatted address; its parts have their own rules
			}
		}
		f.add("wpforms", blog, "wpforms_entries", "fields", entries, "json:"+id+".value", k, explain)
	}
}

// ninja reads one nf3_fields row; submissions keep the value of field N
// in post meta _field_N.
func (f *FieldTypes) ninja(blog string, get func(string) ([]byte, bool)) {
	id, ok1 := get("id")
	typ, ok2 := get("type")
	if !ok1 || !ok2 || !isNumber(string(id)) {
		return
	}
	k, ok := nfTypes[string(typ)]
	if !ok {
		return
	}
	f.add("ninja-forms", blog, "postmeta", "meta_value", map[string]string{"meta_key": "_field_" + string(id)}, "", k,
		fmt.Sprintf("field %s is a %s field (nf3_fields)", id, typ))
}

// jsonID reads a field ID written as a number or a string.
func jsonID(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// isNumber reports whether s is a non-negative integer, which is all a
// form or field ID may be; anything else could smuggle glob characters
// into a where clause.
func isNumber(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
