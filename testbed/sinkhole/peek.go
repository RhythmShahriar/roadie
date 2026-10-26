// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// sniFrom returns the server name of a TLS ClientHello at the start of b,
// or "" when b isn't one (or is cut short).
func sniFrom(b []byte) string {
	// Record header: handshake (22), version, length; then the handshake
	// header: ClientHello (1) and a 3-byte length.
	if len(b) < 9 || b[0] != 22 || b[5] != 1 {
		return ""
	}
	p := b[9:]
	skip := func(n int) bool {
		if n > len(p) {
			return false
		}
		p = p[n:]
		return true
	}
	vec := func(lenBytes int) bool {
		if len(p) < lenBytes {
			return false
		}
		n := 0
		for i := range lenBytes {
			n = n<<8 | int(p[i])
		}
		return skip(lenBytes + n)
	}
	// version, random, session ID, cipher suites, compression methods
	if !skip(2+32) || !vec(1) || !vec(2) || !vec(1) || len(p) < 2 {
		return ""
	}
	ext := p[2:min(len(p), 2+int(binary.BigEndian.Uint16(p)))]
	for len(ext) >= 4 {
		typ, n := binary.BigEndian.Uint16(ext), int(binary.BigEndian.Uint16(ext[2:]))
		if 4+n > len(ext) {
			return ""
		}
		body := ext[4 : 4+n]
		ext = ext[4+n:]
		if typ != 0 || len(body) < 5 { // server_name
			continue
		}
		// list length (2), name type (1, host_name 0), name length (2), name
		l := int(binary.BigEndian.Uint16(body[3:]))
		if body[2] == 0 && 5+l <= len(body) {
			return strings.ToLower(string(body[5 : 5+l]))
		}
	}
	return ""
}

// hostFrom returns the Host header of a plain HTTP request at the start of b.
func hostFrom(b []byte) string {
	for _, line := range bytes.Split(b, []byte("\r\n"))[1:] {
		if k, v, ok := bytes.Cut(line, []byte(":")); ok && strings.EqualFold(string(k), "host") {
			return strings.ToLower(strings.TrimSpace(string(v)))
		}
	}
	return ""
}
