// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func TestAppendValue(t *testing.T) {
	for _, c := range []struct {
		v    core.Value
		m    Mode
		want string
	}{
		{core.Value{Kind: core.Null}, Mode{}, "NULL"},
		{core.Value{Kind: core.String, Raw: []byte(`it's a\b`)}, Mode{}, `'it''s a\b'`},
		{core.Value{Kind: core.String, Raw: []byte(`it's a\b`)}, Mode{NoStdStrings: true}, `'it''s a\\b'`},
		{core.Value{Kind: core.BinaryString, Raw: []byte{0, 0xff}}, Mode{}, `'\x00ff'`},
		{core.Value{Kind: core.BinaryString, Raw: []byte{1}}, Mode{NoStdStrings: true}, `'\\x01'`},
		{core.Value{Kind: core.Number, Raw: []byte("-1.5e3"), Dirty: true}, Mode{}, "-1.5e3"},
		{core.Value{Kind: core.Bit, Raw: []byte("B'01'"), Dirty: true}, Mode{}, "B'01'"},
		{core.Value{Kind: core.Expr, Raw: []byte("DEFAULT")}, Mode{}, "DEFAULT"},
	} {
		got, err := AppendValue(nil, c.v, c.m)
		if err != nil || string(got) != c.want {
			t.Errorf("%+v: %s, %v; want %s", c.v, got, err, c.want)
		}
	}
	for _, v := range []core.Value{
		{Kind: core.Number, Raw: []byte("1); DROP TABLE t; --"), Dirty: true},
		{Kind: core.Hex, Raw: []byte("X'1' || pg_sleep(1)"), Dirty: true},
		{Kind: core.Expr, Raw: []byte("pg_read_file('x')"), Dirty: true},
		{Kind: core.String, Raw: []byte("nul\x00byte")},
		{Kind: core.Number},
	} {
		if _, err := AppendValue(nil, v, Mode{}); err == nil {
			t.Errorf("%q accepted", v.Raw)
		}
	}
}

func TestAppendCopyField(t *testing.T) {
	for _, c := range []struct {
		v    core.Value
		want string
	}{
		{core.Value{Kind: core.Null}, `\N`},
		{core.Value{Kind: core.String, Raw: []byte(`\N`)}, `\\N`},
		{core.Value{Kind: core.String, Raw: []byte("a\tb\nc\rd\\e\bf\fg\vh")}, `a\tb\nc\rd\\e\bf\fg\vh`},
		{core.Value{Kind: core.String, Raw: []byte(`\.`)}, `\\.`},
		{core.Value{Kind: core.BinaryString, Raw: []byte{0xde, 0xad}}, `\\xdead`},
	} {
		got, err := AppendCopyField(nil, c.v)
		if err != nil || string(got) != c.want {
			t.Errorf("%q: %s, %v; want %s", c.v.Raw, got, err, c.want)
		}
	}
}

func TestWriterOrder(t *testing.T) {
	w := NewWriter(discard{})
	pass := func(seq uint64) *core.Batch {
		return &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtOther, Text: []byte("SELECT 1;\n")}}
	}
	if err := w.Write(pass(2)); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(pass(1)); err == nil {
		t.Error("batch out of order accepted")
	}
	if err := w.Write(&core.Batch{Seq: 3, Rows: []core.Row{{}}}); err == nil {
		t.Error("rows with nothing open accepted")
	}
	if err := w.Write(&core.Batch{Seq: 4, First: true, Head: []byte("COPY t (a) FROM stdin;\n")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(pass(5)); err == nil {
		t.Error("statement inside a COPY accepted")
	}
	if err := w.Finish(); err == nil {
		t.Error("open COPY finished")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
