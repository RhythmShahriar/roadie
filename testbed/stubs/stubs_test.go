// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func records(t *testing.T, b *bytes.Buffer) []Record {
	t.Helper()
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(b.Bytes()))
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestHandlerFlagsLiveKeys(t *testing.T) {
	seededLive := testutil.J("sk", "_live_", "Q3vX9mP2kL7wR4tY8nB6cD1f")
	seededTest := testutil.J("sk", "_test_", "A7bC9dE2fG4hJ6kL8mN0pQ3r")
	mailchimp := "0123456789abcdef0123456789abcdef-us21"
	dir := t.TempDir()
	canaries := filepath.Join(dir, "canaries.json")
	b, _ := json.Marshal(map[string]any{"version": 1, "canaries": []map[string]any{
		{"id": "r12-key-a", "kind": "key", "value": seededLive, "live": true},
		{"id": "r12-key-b", "kind": "key", "value": seededTest},
		{"id": "r12-key-c", "kind": "key", "value": mailchimp, "live": true},
	}})
	if err := os.WriteFile(canaries, b, 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := loadKeys(canaries)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	h := &handler{keys: keys, rec: &recorder{w: &log, now: time.Now}}
	send := func(host, path string, set func(*http.Request)) {
		r := httptest.NewRequest(http.MethodPost, "https://"+host+path, strings.NewReader(""))
		set(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", host, w.Code)
		}
	}
	send("api.stripe.com", "/v1/charges", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+seededLive) })
	send("api.stripe.com", "/v1/charges", func(r *http.Request) { r.SetBasicAuth(seededTest, "") })
	send("us21.api.mailchimp.com", "/3.0/lists", func(r *http.Request) { r.SetBasicAuth("anystring", mailchimp) })
	send("api.stripe.com", "/v1/refunds", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testutil.J("rk", "_live_", "unseededButLiveFormat01"))
	})
	send("hooks.zapier.com", "/hooks/catch/1/abc/", func(*http.Request) {})
	send("api-m.paypal.com", "/v1/oauth2/token", func(r *http.Request) { r.SetBasicAuth("Aclient", "Esecret") })
	recs := records(t, &log)
	want := []struct {
		host, auth string
		live       bool
	}{
		{"api.stripe.com", "bearer", true}, {"api.stripe.com", "basic", false}, {"us21.api.mailchimp.com", "basic", true},
		{"api.stripe.com", "bearer", true}, {"hooks.zapier.com", "", false}, {"api-m.paypal.com", "basic", true},
	}
	if len(recs) != len(want) {
		t.Fatalf("%d records", len(recs))
	}
	for i, w := range want {
		if recs[i].Host != w.host || recs[i].Auth != w.auth || recs[i].Live != w.live {
			t.Errorf("request %d: %+v, want %+v", i, recs[i], w)
		}
	}
	for _, k := range []string{seededLive, seededTest, mailchimp} {
		if strings.Contains(log.String(), k) {
			t.Fatal("a stub log holds a key unmasked")
		}
	}
	if recs[0].Fingerprint == "" || recs[0].Fingerprint == recs[1].Fingerprint {
		t.Error("fingerprints do not tell keys apart")
	}
}

// The host certificate verifies against the CA for every stub host, so
// a site that trusts ca.pem talks to the stubs over TLS.
func TestCertificates(t *testing.T) {
	dir := t.TempDir()
	if err := makeCerts(dir, stubHosts, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "host.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("host.key: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); err == nil {
		t.Error("the CA key was written")
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "host.pem"), filepath.Join(dir, "host.key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:33122", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	srv := &http.Server{Handler: &handler{rec: &recorder{w: &log, now: time.Now}}, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, "127.0.0.1:33122")
		},
	}}
	for _, host := range []string{"api.stripe.com", "us21.api.mailchimp.com", "hooks.zapier.com"} {
		resp, err := client.Get("https://" + host + "/ping")
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		resp.Body.Close()
	}
	if resp, err := client.Get("https://shop.example.com/"); err == nil {
		resp.Body.Close()
		t.Error("the host certificate covers a name it should not")
	}
}
