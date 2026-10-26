// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// The side-effect drill (qa.md 6.3 step 4, F8). While Roadie prepares a
// staging the traffic generator hits it every 500 ms and, for r13, a
// host-cron container calls wp-cron.php every 30 s. After the run the
// drill does what a live shop does for five minutes: due cron, the
// Action Scheduler queue, an order, a password reset, a subscription
// renewal and a webhook. Gate 3 then reads what reached the stand-ins
// for the internet: the sinkhole (any connection is a leak), the stubs
// (a call with a live key is a leak) and Echo (a capture to a real
// recipient is a leak; one to a reserved fake domain is the seal
// working).

// stagingHost is the staging site's host on the sealed network; the
// traffic generator sends it as Host through the gate.
const stagingHost = "staging-wp"

// TrafficStats counts the generator's requests by path and status. It
// is not a gate: it shows the site was busy during the run.
type TrafficStats struct {
	Requests int            `json:"requests"`
	Errors   int            `json:"errors"`
	ByPath   map[string]int `json:"by_path"`
	ByStatus map[string]int `json:"by_status"`
}

// Traffic requests the staging pages in turn every Every until stopped.
type Traffic struct {
	// Base is the gate's staging port, http://127.0.0.1:18921.
	Base string
	// Host is sent as the Host header (staging-wp).
	Host string
	// Paths are requested in turn: home, shop, cart, checkout, wp-admin
	// (with Cookies) and wp-cron.php.
	Paths []string
	// Admin are the paths sent with Cookies (a logged-in admin).
	Admin   []string
	Cookies []*http.Cookie
	Every   time.Duration
}

// DefaultPaths are the pages the generator hits when WP-CLI can't name
// the shop's own cart and checkout pages.
var DefaultPaths = []string{"/", "/?post_type=product", "/?page_id=cart", "/?page_id=checkout", "/wp-admin/", "/wp-cron.php"}

