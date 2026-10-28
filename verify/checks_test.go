// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// listen opens a listener on the first free port of Roadie's verify test range
// (127.0.0.1:32900-32919); tests never use a random or public port.
func listen(t *testing.T) net.Listener {
	t.Helper()
	for p := 32900; p <= 32919; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return l
		}
	}
	t.Fatal("no free port in 32900-32919")
	return nil
}

// serve starts h on a test port; tls serves it with httptest's
// self-signed certificate.
func serve(t *testing.T, h http.Handler, useTLS bool) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(h)
	_ = s.Listener.Close()
	s.Listener = listen(t)
	if useTLS {
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	return s
}

// siteHandler serves / and /robots.txt.
type siteHandler struct {
	header   map[string]string
	body     string
	robots   string
	status   int
	requests atomic.Int64
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requests.Add(1)
	if r.URL.Path == "/robots.txt" {
		if h.robots == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, h.robots)
		return
	}
	for k, v := range h.header {
		w.Header().Set(k, v)
	}
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
	_, _ = io.WriteString(w, h.body)
}

func siteRun(t *testing.T, o Options, production ...string) *core.VerifyResult {
	t.Helper()
	return runJob(t, Job{Source: source(options([]string{"1", "blogname", "Shop"})), Opts: o, Production: production})
}

func TestSiteNoindexAndAuth(t *testing.T) {
	sealed := &siteHandler{header: map[string]string{"X-Robots-Tag": "noindex, nofollow"}, robots: "User-agent: *\nDisallow: /\n", status: 200}
	s := serve(t, sealed, false)
	res := siteRun(t, Options{SiteURL: s.URL})
	if find(res, core.FindingSearchIndexable) != nil {
		t.Errorf("noindex and robots block: %v", ids(res))
	}
	if find(res, core.FindingAuthNone) == nil || find(res, core.FindingAuthNone).Severity != core.SeverityLow {
		t.Errorf("no auth: %v", ids(res))
	}
	if c := checkByID(res, core.FindingSearchIndexable); c.Status != "pass" || c.Checked != 2 {
		t.Errorf("search.indexable check %+v", c)
	}
	if c := checkByID(res, core.FindingAuthNone); c.Status != "fail" {
		t.Errorf("auth.none check %+v", c)
	}

	meta := &siteHandler{body: `<html><head><META name="robots" content='noindex,follow'></head></html>`, robots: "User-agent: *\nDisallow: /"}
	if res := siteRun(t, Options{SiteURL: serve(t, meta, false).URL}); find(res, core.FindingSearchIndexable) != nil {
		t.Errorf("meta noindex: %v", ids(res))
	}

	open := &siteHandler{body: "<html>shop</html>", robots: "User-agent: *\nDisallow: /wp-admin/\n"}
	res = siteRun(t, Options{SiteURL: serve(t, open, false).URL})
	if f := find(res, core.FindingSearchIndexable); f == nil || f.Severity != core.SeverityMedium {
		t.Errorf("indexable: %v", ids(res))
	} else if !strings.Contains(f.Fix, "prepare --only noindex") {
		// prepare's --only takes noindex, not exposure.
		t.Errorf("indexable fix: %q", f.Fix)
	}

	// Noindex on the page but robots.txt lets every crawler in.
	half := &siteHandler{header: map[string]string{"X-Robots-Tag": "noindex"}, robots: "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n"}
	res = siteRun(t, Options{SiteURL: serve(t, half, false).URL})
	if f := find(res, core.FindingSearchIndexable); f == nil || !strings.Contains(f.Title, "robots.txt") {
		t.Errorf("robots allows: %+v", res.Findings)
	} else if !strings.Contains(f.Fix, "Disallow: /") || !strings.Contains(f.Fix, "--only noindex") {
		t.Errorf("robots fix: %q", f.Fix)
	}

	// Noindex on the page and no robots.txt (404): a missing robots.txt
	// is no signal, which is the usual Laravel staging.
	laravel := &siteHandler{header: map[string]string{"X-Robots-Tag": "noindex"}}
	res = siteRun(t, Options{SiteURL: serve(t, laravel, false).URL})
	if find(res, core.FindingSearchIndexable) != nil {
		t.Errorf("noindex without robots.txt: %+v", res.Findings)
	}
	// No noindex and no robots.txt is still indexable.
	bare := &siteHandler{body: "<html>shop</html>"}
	res = siteRun(t, Options{SiteURL: serve(t, bare, false).URL})
	if f := find(res, core.FindingSearchIndexable); f == nil || !strings.Contains(f.Title, "no noindex") {
		t.Errorf("bare: %+v", res.Findings)
	}

	locked := &siteHandler{header: map[string]string{"WWW-Authenticate": `Basic realm="staging"`}, status: 401}
	res = siteRun(t, Options{SiteURL: serve(t, locked, false).URL})
	if find(res, core.FindingAuthNone) != nil || find(res, core.FindingSearchIndexable) != nil {
		t.Errorf("basic auth: %v", ids(res))
	}
}

