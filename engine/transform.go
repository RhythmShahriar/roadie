// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"path"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// Rule names of the changes the engine makes outside a profile rule, as
// they appear in Result.Changes.
const (
	changeURLs          = "urls"
	changeKeys          = "keys"
	changeAdminPassword = "admin-password-env"
	changeUnparseable   = "on_unparseable"
)

// formatText is what nested.Format calls a value that isn't serialized,
// JSON or base64-serialized.
const formatText = "text"

// transformer is one worker: it changes the rows of a batch in place as
// the compiled rules say (PRD 11), rewrites production URLs and swaps
// live keys in every string value; then it runs the row fixes.
type transformer struct {
	sh    *shared
	faker core.Faker
	c     *profile.Compiled
	// prefix and stack come from the batch's rule set, not the run: with
	// --one-pass the rules (so the prefix too) are only known as the
	// dump goes by.
	prefix string
	stack  core.Stack
}

func newTransformer(sh *shared, f core.Faker) *transformer { return &transformer{sh: sh, faker: f} }

// rowCtx is what the value functions know about the row being changed.
type rowCtx struct {
	table     string
	get       func(string) ([]byte, bool) // the row's values as they were read
	loc       core.Location
	noRewrite bool // wc_subscriptions_siteurl: no URL rewrite, no key swap
}

// transform changes one batch.
func (t *transformer) transform(ctx context.Context, b *core.Batch, c *profile.Compiled) (*batchOut, error) {
	out := &batchOut{}
	if b.Table == nil || c == nil {
		return out, nil
	}
	t.c, t.prefix, t.stack = c, c.Prefix(), c.Stack()
	tb := b.Table
	pos := positions(b)
	all := c.Rules(tb.Name)
	byPos := make([][]core.Rule, len(b.Columns))
	cols := make([]core.Column, len(b.Columns))
	for i, ci := range b.Columns {
		if ci < 0 || ci >= len(tb.Columns) {
			continue
		}
		cols[i] = tb.Columns[ci]
		for _, r := range all {
			if strings.EqualFold(r.Column, cols[i].Name) {
				byPos[i] = append(byPos[i], r)
			}
		}
	}
	sets := make([]*ruleSet, len(b.Columns))
	for i, rs := range byPos {
		if len(rs) > 0 {
			sets[i] = newRuleSet(rs)
		}
	}
	prefix := t.prefix
	_, isOptions := blogOf(tb.Name, prefix, "options")
	asFix := slices.Contains(c.RowFixes(), "action-scheduler-args")
	if _, ok := blogOf(tb.Name, prefix, "actionscheduler_actions"); !ok {
		asFix = false
	}
	isUsers := prefix != "" && tb.Name == prefix+"users"
	orig := make([][]byte, len(b.Columns))
	nulls := make([]bool, len(b.Columns))
	get := func(col string) ([]byte, bool) {
		i, ok := posOf(pos, col)
		if !ok || nulls[i] {
			return nil, false
		}
		return orig[i], true
	}
	for ri := range b.Rows {
		if ri%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		row := &b.Rows[ri]
		for i := range row.Values {
			if i < len(orig) {
				orig[i], nulls[i] = row.Values[i].Raw, row.Values[i].Kind == core.Null
			}
		}
		if col, drop := t.sh.siteUsers.drops(tb.Name, prefix, get); drop {
			row.Drop = true
			c := out.change(siteUserRule, tb.Name, col, core.DeleteRow)
			c.Rows++
			c.Values++
			continue
		}
		if keptRow(t.sh.kept, tb.Name, prefix, get) {
			t.keepRow(out, tb.Name, isUsers, row, pos)
			continue
		}
		rc := rowCtx{table: tb.Name, get: get, loc: rowLocation(tb, get)}
		if isOptions {
			if n, _ := get("option_name"); string(n) == "wc_subscriptions_siteurl" {
				rc.noRewrite = true
			}
		}
		for i := range row.Values {
			if i >= len(cols) {
				break
			}
			drop, err := t.value(out, rc, cols[i], &row.Values[i], sets[i])
			if err != nil {
				return nil, err
			}
			if drop {
				row.Drop = true
				break
			}
		}
		if row.Drop {
			continue
		}
		if asFix {
			t.actionSchedulerArgs(out, tb.Name, row, pos)
		}
		if isUsers && t.sh.adminID != "" {
			if id, _ := get("ID"); string(id) == t.sh.adminID {
				if i, ok := pos["user_login"]; ok {
					out.firstAdmin = string(row.Values[i].Raw)
				}
			}
		}
	}
	return out, nil
}