// loopbackOnly dials 127.0.0.1 and nothing else, so a generator bug can
// never reach a real site.
func loopbackOnly(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("traffic: refusing to dial %s: only loopback", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// localClient never follows a redirect (a raw copy redirects to
// production) and dials loopback only.
func localClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{DialContext: loopbackOnly, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Run requests until ctx ends and returns the counts.
func (t Traffic) Run(ctx context.Context) TrafficStats {
	st := TrafficStats{ByPath: map[string]int{}, ByStatus: map[string]int{}}
	if len(t.Paths) == 0 {
		return st
	}
	c := localClient(10 * time.Second)
	every := t.Every
	if every <= 0 {
		every = 500 * time.Millisecond
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for i := 0; ; i++ {
		p := t.Paths[i%len(t.Paths)]
		st.Requests++
		st.ByPath[p]++
		status, err := t.get(ctx, c, p)
		if err != nil {
			if ctx.Err() != nil {
				st.Requests--
				st.ByPath[p]--
				return st
			}
			st.Errors++
		} else {
			st.ByStatus[fmt.Sprint(status)]++
		}
		select {
		case <-ctx.Done():
			return st
		case <-tick.C:
		}
	}
}

func (t Traffic) get(ctx context.Context, c *http.Client, path string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Host = t.Host
	if slices.Contains(t.Admin, path) {
		for _, ck := range t.Cookies {
			req.AddCookie(ck)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// wpCLI runs WP-CLI in the staging container as www-data.
func wpCLI(ctx context.Context, ex Execer, args ...string) ([]byte, error) {
	return ex.Exec(ctx, container(stagingHost), "www-data", nil, nil, append([]string{"wp", "--path=/var/www/html"}, args...)...)
}

// stagingPages asks WP-CLI for the shop's page paths and an admin's
// cookies. Without WooCommerce or an admin it falls back to DefaultPaths
// and no cookies (wp-admin then answers with a redirect to the login).
func stagingPages(ctx context.Context, ex Execer) (paths []string, cookies []*http.Cookie) {
	const php = `$p = array("/");
if (function_exists("wc_get_page_permalink")) { foreach (array("shop", "cart", "checkout") as $k) { $p[] = wp_make_link_relative(wc_get_page_permalink($k)); } }
$p[] = "/wp-admin/"; $p[] = "/wp-cron.php";
$c = array(); $a = get_users(array("role" => "administrator", "number" => 1));
if ($a) { $e = time() + 7200; $c[AUTH_COOKIE] = wp_generate_auth_cookie($a[0]->ID, $e, "auth"); $c[LOGGED_IN_COOKIE] = wp_generate_auth_cookie($a[0]->ID, $e, "logged_in"); }
echo wp_json_encode(array("paths" => $p, "cookies" => $c));`
	out, err := wpCLI(ctx, ex, "eval", php)
	var v struct {
		Paths   []string          `json:"paths"`
		Cookies map[string]string `json:"cookies"`
	}
	if err != nil || json.Unmarshal(lastLine(out), &v) != nil || len(v.Paths) == 0 {
		return slices.Clone(DefaultPaths), nil
	}
	for _, p := range v.Paths {
		if p == "" || !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	names := make([]string, 0, len(v.Cookies))
	for n := range v.Cookies {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// A request cookie for the plain-HTTP staging behind the gate: the
		// Secure, HttpOnly and SameSite attributes only mean something on a
		// response.
		cookies = append(cookies, &http.Cookie{Name: n, Value: v.Cookies[n], HttpOnly: true, SameSite: http.SameSiteLaxMode}) // #nosec G124 -- sent, never set
	}
	return paths, cookies
}

// lastLine returns the last non-empty line of WP-CLI's output (PHP
// notices can come first).
func lastLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return bytes.TrimSpace(lines[len(lines)-1])
}

// DrillStep is one thing a live shop does, run with WP-CLI on staging.
type DrillStep struct {
	Name string
	Args []string // after "wp"
}

// DrillSteps are the drill's rounds, in order. The Action Scheduler step
// is a documented bypass: it runs the queue even when Roadie paused the
// runner, so only the blanked credentials and the seal can stop what the
// queued actions send.
var DrillSteps = []DrillStep{
	{"cron", []string{"cron", "event", "run", "--due-now"}},
	{"action-scheduler", []string{"action-scheduler", "run"}},
	{"order", []string{"eval", `if (function_exists("wc_create_order")) { $o = wc_create_order(); $o->set_billing_email("drill@example.invalid"); $o->set_billing_first_name("Drill"); $o->calculate_totals(); $o->update_status("processing"); }`}},
	{"password-reset", []string{"eval", `$u = get_users(array("number" => 1, "orderby" => "ID")); if ($u) { retrieve_password($u[0]->user_login); }`}},
	{"renewal", []string{"eval", `if (function_exists("wcs_get_subscriptions")) { foreach (wcs_get_subscriptions(array("subscription_status" => "active", "subscriptions_per_page" => 5)) as $s) { do_action("woocommerce_scheduled_subscription_payment", $s->get_id()); } }`}},
	{"webhook", []string{"eval", `if (class_exists("WC_Webhook_Data_Store")) { foreach ((new WC_Webhook_Data_Store())->search_webhooks(array("limit" => -1)) as $id) { $w = wc_get_webhook($id); if ($w && "active" === $w->get_status()) { $w->deliver(array("drill" => 1)); } } }`}},
}

// DrillResult says what the drill ran: rounds and, per step, how often it
// exited non-zero. A failing step is not a gate (a blanked key makes the
// gateway step fail, which is the point); gate 3 reads the logs.
type DrillResult struct {
	Rounds int            `json:"rounds"`
	Failed map[string]int `json:"failed,omitempty"`
}

// Drill runs DrillSteps in rounds until For has passed (at least one
// round), with a pause of Pause between rounds.
type Drill struct {
	Exec  Execer
	For   time.Duration
	Pause time.Duration
	now   func() time.Time
}

// Run runs the drill.
func (d Drill) Run(ctx context.Context) DrillResult {
	now := d.now
	if now == nil {
		now = time.Now
	}
	res := DrillResult{Failed: map[string]int{}}
	end := now().Add(d.For)
	for res.Rounds == 0 || now().Before(end) {
		if ctx.Err() != nil {
			break
		}
		for _, st := range DrillSteps {
			if _, err := wpCLI(ctx, d.Exec, st.Args...); err != nil {
				res.Failed[st.Name]++
			}
		}
		res.Rounds++
		if d.Pause > 0 && now().Before(end) {
			select {
			case <-ctx.Done():
			case <-time.After(d.Pause):
			}
		}
	}
	if len(res.Failed) == 0 {
		res.Failed = nil
	}
	return res
}

// HostCron starts the host-cron container: wget of wp-cron.php on
// staging every interval, as a system cron would. It joins the sealed
// network under the Compose project's labels, so make down removes it
// too. stop removes it.
func HostCron(ctx context.Context, every time.Duration) (stop func(), err error) {
	name := project + "-host-cron-1"
	_, _ = docker(ctx, nil, "rm", "-f", name)
	script := fmt.Sprintf("while :; do wget -q -O /dev/null 'http://%s/wp-cron.php?doing_wp_cron' || true; sleep %d; done", stagingHost, int(every.Seconds()))
	_, err = docker(ctx, nil, "run", "-d", "--name", name, "--network", sealedNet, "--dns", sinkholeIP,
		"--label", "com.docker.compose.project="+project, "--label", "com.docker.compose.service=host-cron",
		"--label", "com.docker.compose.container-number=1", "--label", "com.docker.compose.oneoff=False",
		"roadie-testbed-runner:1", "sh", "-c", script)
	if err != nil {
		return func() {}, err
	}
	return func() { _, _ = docker(context.Background(), nil, "rm", "-f", name) }, nil
}

// sinkholeIP is the sinkhole's address on the sealed network
// (docker-compose.yml), every container's DNS server.
const sinkholeIP = "172.31.120.53"

// SinkholeRecord is a line of runtime/log/sinkhole.jsonl (see
// testbed/sinkhole). First is what the client sent first; the harness
// looks for markers in it and never prints it.
type SinkholeRecord struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	Name  string `json:"name,omitempty"`
	Port  int    `json:"port,omitempty"`
	SNI   string `json:"sni,omitempty"`
	Host  string `json:"host,omitempty"`
	First []byte `json:"first,omitempty"`
}

// StubRecord is a line of runtime/log/stubs.jsonl (see testbed/stubs).
// Key is already masked there.
type StubRecord struct {
	Host        string `json:"host"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Live        bool   `json:"live"`
}

// EchoMessage is the part of an Echo capture gate 3 reads.
type EchoMessage struct {
	Channel string   `json:"channel"`
	To      []string `json:"to,omitempty"`
	CC      []string `json:"cc,omitempty"`
	BCC     []string `json:"bcc,omitempty"`
}

// Mark is a place in the two logs: the harness reads only what was
// written after it.
type Mark struct {
	Sinkhole int64 `json:"-"`
	Stubs    int64 `json:"-"`
}

// Logs are the testbed's runtime logs.
type Logs struct {
	Sinkhole string // runtime/log/sinkhole.jsonl
	Stubs    string // runtime/log/stubs.jsonl
}

// Mark returns the logs' current ends.
func (l Logs) Mark() Mark {
	return Mark{Sinkhole: size(l.Sinkhole), Stubs: size(l.Stubs)}
}

func size(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// readJSONL decodes the lines of path after offset into fn. A missing
// file has no lines; a torn last line (still being written) is skipped.
func readJSONL[T any](path string, offset int64, fn func(T)) error {
	f, err := os.Open(path) // #nosec G304 -- the testbed's own log
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var v T
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			fn(v)
		}
	}
	return sc.Err()
}

// Leak is one connection or call gate 3 counts, without any payload.
type Leak struct {
	Where string `json:"where"` // sinkhole, stubs, echo
	What  string `json:"what"`  // "tcp 443 sni=api.example.com", "POST api.stripe.com/v1/charges", "email to 1 real recipient"
}

// Gate3 is the side-effect gate's verdict for one window.
type Gate3 struct {
	Sinkhole       int    `json:"sinkhole"`             // TCP connections that reached the sinkhole
	DNS            int    `json:"dns"`                  // lookups the sinkhole answered (not a failure on their own)
	StubCalls      int    `json:"stub_calls"`           // calls to the payment and messaging stubs
	StubLive       int    `json:"stub_live"`            // of them, calls with a live key
	EchoCaptures   int    `json:"echo_captures"`        // messages Echo caught (sealed)
	EchoRealRecips int    `json:"echo_real_recipients"` // captures to a recipient outside the reserved domains
	Leaks          []Leak `json:"leaks"`
	// Egress lists the r23 paths whose marker reached the sinkhole.
	Egress []string `json:"egress,omitempty"`
	// EchoUnread is set when Echo's captures couldn't be cleared or read
	// in a window where Echo should be up: mail it caught for a real
	// person would go uncounted, so the gate can't pass.
	EchoUnread bool `json:"echo_unread,omitempty"`
	// Notes are reasons a part could not be read (Echo down, say).
	Notes []string `json:"notes,omitempty"`
}

// Pass reports whether nothing got out: zero sinkhole connections, zero
// stub calls with a live key, zero captures to real recipients and
// Echo's captures read.
func (g Gate3) Pass() bool {
	return g.Sinkhole == 0 && g.StubLive == 0 && g.EchoRealRecips == 0 && !g.EchoUnread
}

// egressMarker is what r23's plugin puts in the first bytes of each path.
const egressMarker = "testbed-egress:"

// Judge reads the logs written after m and Echo's captures and returns
// gate 3. real holds values that mark a recipient as a real person even
// on another channel (the scenario's canary phones and emails).
func Judge(l Logs, m Mark, echo []EchoMessage, real map[string]bool) (Gate3, error) {
	g := Gate3{Leaks: []Leak{}}
	egress := map[string]bool{}
	err := readJSONL(l.Sinkhole, m.Sinkhole, func(r SinkholeRecord) {
		switch r.Kind {
		case "dns":
			g.DNS++
		case "tcp":
			g.Sinkhole++
			what := fmt.Sprintf("tcp %d", r.Port)
			if r.SNI != "" {
				what += " sni=" + r.SNI
			}
			if r.Host != "" {
				what += " host=" + r.Host
			}
			g.Leaks = append(g.Leaks, Leak{"sinkhole", what})
			if i := bytes.Index(r.First, []byte(egressMarker)); i >= 0 {
				rest := r.First[i+len(egressMarker):]
				end := bytes.IndexFunc(rest, func(c rune) bool { return c != '_' && (c < 'a' || c > 'z') })
				if end < 0 {
					end = len(rest)
				}
				egress[string(rest[:end])] = true
			}
		}
	})
	if err != nil {
		return g, fmt.Errorf("sinkhole log: %w", err)
	}
	err = readJSONL(l.Stubs, m.Stubs, func(r StubRecord) {
		g.StubCalls++
		if r.Live {
			g.StubLive++
			g.Leaks = append(g.Leaks, Leak{"stubs", fmt.Sprintf("%s %s%s with a live key %s", r.Method, r.Host, r.Path, r.Key)})
		}
	})
	if err != nil {
		return g, fmt.Errorf("stubs log: %w", err)
	}
	for _, msg := range echo {
		g.EchoCaptures++
		n := 0
		for _, to := range slices.Concat(msg.To, msg.CC, msg.BCC) {
			if realRecipient(to, real) {
				n++
			}
		}
		if n > 0 {
			g.EchoRealRecips++
			g.Leaks = append(g.Leaks, Leak{"echo", fmt.Sprintf("%s to %d real recipient(s)", cmpOr(msg.Channel, "message"), n)})
		}
	}
	for p := range egress {
		g.Egress = append(g.Egress, p)
	}
	sort.Strings(g.Egress)
	return g, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// realRecipient reports whether a recipient is a real person: an email
// outside the reserved domains, or a canary value.
func realRecipient(to string, real map[string]bool) bool {
	to = strings.TrimSpace(to)
	if real[to] || real[strings.ToLower(to)] {
		return true
	}
	if i := strings.LastIndexByte(to, '<'); i >= 0 {
		to = strings.TrimSuffix(to[i+1:], ">")
	}
	at := strings.LastIndexByte(to, '@')
	if at < 0 {
		return false
	}
	return !core.ReservedEmailDomain(to[at+1:])
}

// Echo reads and clears Echo's captures through the gate.
type Echo struct {
	Base  string // http://127.0.0.1:18924
	Token string // RHYTHM_ECHO_TOKEN from runtime/echo.env
}

// Messages returns every capture.
func (e Echo) Messages(ctx context.Context) ([]EchoMessage, error) {
	var ms []EchoMessage
	err := e.do(ctx, http.MethodGet, "/v1/messages", &ms)
	return ms, err
}

// Wait asks Echo for its captures until it answers or wait runs out.
// It returns the last error.
func (e Echo) Wait(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		_, err := e.Messages(ctx)
		if err == nil || !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Clear deletes every capture, so the next window starts empty.
func (e Echo) Clear(ctx context.Context) error {
	return e.do(ctx, http.MethodDelete, "/v1/messages", nil)
}

func (e Echo) do(ctx context.Context, method, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, method, e.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Echo-Token", e.Token)
	resp, err := localClient(10 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("echo %s %s: %s", method, path, resp.Status)
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into)
}

// Window is one gate 3 window: the traffic generator (and host cron)
// while Roadie runs, then the drill. Start marks the logs and clears
// Echo; Finish judges.
type Window struct {
	Logs Logs
	Echo Echo
	Real map[string]bool
	// EchoDown says the scenario stopped Echo on purpose (r22): Echo
	// not answering is then a note. Otherwise it fails the gate.
	EchoDown bool

	mark     Mark
	notes    []string
	unread   bool
	cancel   context.CancelFunc
	done     chan TrafficStats
	stopCron func()
	mu       sync.Mutex
}

// Start marks the logs, clears Echo and starts traffic (when t has paths)
// and the host cron (when cron is above zero).
func (w *Window) Start(ctx context.Context, t Traffic, cron time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notes, w.unread = nil, false
	if err := w.Echo.Clear(ctx); err != nil {
		// Echo down is a scenario of its own (r22): note it and go on.
		// Anywhere else old captures would be counted, or new ones lost.
		w.notes = append(w.notes, "Echo's captures couldn't be cleared: "+err.Error())
		w.unread = !w.EchoDown
	}
	w.mark = w.Logs.Mark()
	w.stopCron = func() {}
	if cron > 0 {
		stop, err := HostCron(ctx, cron)
		if err != nil {
			return fmt.Errorf("host cron: %w", err)
		}
		w.stopCron = stop
	}
	tctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.done = make(chan TrafficStats, 1)
	go func() { w.done <- t.Run(tctx) }()
	return nil
}

// Stop ends the traffic and the host cron and returns the traffic counts.
func (w *Window) Stop() TrafficStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel == nil {
		return TrafficStats{}
	}
	w.cancel()
	st := <-w.done
	w.cancel = nil
	w.stopCron()
	return st
}

// Judge reads everything since Start.
func (w *Window) Judge(ctx context.Context) (Gate3, error) {
	notes := slices.Clone(w.notes)
	unread := w.unread
	ms, err := w.Echo.Messages(ctx)
	if err != nil {
		notes = append(notes, "Echo's captures couldn't be read: "+err.Error())
		unread = unread || !w.EchoDown
	}
	g, err := Judge(w.Logs, w.mark, ms, w.Real)
	g.Notes = append(g.Notes, notes...)
	g.EchoUnread = unread
	return g, err
}
