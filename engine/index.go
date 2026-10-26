// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"hash/maphash"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// wpCore maps the WordPress core tables to a column only that table has,
// so the prefix can be guessed from whichever core table a dump shows
// first (mysqldump writes tables by name, so wp_2_commentmeta comes
// before wp_options).
var wpCore = map[string]string{
	"users": "user_login", "usermeta": "umeta_id", "posts": "post_author", "postmeta": "meta_id",
	"comments": "comment_ID", "commentmeta": "comment_id", "options": "option_name", "links": "link_id",
	"terms": "term_group", "termmeta": "meta_id", "term_taxonomy": "term_taxonomy_id", "term_relationships": "term_taxonomy_id",
}

// blogCore are the core tables every multisite blog has its own copy of.
var blogCore = []string{"options", "posts", "postmeta", "comments", "commentmeta", "links",
	"terms", "termmeta", "term_taxonomy", "term_relationships"}

// guessPrefix finds the WordPress prefix from any core table: the
// shortest one wins; a "<N>_" blog part is dropped when what is left
// still ends in "_" (wp_2_ is blog 2 of wp_).
func guessPrefix(s *core.Schema) (string, bool) {
	if p, ok := profile.WordPressPrefix(s); ok {
		return p, true
	}
	best, found := "", false
	for _, t := range s.Tables {
		for suffix, col := range wpCore {
			p, ok := strings.CutSuffix(t.Name, suffix)
			if !ok || t.Col(col) < 0 {
				continue
			}
			p = stripBlog(p)
			if !found || len(p) < len(best) || (len(p) == len(best) && p < best) {
				best, found = p, true
			}
		}
	}
	return best, found
}

// stripBlog turns "wp_2_" into "wp_"; "wp_" stays.
func stripBlog(p string) string {
	rest, ok := strings.CutSuffix(p, "_")
	if !ok {
		return p
	}
	i := strings.LastIndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if i < 0 || i == len(rest)-1 || rest[i] != '_' {
		return p
	}
	return rest[:i+1]
}

// provisionalSchema is the schema so far as a compile should see it in
// pass 1: the prefix guessed when the options table isn't there yet plus
// a stand-in options table for every blog whose core tables have
// started, since profile.Sites needs a blog's options or posts table and
// those come late in a dump.
func provisionalSchema(s *core.Schema) *core.Schema {
	cp := *s
	cp.Tables = slices.Clone(s.Tables)
	if cp.Prefix == "" {
		if p, ok := guessPrefix(s); ok {
			cp.Prefix = p
		}
	}
	if cp.Prefix == "" {
		return &cp
	}
	seen := map[string]bool{}
	for _, t := range s.Tables {
		rest, ok := strings.CutPrefix(t.Name, cp.Prefix)
		if !ok {
			continue
		}
		num, tail, ok := strings.Cut(rest, "_")
		if !ok || num == "" || strings.Trim(num, "0123456789") != "" || num[0] == '0' || !slices.Contains(blogCore, tail) {
			continue
		}
		blog := cp.Prefix + num + "_options"
		if !seen[blog] && cp.Table(blog) == nil {
			seen[blog] = true
			cp.Tables = append(cp.Tables, &core.Table{Name: blog, Columns: []core.Column{
				{Name: "option_name", Type: "varchar(191)"}, {Name: "option_value", Type: "longtext"}}})
		}
	}
	return &cp
}

// addrParts are the parts WooCommerce joins with spaces into an order's
// address index, in its order (WC_Order's billing and shipping data).
var addrParts = map[bool][]string{
	true:  {"first_name", "last_name", "company", "address_1", "address_2", "city", "state", "postcode", "country", "email", "phone"},
	false: {"first_name", "last_name", "company", "address_1", "address_2", "city", "state", "postcode", "country", "phone"},
}

// addrKey names one order's billing or shipping address index: a hash
// of the meta table the index row is in, the order (or post) ID and the
// address type. A shop holds an entry per order address, so the key is
// 8 bytes, not two strings; a collision only gives lengths that don't
// add up so the index keeps its own text rule.
type addrKey uint64

// addrSeed seeds addrKey; the keys never leave the process.
var addrSeed = maphash.MakeSeed()

