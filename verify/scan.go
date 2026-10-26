// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
	findURL   func([]byte) []core.Span
}

// detectorSkip names the --skip entry of each PII detector.
var detectorSkip = map[string]string{
	core.FindingPIIEmail: "emails", core.FindingPIIPhone: "phones", core.FindingPIIName: "names",
	core.FindingPIIAddress: "addresses", core.FindingPIIIP: "ips", core.FindingPIICard: "cards",
	core.FindingPIIIBAN: "cards",
}

func newScanner(j Job, skip skipper) *scanner {
	s := &scanner{skip: skip}
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
	for _, ri := range w.rows {
		row := &b.Rows[ri]
		res.rows++
		get := rowGetter(b, row)
		base := core.Location{Table: t.Name, Row: rowKey(b, row)}
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
			s.value(v.Raw, sc, at, &acc, &res.keys)
		}
	}
	res.findings = acc.Findings()
	return res
}

// rowGetter answers ScanContext.Row for one row.
func rowGetter(b *core.Batch, row *core.Row) func(string) ([]byte, bool) {
	return func(col string) ([]byte, bool) {
		ci := b.Table.Col(col)
		if ci < 0 {
			return nil, false
		}
		pos := ci
		if b.Columns != nil {
			pos = -1
			for p, x := range b.Columns {
				if x == ci {
					pos = p
					break
				}
			}
		}
		if pos < 0 || pos >= len(row.Values) || row.Values[pos].Kind == core.Null {
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
func rowKey(b *core.Batch, row *core.Row) string {
	t := b.Table
	get := rowGetter(b, row)
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

// value scans one field: the gateway and encryption checks on the whole
// value, then the detectors, key rules and URL matcher on every leaf.
func (s *scanner) value(v []byte, sc core.ScanContext, at core.Location, acc *core.Aggregator, hits *[]core.KeyHit) {
	settings := settingsOf(v)
	if !s.skip.skips(core.FindingKeyLiveMode) {
		s.liveMode(v, settings, sc, at, acc)
	}
	if encrypted(v) {
		acc.Add(core.Finding{ID: core.FindingDataEncrypted, Severity: core.SeverityInfo, Title: titles[core.FindingDataEncrypted],
			Locations: []core.LocationCount{{Location: at}}})
	}
	sibs := map[string]map[string]string{}
	leaf := func(p string, l []byte) {
		lat := at
		lat.Path = p
		lsc := sc
		inner, key := keys.LeafKey(p)
		if p != "" {
			lsc.ColumnKind = ""
			if ipKeys[strings.ToLower(key)] {
				lsc.ColumnKind = core.KIP
			}
		}
		emit := func(h core.Hit) {
			acc.Add(core.Finding{ID: h.ID, Severity: h.Severity, Title: titles[h.ID], Fix: fixes[h.ID],
				Locations: []core.LocationCount{{Location: lat}}, Examples: examples(h.Masked), Detail: encodingDetail(h)})
		}
		for _, d := range s.detectors {
			d.Scan(l, lsc, emit)
		}
		if s.survivors != nil {
			s.survivors.Scan(l, lsc, emit)
		}
		if !s.skip.skips("key.") {
			var sib map[string]string
			if settings != nil && singleFormat(p) {
				parent := parentOf(inner)
				if sib = sibs[parent]; sib == nil {
					sib = keys.Siblings(settings, inner)
					sibs[parent] = sib
				}
			}
			for _, h := range keys.ScanValue(l, lat, sib) {
				addKeyHit(h, acc, hits)
			}
		}
		if s.findURL != nil {
			for range s.findURL(l) {
				id := core.FindingURLProduction
				if siteURLOption(sc) {
					id = core.FindingURLProductionSiteURL
				}
				acc.Add(core.Finding{ID: id, Severity: urlSeverity(id), Title: titles[id], Fix: fixes[id],
					Locations: []core.LocationCount{{Location: lat}}})
			}
		}
	}
	switch formatOf(v) {
	case "serialized", "json", "base64-serialized":
		if err := leavesOf(v, leaf, nested.Options{}); err == nil {
			return
		}
	}
	leaf("", v)
}

// addKeyHit turns a key hit into a finding: live keys by their own ID,
// unknown ones as key.unknown-env. Test keys are no finding.
func addKeyHit(h core.KeyHit, acc *core.Aggregator, hits *[]core.KeyHit) {
	if h.Env == keys.EnvTest {
		return
	}
	id := h.ID
	if h.Env == keys.EnvUnknown {
		id = core.FindingKeyUnknownEnv
	}
	acc.Add(core.Finding{ID: id, Severity: h.Severity, Title: keyTitle(h), Fix: fixes[id],
		Locations: []core.LocationCount{{Location: h.At}}, Examples: examples(h.Masked)})
	*hits = append(*hits, h)
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
	if !strings.HasSuffix(sc.Table, "options") || !strings.EqualFold(sc.Column, "option_value") || sc.Row == nil {
		return false
	}
	name, ok := sc.Row("option_name")
	return ok && (string(name) == "siteurl" || string(name) == "home")
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
func (s *scanner) liveMode(v []byte, settings map[string]string, sc core.ScanContext, at core.Location, acc *core.Aggregator) {
	if !strings.HasSuffix(sc.Table, "options") || !strings.EqualFold(sc.Column, "option_value") || sc.Row == nil {
		return
	}
	name, ok := sc.Row("option_name")
	if !ok {
		return
	}
	g, ok := keys.GatewayFor(string(name))
	if !ok || g.TestValue == "" && g.LiveValue == "" {
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
	acc.Add(core.Finding{ID: core.FindingKeyLiveMode, Severity: core.SeverityHigh,
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
