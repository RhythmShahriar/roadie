// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// Record is one line of runtime/stubs.jsonl. Key is masked; Fingerprint
// (12 hex characters of its sha256) lets the harness tell two keys apart
// without the value.
type Record struct {
	At          string `json:"at"`
	Host        string `json:"host"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Auth        string `json:"auth,omitempty"` // bearer, basic, header, query, form
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Live        bool   `json:"live"`
}

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

// seededKeys holds the keys the seeder made, by value.
type seededKeys struct {
	live, test map[string]bool
}

// loadKeys reads the keys from canaries.json; a missing file gives none
// (only the live-format rule then applies).
func loadKeys(path string) (seededKeys, error) {
	k := seededKeys{live: map[string]bool{}, test: map[string]bool{}}
	b, err := os.ReadFile(path) // #nosec G304 -- the testbed's own runtime file
	if errors.Is(err, fs.ErrNotExist) {
		return k, nil
	}
	if err != nil {
		return k, err
	}
	var f struct {
		Canaries []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
			Live  bool   `json:"live"`
		} `json:"canaries"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return k, err
	}
	for _, c := range f.Canaries {
		if c.Kind == "key" {
			if c.Live {
				k.live[c.Value] = true
			} else {
				k.test[c.Value] = true
			}
		}
	}
	return k, nil
}

// livePrefixes mark a key as live by its format alone, joined here so the
// source holds no live-format literal.
var livePrefixes = []string{"sk" + "_live_", "rk" + "_live_", "pk" + "_live_"}

// liveHosts are API hosts that only take live credentials (PayPal's
// sandbox is api-m.sandbox.paypal.com).
var liveHosts = map[string]bool{"api.paypal.com": true, "api-m.paypal.com": true}

func (k seededKeys) isLive(key, host string) bool {
	if key == "" || k.test[key] {
		return false
	}
	if k.live[key] || liveHosts[host] {
		return true
	}
	for _, p := range livePrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

type handler struct {
	keys seededKeys
	rec  *recorder
}

// credential finds the key a request carries.
func credential(r *http.Request) (auth, key string) {
	if a := r.Header.Get("Authorization"); a != "" {
		scheme, rest, _ := strings.Cut(a, " ")
		switch strings.ToLower(scheme) {
		case "bearer":
			return "bearer", strings.TrimSpace(rest)
		case "basic":
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
			if err != nil {
				return "basic", ""
			}
			user, pass, _ := strings.Cut(string(raw), ":")
			// Stripe sends the key as the user, Mailchimp and Twilio as the password.
			if pass == "" || strings.HasPrefix(user, "sk_") || strings.HasPrefix(user, "rk_") {
				return "basic", user
			}
			return "basic", pass
		}
	}
	for _, h := range []string{"X-Api-Key", "Api-Key"} {
		if v := r.Header.Get(h); v != "" {
			return "header", v
		}
	}
	for _, q := range []string{"key", "api_key", "apikey"} {
		if v := r.URL.Query().Get(q); v != "" {
			return "query", v
		}
	}
	return "", ""
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	auth, key := credential(r)
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if key == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if v, err := url.ParseQuery(string(body)); err == nil && v.Get("key") != "" {
			auth, key = "form", v.Get("key")
		}
	}
	rec := Record{Host: host, Method: r.Method, Path: r.URL.Path, Auth: auth, Live: h.keys.isLive(key, host)}
	if key != "" {
		sum := sha256.Sum256([]byte(key))
		rec.Key, rec.Fingerprint = core.MaskKey(key), hex.EncodeToString(sum[:6])
	}
	h.rec.add(rec)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"testbed_stub","object":"testbed","livemode":false}`)
}