// newAddrKey is the key of one order's address index.
func newAddrKey(table string, id []byte, billing bool) addrKey {
	var h maphash.Hash
	h.SetSeed(addrSeed)
	_, _ = h.WriteString(table)
	_ = h.WriteByte(0)
	_, _ = h.Write(id)
	if billing {
		_ = h.WriteByte(1)
	}
	return addrKey(h.Sum64())
}

// keptUsers are the users keep_users names, found in pass 1.
type keptUsers struct {
	ids    map[string]bool
	emails []string
	logins []string
	// orders are the HPOS orders of kept users, as blog prefix + "|" +
	// order ID, so the order's addresses, meta and operational data stay
	// with the order row.
	orders map[string]bool
}

func (k *keptUsers) has(id []byte) bool { return k != nil && len(id) > 0 && k.ids[string(id)] }

func (k *keptUsers) hasOrder(blog string, id []byte) bool {
	return k != nil && len(id) > 0 && k.orders[blog+"|"+string(id)]
}

// formRow is a row of a form plugin's field list, kept for the field-type
// row fixes until the final rules are known.
type formRow struct {
	table string
	vals  map[string][]byte
}

// indexer watches the source in pass 1 (or the reader in --one-pass) and
// keeps what the transform needs. It runs in one goroutine.
type indexer struct {
	ctx     context.Context
	cfg     *roadiecfg.Config
	defs    []profile.Def
	site    core.Site
	known   core.Known // nil: don't fill it
	samples *samples

	options  map[string]bool
	siteURLs map[string][2]string // blog prefix -> siteurl, home
	blogs    []blogDomain
	hpos     [2]string // woocommerce_custom_orders_table_enabled, _data_sync_enabled
	kept     *keptUsers
	// siteBlogs are copy --site's blogs; siteUsers is made from them
	// once the prefix is known.
	siteBlogs []int
	siteUsers *siteUsers
	admins    []uint64
	forms     []formRow
	// entries are the form entry values the field-type rules may cover,
	// for Known; nil when Known isn't filled in pass 1 (--one-pass).
	entries *formEntries
	addr    *addrIndex
	rows    map[string]int64
	order   []string // tables in the order their rows came

	// prov is the rule set of the profiles detected so far, wide every
	// profile of the stack; both are rebuilt when the schema or the
	// options change what they would be.
	prov, wide *profile.Compiled
	prefix     string
	sig        string
	nTables    int
	nOptions   int
}

func newIndexer(ctx context.Context, cfg *roadiecfg.Config, defs []profile.Def, site core.Site, known core.Known) *indexer {
	x := &indexer{ctx: ctx, cfg: cfg, defs: defs, site: site, known: known, samples: newSamples(),
		options: map[string]bool{}, siteURLs: map[string][2]string{}, rows: map[string]int64{},
		kept: &keptUsers{ids: map[string]bool{}, orders: map[string]bool{}}, addr: newAddrIndex(), nTables: -1}
	if known != nil {
		x.entries = &formEntries{}
	}
	return x
}

// refresh rebuilds the provisional rule sets when the schema has new
// tables or the options new names and that changes the stack, prefix,
// blogs or detected profiles.
func (x *indexer) refresh(s *core.Schema) {
	if len(s.Tables) == x.nTables && len(x.options) == x.nOptions {
		return
	}
	x.nTables, x.nOptions = len(s.Tables), len(x.options)
	ps := provisionalSchema(s)
	stack, err := profile.ResolveStack(x.ctx, x.cfg.Stack, ps, x.site, len(x.cfg.Columns) > 0)
	if err != nil {
		if ps.Prefix == "" {
			x.prov, x.wide, x.sig = nil, nil, ""
			return
		}
		stack = core.StackWordPress
	}
	h := profile.Hints{Options: x.options, Site: x.site, Ctx: x.ctx}
	detected := profile.DetectWithOptions(x.ctx, ps, x.site, x.defs, x.options)
	sites := profile.Sites(ps, ps.Prefix)
	sig := string(stack) + "|" + ps.Prefix + "|" + strings.Join(sites, ",") + "|" + strings.Join(detected, ",")
	if sig == x.sig {
		return
	}
	x.sig, x.prefix = sig, ps.Prefix
	cfg := *x.cfg
	cfg.Stack = string(stack)
	x.prov, _ = profile.CompileWith(nil, x.defs, ps, &cfg, h)
	var all []string
	for _, d := range x.defs {
		if d.Stack == stack {
			all = append(all, d.ID)
		}
	}
	x.wide, _ = profile.CompileWith(all, x.defs, ps, &cfg, h)
}

