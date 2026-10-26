// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/keys"
)

// scanner holds what every worker shares; it is read-only once built.
type scanner struct {
	detectors []core.Detector
	survivors core.Detector
	skip      skipper
	// noKeys and noLiveMode are the --skip answers the hot path needs,
	// worked out once.
	noKeys, noLiveMode bool
	findURL            func([]byte) []core.Span
	filter             *filter
	// wcpayDev is WCPAY_DEV_MODE from wp-config.php: WooPayments runs on
	// a test account whatever its test_mode says.
	wcpayDev bool
}

// detectorSkip names the --skip entry of each PII detector.
var detectorSkip = map[string]string{
	core.FindingPIIEmail: "emails", core.FindingPIIPhone: "phones", core.FindingPIIName: "names",
	core.FindingPIIAddress: "addresses", core.FindingPIIIP: "ips", core.FindingPIICard: "cards",
	core.FindingPIIIBAN: "cards",
}

func newScanner(j Job, skip skipper, f *filter) *scanner {
	s := &scanner{skip: skip, filter: f, noKeys: skip.skips("key."), noLiveMode: skip.skips(core.FindingKeyLiveMode)}
	for _, d := range detect.All() {
		if skip[detectorSkip[d.ID()]] {
			continue
		}
		s.detectors = append(s.detectors, detect.WithAllow(d, j.Opts.Allow))
	}
	if j.Known != nil {
		s.survivors = detect.WithAllow(detect.Survivors(j.Known), detect.Allow{KeptEmails: j.Opts.Allow.KeptEmails})
	}
	if !skip.skips(core.FindingURLProduction) {
		s.findURL = newURLFinder(j.Production)
	}
	return s
}

// batch scans the chosen rows of one batch.
func (s *scanner) batch(w work) result {
	res := result{idx: w.idx}
	b := w.b
	t := b.Table
	var acc core.Aggregator
	out := filtered{agg: &acc, f: s.filter, hidden: &res.hidden}
	idx := newColIndex(b)
	f := newField(s, out, &res.keys)
	for _, ri := range w.rows {
		row := &b.Rows[ri]
		res.rows++
		get := idx.getter(row)
		base := core.Location{Table: t.Name, Row: rowKey(b, get)}
		for pos, v := range row.Values {
			if v.Kind != core.String && v.Kind != core.BinaryString || len(v.Raw) == 0 {
				continue
			}
			c := columnAt(b, pos)
			if c == nil {
				continue
			}
			res.bytes += int64(len(v.Raw))
			at := base
			at.Column = c.Name
			sc := core.ScanContext{Table: t.Name, Column: c.Name, ColumnKind: w.kinds[c.Name], Row: get}
			f.value(v.Raw, sc, at)
		}
	}
	res.findings = acc.Findings()
	res.unparsed = f.unparsed
	return res
}

// colIndex maps the column names of one batch to value positions, so the
// detectors' many row lookups cost a map read each.
type colIndex map[string]int

// newColIndex indexes b's columns by name and lower-case name.
func newColIndex(b *core.Batch) colIndex {
	idx := colIndex{}
	for i, c := range b.Table.Columns {
		pos := i
		if b.Columns != nil {
			pos = slices.Index(b.Columns, i)
		}
		if pos < 0 {
			continue
		}
		if _, ok := idx[c.Name]; !ok {
			idx[c.Name] = pos
		}
		if l := strings.ToLower(c.Name); l != c.Name {
			if _, ok := idx[l]; !ok {
				idx[l] = pos
			}
		}
	}
	return idx
}

// getter answers ScanContext.Row for one row of the batch.
func (idx colIndex) getter(row *core.Row) func(string) ([]byte, bool) {
	return func(col string) ([]byte, bool) {
		pos, ok := idx[col]
		if !ok {
			pos, ok = idx[strings.ToLower(col)]
		}
		if !ok || pos >= len(row.Values) || row.Values[pos].Kind == core.Null {
			return nil, false
		}
		return row.Values[pos].Raw, true
	}
}

// rowKeyColumns are the key columns of key/value tables, named in a
// location next to the primary key.
var rowKeyColumns = []string{"option_name", "meta_key"}

// rowKey builds Location.Row from the primary key and the option or meta
// key of a row, through core.MaskRow.
func rowKey(b *core.Batch, get func(string) ([]byte, bool)) string {
	t := b.Table
	pk := map[string]string{}
	cols := append(append([]string(nil), t.PrimaryKey...), rowKeyColumns...)
	for _, c := range cols {
		if v, ok := get(c); ok && len(v) <= 191 {
			pk[c] = string(v)
		}
	}
	if len(pk) == 0 {
		return ""
	}
	return core.MaskRow(t.Name, pk)
}

