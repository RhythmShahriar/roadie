// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// The testbed's local test ports are 127.0.0.1:32930-32949.
const (
	dnsTestAddr = "127.0.0.1:32930"
	tcpTestAddr = "127.0.0.1:32931"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }

func (s *syncBuf) records(t *testing.T) []Record {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(s.b.Bytes()))
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func newTestSinkhole(log *syncBuf) *sinkhole {
	return &sinkhole{
		rec:     &recorder{w: log, now: func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }},
		res:     resolver{self: netip.MustParseAddr("172.31.120.53"), stubs: netip.MustParseAddr("172.31.120.54"), stubHosts: []string{"api.stripe.com", "*.mailchimp.com"}},
		timeout: 500 * time.Millisecond,
	}
}

func TestDNS(t *testing.T) {
	log := &syncBuf{}
	s := newTestSinkhole(log)
	pc, err := net.ListenPacket("udp", dnsTestAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ln, err := net.Listen("tcp", dnsTestAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go s.serveDNSUDP(pc)
	go s.serveDNSTCP(ln)
	for _, network := range []string{"udp", "tcp"} {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, dnsTestAddr)
		}}
		for name, want := range map[string]string{
			"example.org":            "172.31.120.53",
			"smtp.gmail.com":         "172.31.120.53",
			"API.Stripe.com":         "172.31.120.54",
			"us21.api.mailchimp.com": "172.31.120.54",
			"notmailchimp.com":       "172.31.120.53", // a lookalike is not a stub host
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			addrs, err := r.LookupHost(ctx, name)
			cancel()
			if err != nil || len(addrs) != 1 || addrs[0] != want {
				t.Errorf("%s %s: %v %v, want %s", network, name, addrs, err, want)
			}
		}
	}
	recs := log.records(t)
	if len(recs) == 0 || recs[0].Kind != "dns" || recs[0].From != "127.0.0.1" {
		t.Errorf("records %+v", recs)
	}
}

