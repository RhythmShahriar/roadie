// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "testing"

func TestDecodeBytea(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{`\x6a61`, "ja", true},
		{`\X6A61`, "ja", true},
		{`\x`, "", true},
		{`\x6a6`, "", false},
		{`\xzz`, "", false},
		{`plain`, "plain", true},
		{`a\\b`, `a\b`, true},
		{`\000\101\377`, "\x00A\xff", true},
		{`\400`, "", false},
		{`bad\q`, "", false},
		{`end\`, "", false},
	} {
		got, ok := DecodeBytea([]byte(c.in))
		if ok != c.ok || ok && string(got) != c.want {
			t.Errorf("DecodeBytea(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDecodeHexLiteral(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"0x4A61", "Ja", true},
		{"X'4a61'", "Ja", true},
		{"x''", "", true},
		{"0x4A6", "", false},
		{"4A61", "", false},
	} {
		got, ok := DecodeHexLiteral([]byte(c.in))
		if ok != c.ok || ok && string(got) != c.want {
			t.Errorf("DecodeHexLiteral(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// ScanBytes reads a hex literal and bytea hex text as their bytes, so a
// scan sees an email written by mysqldump --hex-blob or pg_dump.
func TestScanBytes(t *testing.T) {
	for _, c := range []struct {
		v    Value
		want string
		ok   bool
	}{
		{Value{Kind: String, Raw: []byte("a@b.example")}, "a@b.example", true},
		{Value{Kind: BinaryString, Raw: []byte("raw")}, "raw", true},
		{Value{Kind: Hex, Raw: []byte("0x612E62")}, "a.b", true},
		{Value{Kind: Hex, Raw: []byte("X''")}, "", false},
		{Value{Kind: String, Raw: []byte(`\x612e62`)}, "a.b", true},
		{Value{Kind: String, Raw: []byte(`\xnothex`)}, `\xnothex`, true},
		{Value{Kind: String}, "", false},
		{Value{Kind: Number, Raw: []byte("12")}, "", false},
		{Value{Kind: Null}, "", false},
	} {
		got, ok := c.v.ScanBytes()
		if ok != c.ok || ok && string(got) != c.want {
			t.Errorf("ScanBytes(%d %q) = %q, %v; want %q, %v", c.v.Kind, c.v.Raw, got, ok, c.want, c.ok)
		}
	}
}