// rowGet returns a column reader over one row of a batch; a NULL or a
// missing column reads as absent.
func rowGet(b *core.Batch, pos map[string]int, r *core.Row) func(string) ([]byte, bool) {
	return func(col string) ([]byte, bool) {
		i, ok := posOf(pos, col)
		if !ok || i >= len(r.Values) || r.Values[i].Kind == core.Null {
			return nil, false
		}
		return r.Values[i].Raw, true
	}
}

// posOf looks a column up in positions: as written first, since rules
// mostly name columns in lower case already, then lower-cased.
func posOf(pos map[string]int, col string) (int, bool) {
	if i, ok := pos[col]; ok {
		return i, true
	}
	i, ok := pos[strings.ToLower(col)]
	return i, ok
}

// positions maps each column name of a batch (lower case) to its value
// position.
func positions(b *core.Batch) map[string]int {
	pos := make(map[string]int, len(b.Columns))
	for i, ci := range b.Columns {
		if ci >= 0 && ci < len(b.Table.Columns) {
			pos[strings.ToLower(b.Table.Columns[ci].Name)] = i
		}
	}
	return pos
}

// blogOf returns the blog part ("wp_" or "wp_2_") of a table name ending
// in suffix under prefix.
func blogOf(table, prefix, suffix string) (string, bool) {
	p, ok := strings.CutSuffix(table, suffix)
	if !ok || prefix == "" || !strings.HasPrefix(p, prefix) {
		return "", false
	}
	if p == prefix {
		return p, true
	}
	num, ok := strings.CutSuffix(strings.TrimPrefix(p, prefix), "_")
	if !ok || num == "" || strings.Trim(num, "0123456789") != "" || num[0] == '0' {
		return "", false
	}
	return p, true
}

// observe reads one batch. s is the source's schema as it stands.
func (x *indexer) observe(b *core.Batch, s *core.Schema) {
	if b == nil || b.Table == nil {
		return
	}
	x.refresh(s)
	name := b.Table.Name
	if _, ok := x.rows[name]; !ok {
		x.order = append(x.order, name)
	}
	x.rows[name] += int64(len(b.Rows))
	pos := positions(b)
	x.collect(b, pos)
	x.rules(b, pos)
}

// collect keeps the facts pass 1 reads from particular tables.
func (x *indexer) collect(b *core.Batch, pos map[string]int) {
	name, p := b.Table.Name, x.prefix
	var fixes []string
	if x.entries != nil && x.prov != nil {
		fixes = x.prov.RowFixes()
	}
	isBlog := func(table, suffix string) bool { _, ok := blogOf(table, p, suffix); return ok }
	for ri := range b.Rows {
		get := rowGet(b, pos, &b.Rows[ri])
		str := func(c string) string { v, _ := get(c); return string(v) }
		if blog, ok := blogOf(name, p, "options"); ok {
			opt := str("option_name")
			x.options[opt] = true
			u := x.siteURLs[blog]
			switch opt {
			case "siteurl":
				u[0] = str("option_value")
			case "home":
				u[1] = str("option_value")
			case "woocommerce_custom_orders_table_enabled":
				if blog == p {
					x.hpos[0] = str("option_value")
				}
			case "woocommerce_custom_orders_table_data_sync_enabled":
				if blog == p {
					x.hpos[1] = str("option_value")
				}
			}
			x.siteURLs[blog] = u
		}
		if len(x.siteBlogs) > 0 && p != "" {
			if x.siteUsers == nil {
				x.siteUsers = newSiteUsers(p, x.siteBlogs)
			}
			switch name {
			case p + "users":
				x.siteUsers.observeUser(get)
			case p + "usermeta":
				x.siteUsers.observeMeta(get)
			case p + "sitemeta":
				x.siteUsers.observeSiteMeta(get)
			}
		}
		switch {
		case p != "" && (name == p+"blogs" || name == p+"site"):
			x.blogs = append(x.blogs, blogDomain{Domain: str("domain"), Path: str("path")})
		case p != "" && name == p+"users":
			x.keepUser(get)
		case len(x.kept.ids) > 0 && strings.HasSuffix(name, "wc_orders"):
			// A dump has the users table before wc_orders, so the kept
			// IDs are known by now.
			if blog, ok := blogOf(name, p, "wc_orders"); ok {
				if c, _ := get("customer_id"); x.kept.has(c) {
					id, _ := get("id")
					x.kept.orders[blog+"|"+string(id)] = true
				}
			}
		case p != "" && name == p+"usermeta":
			if str("meta_key") == p+"capabilities" {
				if v, _ := get("meta_value"); bytes.Contains(v, []byte(`"administrator"`)) {
					if id, err := strconv.ParseUint(str("user_id"), 10, 64); err == nil {
						x.admins = append(x.admins, id)
					}
				}
			}
		}
		if x.wantsForm(name, get) {
			vals := map[string][]byte{}
			for c, i := range pos {
				if b.Rows[ri].Values[i].Kind != core.Null {
					vals[c] = bytes.Clone(b.Rows[ri].Values[i].Raw)
				}
			}
			// positions are lower case; the field readers ask for "ID".
			if v, ok := vals["id"]; ok {
				vals["ID"] = v
			}
			x.forms = append(x.forms, formRow{table: name, vals: vals})
		}
		x.entries.observe(name, fixes, isBlog, get)
		x.address(name, get)
	}
}

