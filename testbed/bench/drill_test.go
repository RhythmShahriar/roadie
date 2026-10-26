// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The lane's test ports (127.0.0.1:33120-33139).
const (
	portTraffic = "127.0.0.1:33120"
	portEcho    = "127.0.0.1:33121"
)

func writeLines(t *testing.T, path string, recs ...any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func testLogs(t *testing.T) Logs {
	dir := t.TempDir()
	return Logs{Sinkhole: filepath.Join(dir, "sinkhole.jsonl"), Stubs: filepath.Join(dir, "stubs.jsonl")}
}

// One connection to the sinkhole fails the gate; a DNS lookup alone
// doesn't; nothing written before the mark counts.
func TestJudgeSinkhole(t *testing.T) {
	l := testLogs(t)
	writeLines(t, l.Sinkhole, SinkholeRecord{Kind: "tcp", Port: 443, SNI: "before.example.com"})
	m := l.Mark()
	writeLines(t, l.Sinkhole, SinkholeRecord{Kind: "dns", Name: "api.example.com"})
	g, err := Judge(l, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Pass() || g.DNS != 1 || g.Sinkhole != 0 {
		t.Fatalf("a lookup alone must pass: %+v", g)
	}
	writeLines(t, l.Sinkhole, SinkholeRecord{Kind: "tcp", Port: 587, Host: "smtp.example.com", First: []byte("EHLO staging")})
	g, err = Judge(l, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.Pass() || g.Sinkhole != 1 || len(g.Leaks) != 1 {
		t.Fatalf("one sinkhole hit must fail: %+v", g)
	}
	if want := "tcp 587 host=smtp.example.com"; g.Leaks[0].What != want {
		t.Errorf("leak %q, want %q", g.Leaks[0].What, want)
	}
	if strings.Contains(g.Leaks[0].What, "EHLO") {
		t.Error("a leak must never carry the bytes the client sent")
	}
}

func TestJudgeStubs(t *testing.T) {
	l := testLogs(t)
	m := l.Mark()
	writeLines(t, l.Stubs, StubRecord{Host: "api.stripe.com", Method: "POST", Path: "/v1/charges", Key: "sk_t•••", Live: false})
	g, err := Judge(l, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Pass() || g.StubCalls != 1 {
		t.Fatalf("a call with a test key must pass: %+v", g)
	}
	writeLines(t, l.Stubs, StubRecord{Host: "api.stripe.com", Method: "POST", Path: "/v1/charges", Key: "sk_l•••", Live: true})
	g, _ = Judge(l, m, nil, nil)
	if g.Pass() || g.StubLive != 1 {
		t.Fatalf("a call with a live key must fail: %+v", g)
	}
}

func TestJudgeEcho(t *testing.T) {
	l := testLogs(t)
	real := map[string]bool{"+447911123456": true}
	cases := []struct {
		msg  EchoMessage
		pass bool
	}{
		{EchoMessage{Channel: "email", To: []string{"user-17@example.invalid"}}, true},
		{EchoMessage{Channel: "email", To: []string{"Drill <drill@example.invalid>"}, CC: []string{"ops@staging.test"}}, true},
		{EchoMessage{Channel: "email", To: []string{"orders@example.com"}}, true},
		{EchoMessage{Channel: "webhook"}, true},
		{EchoMessage{Channel: "email", To: []string{"ada.kowalczyk@gmail.com"}}, false},
		{EchoMessage{Channel: "email", To: []string{"a@example.invalid"}, BCC: []string{"Ada <ada@outlook.com>"}}, false},
		{EchoMessage{Channel: "sms", To: []string{"+447911123456"}}, false},
	}
	for _, c := range cases {
		g, err := Judge(l, Mark{}, []EchoMessage{c.msg}, real)
		if err != nil {
			t.Fatal(err)
		}
		if g.Pass() != c.pass {
			t.Errorf("%+v: pass %v, want %v (%+v)", c.msg, g.Pass(), c.pass, g)
		}
		if g.EchoCaptures != 1 {
			t.Errorf("%+v: %d captures counted", c.msg, g.EchoCaptures)
		}
	}
}

func TestJudgeEgressMarkers(t *testing.T) {
	l := testLogs(t)
	writeLines(t, l.Sinkhole,
		SinkholeRecord{Kind: "tcp", Port: 80, First: []byte("GET /testbed-egress:curl HTTP/1.1\r\n")},
		SinkholeRecord{Kind: "tcp", Port: 587, First: []byte("EHLO testbed-egress:smtp_socket\r\n")},
		SinkholeRecord{Kind: "tcp", Port: 443, First: []byte{0x16, 0x03, 0x01}},
	)
	g, err := Judge(l, Mark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"curl", "smtp_socket"}; !slices.Equal(g.Egress, want) {
		t.Errorf("egress %v, want %v", g.Egress, want)
	}
	if g.Sinkhole != 3 || g.Pass() {
		t.Errorf("every connection counts: %+v", g)
	}
}

func TestReadJSONLSkipsTornLines(t *testing.T) {
	l := testLogs(t)
	writeLines(t, l.Sinkhole, SinkholeRecord{Kind: "tcp", Port: 25})
	f, err := os.OpenFile(l.Sinkhole, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"kind":"tcp","po`)
	_ = f.Close()
	g, err := Judge(l, Mark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.Sinkhole != 1 {
		t.Errorf("%d connections, want 1 (the torn line skipped)", g.Sinkhole)
	}
	// A missing log has no lines.
	if _, err := Judge(Logs{Sinkhole: "/nonexistent/a", Stubs: "/nonexistent/b"}, Mark{}, nil, nil); err != nil {
		t.Errorf("a missing log is empty, got %v", err)
	}
}

func listen(t *testing.T, addr string, h http.Handler) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("lane port %s busy: %v", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestTraffic(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]string{}
	listen(t, portTraffic, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		c, _ := r.Cookie("wordpress_logged_in_x")
		v := r.Host + " " + r.URL.RequestURI()
		if c != nil {
			v += " cookie"
		}
		seen[r.URL.Path] = append(seen[r.URL.Path], v)
		mu.Unlock()
		if r.URL.Path == "/" {
			// A raw copy redirects to production: never follow it.
			http.Redirect(w, r, "https://shop.example.com/", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	tr := Traffic{Base: "http://" + portTraffic, Host: stagingHost, Paths: []string{"/", "/wp-admin/", "/wp-cron.php"},
		Admin: []string{"/wp-admin/"}, Cookies: []*http.Cookie{{Name: "wordpress_logged_in_x", Value: "v"}}, Every: 5 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	st := tr.Run(ctx)
	if st.Requests < 6 || st.Errors != 0 {
		t.Fatalf("stats %+v", st)
	}
	if st.ByStatus["301"] == 0 || st.ByStatus["200"] == 0 {
		t.Errorf("by status %v: the redirect must be counted, not followed", st.ByStatus)
	}
	mu.Lock()
	defer mu.Unlock()
	for path, vs := range seen {
		for _, v := range vs {
			if !strings.HasPrefix(v, stagingHost+" ") {
				t.Errorf("%s: Host %q, want %s", path, v, stagingHost)
			}
			if strings.HasSuffix(v, "cookie") != (path == "/wp-admin/") {
				t.Errorf("%s: %q: cookies go to wp-admin only", path, v)
			}
		}
	}
}

func TestTrafficDialsLoopbackOnly(t *testing.T) {
	if _, err := loopbackOnly(context.Background(), "tcp", "192.0.2.1:80"); err == nil || !strings.Contains(err.Error(), "only loopback") {
		t.Errorf("a public address must be refused, got %v", err)
	}
	if _, err := loopbackOnly(context.Background(), "tcp", "shop.example.com:80"); err == nil {
		t.Error("a name must be refused")
	}
}

// fakeExec records commands and answers from a script.
type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	fail  func(args []string) bool
	out   func(args []string) []byte
}

func (f *fakeExec) Exec(_ context.Context, c, user string, env []string, _ io.Reader, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{c, user}, args...))
	var out []byte
	if f.out != nil {
		out = f.out(args)
	}
	if f.fail != nil && f.fail(args) {
		return out, &ExitError{Code: 1}
	}
	return out, nil
}

func TestDrillRunsEveryStep(t *testing.T) {
	ex := &fakeExec{fail: func(args []string) bool { return slices.Contains(args, "action-scheduler") }}
	res := Drill{Exec: ex, For: 0}.Run(context.Background())
	if res.Rounds != 1 {
		t.Fatalf("rounds %d, want 1", res.Rounds)
	}
	if len(ex.calls) != len(DrillSteps) {
		t.Fatalf("%d calls, want %d", len(ex.calls), len(DrillSteps))
	}
	for i, c := range ex.calls {
		if c[0] != container(stagingHost) || c[1] != "www-data" || c[2] != "wp" {
			t.Errorf("call %d %q: WP-CLI as www-data in staging", i, c[:3])
		}
		if !slices.Equal(c[4:], DrillSteps[i].Args) {
			t.Errorf("call %d %q, want step %s", i, c[4:], DrillSteps[i].Name)
		}
	}
	if res.Failed["action-scheduler"] != 1 || len(res.Failed) != 1 {
		t.Errorf("failed %v", res.Failed)
	}
	names := map[string]bool{}
	for _, s := range DrillSteps {
		names[s.Name] = true
	}
	for _, want := range []string{"cron", "action-scheduler", "order", "password-reset", "renewal", "webhook"} {
		if !names[want] {
			t.Errorf("the drill lacks step %s (qa.md 6.3 step 4)", want)
		}
	}
}

func TestDrillRunsForItsTime(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	ex := &fakeExec{out: func([]string) []byte { now = now.Add(time.Second); return nil }}
	res := Drill{Exec: ex, For: 30 * time.Second, now: clock}.Run(context.Background())
	if want := 5; res.Rounds != want { // 6 steps a round, a second each
		t.Errorf("rounds %d, want %d", res.Rounds, want)
	}
}

func TestStagingPages(t *testing.T) {
	ex := &fakeExec{out: func([]string) []byte {
		return []byte("PHP Notice: something\n" + `{"paths":["/","shop/","/cart/","/","/wp-admin/"],"cookies":{"wordpress_logged_in_a":"x","wordpress_a":"y"}}` + "\n")
	}}
	paths, cookies := stagingPages(context.Background(), ex)
	if want := []string{"/", "/shop/", "/cart/", "/wp-admin/"}; !slices.Equal(paths, want) {
		t.Errorf("paths %q, want %q", paths, want)
	}
	if len(cookies) != 2 || cookies[0].Name != "wordpress_a" {
		t.Errorf("cookies %+v", cookies)
	}
	paths, cookies = stagingPages(context.Background(), &fakeExec{fail: func([]string) bool { return true }})
	if !slices.Equal(paths, DefaultPaths) || cookies != nil {
		t.Errorf("fallback %q %v", paths, cookies)
	}
}

func TestWindow(t *testing.T) {
	var mu sync.Mutex
	var deletes int
	token := "t" + "ok"
	listen(t, portEcho, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Echo-Token") != token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode([]EchoMessage{{Channel: "email", To: []string{"real@gmail.com"}}})
		}
	}))
	l := testLogs(t)
	writeLines(t, l.Sinkhole, SinkholeRecord{Kind: "tcp", Port: 443})
	w := &Window{Logs: l, Echo: Echo{Base: "http://" + portEcho, Token: token}}
	if err := w.Start(context.Background(), Traffic{}, 0); err != nil {
		t.Fatal(err)
	}
	w.Stop()
	g, err := w.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deletes != 1 || g.Sinkhole != 0 || g.EchoRealRecips != 1 || g.Pass() {
		t.Errorf("deletes %d, gate %+v", deletes, g)
	}

	// Echo down (r22) is a note, not an error.
	w = &Window{Logs: l, Echo: Echo{Base: "http://127.0.0.1:33139", Token: token}}
	if err := w.Start(context.Background(), Traffic{}, 0); err != nil {
		t.Fatal(err)
	}
	g, err = w.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Notes) != 2 || !g.Pass() {
		t.Errorf("Echo down: %+v", g)
	}
}

func TestExitError(t *testing.T) {
	var err error = &ExitError{Code: 3, Stderr: "secret stuff"}
	var ee *ExitError
	if !errors.As(err, &ee) || err.Error() != "exit status 3" {
		t.Errorf("%v", err)
	}
}

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.env")
	if err := os.WriteFile(p, []byte("# c\nA=1\n\nB = two=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := readEnvFile(p)
	if err != nil || m["A"] != "1" || m["B"] != "two=2" || len(m) != 2 {
		t.Errorf("%v %v", m, err)
	}
}
