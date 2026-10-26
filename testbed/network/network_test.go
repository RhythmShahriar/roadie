// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package network

import "testing"

func TestProblems(t *testing.T) {
	const sink, stubs = "172.31.120.53", "172.31.120.54"
	sealed := Result{
		Resolved: map[string][]string{"example.org": {sink}, "api.stripe.com": {stubs}, "smtp.gmail.com": nil},
		Dialed:   map[string]string{"1.1.1.1:443": ""},
		ByName:   map[string]string{"example.org:443": sink + ":443", "smtp.gmail.com:587": ""},
	}
	if p := Problems(sealed, sink, stubs); len(p) != 0 {
		t.Errorf("a sealed container: %v", p)
	}
	for name, r := range map[string]Result{
		"real DNS":    {Resolved: map[string][]string{"example.org": {"93.184.215.14"}}},
		"public dial": {Dialed: map[string]string{"1.1.1.1:443": "172.31.120.9:40000"}},
		"by name":     {ByName: map[string]string{"example.org:443": "93.184.215.14:443"}},
		"udp dns":     {UDPDNS: true},
	} {
		if p := Problems(r, sink, stubs); len(p) != 1 {
			t.Errorf("%s: problems %v, want one", name, p)
		}
	}
}
