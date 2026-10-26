// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// listen opens a listener on the first free port of this lane's range
// (127.0.0.1:33060-33079); tests never use a random or public port.
func listen(t *testing.T) net.Listener {
	t.Helper()
	for p := 33060; p <= 33079; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return l
		}
	}
	t.Fatal("no free port in 33060-33079")
	return nil
}

// serve starts h on a lane port; tls serves it with httptest's
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
	}

	// Noindex on the page but robots.txt lets every crawler in.
	half := &siteHandler{header: map[string]string{"X-Robots-Tag": "noindex"}, robots: "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n"}
	res = siteRun(t, Options{SiteURL: serve(t, half, false).URL})
	if f := find(res, core.FindingSearchIndexable); f == nil || !strings.Contains(f.Title, "robots.txt") {
		t.Errorf("robots allows: %+v", res.Findings)
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
	_, err := Run(context.Background(), Job{Source: source(options([]string{"1", "blogname", "Shop"})), Production: []string{"127.0.0.1"},
		Opts: Options{SiteURL: s.URL}})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrTargetIsProduction || ce.Exit != 3 {
		t.Fatalf("got %v", err)
	}
	if h.requests.Load() != 0 {
		t.Errorf("%d requests to production", h.requests.Load())
	}
}

// fakeEcho answers health in mode and lists sent messages.
func fakeEcho(t *testing.T, mode string, sent int) *httptest.Server {
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/health":
			fmt.Fprintf(w, `{"tool":"rhythm-echo","version":"1.1.2","mode":%q,"wire":1,"providers":[]}`, mode)
		case "/v1/messages":
			if r.URL.Query().Get("since") == "" {
				t.Error("messages listed without since")
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
	res := siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 0).URL})
	if find(res, core.FindingSideEffectMail) != nil {
		t.Errorf("sealed: %+v", res.Findings)
	}
	res = siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "observe", 0).URL})
	if f := find(res, core.FindingSideEffectMail); f == nil || f.Severity != core.SeverityCritical || !strings.Contains(f.Title, "observe") {
		t.Errorf("observe: %+v", res.Findings)
	}
	res = siteRun(t, Options{Echo: true, EchoURL: fakeEcho(t, "seal", 3).URL})
	if f := find(res, core.FindingSideEffectMail); f == nil || !strings.Contains(f.Title, "let 3 messages through") {
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

func TestEchoURLMustBeLocal(t *testing.T) {
	for u, ok := range map[string]bool{
		"": true, "http://127.0.0.1:8025": true, "http://localhost:8025": true, "http://10.1.2.3:8025": true,
		"http://[::1]:8025": true, "https://echo.example.com": true,
		"http://echo.example.com:8025": false, "http://8.8.8.8:8025": false, "ftp://127.0.0.1": false,
	} {
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
	if err := os.WriteFile(filepath.Join(dir, "rhythm-soundcheck"), b, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ROADIE_FAKE_SOUNDCHECK", exit)
}

// soundcheckMain is the fake Soundcheck: it checks its arguments, prints a
// report and exits with ROADIE_FAKE_SOUNDCHECK.
func soundcheckMain() {
	args := os.Args[1:]
	if len(args) < 3 || args[0] != "run" || args[2] != "--json" {
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
}

func TestRobotsAndMeta(t *testing.T) {
	for body, want := range map[string]bool{
		"User-agent: *\nDisallow: /":                            false,
		"User-agent: *\nDisallow: /wp-admin/":                   true,
		"User-agent: Googlebot\nDisallow: /":                    true,
		"User-agent: Bingbot\nUser-agent: *\nDisallow: / # all": false,
		"": true,
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
		`<meta name="robots" content="noindex">`:           true,
		`<meta content="noindex, nofollow" name=robots />`: true,
		`<meta name="description" content="noindex">`:      false,
		`<meta name="robots" content="index,follow">`:      false,
		`<meta name="robots"`:                              false,
	} {
		if got := metaNoindex([]byte(b)); got != want {
			t.Errorf("metaNoindex(%s) = %v", b, got)
		}
	}
}