// keepRow leaves a kept user's row as it is, except the password when
// --admin-password-env sets one; it remembers its values so verify
// doesn't count them as survivors.
func (t *transformer) keepRow(out *batchOut, table string, isUsers bool, row *core.Row, pos map[string]int) {
	out.keptRows++
	for _, v := range row.Values {
		if v.Kind == core.String && len(v.Raw) > 0 && len(v.Raw) <= 256 {
			out.keptValues = append(out.keptValues, bytes.Clone(v.Raw))
		}
	}
	if !isUsers || t.sh.adminHash == nil {
		return
	}
	if i, ok := pos["user_pass"]; ok {
		row.Values[i] = core.Value{Kind: core.String, Raw: bytes.Clone(t.sh.adminHash), Dirty: true}
		c := out.change(changeAdminPassword, table, "user_pass", core.ResetPassword)
		c.Rows++
		c.Values++
	}
}

// rowLocation is where a row's findings point: option_name or meta_key
// and the primary key, never a data value.
func rowLocation(tb *core.Table, get func(string) ([]byte, bool)) core.Location {
	pk := map[string]string{}
	for _, c := range []string{"option_name", "meta_key"} {
		if v, ok := get(c); ok {
			pk[c] = string(v)
		}
	}
	if _, ok := pk["option_name"]; !ok {
		for _, c := range tb.PrimaryKey {
			if v, ok := get(c); ok {
				pk[c] = string(v)
			}
		}
	}
	return core.Location{Table: tb.Name, Row: core.MaskRow(tb.Name, pk)}
}

// counter counts one value's changes per rule: a rule that changed
// several leaves of one value changed one row.
type counter struct {
	out        *batchOut
	table, col string
	// changedRules are the rule IDs that changed this value so far; a
	// value has few, so a slice beats a map.
	changedRules []string
}

func (c *counter) changed(r *core.Rule, n int64) {
	ch := c.out.change(r.ID, c.table, c.col, r.Action)
	ch.Values += n
	if !slices.Contains(c.changedRules, r.ID) {
		c.changedRules = append(c.changedRules, r.ID)
		ch.Rows++
	}
}

func (c *counter) kept(r *core.Rule) { c.out.change(r.ID, c.table, c.col, r.Action).Kept++ }

