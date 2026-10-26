// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- Action Scheduler's own lookup hash, not a security use
	"encoding/hex"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// asArgsMax is the size of actionscheduler_actions.args: JSON up to 191
// bytes is stored there, longer JSON in extended_args with its md5 in
// args (ActionScheduler_DBStore::save_action_to_db).
const asArgsMax = 191

// actionSchedulerArgs keeps an action's args and extended_args the way
// Action Scheduler writes them after the rules changed either: a fake
// can push the JSON over 191 bytes or bring it back under.
func (t *transformer) actionSchedulerArgs(out *batchOut, table string, row *core.Row, pos map[string]int) {
	ai, ok1 := pos["args"]
	ei, ok2 := pos["extended_args"]
	if !ok1 || !ok2 {
		return
	}
	args, ext := &row.Values[ai], &row.Values[ei]
	if !args.Dirty && !ext.Dirty {
		return
	}
	js := args.Raw
	if ext.Kind != core.Null && len(ext.Raw) > 0 {
		js = ext.Raw
	}
	js = bytes.Clone(js)
	var wantArgs []byte
	var wantExt *[]byte
	if len(js) <= asArgsMax {
		wantArgs = js
	} else {
		sum := md5.Sum(js) // #nosec G401 -- the lookup hash Action Scheduler stores
		wantArgs = []byte(hex.EncodeToString(sum[:]))
		wantExt = &js
	}
	changed := false
	if args.Kind == core.Null || !bytes.Equal(args.Raw, wantArgs) {
		setValue(args, wantArgs, false)
		changed = true
	}
	switch {
	case wantExt == nil && ext.Kind != core.Null:
		setValue(ext, nil, true)
		changed = true
	case wantExt != nil && (ext.Kind == core.Null || !bytes.Equal(ext.Raw, *wantExt)):
		setValue(ext, *wantExt, false)
		changed = true
	}
	if changed {
		c := out.change("action-scheduler-args", table, "args", core.Fixed)
		c.Rows++
		c.Values++
	}
}

// addressIndex rebuilds an order's _billing_address_index or
// _shipping_address_index as WooCommerce does (the address parts joined
// by spaces) from the parts as their own rows' rules change them, so a
// search for the fake name finds the order. It needs the part lengths
// pass 1 recorded; when they don't add up to the index (a part changed
// since, or pass 1 didn't run), ok is false and the index's own rule
// applies.
func (t *transformer) addressIndex(rc rowCtx, col core.Column, orig []byte) ([]byte, bool) {
	if t.c == nil || !slices.Contains(t.c.RowFixes(), "hpos-address-index") || !strings.EqualFold(col.Name, "meta_value") {
		return nil, false
	}
	key, _ := rc.get("meta_key")
	var billing bool
	switch string(key) {
	case "_billing_address_index":
		billing = true
	case "_shipping_address_index":
	default:
		return nil, false
	}
	blog, idCol := "", ""
	if b, ok := blogOf(rc.table, t.prefix, "postmeta"); ok {
		blog, idCol = b, "post_id"
	} else if b, ok := blogOf(rc.table, t.prefix, "wc_orders_meta"); ok {
		blog, idCol = b, "order_id"
	} else {
		return nil, false
	}
	id, _ := rc.get(idCol)
	lens, ok := t.sh.addr.get(newAddrKey(rc.table, id, billing))
	parts := addrParts[billing]
	if !ok {
		return nil, false
	}
	// WooCommerce joins every part with a space, empty ones too; an
	// index written by other code (an import, an older version) may skip
	// the empty parts. Either layout is rebuilt part by part, so the
	// index holds the same fakes as the parts.
	total, nonEmpty, skipped := len(parts)-1, 0, 0
	for i := range parts {
		total += lens[i]
		if lens[i] > 0 {
			nonEmpty++
			skipped += lens[i]
		}
	}
	skipEmpty := false
	if total != len(orig) {
		if nonEmpty == 0 || skipped+nonEmpty-1 != len(orig) {
			return nil, false
		}
		skipEmpty = true
	}
	typ := "_shipping_"
	if billing {
		typ = "_billing_"
	}
	out := make([]byte, 0, len(orig)+32)
	off, wrote := 0, false
	for i, part := range parts {
		if skipEmpty && lens[i] == 0 {
			continue
		}
		if skipEmpty && wrote || !skipEmpty && i > 0 {
			if orig[off] != ' ' {
				return nil, false
			}
			out = append(out, ' ')
			off++
		}
		wrote = true
		n := lens[i]
		val := orig[off : off+n]
		off += n
		r, colOf := t.partRule(rc.table, blog, typ, part)
		if r == nil {
			out = append(out, val...)
			continue
		}
		nv, _, changed, err := t.apply(*r, val, colOf)
		if err != nil || !changed {
			out = append(out, val...)
			continue
		}
		out = append(out, nv...)
	}
	return out, true
}

// partRule finds the rule one address part's own row gets: the meta row
// "_billing_city" in the same table, or for HPOS the column of
// wc_order_addresses.
func (t *transformer) partRule(table, blog, typ, part string) (*core.Rule, core.Column) {
	longtext := core.Column{Name: "meta_value", Type: "longtext", Nullable: true}
	if strings.HasSuffix(table, "wc_orders_meta") {
		for _, r := range t.c.Rules(blog + "wc_order_addresses") {
			if r.Column == part && len(r.Where) == 0 && r.Path == "" {
				return &r, core.Column{Name: part, Type: "varchar(255)", Nullable: true}
			}
		}
	}
	key := typ + part
	get := func(c string) (string, bool) {
		if c == "meta_key" {
			return key, true
		}
		return "", false
	}
	for _, r := range t.c.Rules(table) {
		if strings.EqualFold(r.Column, "meta_value") && r.Path == "" && len(r.Where) > 0 && profile.MatchWhere(r, get) {
			return &r, longtext
		}
	}
	return nil, longtext
}
