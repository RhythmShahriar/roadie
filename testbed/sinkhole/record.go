// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Record is one line of runtime/sinkhole.jsonl. Kind "dns" is a lookup,
// "tcp" a connection: every tcp record during a scenario is a side
// effect that got past Roadie's kill switch (only the sealed network
// stopped it). First holds up to 256 bytes the client sent; the harness
// reads it, never prints it.
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
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.w.Write(append(b, '\n'))
}