// value changes one value. It reports whether the row is to be deleted.
func (t *transformer) value(out *batchOut, rc rowCtx, col core.Column, v *core.Value, rules *ruleSet) (bool, error) {
	if v.Kind != core.String && v.Kind != core.BinaryString {
		return false, nil
	}
	whole, paths := rules.match(rc.get)
	cnt := &counter{out: out, table: rc.table, col: col.Name}
	if whole != nil && whole.Action == core.DeleteRow {
		cnt.changed(whole, 1)
		return true, nil
	}
	orig := v.Raw
	if whole != nil && len(paths) == 0 {
		switch whole.Action {
		case core.Blank, core.SetNull, core.Fixed, core.Fake, core.ResetPassword:
			nv, null, changed, err := t.apply(*whole, orig, col)
			if err != nil {
				return false, err
			}
			if !changed {
				cnt.kept(whole)
				// An empty or unchanged value still gets the URL and key pass.
				break
			}
			cnt.changed(whole, 1)
			setValue(v, nv, null)
			return false, nil
		case core.RewriteURL:
			if strings.EqualFold(col.Name, "path") && t.sh.urls != nil && isMultisiteTable(rc.table, t.prefix) {
				if nv, ok := stagingBlogPath(t.sh.stagingPath, orig); ok {
					cnt.changed(whole, 1)
					setValue(v, nv, false)
				} else {
					cnt.kept(whole)
				}
				return false, nil
			}
			// A host column (blogs.domain, site.domain, a sitemeta domain
			// key) holds a bare host, which the URL forms never match.
			if (strings.EqualFold(col.Name, "domain") || isBareHost(orig)) && t.sh.urls != nil {
				if nv, ok := t.sh.urls.RewriteHost(orig); ok {
					cnt.changed(whole, 1)
					setValue(v, bytes.Clone(hostOnly(nv)), false)
				} else {
					cnt.kept(whole)
				}
				return false, nil
			}
		}
	}
	if fixed, ok := t.addressIndex(rc, col, orig); ok && !t.sh.parts.noData {
		if !bytes.Equal(fixed, orig) {
			setValue(v, fixed, false)
			c := out.change("hpos-address-index", rc.table, col.Name, core.Fixed)
			c.Rows++
			c.Values++
			out.indexFixed++
		}
		return false, nil
	}
	if !t.sh.parts.noData && looksSerialized(orig) && nestedFormat(orig) == formatText {
		done, err := t.unparseable(out, rc, col, v)
		if done || err != nil {
			return false, err
		}
	}
	var siblings map[string]string
	if !rc.noRewrite && nestedFormat(orig) != formatText {
		siblings = map[string]string{}
		_ = nestedLeaves(orig, func(p string, leaf []byte) {
			if len(leaf) <= 512 {
				siblings[p] = string(leaf)
			}
		}, nested.Options{})
	}
	var leafErr error
	fn := func(p string, leaf []byte) ([]byte, bool) {
		cur, changed := leaf, false
		matched := false
		for i := range paths {
			if ok, _ := path.Match(paths[i].Path, p); !ok {
				continue
			}
			matched = true
			nv, ch, err := t.applyLeaf(paths[i], cur, col)
			if err != nil {
				leafErr = err
				return leaf, false
			}
			if ch {
				cnt.changed(&paths[i], 1)
				cur, changed = nv, true
			} else {
				cnt.kept(&paths[i])
			}
			break
		}
		if !matched && whole != nil && whole.Action == core.Scrub {
			if nv, ch := t.scrub(cur); ch {
				cnt.changed(whole, 1)
				cur, changed = nv, true
			}
		}
		if rc.noRewrite {
			return cur, changed
		}
		if t.sh.urls != nil {
			if nv, n := t.sh.urls.Rewrite(cur); n > 0 {
				c := out.change(changeURLs, rc.table, col.Name, core.RewriteURL)
				c.Rows++
				c.Values += int64(n)
				if whole != nil && whole.Action == core.RewriteURL {
					cnt.changed(whole, int64(n))
				}
				cur, changed = nv, true
			}
		}
		nv, ch, err := t.keys(out, rc, col, p, cur, siblings)
		if err != nil {
			leafErr = err
			return cur, changed
		}
		if ch {
			cur, changed = nv, true
		}
		return cur, changed
	}
	nv, changed, err := nestedRewrite(orig, fn, nested.Options{})
	if err == nil {
		err = leafErr
	}
	if err != nil {
		return false, err
	}
	if changed {
		setValue(v, nv, false)
	} else if whole != nil && whole.Action == core.Keep {
		cnt.kept(whole)
	}
	return false, nil
}

// isMultisiteTable reports whether a table is the network's blogs or
// site table, whose path column is the site's path on its host.
func isMultisiteTable(table, prefix string) bool {
	return prefix != "" && (table == prefix+"blogs" || table == prefix+"site")
}

// setValue replaces a value's bytes (or makes it NULL) and marks it dirty.
func setValue(v *core.Value, b []byte, null bool) {
	if null {
		*v = core.Value{Kind: core.Null, Dirty: true}
		return
	}
	kind := v.Kind
	if kind != core.BinaryString {
		kind = core.String
	}
	*v = core.Value{Kind: kind, Raw: b, Dirty: true}
}

// apply runs a whole-value rule. It returns the new bytes, whether the
// value becomes NULL and whether anything changed. An empty value is
// never faked: a fake there would invent data the row didn't have.
func (t *transformer) apply(r core.Rule, orig []byte, col core.Column) ([]byte, bool, bool, error) {
	switch r.Action {
	case core.Blank:
		return []byte{}, false, len(orig) > 0, nil
	case core.SetNull:
		if !col.Nullable && col.Type != "" {
			// NULL in a NOT NULL column would fail the import; blank is
			// the closest the column takes.
			return []byte{}, false, len(orig) > 0, nil
		}
		return nil, true, true, nil
	case core.Fixed:
		return []byte(r.Value), false, !bytes.Equal(orig, []byte(r.Value)), nil
	case core.Fake:
		if len(bytes.TrimSpace(orig)) == 0 {
			return orig, false, false, nil
		}
		nv, err := t.faker.Fake(r.Fake, orig, core.FakeOpts{MaxLen: col.MaxLen, Unique: col.Unique, Locale: t.sh.cfg.Pseudonymise.Locale})
		if err != nil {
			return nil, false, false, err
		}
		return nv, false, !bytes.Equal(nv, orig), nil
	case core.ResetPassword:
		if len(orig) == 0 {
			return orig, false, false, nil
		}
		h, err := resetHash(t.stack, t.sh.key, orig)
		if err != nil {
			return nil, false, false, err
		}
		return h, false, true, nil
	}
	return orig, false, false, nil
}