func TestSiteRedirectsRefused(t *testing.T) {
	other := serve(t, &siteHandler{body: "other service"}, false)
	for name, to := range map[string]string{"metadata": "http://169.254.169.254/latest/meta-data/", "another port": other.URL + "/"} {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) })
		res := siteRun(t, Options{SiteURL: serve(t, h, false).URL})
		if len(res.Coverage.Skipped) == 0 || !strings.Contains(res.Coverage.Skipped[0].Reason, "refused a redirect off the staging host") {
			t.Errorf("%s: skipped %+v", name, res.Coverage.Skipped)
		}
		if find(res, core.FindingAuthNone) != nil {
			t.Errorf("%s: followed the redirect", name)
		}
	}
}

func TestSiteBodyCap(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("x", 64<<10))
		for range (1 << 30) / len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	start := time.Now()
	res := siteRun(t, Options{SiteURL: serve(t, h, false).URL})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a 1 GB page took %v", d)
	}
	if find(res, core.FindingAuthNone) == nil {
		t.Errorf("capped page not read: %v %+v", ids(res), res.Coverage.Skipped)
	}
}

func TestSiteInsecureTLSOneHost(t *testing.T) {
	s := serve(t, &siteHandler{body: "tls"}, true)
	res := siteRun(t, Options{SiteURL: s.URL, InsecureTLS: "127.0.0.1"})
	if find(res, core.FindingAuthNone) == nil {
		t.Errorf("insecure host: %v %+v", ids(res), res.Coverage.Skipped)
	}
	res = siteRun(t, Options{SiteURL: s.URL, InsecureTLS: "staging.example"})
	if len(res.Coverage.Skipped) == 0 || !strings.Contains(res.Coverage.Skipped[0].Reason, "certificate") {
		t.Errorf("other host: %+v", res.Coverage.Skipped)
	}
	cfg := insecureFor("a.example")
	if !cfg.InsecureSkipVerify || cfg.VerifyConnection(tls.ConnectionState{ServerName: "b.example"}) == nil {
		t.Error("insecureFor trusts another host")
	}
}

func TestSiteProductionRefused(t *testing.T) {
	h := &siteHandler{body: "prod"}
	s := serve(t, h, false)
	_, port, _ := strings.Cut(strings.TrimPrefix(s.URL, "http://127.0.0.1"), ":")
	for _, site := range []string{s.URL, "http://[::ffff:127.0.0.1]:" + port, "http://127.0.0.1.:" + port} {
		_, err := Run(context.Background(), Job{Source: source(options([]string{"1", "blogname", "Shop"})), Production: []string{"127.0.0.1"},
			Opts: Options{SiteURL: site}})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrTargetIsProduction || ce.Exit != 3 {
			t.Errorf("%s: got %v", site, err)
		}
	}
	if h.requests.Load() != 0 {
		t.Errorf("%d requests to production", h.requests.Load())
	}
}