// wantsForm reports whether a row is a form definition a field-type row
// fix reads.
func (x *indexer) wantsForm(table string, get func(string) ([]byte, bool)) bool {
	if _, ok := blogOf(table, x.prefix, "gf_form_meta"); ok {
		return true
	}
	if _, ok := blogOf(table, x.prefix, "nf3_fields"); ok {
		return true
	}
	if _, ok := blogOf(table, x.prefix, "posts"); ok {
		t, _ := get("post_type")
		return string(t) == "wpforms"
	}
	return false
}

// keepUser records a users row keep_users names.
func (x *indexer) keepUser(get func(string) ([]byte, bool)) {
	id, _ := get("ID")
	if len(id) == 0 || x.kept.ids[string(id)] || !x.wouldKeep(get) {
		return
	}
	login, _ := get("user_login")
	email, _ := get("user_email")
	x.kept.ids[string(id)] = true
	x.kept.logins = append(x.kept.logins, string(login))
	x.kept.emails = append(x.kept.emails, string(email))
}

// address records the length of each order address part, for the
// hpos-address-index row fix.
func (x *indexer) address(table string, get func(string) ([]byte, bool)) {
	set := func(k addrKey, billing bool, part string, v []byte) {
		i := slices.Index(addrParts[billing], part)
		if i < 0 {
			return
		}
		x.addr.set(k, i, len(v))
	}
	for _, meta := range []struct{ suffix, idCol string }{{"postmeta", "post_id"}, {"wc_orders_meta", "order_id"}} {
		if _, ok := blogOf(table, x.prefix, meta.suffix); !ok {
			continue
		}
		key, _ := get("meta_key")
		id, _ := get(meta.idCol)
		for _, billing := range []bool{true, false} {
			typ := "_shipping_"
			if billing {
				typ = "_billing_"
			}
			if part, ok := strings.CutPrefix(string(key), typ); ok {
				v, _ := get("meta_value")
				set(newAddrKey(table, id, billing), billing, part, v)
			}
		}
		return
	}
	if blog, ok := blogOf(table, x.prefix, "wc_order_addresses"); ok {
		id, _ := get("order_id")
		typ, _ := get("address_type")
		billing := string(typ) == "billing"
		if !billing && string(typ) != "shipping" {
			return
		}
		k := newAddrKey(blog+"wc_orders_meta", id, billing)
		for _, part := range addrParts[billing] {
			v, _ := get(part)
			set(k, billing, part, v)
		}
	}
}

// personal reports whether a fake kind is a value exact mode looks for
// later: every structured kind but the fixed user agent and free text.
func personal(k core.FakeKind) bool {
	return k != "" && k != core.KUserAgent && k != core.KText
}