func TestParseQueryRejects(t *testing.T) {
	good := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
	if q, err := parseQuery(good); err != nil || q.name != "a" || q.qtype != typeA {
		t.Fatalf("good query: %+v %v", q, err)
	}
	resp := append([]byte{}, good...)
	resp[2] |= 0x80
	for name, msg := range map[string][]byte{
		"short":       good[:11],
		"response":    resp,
		"no question": {0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"cut label":   good[:14],
		"no type":     good[:16],
		"long label":  append(append([]byte{}, good[:12]...), 64, 'x'),
	} {
		if _, err := parseQuery(msg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzParseQuery(f *testing.F) {
	f.Add([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1})
	f.Add([]byte{0xff, 0xff, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 28, 0, 1})
	f.Fuzz(func(t *testing.T, msg []byte) {
		q, err := parseQuery(msg)
		if err != nil {
			return
		}
		out := answer(q, netip.MustParseAddr("10.0.0.1"))
		var p dnsParser
		if !p.check(out, q) {
			t.Fatalf("answer to %q does not parse back", q.name)
		}
	})
}

// dnsParser checks an answer with its own reading of the header.
type dnsParser struct{}

func (dnsParser) check(out []byte, q question) bool {
	if len(out) < 12+len(q.raw) || out[0] != byte(q.id>>8) || out[1] != byte(q.id) || out[2]&0x80 == 0 {
		return false
	}
	return bytes.Equal(out[12:12+len(q.raw)], q.raw)
}

func TestTCPRecordsWhatClientsSend(t *testing.T) {
	log := &syncBuf{}
	s := newTestSinkhole(log)
	ln, err := net.Listen("tcp", tcpTestAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go s.serveTCP(ln, 443, 0)

	// A TLS client: the handshake fails, the ClientHello is recorded.
	c, err := tls.Dial("tcp", tcpTestAddr, &tls.Config{ServerName: "api.example.net", MinVersion: tls.VersionTLS12})
	if err == nil {
		c.Close()
		t.Fatal("the sinkhole completed a TLS handshake")
	}
	// A plain HTTP request.
	hc, err := net.Dial("tcp", tcpTestAddr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = hc.Write([]byte("POST /v1/charges HTTP/1.1\r\nHost: API.example.net\r\nContent-Length: 0\r\n\r\n"))
	_ = hc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := hc.Read(make([]byte, 64))
	hc.Close()
	if n != 0 {
		t.Error("the sinkhole answered an HTTP request")
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(log.records(t)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := map[string]Record{}
	for _, r := range log.records(t) {
		got[r.SNI+"|"+r.Host] = r
	}
	if r, ok := got["api.example.net|"]; !ok || r.Port != 443 || r.Kind != "tcp" || len(r.First) == 0 {
		t.Errorf("TLS record missing: %+v", log.records(t))
	}
	if r, ok := got["|api.example.net"]; !ok || !strings.HasPrefix(string(r.First), "POST /v1/charges") {
		t.Errorf("HTTP record missing: %+v", log.records(t))
	}
}

func TestSMTPGreeting(t *testing.T) {
	log := &syncBuf{}
	s := newTestSinkhole(log)
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { s.handle(server, 587, 0); close(done) }()
	r := bufio.NewReader(client)
	line, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "220 ") {
		t.Fatalf("greeting %q %v", line, err)
	}
	_, _ = client.Write([]byte("EHLO staging\r\n"))
	client.Close()
	<-done
	recs := log.records(t)
	if len(recs) != 1 || recs[0].Port != 587 || string(recs[0].First) != "EHLO staging\r\n" {
		t.Errorf("records %+v", recs)
	}
}

func TestPeekNegatives(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("GET / HTTP/1.1\r\n"), {22, 3, 1, 0, 5, 2, 0, 0, 1}} {
		if sniFrom(b) != "" {
			t.Errorf("SNI from %q", b)
		}
	}
	if hostFrom([]byte("\x16\x03\x01binary")) != "" {
		t.Error("Host from binary bytes")
	}
}

func TestMaskHeaders(t *testing.T) {
	key := "sk_" + "live_" + strings.Repeat("Ab3", 8)
	for _, c := range []struct{ in, want string }{
		{"GET /x HTTP/1.1\r\nHost: a\r\nAuthorization: Bearer " + key + "\r\nCookie: s=" + key + "\r\n\r\n",
			"GET /x HTTP/1.1\r\nHost: a\r\nAuthorization: [masked]\r\nCookie: [masked]\r\n\r\n"},
		{"POST /testbed-egress:curl HTTP/1.1\r\nproxy-authorization: Basic " + key + "\nX-Api-Key: " + key,
			"POST /testbed-egress:curl HTTP/1.1\r\nproxy-authorization: [masked]\nX-Api-Key: [masked]"},
		// Nothing to mask: unchanged, the r23 marker included.
		{"testbed-egress:fsockopen\r\n", "testbed-egress:fsockopen\r\n"},
		{"GET / HTTP/1.1\r\nX-Authorization-Hint: none\r\nHost: b\r\n", "GET / HTTP/1.1\r\nX-Authorization-Hint: none\r\nHost: b\r\n"},
		{"EHLO staging\r\n", "EHLO staging\r\n"},
		{"", ""},
	} {
		in := []byte(c.in)
		if got := string(maskHeaders(in)); got != c.want {
			t.Errorf("maskHeaders(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if string(in) != c.in {
			t.Errorf("maskHeaders changed its input %q", c.in)
		}
	}
	var log syncBuf
	r := &recorder{w: &log, now: time.Now}
	r.add(Record{Kind: "tcp", First: []byte("GET / HTTP/1.1\r\nAuthorization: " + key + "\r\n")})
	recs := log.records(t)
	if len(recs) != 1 || strings.Contains(string(recs[0].First), "live_") || !strings.Contains(string(recs[0].First), "Authorization: [masked]") {
		t.Errorf("the log holds the key: %+v", recs)
	}
}