// A trailing dot, upper case or an IPv4-mapped IPv6 literal names the
// same host, so none of them gets past the production guard; a staging
// host next to production does.
func TestSiteProductionHostForms(t *testing.T) {
	for site, refused := range map[string]bool{
		"https://shop.com./":             true,
		"https://www.shop.com./":         true,
		"https://SHOP.COM../":            true,
		"https://cdn.shop.de./":          true,
		"http://[::ffff:10.0.0.5]:8080/": true,
		"http://[::ffff:a00:5]/":         true,
		"http://10.0.0.5./":              true,
		"https://staging.shop.com./":     false,
		"https://shop.com.evil.test/":    false,
		"http://[::1]/":                  false,
		"http://10.0.0.6/":               false,
	} {
		err := checkOptions(Job{Production: []string{"https://Shop.com./", "*.shop.de", "[::ffff:10.0.0.5]"}, Opts: Options{SiteURL: site}})
		if got := errors.Is(err, &core.Error{Code: core.ErrTargetIsProduction}); got != refused {
			t.Errorf("%s: refused %v, want %v (%v)", site, got, refused, err)
		}
	}
}

// fakeEcho answers health in mode and lists sent messages; it fails the
// test unless the list asks for messages since since.
func fakeEcho(t *testing.T, mode string, sent int, since time.Time) *httptest.Server {
	want := since.UTC().Format(time.RFC3339Nano)
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/health":
			fmt.Fprintf(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":%q,"wire":1,"providers":[]}`, mode)
		case "/v1/messages":
			if got := r.URL.Query().Get("since"); got != want {
				t.Errorf("messages listed since %q, want %q", got, want)
			}
			var ms []string
			for i := range sent {
				ms = append(ms, fmt.Sprintf(`{"id":"m%d","status":"sent"}`, i))
			}
			ms = append(ms, `{"id":"f","status":"faked"}`)
			fmt.Fprintf(w, `{"messages":[%s]}`, strings.Join(ms, ","))
		default:
			http.NotFound(w, r)
		}
	}), false)
}

func TestEcho(t *testing.T) {
	copied := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	now := copied.Add(time.Hour)
	clock := func() time.Time { return now }
	res := siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 0, copied).URL, EchoSince: copied, Clock: clock})
	if find(res, core.FindingSideEffectMail) != nil {
		t.Errorf("sealed: %+v", res.Findings)
	}
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "pass" || c.Detail != "" {
		t.Errorf("sealed check %+v", c)
	}
	res = siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "observe", 0, copied).URL, EchoSince: copied, Clock: clock})
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Severity != core.SeverityCritical || !strings.Contains(f.Title, "observe") {
		t.Errorf("observe: %+v", res.Findings)
	}
	res = siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 3, copied).URL, EchoSince: copied, Clock: clock})
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Title != "Echo let 3 messages through since the copy" {
		t.Errorf("sent: %+v", res.Findings)
	}
	l := listen(t)
	url := "http://" + l.Addr().String()
	_ = l.Close()
	res = siteRun(t, Options{Echo: true, EchoURL: url})
	want := "verify --echo: Echo isn't reachable at " + url + "; start it with rhythm echo up."
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Title != want {
		t.Errorf("unreachable: %+v", res.Findings)
	}
}

// Without the copy's start time the count starts with verify; both
// the finding and the check say so.
func TestEchoSinceUnknown(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	res := siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 0, now).URL, Clock: clock})
	want := "counted only messages since verify started; the copy's start time wasn't known"
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "pass" || c.Detail != want {
		t.Errorf("check %+v", c)
	}
	res = siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 2, now).URL, Clock: clock})
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Title != "Echo let 2 messages through since verify started" {
		t.Errorf("sent: %+v", res.Findings)
	}
}

