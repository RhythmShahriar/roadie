// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command probe runs inside a testbed container (TestNetworkIsSealed
// copies it in) and reports, as JSON, what the container can reach: the
// addresses public names resolve to, whether dialing public IPs works
// and where a dial to a public name lands. On a sealed network every name
// resolves to the sinkhole (or the stubs) and every public IP is
// unreachable.
package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"time"
)

// Result is what the probe prints.
type Result struct {
	Resolved map[string][]string `json:"resolved"`
	// Dialed holds, per public IP:port, "" when the dial failed (sealed)
	// or the local address when it worked.
	Dialed map[string]string `json:"dialed"`
	// ByName holds, per public name:port, the remote address the dial
	// reached, or "" when it failed.
	ByName map[string]string `json:"by_name"`
	// UDPDNS is true when a DNS query to a public resolver got an answer.
	UDPDNS bool `json:"udp_dns"`
}

// Public names and addresses only ever resolved or dialed from inside
// the sealed network, where they cannot be reached.
var (
	names  = []string{"example.org", "smtp.gmail.com", "api.stripe.com", "downloads.wordpress.org"}
	ips    = []string{"1.1.1.1:443", "8.8.8.8:53", "9.9.9.9:80"}
	byName = []string{"example.org:443", "smtp.gmail.com:587"}
)

func main() {
	res := Result{Resolved: map[string][]string{}, Dialed: map[string]string{}, ByName: map[string]string{}}
	for _, n := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		addrs, _ := net.DefaultResolver.LookupHost(ctx, n)
		cancel()
		res.Resolved[n] = addrs
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	for _, a := range ips {
		c, err := d.Dial("tcp", a)
		res.Dialed[a] = ""
		if err == nil {
			res.Dialed[a] = c.LocalAddr().String()
			_ = c.Close()
		}
	}
	for _, a := range byName {
		c, err := d.Dial("tcp", a)
		res.ByName[a] = ""
		if err == nil {
			res.ByName[a] = c.RemoteAddr().String()
			_, _ = c.Write([]byte("roadie-probe\r\n"))
			_ = c.Close()
		}
	}
	if c, err := d.Dial("udp", "8.8.8.8:53"); err == nil {
		// A query for example.org, A record.
		q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := c.Write(q); err == nil {
			n, err := c.Read(make([]byte, 512))
			res.UDPDNS = err == nil && n > 0
		}
		_ = c.Close()
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}
