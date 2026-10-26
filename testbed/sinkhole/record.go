// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Record is one line of runtime/sinkhole.jsonl. Kind "dns" is a lookup,
// "tcp" a connection: every tcp record during a scenario is a side
// effect that got past Roadie's kill switch (only the sealed network
// stopped it). First holds up to 256 bytes the client sent, with the
// values of credential headers masked (maskHeaders); the harness reads
// it, never prints it.
type Record struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	Name  string `json:"name,omitempty"` // dns: the name asked for
	Type  uint16 `json:"type,omitempty"` // dns: the query type
	Port  int    `json:"port,omitempty"` // tcp: the port the client dialed
	SNI   string `json:"sni,omitempty"`  // tcp: the TLS server name
	Host  string `json:"host,omitempty"` // tcp: the HTTP Host header
	First []byte `json:"first,omitempty"`
}

// recorder appends records as JSON lines.
type recorder struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func (r *recorder) add(rec Record) {
	rec.At = r.now().UTC().Format(time.RFC3339Nano)
	rec.First = maskHeaders(rec.First)
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.w.Write(append(b, '\n'))
}

// secretHeaders carry credentials in plain HTTP: their values never reach
// the log.
var secretHeaders = [][]byte{
	[]byte("authorization:"), []byte("proxy-authorization:"), []byte("cookie:"),
	[]byte("set-cookie:"), []byte("x-api-key:"), []byte("api-key:"),
}

// masked replaces a credential header's value.
var masked = []byte(" [masked]")

// maskHeaders returns b with the value of every credential header line
// replaced by "[masked]", to the end of the line or of b (a header cut
// at 256 bytes). Everything else, r23's egress marker included, is kept
// as it was. b itself is not changed.
func maskHeaders(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	var out []byte
	for line := range bytes.SplitAfterSeq(b, []byte("\n")) {
		lower := bytes.ToLower(line)
		secret := false
		for _, h := range secretHeaders {
			if bytes.HasPrefix(lower, h) {
				end := line[len(bytes.TrimRight(line, "\r\n")):]
				out = append(append(append(out, line[:len(h)]...), masked...), end...)
				secret = true
				break
			}
		}
		if !secret {
			out = append(out, line...)
		}
	}
	return out
}
