// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "encoding/hex"

// DecodeHexLiteral reads a MySQL hex literal, 0x4A61 or X'4A61' (either
// case), into its bytes.
func DecodeHexLiteral(raw []byte) ([]byte, bool) {
	var digits []byte
	switch {
	case len(raw) >= 2 && raw[0] == '0' && (raw[1] == 'x' || raw[1] == 'X'):
		digits = raw[2:]
	case len(raw) >= 3 && (raw[0] == 'x' || raw[0] == 'X') && raw[1] == '\'' && raw[len(raw)-1] == '\'':
		digits = raw[2 : len(raw)-1]
	default:
		return nil, false
	}
	return decodeHex(digits)
}

// DecodeByteaHex reads PostgreSQL's bytea hex output, \x4a61, into its
// bytes. Anything else, including an odd count of digits, is refused.
func DecodeByteaHex(raw []byte) ([]byte, bool) {
	if len(raw) < 2 || raw[0] != '\\' || raw[1] != 'x' && raw[1] != 'X' {
		return nil, false
	}
	return decodeHex(raw[2:])
}

// DecodeBytea reads a bytea value in either output format: hex (\x4a61)
// or escape, where a backslash is written \\ and other bytes may be
// written as three octal digits (\101). A backslash followed by anything
// else is refused.
func DecodeBytea(raw []byte) ([]byte, bool) {
	if b, ok := DecodeByteaHex(raw); ok {
		return b, true
	}
	if len(raw) >= 2 && raw[0] == '\\' && (raw[1] == 'x' || raw[1] == 'X') {
		return nil, false
	}
	n := 0
	for _, c := range raw {
		if c == '\\' {
			n++
		}
	}
	if n == 0 {
		return raw, true
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		switch {
		case i+1 < len(raw) && raw[i+1] == '\\':
			out = append(out, '\\')
			i++
		case i+3 < len(raw) && isOctal(raw[i+1]) && raw[i+1] <= '3' && isOctal(raw[i+2]) && isOctal(raw[i+3]):
			out = append(out, (raw[i+1]-'0')<<6|(raw[i+2]-'0')<<3|(raw[i+3]-'0'))
			i += 3
		default:
			return nil, false
		}
	}
	return out, true
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

func decodeHex(digits []byte) ([]byte, bool) {
	b := make([]byte, hex.DecodedLen(len(digits)))
	if _, err := hex.Decode(b, digits); err != nil {
		return nil, false
	}
	return b, true
}

// ScanBytes returns the bytes a value holds, for a scan that looks for
// personal data and keys: String and BinaryString as they are, a hex
// literal (mysqldump --hex-blob) decoded, and a string in bytea hex
// output (\x...) decoded, since a dump read without column types can't
// tell a bytea column from text. It reports false for a value with no
// bytes to scan.
func (v Value) ScanBytes() ([]byte, bool) {
	switch v.Kind {
	case String:
		if len(v.Raw) >= 4 {
			if b, ok := DecodeByteaHex(v.Raw); ok {
				return b, true
			}
		}
		return v.Raw, len(v.Raw) > 0
	case BinaryString:
		return v.Raw, len(v.Raw) > 0
	case Hex:
		b, ok := DecodeHexLiteral(v.Raw)
		return b, ok && len(b) > 0
	}
	return nil, false
}