// A failed message count fails closed: the check can't pass on a count
// it never got.
func TestEchoListFails(t *testing.T) {
	broken := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"seal","wire":1,"providers":[]}`)
			return
		}
		http.Error(w, "store locked", http.StatusInternalServerError)
	}), false)
	res := siteRun(t, Options{Echo: true, EchoURL: broken.URL})
	want := "verify --echo: couldn't count what Echo let through: Echo answered 500 Internal Server Error"
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Severity != core.SeverityCritical || f.Title != want {
		t.Errorf("list fails: %+v", res.Findings)
	}
	if c := checkByID(res, core.FindingSideEffectMail); c.Status != "fail" {
		t.Errorf("side-effect.mail check %+v", c)
	}
}

// Counting that stops at the page cap while Echo has more pages fails
// closed: the uncounted pages may hold sent mail.
func TestEchoPageCap(t *testing.T) {
	old := maxEchoPages
	maxEchoPages = 2
	t.Cleanup(func() { maxEchoPages = old })
	pages := 0
	endless := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/health" {
			_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"seal","wire":1,"providers":[]}`)
			return
		}
		pages++
		fmt.Fprintf(w, `{"messages":[{"id":"m%d","status":"faked"}],"next":"c%d"}`, pages, pages)
	}), false)
	res := siteRun(t, Options{Echo: true, EchoURL: endless.URL})
	want := "verify --echo: couldn't count every message Echo let through (stopped after 2 pages)"
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Severity != core.SeverityCritical || f.Title != want {
		t.Errorf("capped: %+v", res.Findings)
	}
	if pages != 2 {
		t.Errorf("%d pages read, want 2", pages)
	}
	// The last page within the cap ends the count cleanly.
	pages = 0
	last := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/health" {
			_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"seal","wire":1,"providers":[]}`)
			return
		}
		pages++
		next := ""
		if pages < 2 {
			next = "c1"
		}
		fmt.Fprintf(w, `{"messages":[{"id":"m%d","status":"faked"}],"next":%q}`, pages, next)
	}), false)
	res = siteRun(t, Options{Echo: true, EchoURL: last.URL})
	if f := find(res, core.FindingSideEffectMail); f != nil {
		t.Errorf("two pages within the cap: %+v", f)
	}
}

// Echo beyond loopback asks for a token on every route; the finding
// says so in place of calling Echo unreachable.
func TestEchoWantsToken(t *testing.T) {
	locked := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token required", http.StatusUnauthorized)
	}), false)
	res := siteRun(t, Options{Echo: true, EchoURL: locked.URL})
	f := find(res, core.FindingSideEffectMail)
	if f == nil || !strings.Contains(f.Title, "wants a token") || strings.Contains(f.Title, "isn't reachable") {
		t.Errorf("401: %+v", res.Findings)
	}
	// A wrong token is refused all the same; the right one is sent in
	// X-Echo-Token and never shows in a finding.
	token := "echo-" + "test-token-1"
	res = siteRun(t, Options{Echo: true, EchoURL: locked.URL, EchoToken: "wrong"})
	if f := find(res, core.FindingSideEffectMail); f == nil || !strings.Contains(f.Title, "refused the token") {
		t.Errorf("wrong token: %+v", res.Findings)
	}
	var got atomic.Value
	open := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-Echo-Token"))
		if r.Header.Get("X-Echo-Token") != token {
			http.Error(w, "token required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/health" {
			fmt.Fprint(w, `{"mode":"seal"}`)
			return
		}
		fmt.Fprint(w, `{"messages":[],"next":""}`)
	}), false)
	res = siteRun(t, Options{Echo: true, EchoURL: open.URL, EchoToken: token})
	if got.Load() != token {
		t.Errorf("token sent: %v", got.Load())
	}
	for _, f := range res.Findings {
		if strings.Contains(f.Title, "token") {
			t.Errorf("right token: %+v", f)
		}
		if strings.Contains(fmt.Sprint(f), token) {
			t.Errorf("a finding shows the token: %+v", f)
		}
	}
}

func TestEchoURLMustBeLocal(t *testing.T) {
	for u, ok := range map[string]bool{
		"": true, "http://127.0.0.1:8025": true, "http://localhost:8025": true, "http://10.1.2.3:8025": true,
		"http://[::1]:8025": true, "https://echo.example.com": true,
		"http://echo.example.com:8025": false, "http://8.8.8.8:8025": false, "ftp://127.0.0.1": false,
		// The same rule as --echo-url: a LAN, mDNS or Docker name is local.
		"http://echo.lan:8025": true, "http://echo.local:8025": true, "http://echo:8025": true,
		"http://echo.internal:8025": true, "http://user:pw@127.0.0.1:8025": false,
	} {
		if (roadiecfg.ValidateEchoURL(u) == nil) != ok && u != "" {
			t.Errorf("roadiecfg.ValidateEchoURL(%q) disagrees", u)
		}
		_, err := echoURL(u)
		if (err == nil) != ok {
			t.Errorf("echoURL(%q) = %v", u, err)
		}
		if err != nil {
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 {
				t.Errorf("%q: %v", u, err)
			}
		}
	}
	_, err := Run(context.Background(), Job{Source: source(options([]string{"1", "blogname", "Shop"})), Opts: Options{Echo: true, EchoURL: "http://echo.example.com"}})
	if !errors.Is(err, &core.Error{Code: core.ErrConfig}) {
		t.Errorf("Run with a public Echo: %v", err)
	}
}

// fakeSoundcheck puts this test binary on PATH as rhythm-soundcheck; with
// ROADIE_FAKE_SOUNDCHECK set, TestMain acts as it (see soundcheckMain).
func fakeSoundcheck(t *testing.T, exit string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	name := "rhythm-soundcheck"
	if runtime.GOOS == "windows" {
		// exec.LookPath only finds a name with a PATHEXT extension there.
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ROADIE_FAKE_SOUNDCHECK", exit)
}

// soundcheckMain is the fake Soundcheck: it checks its arguments, prints a
// report and exits with ROADIE_FAKE_SOUNDCHECK.
func soundcheckMain() {
	args := os.Args[1:]
	if len(args) < 3 || args[0] != "run" || !strings.HasPrefix(args[1], "http") || args[2] != "--json" {
		fmt.Fprintln(os.Stderr, "bad args", args)
		os.Exit(2)
	}
	fmt.Printf(`{"schema":"rhythm-soundcheck/v1","summary":{"pages":4,"failed":%d,"echo":%q}}`+"\n",
		map[string]int{"0": 0, "1": 2}[os.Getenv("ROADIE_FAKE_SOUNDCHECK")], strings.Join(args[3:], " "))
	code := 0
	if os.Getenv("ROADIE_FAKE_SOUNDCHECK") == "1" {
		code = 1
	}
	os.Exit(code)
}

func TestSoundcheck(t *testing.T) {
	fakeSoundcheck(t, "0")
	r := Soundcheck(context.Background(), "https://staging.shop.test", "http://127.0.0.1:8025")
	if !r.Ran || r.ExitCode != 0 || r.Schema != "rhythm-soundcheck/v1" || r.Counts["pages"] != 4 || SoundcheckFinding(r) != nil {
		t.Errorf("exit 0: %+v", r)
	}
	fakeSoundcheck(t, "1")
	r = Soundcheck(context.Background(), "https://staging.shop.test", "")
	f := SoundcheckFinding(r)
	if !r.Ran || r.ExitCode != 1 || r.Counts["failed"] != 2 || f == nil || f.ID != core.FindingSoundcheckFailed || f.Severity != core.SeverityMedium {
		t.Errorf("exit 1: %+v %+v", r, f)
	}
	t.Setenv("PATH", t.TempDir())
	r = Soundcheck(context.Background(), "https://staging.shop.test", "")
	if r.Ran || r.Note != "Soundcheck not run: install it with rhythm install soundcheck" || SoundcheckFinding(r) != nil {
		t.Errorf("missing: %+v", r)
	}
	// A site that isn't an http(s) URL never reaches Soundcheck, where
	// a leading "-" would be read as a flag.
	fakeSoundcheck(t, "0")
	for _, u := range []string{"--config=/etc/passwd", "-h", "ftp://staging.shop.test", ""} {
		r = Soundcheck(context.Background(), u, "")
		if r.Ran || r.Note != SoundcheckNotURL || u != "" && strings.Contains(r.Note, u) {
			t.Errorf("%q: %+v", u, r)
		}
	}
	// A bare host is run as https, the way --site reads it.
	if r = Soundcheck(context.Background(), "staging.shop.test", ""); !r.Ran || r.ExitCode != 0 {
		t.Errorf("bare host: %+v", r)
	}
}

func TestRobotsAndMeta(t *testing.T) {
	for body, want := range map[string]bool{
		"User-agent: *\nDisallow: /":                            false,
		"User-agent: *\nDisallow: /wp-admin/":                   true,
		"User-agent: Googlebot\nDisallow: /":                    true,
		"User-agent: Bingbot\nUser-agent: *\nDisallow: / # all": false,
		"":                                     true,
		"User-agent: *\nDisallow: /\nAllow: /": true,
		"User-agent: *\nAllow: /wp-content/\nDisallow: /": false,
		"User-agent: *\nDisallow:":                        true,
	} {
		if got := robotsAllowAll([]byte(body)); got != want {
			t.Errorf("robotsAllowAll(%q) = %v", body, got)
		}
	}
	for v, want := range map[string]bool{"noindex": true, "NOINDEX, nofollow": true, "none": true, "googlebot: noindex": false, "nofollow": false, "": false} {
		if got := hasNoindex([]string{v}); got != want {
			t.Errorf("hasNoindex(%q) = %v", v, got)
		}
	}
	for b, want := range map[string]bool{
		`<meta name="robots" content="noindex">`:            true,
		`<meta content="noindex, nofollow" name=robots />`:  true,
		`<meta name="description" content="noindex">`:       false,
		`<meta name="robots" content="index,follow">`:       false,
		`<meta name="robots"`:                               false,
		`<meta name="robots" content="index, nonexistent">`: false,
		`<meta name="googlebot" content="none">`:            false,
		`<meta name="bingbot" content="noindex">`:           false,
	} {
		if got := metaNoindex([]byte(b)); got != want {
			t.Errorf("metaNoindex(%s) = %v", b, got)
		}
	}
}

// fuzzBound is the time one fuzz input may take: generous, since the
// inputs are small and only a blow-up should fail.
func fuzzBound() time.Duration {
	if raceEnabled {
		return 15 * time.Second
	}
	return time.Second
}

// FuzzRobots reads untrusted robots.txt bodies: no panic and a linear
// bound; a group of User-agent: * with Disallow: / blocks unless an
// Allow: / follows.
func FuzzRobots(f *testing.F) {
	for _, s := range []string{
		"User-agent: *\nDisallow: /", "User-agent: *\nDisallow: /wp-admin/", "User-agent: Bingbot\nUser-agent: *\nDisallow: / # all",
		"user-agent:*\r\ndisallow:/\r\n", "Disallow: /", "User-agent: *\n\nUser-agent: GPTBot\nDisallow: /", "", "\xff:\x00", ":::\n#",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		start := time.Now()
		robotsAllowAll(b)
		if d := time.Since(start); d > fuzzBound() {
			t.Fatalf("%d bytes took %v", len(b), d)
		}
		// An Allow: / for * after it may lift the block; any other text can't.
		if robotsAllowAll(append([]byte("User-agent: *\nDisallow: /\n\n"), b...)) && !bytes.Contains(bytes.ReplaceAll(bytes.ToLower(b), []byte("disallow"), nil), []byte("allow")) {
			t.Fatalf("a leading * block didn't block with %q after it", b)
		}
	})
}

// FuzzMetaNoindex reads untrusted page bodies for meta robots: no panic
// and a linear bound; an added noindex tag is always found.
func FuzzMetaNoindex(f *testing.F) {
	for _, s := range []string{
		`<meta name="robots" content="noindex">`, `<meta content="noindex, nofollow" name=robots />`,
		`<meta name="description" content="noindex">`, `<meta name="robots"`, `<meta name='`, `<meta name=`,
		`<metaname=robots content=noindex>`, "<meta\tname=\"googlebot\"\ncontent=\"none\">", "<<meta<meta>>",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		start := time.Now()
		metaNoindex(b)
		if d := time.Since(start); d > fuzzBound() {
			t.Fatalf("%d bytes took %v", len(b), d)
		}
		if bytes.IndexByte(b, '<') < 0 && !metaNoindex(append(b, `<meta name="robots" content="noindex">`...)) {
			t.Fatalf("noindex missed after %q", b)
		}
		_ = attr(string(bytes.ToLower(b)), "content")
	})
}

