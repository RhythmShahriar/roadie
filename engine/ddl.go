// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
)

// ddlEmail finds real-looking emails in a DDL literal, as verify does.
var ddlEmail = detect.Email()

// ddl scrubs the string literals a CREATE TABLE or ALTER TABLE keeps (a
// column's DEFAULT, a COMMENT): a literal that is a known production
// value gets its fake and a real-looking email inside one gets a fake
// email. Anything else in a literal ('publish', '0000-00-00') is left as
// it is: blanking a DEFAULT would change the schema.
func (t *transformer) ddl(st *core.Statement) (*core.Statement, int) {
	if st == nil || !core.IsDDL(st.Kind) || t.sh.parts.noData {
		return st, 0
	}
	lits := core.DDLLiterals(st.Text)
	if len(lits) == 0 {
		return st, 0
	}
	var out []byte
	last, n := 0, 0
	for _, l := range lits {
		content := st.Text[l[0]:l[1]]
		nv, ok := t.ddlLiteral(content)
		if !ok {
			continue
		}
		out = append(append(out, st.Text[last:l[0]]...), nv...)
		last = l[1]
		n++
	}
	if n == 0 {
		return st, 0
	}
	out = append(out, st.Text[last:]...)
	return &core.Statement{Kind: st.Kind, Text: out, Line: st.Line}, n
}

// ddlLiteral is one literal's new content, quoted for SQL, or false
// when it stays.
func (t *transformer) ddlLiteral(content []byte) ([]byte, bool) {
	opts := core.FakeOpts{Locale: t.sh.cfg.Pseudonymise.Locale}
	if k := t.sh.known; k != nil && k.Len() > 0 && !bytes.ContainsAny(content, `\'`) {
		if kind, enc, ok := k.Contains(content); ok && enc == "raw" {
			if nv, err := t.faker.Fake(kind, content, opts); err == nil && len(nv) > 0 && !bytes.ContainsRune(nv, '\\') {
				return bytes.ReplaceAll(nv, []byte("'"), []byte("''")), true
			}
		}
	}
	var hits [][2]int
	ddlEmail.Scan(content, core.ScanContext{}, func(h core.Hit) {
		if h.ID == core.FindingPIIEmail && (h.Encoding == "" || h.Encoding == "raw") {
			hits = append(hits, [2]int{h.Start, h.End})
		}
	})
	if len(hits) == 0 {
		return nil, false
	}
	var out []byte
	last := 0
	for _, h := range hits {
		if h[0] < last {
			continue
		}
		nv, err := t.faker.Fake(core.KEmail, content[h[0]:h[1]], opts)
		if err != nil || len(nv) == 0 || bytes.ContainsAny(nv, `\'`) {
			continue
		}
		out = append(append(out, content[last:h[0]]...), nv...)
		last = h[1]
	}
	if out == nil {
		return nil, false
	}
	return append(out, content[last:]...), true
}