// applyLeaf runs a path rule on one leaf inside a structured value.
func (t *transformer) applyLeaf(r core.Rule, leaf []byte, col core.Column) ([]byte, bool, error) {
	switch r.Action {
	case core.Scrub:
		nv, ch := t.scrub(leaf)
		return nv, ch, nil
	case core.Keep, core.SwapKey, core.RewriteURL:
		return leaf, false, nil // the URL and key pass after the rule does these
	case core.SetNull:
		return []byte{}, len(leaf) > 0, nil // a leaf can't be NULL; empty is the closest
	}
	// Inside a value the column's byte limit doesn't hold for one leaf.
	col.MaxLen, col.Nullable = 0, true
	nv, _, ch, err := t.apply(r, leaf, col)
	return nv, ch, err
}

// scrub replaces the emails, phones, cards, IPs and known names in free
// text by their fakes.
func (t *transformer) scrub(text []byte) ([]byte, bool) {
	if len(text) == 0 {
		return text, false
	}
	nv, n := scrubText(text, t.sh.spans, t.sh.known, t.faker)
	if n == 0 || nv == nil {
		return text, false
	}
	return nv, true
}

// keys finds keys in one leaf and swaps or blanks the live ones as
// keys.mode says (PRD 12.3); every hit goes into Result.Keys.
func (t *transformer) keys(out *batchOut, rc rowCtx, col core.Column, leafPath string, leaf []byte, siblings map[string]string) ([]byte, bool, error) {
	if t.sh.parts.noKeys {
		return leaf, false, nil
	}
	at := rc.loc
	at.Column, at.Path = col.Name, leafPath
	hits := scanKeys(leaf, at, siblings)
	if len(hits) == 0 {
		return leaf, false, nil
	}
	cur, changed := leaf, false
	mode := t.sh.cfg.Keys.Mode
	for _, h := range hits {
		h.At = at
		if h.Env != "live" {
			h.Action = "kept"
			out.keys = append(out.keys, h)
			continue
		}
		nv, action, err := replaceKey(h, cur, mode, t.sh.cfg.Keys.TestKeys)
		if err != nil {
			return leaf, false, err
		}
		h.Action = action
		out.keys = append(out.keys, h)
		if !bytes.Equal(nv, cur) {
			cur, changed = nv, true
			c := out.change(changeKeys, rc.table, col.Name, core.SwapKey)
			c.Rows++
			c.Values++
		}
	}
	return cur, changed, nil
}

// looksSerialized is WordPress's is_serialized test: the shape of a PHP
// serialized value, valid or not.
func looksSerialized(b []byte) bool {
	b = bytes.TrimSpace(b)
	if len(b) < 4 {
		return string(b) == "N;"
	}
	if b[1] != ':' {
		return false
	}
	last := b[len(b)-1]
	switch b[0] {
	case 's':
		return last == ';' || last == '"' || bytes.HasSuffix(b, []byte(`";`))
	case 'a', 'O', 'C':
		return last == '}'
	case 'b', 'i', 'd', 'E':
		return last == ';'
	}
	return false
}

// unparseable handles a value that looks serialized but doesn't parse
// (PRD 15.2 data.unparseable): one holding personal-looking text is
// blanked or stops the run, as on_unparseable says. It reports whether
// the value is dealt with; one without such text goes on as plain text.
func (t *transformer) unparseable(out *batchOut, rc rowCtx, col core.Column, v *core.Value) (bool, error) {
	if t.sh.spans == nil || len(t.sh.spans.Spans(v.Raw)) == 0 {
		return false, nil
	}
	at := rc.loc
	at.Column = col.Name
	if t.sh.cfg.Pseudonymise.OnUnparseable == "fail" {
		return true, &core.Error{Code: core.FindingDataUnparseable, Exit: 3, Msg: "data.unparseable: a value at " + at.String() +
			" looks serialized, doesn't parse and holds personal-looking text; on_unparseable: fail stops here." +
			" Fix the value, or set on_unparseable: blank to blank such values."}
	}
	setValue(v, []byte{}, false)
	c := out.change(changeUnparseable, rc.table, col.Name, core.Blank)
	c.Rows++
	c.Values++
	out.findings = append(out.findings, core.Finding{ID: core.FindingDataUnparseable, Severity: core.SeverityMedium,
		Title: "A serialized-looking value didn't parse and held personal-looking text; it was blanked", Count: 1,
		Locations: []core.LocationCount{{Location: core.Location{Table: rc.table, Column: col.Name}, Count: 1}},
		Fix:       "Check the plugin that wrote it; on_unparseable: fail stops the copy instead."})
	return true, nil
}