// rules adds the structured personal values to Known (with every profile
// of the stack, so a plugin detected later in the dump still counts) and
// samples the text columns no detected rule covers.
func (x *indexer) rules(b *core.Batch, pos map[string]int) {
	name := b.Table.Name
	if x.known != nil && x.wide != nil {
		var rs []core.Rule
		for _, r := range x.wide.Rules(name) {
			if r.Action == core.Fake && personal(r.Fake) {
				rs = append(rs, r)
			}
		}
		if len(rs) > 0 {
			set := newRuleSet(rs)
			for ri := range b.Rows {
				x.addKnown(b, pos, &b.Rows[ri], set)
			}
		}
	}
	if x.prov != nil && !scanTable(x.prov, name) {
		return
	}
	covered := map[string]bool{}
	if x.prov != nil {
		covered = coveredColumns(x.prov, b.Table)
	}
	for i, ci := range b.Columns {
		if ci < 0 || ci >= len(b.Table.Columns) {
			continue
		}
		col := b.Table.Columns[ci]
		if covered[col.Name] || !textColumn(col) || x.samples.full(name, col.Name) {
			continue
		}
		for ri := range b.Rows {
			if v := b.Rows[ri].Values[i]; v.Kind == core.String {
				x.samples.add(name, col.Name, v.Raw)
			}
		}
	}
}

// keptRow reports whether a row belongs to a kept user: the users row
// itself, their user meta, their HPOS orders and those orders' addresses,
// meta and operational data.
func keptRow(kept *keptUsers, table, prefix string, get func(string) ([]byte, bool)) bool {
	if kept == nil || len(kept.ids) == 0 || prefix == "" {
		return false
	}
	col := ""
	switch table {
	case prefix + "users":
		col = "ID"
	case prefix + "usermeta":
		col = "user_id"
	default:
		if _, ok := blogOf(table, prefix, "wc_orders"); ok {
			col = "customer_id"
			break
		}
		for _, child := range []string{"wc_order_addresses", "wc_orders_meta", "wc_order_operational_data"} {
			if blog, ok := blogOf(table, prefix, child); ok {
				id, _ := get("order_id")
				return kept.hasOrder(blog, id)
			}
		}
	}
	if col == "" {
		return false
	}
	id, _ := get(col)
	return kept.has(id)
}

// addKnown adds one row's structured values to Known.
func (x *indexer) addKnown(b *core.Batch, pos map[string]int, r *core.Row, rs *ruleSet) {
	get := rowGet(b, pos, r)
	if keptRow(x.kept, b.Table.Name, x.prefix, get) {
		return
	}
	if b.Table.Name == x.prefix+"users" {
		if id, _ := get("ID"); x.wouldKeep(get) || x.kept.has(id) {
			return
		}
	}
	rs.each(get, func(rule *core.Rule) {
		v, ok := get(rule.Column)
		if !ok || len(bytes.TrimSpace(v)) == 0 {
			return
		}
		if rule.Path == "" {
			x.known.Add(rule.Fake, v)
			return
		}
		_ = nestedLeaves(v, func(p string, leaf []byte) {
			if leafMatch(rule.Path, p) && len(bytes.TrimSpace(leaf)) > 0 {
				x.known.Add(rule.Fake, leaf)
			}
		}, nested.Options{})
	})
}

// wouldKeep reports whether keep_users names this users row: a glob on
// the login or the email, case ignored.
func (x *indexer) wouldKeep(get func(string) ([]byte, bool)) bool {
	login, _ := get("user_login")
	email, _ := get("user_email")
	for _, g := range x.cfg.Pseudonymise.KeepUsers {
		g = strings.ToLower(g)
		if ml, _ := path.Match(g, strings.ToLower(string(login))); ml {
			return true
		}
		if me, _ := path.Match(g, strings.ToLower(string(email))); me {
			return true
		}
	}
	return false
}

// firstAdmin returns the ID of the lowest-numbered administrator.
func (x *indexer) firstAdmin() string {
	if len(x.admins) == 0 {
		return ""
	}
	return strconv.FormatUint(slices.Min(x.admins), 10)
}

// hposOn reports whether WooCommerce keeps orders in its own tables.
func (x *indexer) hposOn() bool { return x.hpos[0] == "yes" }

// siteURLList returns the main site's siteurl and home, for the URL
// pairs. Blogs come from the blogs table, by domain.
func (x *indexer) siteURLList() []string {
	var out []string
	for _, u := range x.siteURLs[x.prefix] {
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}
