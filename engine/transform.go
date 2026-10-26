// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"hash/maphash"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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
	changeKnown         = "known-values"
)

// sweepKinds are the kinds the known-value sweep replaces in leaves no
// rule covers. Emails, phones and personal URLs are specific enough that
// a whole leaf equal to a production one is that person's; a lone name
// word or a login can be an ordinary setting ("Green", "admin") and is
// left to verify's exact.survivors check.
var sweepKinds = []core.FakeKind{core.KEmail, core.KPhone, core.KURL}

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
	switch v.Kind {
	case core.String, core.BinaryString:
	case core.Hex:
		return t.hexValue(out, rc, col, v, rules)
	case core.Expr:
		return false, t.exprValue(out, rc, col, v, rules)
	default:
		return false, nil
	}
	whole, paths := rules.match(rc.get)
	cnt := &counter{out: out, table: rc.table, col: col.Name}
	if whole != nil && whole.Action == core.DeleteRow {
		cnt.changed(whole, 1)
		return true, nil
	}
	orig := v.Raw
	if whole != nil {
		// A rule that replaces the whole value wins over path rules of
		// other where clauses on the same row: they would change leaves
		// of a value the whole rule says must not survive at all.
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
			if len(paths) > 0 {
				break // the leaves get the URL pass below
			}
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
			if !leafMatch(paths[i].Path, p) {
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
			// A free-text field that holds one production value whole (a
			// form's address or date field without a type) gets that
			// value's fake; the scrubber finds only names and shapes.
			nv, ch, err := t.sweep(cur, nil)
			if err != nil {
				leafErr = err
				return leaf, false
			}
			if !ch {
				nv, ch = t.scrub(cur)
			}
			if ch {
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
		// After the URL rewrite: production's own URL becomes the staging
		// URL even when a user's profile URL is the same.
		if !matched && whole == nil && !changed {
			nv, ch, err := t.sweep(cur, sweepKinds)
			if err != nil {
				leafErr = err
				return leaf, false
			}
			if ch {
				c := out.change(changeKnown, rc.table, col.Name, core.Fake)
				c.Rows++
				c.Values++
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
// hexValue runs the rules on a value the dump wrote in hex (mysqldump
// --hex-blob writes binary columns as 0x...): on its bytes, as for a
// quoted string. A changed value is written back as a binary string; an
// unchanged one keeps its hex text.
func (t *transformer) hexValue(out *batchOut, rc rowCtx, col core.Column, v *core.Value, rules *ruleSet) (bool, error) {
	b, ok := decodeHexLiteral(v.Raw)
	if !ok {
		return false, nil
	}
	hv := core.Value{Kind: core.BinaryString, Raw: b}
	del, err := t.value(out, rc, col, &hv, rules)
	if err == nil && hv.Dirty {
		*v = hv
	}
	return del, err
}

// decodeHexLiteral reads 0x4A61 or X'4A61' (either case) into bytes.
func decodeHexLiteral(raw []byte) ([]byte, bool) {
	var digits []byte
	switch {
	case len(raw) >= 2 && raw[0] == '0' && (raw[1] == 'x' || raw[1] == 'X'):
		digits = raw[2:]
	case len(raw) >= 3 && (raw[0] == 'x' || raw[0] == 'X') && raw[1] == '\'' && raw[len(raw)-1] == '\'':
		digits = raw[2 : len(raw)-1]
	default:
		return nil, false
	}
	b, err := hex.DecodeString(string(digits))
	return b, err == nil
}

// exprValue handles a value the dump wrote as an SQL expression Roadie
// doesn't evaluate: adjacent literals ('jane' '@shop.com'), a function
// call. In a column no rule changes it stays as written. Where a rule
// would pseudonymise it, it can't be read, so it is treated as
// unparseable: blanked and reported, or the copy stops with
// on_unparseable: fail. It never passes through unchanged.
func (t *transformer) exprValue(out *batchOut, rc rowCtx, col core.Column, v *core.Value, rules *ruleSet) error {
	if t.sh.parts.noData {
		return nil
	}
	whole, paths := rules.match(rc.get)
	if whole == nil && len(paths) == 0 || whole != nil && whole.Action == core.Keep && len(paths) == 0 {
		return nil
	}
	at := rc.loc
	at.Column = col.Name
	if t.sh.cfg.Pseudonymise.OnUnparseable == "fail" {
		return &core.Error{Code: core.FindingDataUnparseable, Exit: 3, Msg: "data.unparseable: a value at " + at.String() +
			" is an SQL expression Roadie can't read, in a column it pseudonymises; on_unparseable: fail stops here." +
			" Export the dump again without it, or set on_unparseable: blank to blank such values."}
	}
	*v = core.Value{Kind: core.String, Raw: []byte{}, Dirty: true}
	c := out.change(changeUnparseable, rc.table, col.Name, core.Blank)
	c.Rows++
	c.Values++
	out.findings = append(out.findings, core.Finding{ID: core.FindingDataUnparseable, Severity: core.SeverityMedium,
		Title: "A value written as an SQL expression stood in a pseudonymised column; it was blanked", Count: 1,
		Locations: []core.LocationCount{{Location: core.Location{Table: rc.table, Column: col.Name}, Count: 1}},
		Fix:       "Export the dump again with plain values; on_unparseable: fail stops the copy instead."})
	return nil
}

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

// sweep replaces a leaf when the whole leaf is a production value of
// one of kinds (nil: any kind) that pass 1 indexed: the same person's
// value in a plugin setting, a theme option or a free-text field gets
// the same fake as in the covered columns, instead of only being
// reported by verify. Kept users' values aren't in Known, so they stay.
func (t *transformer) sweep(leaf []byte, kinds []core.FakeKind) ([]byte, bool, error) {
	if t.sh.parts.noData || t.sh.known == nil || t.sh.known.Len() == 0 {
		return leaf, false, nil
	}
	tok := bytes.TrimSpace(leaf)
	if len(tok) < 4 || len(tok) > 320 {
		return leaf, false, nil
	}
	kind, enc, ok := t.sh.known.Contains(tok)
	if !ok || enc != "raw" || kinds != nil && !slices.Contains(kinds, kind) {
		return leaf, false, nil
	}
	nv, err := t.faker.Fake(kind, tok, core.FakeOpts{Locale: t.sh.cfg.Pseudonymise.Locale})
	if err != nil {
		return leaf, false, err
	}
	return nv, true, nil
}

// scrub replaces the emails, phones, cards, IPs and known names in free
// text by their fakes, then the known streets, address lines and
// postcodes.
func (t *transformer) scrub(text []byte) ([]byte, bool) {
	if len(text) == 0 {
		return text, false
	}
	nv, n := scrubText(text, t.sh.spans, t.sh.known, t.faker)
	if n == 0 || nv == nil {
		nv = text
	}
	nv, swept := t.sweepText(nv)
	if n == 0 && !swept {
		return text, false
	}
	return nv, true
}

// textKinds are the kinds sweepText finds inside free text. A street, an
// address line or a postcode is written word for word as the customer
// typed it, so a known one is found as a run of words. Names are the
// scrubber's; a city or a country on its own points at no one and stays.
var textKinds = []core.FakeKind{core.KStreet, core.KAddress, core.KPostcode}

// maxTextWords is the longest run of words sweepText looks up.
const maxTextWords = 8

// sweepText replaces each run of words in free text that is a known
// street, address line or postcode by its fake, the longest run first:
// "111 Kingsmead Road, Dublin D06 F2X3" gets a fake street and postcode
// and keeps "Dublin". Every such value holds a digit, so a run without
// one is never looked up.
func (t *transformer) sweepText(text []byte) ([]byte, bool) {
	if t.sh.parts.noData || t.sh.known == nil || t.sh.known.Len() == 0 || !bytes.ContainsAny(text, "0123456789") {
		return text, false
	}
	words := textWords(text)
	var out []byte
	last := 0
	for i := 0; i < len(words); {
		if t.sh.starts != nil && !t.sh.starts.has(text[words[i][0]:words[i][1]]) {
			i++
			continue
		}
		// The run may only cross white space: "Road, Dublin" is two values.
		end := i + 1
		for end < len(words) && end-i < maxTextWords && len(bytes.TrimSpace(text[words[end-1][1]:words[end][0]])) == 0 {
			end++
		}
		n, kind := 0, core.FakeKind("")
		for k := end - i; k >= 1 && n == 0; k-- {
			tok := text[words[i][0]:words[i+k-1][1]]
			if len(tok) < 4 || !bytes.ContainsAny(tok, "0123456789") {
				continue
			}
			if kd, enc, ok := t.sh.known.Contains(tok); ok && enc == "raw" && slices.Contains(textKinds, kd) {
				n, kind = k, kd
			}
		}
		if n == 0 {
			i++
			continue
		}
		a, b := words[i][0], words[i+n-1][1]
		nv, err := t.faker.Fake(kind, text[a:b], core.FakeOpts{Locale: t.sh.cfg.Pseudonymise.Locale})
		if err == nil && len(nv) > 0 {
			out = append(append(out, text[last:a]...), nv...)
			last = b
		}
		i += n
	}
	if out == nil {
		return text, false
	}
	return append(out, text[last:]...), true
}

// textStarts is Known while pass 1 fills it, recording the first word
// of every street, address line and postcode it is given: sweepText
// looks a run of words up only where one of them starts, so free text
// costs a map lookup per word instead of a Known lookup per run.
type textStarts struct {
	core.Known
	seed maphash.Seed
	mu   sync.Mutex
	set  map[uint64]struct{}
}

func newTextStarts(k core.Known) *textStarts {
	return &textStarts{Known: k, seed: maphash.MakeSeed(), set: map[uint64]struct{}{}}
}

// Add records value's first word for the text kinds, then adds it to
// Known.
func (s *textStarts) Add(kind core.FakeKind, value []byte) {
	if slices.Contains(textKinds, kind) {
		if w := textWords(value); len(w) > 0 {
			h := s.hash(value[w[0][0]:w[0][1]])
			s.mu.Lock()
			s.set[h] = struct{}{}
			s.mu.Unlock()
		}
	}
	s.Known.Add(kind, value)
}

// has reports whether word, in any case, starts a known value. It is
// read once pass 1 is over, with no more Adds.
func (s *textStarts) has(word []byte) bool {
	_, ok := s.set[s.hash(word)]
	return ok
}

// hash is the seeded hash of word lower-cased.
func (s *textStarts) hash(word []byte) uint64 {
	var buf [64]byte
	low := buf[:0]
	if len(word) > len(buf) {
		low = make([]byte, 0, len(word))
	}
	for i := 0; i < len(word); {
		r, n := utf8.DecodeRune(word[i:])
		low = utf8.AppendRune(low, unicode.ToLower(r))
		i += n
	}
	return maphash.Bytes(s.seed, low)
}

// textWords splits text into words at white space and the punctuation
// that ends a part of an address; a word keeps inner dots, hyphens,
// slashes and apostrophes ("12/3", "St.-Denis") and loses a final dot.
func textWords(text []byte) [][2]int {
	var out [][2]int
	start := -1
	flush := func(end int) {
		for end > start && text[end-1] == '.' {
			end--
		}
		if end > start {
			out = append(out, [2]int{start, end})
		}
		start = -1
	}
	for i, c := range text {
		if c <= ' ' || strings.IndexByte(",;:()[]{}\"<>!?|", c) >= 0 {
			if start >= 0 {
				flush(i)
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		flush(len(text))
	}
	return out
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
