// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package network

import (
	"fmt"
	"net"
	"sort"
)

// Result is what the probe reports from inside one container (see
// probe/main.go, which prints the same JSON).
type Result struct {
	Resolved map[string][]string `json:"resolved"`
	Dialed   map[string]string   `json:"dialed"`
	ByName   map[string]string   `json:"by_name"`
	UDPDNS   bool                `json:"udp_dns"`
}

// Problems lists every way r shows a container that is not sealed: a
// name resolving anywhere but the sinkhole or the stubs, a public address
// that answered, a dial by name that landed anywhere but the sinkhole, or
// a public DNS resolver that replied. No problems means sealed.
func Problems(r Result, sinkhole, stubs string) []string {
	var out []string
	for name, addrs := range r.Resolved {
		for _, a := range addrs {
			if a != sinkhole && a != stubs {
				out = append(out, fmt.Sprintf("%s resolves to %s: only the sinkhole may answer", name, a))
			}
		}
	}
	for a, local := range r.Dialed {
		if local != "" {
			out = append(out, "dialed the public address "+a)
		}
	}
	for a, remote := range r.ByName {
		if remote == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(remote); err != nil || host != sinkhole {
			out = append(out, fmt.Sprintf("%s reached %s, not the sinkhole", a, remote))
		}
	}
	if r.UDPDNS {
		out = append(out, "a public DNS resolver answered")
	}
	sort.Strings(out)
	return out
}