// ipKeys are leaf keys whose values are IP addresses (session tokens,
// form entries).
var ipKeys = map[string]bool{"ip": true, "ip_address": true, "ipaddress": true, "user_ip": true, "remote_addr": true, "client_ip": true}

// field scans the values of one batch, one at a time. A worker keeps one
// per batch so the callbacks the detectors and the nested walker take are
// made once, not for every value.
type field struct {
	s        *scanner
	acc      sink
	hits     *[]core.KeyHit
	sc       core.ScanContext
	at       core.Location
	settings map[string]string
	sibs     map[string]map[string]string
	public   bool
	lat      core.Location // the leaf being scanned, for emit
	// unparsed counts serialized-looking values that didn't parse, by
	// table, NUL, column.
	unparsed map[string]int64
	emitFn   func(core.Hit)
	leafFn   func(string, []byte)
	// pending holds the leaves of the value being walked and arena their
	// bytes. They are scanned only once the walk succeeds: a walk that
	// fails partway leaves the value to be read as plain text; leaves
	// scanned before the failure would then be counted twice.
	pending []pendingLeaf
	arena   []byte
}

// pendingLeaf is one walked leaf: its path and its bytes in field.arena.
type pendingLeaf struct {
	path       string
	start, end int
}

func newField(s *scanner, acc sink, hits *[]core.KeyHit) *field {
	f := &field{s: s, acc: acc, hits: hits}
	f.emitFn, f.leafFn = f.emit, f.collect
	return f
}

// value scans one field: the gateway and encryption checks on the whole
// value, then the detectors, key rules and URL matcher on every leaf.
func (f *field) value(v []byte, sc core.ScanContext, at core.Location) {
	s := f.s
	f.sc, f.at = sc, at
	f.settings = settingsOf(v)
	f.sibs = nil
	if !s.noLiveMode {
		s.liveMode(v, f.settings, sc, at, f.acc)
	}
	if encrypted(v) {
		f.acc.add(core.Finding{ID: core.FindingDataEncrypted, Severity: core.SeverityInfo, Title: titles[core.FindingDataEncrypted],
			Locations: []core.LocationCount{{Location: at}}})
	}
	f.public = publicOption(sc)
	switch formatOf(v) {
	case "serialized", "json", "base64-serialized":
		f.pending, f.arena = f.pending[:0], f.arena[:0]
		if err := leavesOf(v, f.leafFn, nested.Options{}); err == nil {
			for _, l := range f.pending {
				f.leaf(l.path, f.arena[l.start:l.end])
			}
			f.keys()
			return
		}
	}
	if looksSerialized(v) {
		if f.unparsed == nil {
			f.unparsed = map[string]int64{}
		}
		f.unparsed[sc.Table+"\x00"+sc.Column]++
	}
	f.leaf("", v)
}

// keys scans the keys of a nested value. A plugin that keys its data by
// the customer's address ({"jane@shop.com": {...}}) keeps it in the
// leaf's path, where neither a rule nor the value detectors reach it.
// Each distinct path with an @ in it is read once as text; the finding
// points at the column, since the path itself would show the address.
func (f *field) keys() {
	s := f.s
	var seen []string
	for _, l := range f.pending {
		if strings.IndexByte(l.path, '@') < 0 || slices.Contains(seen, l.path) {
			continue
		}
		seen = append(seen, l.path)
		f.lat = f.at
		f.lat.Path = core.MaskPath(l.path)
		// nested escapes a dot inside a key (ada\.lee@shop\.com).
		key := []byte(strings.ReplaceAll(l.path, `\.`, "."))
		if !f.public {
			for _, d := range s.detectors {
				d.Scan(key, f.sc, f.emitFn)
			}
		}
		if s.survivors != nil {
			s.survivors.Scan(key, f.sc, f.emitFn)
		}
	}
}

// collect keeps one walked leaf until the walk ends. The bytes are
// copied: the walker may reuse its buffer for the next leaf.
func (f *field) collect(p string, l []byte) {
	start := len(f.arena)
	f.arena = append(f.arena, l...)
	f.pending = append(f.pending, pendingLeaf{path: p, start: start, end: len(f.arena)})
}

// emit records one detector hit at the current leaf.
func (f *field) emit(h core.Hit) {
	f.acc.add(core.Finding{ID: h.ID, Severity: h.Severity, Title: titles[h.ID], Fix: fixes[h.ID],
		Locations: []core.LocationCount{{Location: f.lat}}, Examples: examples(h.Masked), Detail: encodingDetail(h)})
}

