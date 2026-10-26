// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Soundcheck runs 'rhythm-soundcheck run' health on the staging site
// after the boot check, when the binary is on PATH. Its verdict is a run
// note, not a scored gate: the staging site's own URL (http://staging-wp)
// only resolves inside the sealed network, so a browser on this machine
// can't load the site's assets and would report errors the copy didn't
// cause. The boot check stays the scored gate.
//
// Chrome runs on this machine, outside the sealed network, and a copied
// page can name real hosts (an avatar, an emoji script). So every request
// goes through a seal proxy that passes the staging site only and refuses
// the rest: nothing reaches a real site or this machine's DNS.
type Soundcheck struct {
	Bin     string        // rhythm-soundcheck; empty skips the check
	Base    string        // http://127.0.0.1:18921
	Timeout time.Duration // default 3 minutes
	// ProxyAddr is where the seal proxy listens; default 127.0.0.1:0.
	ProxyAddr string
}

// findSoundcheck returns rhythm-soundcheck on PATH, or "" when there is
// none (the check is then left out with a note).
func findSoundcheck() string {
	p, err := exec.LookPath("rhythm-soundcheck")
	if err != nil {
		return ""
	}
	return p
}

// Health checks paths once in dir with an isolated home and returns the
// note to record. It never downloads a browser (--no-download), sends
// every request through the seal proxy (--proxy) and fails on issues at
// error level (--fail-on error).
func (s Soundcheck) Health(ctx context.Context, dir string, paths []string) string {
	if s.Bin == "" {
		return "soundcheck: not run (rhythm-soundcheck isn't on PATH)"
	}
	if len(paths) == 0 {
		return "soundcheck: not run (no pages to check)"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "soundcheck: not run (" + err.Error() + ")"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	seal, err := startSeal(s.ProxyAddr, s.Base)
	if err != nil {
		return "soundcheck: not run (no seal proxy: " + err.Error() + ")"
	}
	defer seal.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"run", s.Base + "/", "--quick", "--modules", "health", "--no-download",
		"--proxy", seal.URL(), "--fail-on", "error", "--plain", "-q", "--limit", fmt.Sprint(len(paths))}
	for _, p := range paths {
		args = append(args, "--page", p)
	}
	cmd := exec.CommandContext(ctx, s.Bin, args...) // #nosec G204 -- rhythm-soundcheck from PATH, fixed flags
	cmd.Dir = dir
	home := filepath.Join(dir, "home")
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "RHYTHM_CONFIG_DIR="+filepath.Join(home, "rhythm"),
		"RHYTHM_CACHE_DIR="+filepath.Join(home, "cache"), "RHYTHM_NO_UPDATE_CHECK=1")
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, "soundcheck.log"), out, 0o600)
	return soundcheckNote(err, ctx.Err(), timeout, out, len(paths)) + seal.note()
}

// soundcheckNote is the run note for one Soundcheck run.
func soundcheckNote(err, ctxErr error, timeout time.Duration, out []byte, pages int) string {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return fmt.Sprintf("soundcheck: %d pages healthy", pages)
	case ctxErr != nil:
		return "soundcheck: not run (timed out after " + timeout.String() + ")"
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return "soundcheck: issues at error level: " + orDash(string(lastLine(out)))
	case errors.As(err, &ee):
		return fmt.Sprintf("soundcheck: not run (exit %d: %s)", ee.ExitCode(), orDash(string(lastLine(out))))
	}
	return "soundcheck: not run (" + err.Error() + ")"
}

// seal is the proxy Soundcheck's browser and its own requests go
// through. It forwards a plain HTTP request for the staging site's host
// and refuses every other one, CONNECT included, so a page that names a
// real host can't reach it. Chrome sends loopback requests straight to
// the site, never through a proxy: this proxy sees the rest.
type seal struct {
	ln      net.Listener
	srv     *http.Server
	site    string // host:port of the staging site
	mu      sync.Mutex
	refused map[string]int // host, never the path or query: a path may hold a hash of an email
}

// startSeal starts a seal proxy on addr for the site at base.
func startSeal(addr, base string) (*seal, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, fmt.Errorf("the staging site %q isn't an http URL", base)
	}
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &seal{ln: ln, site: u.Host, refused: map[string]int{}}
	fwd := httputil.NewSingleHostReverseProxy(u)
	fwd.Transport = &http.Transport{Proxy: nil, DisableKeepAlives: true}
	s.srv = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if r.Method != http.MethodConnect && r.URL.Host != "" {
			host = r.URL.Host
		}
		if r.Method == http.MethodConnect || !strings.EqualFold(host, s.site) {
			s.refuse(host)
			http.Error(w, "sealed: only the staging site is reachable", http.StatusForbidden)
			return
		}
		fwd.ServeHTTP(w, r)
	})}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// URL is the proxy's address for --proxy.
func (s *seal) URL() string { return "http://" + s.ln.Addr().String() }

// Close stops the proxy.
func (s *seal) Close() { _ = s.srv.Close() }

func (s *seal) refuse(host string) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	s.mu.Lock()
	s.refused[strings.ToLower(host)]++
	s.mu.Unlock()
}

// note names the hosts the proxy refused, for the run note: at most
// five, sorted, with the request count.
func (s *seal) note() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.refused) == 0 {
		return ""
	}
	hosts := make([]string, 0, len(s.refused))
	n := 0
	for h, c := range s.refused {
		hosts = append(hosts, h)
		n += c
	}
	sort.Strings(hosts)
	more := ""
	if len(hosts) > 5 {
		more = fmt.Sprintf(" and %d more", len(hosts)-5)
		hosts = hosts[:5]
	}
	return fmt.Sprintf("; %d request(s) outside the site refused: %s%s", n, strings.Join(hosts, ", "), more)
}

// bootPaths are the staging pages the boot check and Soundcheck load:
// every page the traffic generator knows but the admin and cron ones.
func bootPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasPrefix(p, "/wp-admin") && !strings.HasPrefix(p, "/wp-cron") {
			out = append(out, p)
		}
	}
	return out
}
