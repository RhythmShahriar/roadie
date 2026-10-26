// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

// A minimal DNS responder, enough for the sealed network: every A query
// is answered with one address (the sinkhole's, or the stubs' for the
// payment and messaging hosts they stand in for) and every other type
// with an empty NOERROR answer, so nothing waits for a timeout.

var errDNS = errors.New("sinkhole: malformed DNS query")

// question is the one question of a query.
type question struct {
	id    uint16
	name  string // lower case, no trailing dot
	qtype uint16
	raw   []byte // the question section as sent, echoed in the answer
	rd    bool
}

const (
	typeA    = 1
	typeAAAA = 28
	typeANY  = 255
	classIN  = 1
)

// parseQuery reads the header and the first question of a DNS message.
func parseQuery(msg []byte) (question, error) {
	if len(msg) < 12 {
		return question{}, errDNS
	}
	var q question
	q.id = binary.BigEndian.Uint16(msg)
	flags := binary.BigEndian.Uint16(msg[2:])
	if flags&0x8000 != 0 { // a response, not a query
		return question{}, errDNS
	}
	q.rd = flags&0x0100 != 0
	if binary.BigEndian.Uint16(msg[4:]) < 1 {
		return question{}, errDNS
	}
	i := 12
	var labels []string
	for {
		if i >= len(msg) {
			return question{}, errDNS
		}
		n := int(msg[i])
		i++
		if n == 0 {
			break
		}
		if n > 63 || i+n > len(msg) { // compression pointers never appear in a question
			return question{}, errDNS
		}
		labels = append(labels, strings.ToLower(string(msg[i:i+n])))
		i += n
		if len(labels) > 127 {
			return question{}, errDNS
		}
	}
	if i+4 > len(msg) {
		return question{}, errDNS
	}
	q.qtype = binary.BigEndian.Uint16(msg[i:])
	q.name = strings.Join(labels, ".")
	q.raw = msg[12 : i+4]
	return q, nil
}

// answer builds the response to q: an A record with addr for A and ANY
// queries, no records otherwise.
func answer(q question, addr netip.Addr) []byte {
	flags := uint16(0x8000 | 0x0400) // response, authoritative
	if q.rd {
		flags |= 0x0100 | 0x0080 // recursion desired and "available"
	}
	withA := (q.qtype == typeA || q.qtype == typeANY) && addr.Is4()
	out := make([]byte, 12, 12+len(q.raw)+16)
	binary.BigEndian.PutUint16(out, q.id)
	binary.BigEndian.PutUint16(out[2:], flags)
	binary.BigEndian.PutUint16(out[4:], 1)
	if withA {
		binary.BigEndian.PutUint16(out[6:], 1)
	}
	out = append(out, q.raw...)
	if withA {
		ip := addr.As4()
		out = append(out,
			0xc0, 12, // the name: a pointer to the question
			0, typeA, 0, classIN,
			0, 0, 0, 30, // TTL 30 s
			0, 4, ip[0], ip[1], ip[2], ip[3])
	}
	return out
}

// resolver picks the address a name gets.
type resolver struct {
	self, stubs netip.Addr
	stubHosts   []string // exact names, or *.domain for any name under it
}

func (r resolver) addr(name string) netip.Addr {
	for _, h := range r.stubHosts {
		if rest, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(name, "."+rest) {
				return r.stubs
			}
		} else if name == h {
			return r.stubs
		}
	}
	return r.self
}