// leaf scans one string leaf at path p (or the whole value when p is "").
func (f *field) leaf(p string, l []byte) {
	s := f.s
	f.lat = f.at
	f.lat.Path = core.MaskPath(p)
	lsc := f.sc
	inner, key := keys.LeafKey(p)
	if p != "" {
		lsc.ColumnKind = ""
		if ipKeys[strings.ToLower(key)] {
			lsc.ColumnKind = core.KIP
		}
	}
	if p != "" && encrypted(l) {
		// A Laravel job that implements ShouldBeEncrypted keeps its
		// command encrypted inside the JSON payload of the jobs table.
		f.acc.add(core.Finding{ID: core.FindingDataEncrypted, Severity: core.SeverityInfo, Title: titles[core.FindingDataEncrypted],
			Locations: []core.LocationCount{{Location: f.lat}}})
	}
	if !f.public {
		for _, d := range s.detectors {
			d.Scan(l, lsc, f.emitFn)
		}
	}
	if s.survivors != nil {
		s.survivors.Scan(l, lsc, f.emitFn)
	}
	if !s.noKeys {
		var sib map[string]string
		if f.settings != nil && singleFormat(p) {
			parent := parentOf(inner)
			if sib = f.sibs[parent]; sib == nil {
				sib = keys.Siblings(f.settings, inner)
				if f.sibs == nil {
					f.sibs = map[string]map[string]string{}
				}
				f.sibs[parent] = sib
			}
		}
		for _, h := range keys.ScanValue(l, f.lat, sib) {
			addKeyHit(h, f.acc, f.hits)
		}
	}
	if s.findURL != nil && !optionNamed(f.sc, "wc_subscriptions_siteurl") {
		// WooCommerce Subscriptions keeps production's URL there on
		// purpose: a copy whose URL differs from it stays in staging
		// mode. Roadie never rewrites it, so it is no finding (PRD 15.2).
		for range s.findURL(l) {
			id := core.FindingURLProduction
			if siteURLOption(f.sc) {
				id = core.FindingURLProductionSiteURL
			}
			f.acc.add(core.Finding{ID: id, Severity: urlSeverity(id), Title: titles[id], Fix: fixes[id],
				Locations: []core.LocationCount{{Location: f.lat}}})
		}
	}
}

// addKeyHit turns a key hit into a finding: live keys by their own ID,
// unknown ones as key.unknown-env. Test keys are no finding. A hit whose
// finding is hidden is left out of hits too.
func addKeyHit(h core.KeyHit, acc sink, hits *[]core.KeyHit) {
	if h.Env == keys.EnvTest {
		return
	}
	id := h.ID
	if h.Env == keys.EnvUnknown {
		id = core.FindingKeyUnknownEnv
	}
	if acc.add(core.Finding{ID: id, Severity: h.Severity, Title: keyTitle(h), Fix: fixes[id],
		Locations: []core.LocationCount{{Location: h.At}}, Examples: examples(h.Masked)}) {
		*hits = append(*hits, h)
	}
}

func examples(masked string) []string {
	if masked == "" {
		return nil
	}
	return []string{masked}
}

// encodingDetail names the encoding of an exact.survivors hit.
func encodingDetail(h core.Hit) string {
	if h.ID == core.FindingExactSurvivors && h.Encoding != "" {
		return "found as " + h.Encoding
	}
	return ""
}

func urlSeverity(id string) core.Severity {
	if id == core.FindingURLProductionSiteURL {
		return core.SeverityCritical
	}
	return core.SeverityMedium
}

// siteURLOption reports whether the value is the siteurl or home option.
func siteURLOption(sc core.ScanContext) bool {
	return optionNamed(sc, "siteurl", "home")
}

// optionNamed reports whether sc is the option_value of an options row
// named one of names.
func optionNamed(sc core.ScanContext, names ...string) bool {
	if !strings.HasSuffix(sc.Table, "options") || !strings.EqualFold(sc.Column, "option_value") || sc.Row == nil {
		return false
	}
	name, ok := sc.Row("option_name")
	return ok && slices.Contains(names, string(name))
}

// publicOptionPrefixes are the option names under which WordPress caches
// what it fetched from wordpress.org and plugin vendors: update checks
// with plugin headers (author names and emails), feeds and the plugin
// directory. They hold public data, never a customer's.
var publicOptionPrefixes = []string{
	"_site_transient_update_", "_site_transient_theme_roots", "_site_transient_wporg_",
	"_site_transient_poptags_", "_site_transient_browser_", "_site_transient_php_check_",
	"_site_transient_feed_", "_transient_feed_", "_transient_plugin_slugs", "_transient_wporg_",
	"_transient_dash_", "_site_transient_community-events-", "_transient_timeout_",
	"_site_transient_timeout_",
}