// FuzzPHPArrayValue reads untrusted wp-config array source: no panic,
// and a value found is always a substring of the source.
func FuzzPHPArrayValue(f *testing.F) {
	for _, s := range []string{
		`array('bucket' => 'shop-media', "access-key-id" => "AKIA")`, `'bucket'=>`, `'bucket' => '`, `"bucket"`, `'bucket' => 5`, "",
	} {
		f.Add(s, "bucket")
	}
	f.Fuzz(func(t *testing.T, src, key string) {
		if v := phpArrayValue(src, key); v != "" && !strings.Contains(src, v) {
			t.Fatalf("phpArrayValue(%q, %q) = %q, not in the source", src, key, v)
		}
	})
}

// Echo's redirects stay on its own host: a redirect elsewhere is not
// followed and the check fails closed.
func TestEchoRedirectOffHost(t *testing.T) {
	hit := false
	other := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"seal","wire":1,"providers":[]}`)
	}), false)
	bounce := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}), false)
	res := siteRun(t, Options{Echo: true, EchoURL: bounce.URL})
	if f := find(res, core.FindingSideEffectMail); f == nil || !strings.Contains(f.Title, "isn't reachable") {
		t.Errorf("redirect off host: %+v", res.Findings)
	}
	if hit {
		t.Error("verify followed Echo's redirect to another host")
	}
	// A redirect on Echo's own host is followed.
	same := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/health":
			http.Redirect(w, r, "/v1/health2", http.StatusFound)
		case "/v1/health2":
			_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"seal","wire":1,"providers":[]}`)
		default:
			_, _ = io.WriteString(w, `{"messages":[]}`)
		}
	}), false)
	res = siteRun(t, Options{Echo: true, EchoURL: same.URL})
	if find(res, core.FindingSideEffectMail) != nil {
		t.Errorf("redirect on host: %+v", res.Findings)
	}
}

// Echo's mode reaches a finding title without control or bidi
// characters.
func TestEchoModeControlChars(t *testing.T) {
	s := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/health" {
			_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":"obs\u001b[2Jerve\u202e\u0007","wire":1,"providers":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"messages":[]}`)
	}), false)
	res := siteRun(t, Options{Echo: true, EchoURL: s.URL})
	f := find(res, core.FindingSideEffectMail)
	if f == nil || f.Title != "Echo is in obs[2Jerve mode, so mail from this copy reaches real people" {
		t.Errorf("mode title: %+v", res.Findings)
	}
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{{"seal", 40, "seal"}, {"a\nb\tc", 40, "abc"}, {"", 40, ""}, {"abcdef", 3, "abc..."}, {"abc", 3, "abc"}, {"\xffok", 40, "ok"}} {
		if got := printable(c.in, c.n); got != c.want {
			t.Errorf("printable(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