// publicOptions are whole option names of the same kind.
var publicOptions = map[string]bool{
	"active_plugins": true, "recently_activated": true, "uninstall_plugins": true,
	"active_sitewide_plugins": true, "auto_update_plugins": true, "auto_update_themes": true,
}

// publicOption reports whether the value is an option or network option
// that caches public plugin and wordpress.org data. The PII detectors
// skip it; the survivors, key and URL checks still read it.
func publicOption(sc core.ScanContext) bool {
	if sc.Row == nil {
		return false
	}
	key := ""
	switch {
	case hasSuffixFold(sc.Table, "options") && strings.EqualFold(sc.Column, "option_value"):
		key = "option_name"
	case hasSuffixFold(sc.Table, "sitemeta") && strings.EqualFold(sc.Column, "meta_value"):
		key = "meta_key"
	default:
		return false
	}
	name, ok := sc.Row(key)
	if !ok {
		return false
	}
	if publicOptions[string(name)] {
		return true
	}
	for _, p := range publicOptionPrefixes {
		if bytes.HasPrefix(name, []byte(p)) {
			return true
		}
	}
	return false
}

// hasSuffixFold is strings.HasSuffix ignoring ASCII case, without the
// copy strings.ToLower makes.
func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// settingsOf reads a serialized or JSON value as settings, or nil.
func settingsOf(v []byte) map[string]string {
	t := bytes.TrimSpace(v)
	if len(t) < 2 || bytes.IndexByte([]byte("aO{["), t[0]) < 0 {
		return nil
	}
	m, _ := keys.Settings(t)
	return m
}

// singleFormat reports whether a leaf path has one format level, so its
// siblings in the outer settings are its own.
func singleFormat(p string) bool {
	n := strings.Count(p, "serialized:") + strings.Count(p, "json:")
	return n == 1 && !strings.HasPrefix(p, "base64:")
}

// parentOf returns the parent of a settings path.
func parentOf(p string) string {
	if i := strings.LastIndexAny(p, ".["); i >= 0 {
		return p[:i]
	}
	return ""
}

// liveMode flags a known gateway left in live mode (key.live-mode, PRD
// 15.2), even with blank secrets.
func (s *scanner) liveMode(v []byte, settings map[string]string, sc core.ScanContext, at core.Location, acc sink) {
	if !strings.HasSuffix(sc.Table, "options") || !strings.EqualFold(sc.Column, "option_value") || sc.Row == nil {
		return
	}
	name, ok := sc.Row("option_name")
	if !ok {
		return
	}
	g, ok := keys.GatewayFor(string(name))
	if !ok || g.TestValue == "" && g.LiveValue == "" || g.ID == "woopayments" && s.wcpayDev {
		return
	}
	if g.ModePath == "" {
		settings = map[string]string{"": string(bytes.TrimSpace(v))}
	}
	if settings == nil || !g.Enabled(settings) || g.Mode(settings) != keys.EnvLive {
		return
	}
	lat := at
	if g.ModePath != "" {
		lat.Path = "serialized:" + g.ModePath
	}
	acc.add(core.Finding{ID: core.FindingKeyLiveMode, Severity: core.SeverityHigh,
		Title: g.Name + " is in live mode", Fix: fixes[core.FindingKeyLiveMode],
		Locations: []core.LocationCount{{Location: lat}}})
}

// encrypted reports whether v is a Laravel Crypt payload: base64 of a JSON
// object with exactly iv, value and mac (and tag for AEAD ciphers).
func encrypted(v []byte) bool {
	v = bytes.TrimSpace(v)
	if len(v) < 40 || !bytes.HasPrefix(v, []byte("eyJ")) || len(v) > 64<<20 {
		return false
	}
	dec := make([]byte, base64.StdEncoding.DecodedLen(len(v)))
	n, err := base64.StdEncoding.Decode(dec, v)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(dec[:n], &m) != nil {
		return false
	}
	for _, k := range []string{"iv", "value", "mac"} {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	_, tag := m["tag"]
	return len(m) == 3 || len(m) == 4 && tag
}

// looksSerialized is WordPress's is_serialized test (strict): a value
// PHP's maybe_unserialize would try to read. Such a value that nested
// can't parse is noted under coverage.
func looksSerialized(v []byte) bool {
	v = bytes.TrimSpace(v)
	if string(v) == "N;" {
		return true
	}
	if len(v) < 4 || v[1] != ':' {
		return false
	}
	last := v[len(v)-1]
	if last != ';' && last != '}' {
		return false
	}
	switch v[0] {
	case 's':
		return last == ';' && v[len(v)-2] == '"'
	case 'a', 'O', 'E':
		return last == '}' && bytes.IndexByte(v[2:], ':') > 0
	case 'b', 'i', 'd':
		return last == ';' && len(v) > 2
	}
	return false
}
