// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/verify"
)

var (
	copyArgs = []string{"--from", "ssh://deploy@shop.com/var/www/shop", "--to", "/var/www/staging", "--url", "https://staging.shop.test", "--no-config"}
	planArgs = []string{"--from", "ssh://deploy@shop.com/var/www/shop", "--to", "/var/www/staging", "--url", "https://staging.shop.test", "--no-config"}
)

func args(base []string, more ...string) []string {
	return append(append([]string(nil), base...), more...)
}

// The screens of product.md 5.2 to 5.5 from canned results, plain and in
// color.
func TestScreensGolden(t *testing.T) {
	for _, c := range []struct {
		name   string
		result *core.Result
		cmd    string
		args   []string
	}{
		{"plan", planResult(), "plan", planArgs},
		{"plan-verbose", planResult(), "plan", args(planArgs, "-v")},
		{"copy-pass", copyPass(), "copy", copyArgs},
		{"copy-pass-quiet", copyPass(), "copy", args(copyArgs, "-q")},
		{"copy-pass-verbose", copyPass(), "copy", args(copyArgs, "-v", "--debug")},
		{"copy-fail", copyFail(), "copy", copyArgs},
	} {
		for _, color := range []bool{false, true} {
			name := c.name
			env := func(string) (string, bool) { return "", false }
			if color {
				name += "-color"
				env = colorEnv
			}
			useFixture(t, c.result, nil, func(d *deps) { d.env = env })
			testutil.Golden(t, "screen/"+name+".txt", []byte(screen(t, c.cmd, c.args...)))
		}
	}
}

func TestPrepareConfirmGolden(t *testing.T) {
	r := copyPass()
	r.Kind = "prepare"
	r.Source = r.Target
	r.Backup, r.Restore = ".roadie/backups/2026-10-26T14-02-11.sql.gz", "gunzip -c .roadie/backups/2026-10-26T14-02-11.sql.gz | wp db import -"
	for _, c := range []struct {
		name, input string
		code        int
	}{{"prepare-confirm", "staging.shop.test\n", 0}, {"prepare-wrong-host", "shop.com\n", 2}} {
		fx := useFixture(t, r, nil, func(d *deps) {
			d.terminal = func() bool { return true }
			d.stdin = strings.NewReader(c.input)
		})
		got := screen(t, "prepare", "/var/www/staging", "--url", "https://staging.shop.test", "--no-config")
		testutil.Golden(t, "screen/"+c.name+".txt", []byte(got))
		if !strings.Contains(got, prepareQuestion("staging.shop.test")) || !strings.HasSuffix(got, "--- exit "+strconv.Itoa(c.code)+"\n") {
			t.Errorf("%s:\n%s", c.name, got)
		}
		if len(fx.jobs) != 1 || fx.jobs[0].Mode != engine.Prepare || fx.jobs[0].From.Raw != "/var/www/staging" || fx.jobs[0].To.Raw != "/var/www/staging" {
			t.Errorf("%s job: %+v", c.name, fx.jobs)
		}
	}
	// Without a terminal there is no question to ask: Confirm is nil.
	fx := useFixture(t, r, nil, nil)
	runCmd(t, "prepare", "/var/www/staging", "--no-config", "--confirm-target", "staging.shop.test")
	if fx.jobs[0].Confirm != nil {
		t.Error("Confirm set without a terminal")
	}
}

// prdErrors are PRD 17's messages, as the engine and the other lanes
// return them.
var prdErrors = map[string]string{
	core.ErrTargetIsProduction:    "target-is-production: /var/www/shop is shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy.",
	core.ErrTargetSaysProduction:  "target-says-production: /var/www/staging has WP_ENVIRONMENT_TYPE unset, which WordPress reads as production. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging staging.shop.test.",
	core.ErrTargetIsSource:        "target-is-source: --from and --to both reach database shop on db.shop.com. A copy can't replace its own source.",
	core.ErrTargetHoldsProduction: "target-holds-production: /var/www/staging calls itself https://shop.com; Roadie can't tell whether that is the live site. Pass --production-url <production URL> (the live site's URL) so Roadie knows which URL is live, or --url https://shop.com if it is this staging's own URL.",
	core.ErrOutputIsInput:         "output-is-input: --to prod.sql.gz is the file Roadie reads. Write the copy to a new file.",
	core.ErrPrepareConfirm:        "prepare-confirm: there's no terminal to confirm on. Pass --confirm-target staging.shop.test (the site's host) to go on.",
	core.ErrConfirmTarget:         "confirm-target: there's no terminal to confirm on. Pass --confirm-target staging.shop.test (the site's host) to go on.",
	core.ErrDumpTruncated:         `dump-truncated: prod.sql.gz ends inside table wp_postmeta at 1.7 GB, with no "Dump completed" line. Export it again; the target is untouched.`,
	core.ErrDumpFormat:            "dump-format: prod.dump is a pg_dump custom-format archive. Roadie reads plain SQL: pg_dump -Fp, mysqldump, mariadb-dump or wp db export.",
	core.ErrDialectMismatch:       "dialect-mismatch: prod.sql is a MySQL dump and --to is a PostgreSQL database. Roadie copies MySQL to MySQL or MariaDB, PostgreSQL to PostgreSQL and SQLite to SQLite.",
	core.ErrDumpRefused:           "dump-refused: prod.sql line 4,120 holds a CREATE TRIGGER statement, which could run code on the staging database. Export with --skip-triggers, or pass --keep-routines to keep it without its DEFINER.",
	core.ErrDumpBomb:              "dump-bomb: prod.sql.gz grew past 20 times its size (40 GB) while reading. If it's real, pass --max-output-ratio 50; the target is untouched.",
	core.ErrValueTooLarge:         "value-too-large: a value in wp_options at line 88 is over 256 MiB. Pass --max-value 1GiB if it's real; the target is untouched.",
	core.ErrSiteConfig:            "site-config: /var/www/staging/wp-config.php sets DB_PASSWORD from getenv('DB_PASS'), which Roadie can't run. Pass the target as a DSN with ROADIE_TO_PASSWORD, or use @staging through WP-CLI.",
	core.ErrSSH:                   "ssh: deploy@shop.com refused the key (Permission denied (publickey)). Check ssh deploy@shop.com works on its own; Roadie uses your ssh setup and never skips host key checks.",
	core.ErrSSHHostKey:            "ssh-host-key: deploy@shop.com isn't in known_hosts (ED25519 SHA256:…). Connect once with ssh to check and add it; Roadie never skips host key checks.",
	core.ErrWPCLI:                 "wp-cli: wp isn't on PATH for deploy@shop.com. Install WP-CLI there, set remote.wp in .roadie/config.yaml to its path, or export the database yourself and use --from dump.sql.gz.",
	core.ErrImport:                "import: MariaDB 10.6 doesn't know collation utf8mb4_0900_ai_ci (MySQL 8). Run again with --fix-collation. The target is untouched.",
	core.ErrImportMixed:           "import: the connection to staging@localhost was lost after 37 of 61 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again.",
	core.ErrUncovered:             "uncovered: 2 columns look personal and no rule covers them: wp_loyalty_members.email, wp_loyalty_members.phone. Add them under columns: (rhythm-roadie explain uncovered-column), or --allow-uncovered to copy and list them as findings. The target is untouched.",
	core.ErrStack:                 "stack: no profile matches this dump (no wp_options or migrations table). For your own schema, list the tables under columns: and set stack: sql.",
	core.ErrURLMap:                "url-map: fr.shop.com has no staging URL; add --url-map fr.shop.com=fr.staging.shop.test.",
	core.ErrKeyManual:             "key.stripe-live: wp-content/themes/shop/functions.php line 12 holds a live Stripe key. Roadie only edits the database, wp-config.php and .env. Move it into wp-config.php or blank it by hand, then run verify.",
	core.ErrKillSwitch:            "side-effect.mail: couldn't write wp-content/mu-plugins/rhythm-echo.php (permission denied), so mail from this copy isn't sealed. Fix the folder's owner, then: rhythm-roadie prepare /var/www/staging --only side-effects.",
	core.ErrTestKeyIsLive:         "test-key-is-live: keys.test_keys.stripe reads a live key (sk_l•••••). Give a test key (sk_test_...) or remove the entry to blank it.",
	core.ErrConfig:                ".roadie/config.yaml line 14: keys.test_keys.stripe reads ${STRIPE_TEST}; only ROADIE_ variables are read. Rename it ROADIE_STRIPE_TEST.",
	core.ErrNothingScanned:        "nothing-scanned: verify read 0 rows from staging@localhost (58 tables, access denied on every one). A verify that sees nothing can't pass; check the user's SELECT rights.",
	core.ErrLock:                  "lock: another Roadie run is copying to /var/www/staging (pid 4120, started 14:02). Wait for it, or remove /home/u/.cache/rhythm/roadie/locks/ab12.lock if it crashed.",
	core.ErrFakeCollision:         "fake-collision: two different emails got the same fake in this run. Run again (a new key fixes it); this should happen about once in 100 million runs.",
	core.ErrEchoUnreachable:       "verify --echo: Echo isn't reachable at http://127.0.0.1:8025; start it with rhythm echo up.",
	core.ErrReportEdited:          "report check: report.json was changed after it was signed (summary.high 3 → 0).",
	core.ErrReportUnsigned:        "report check: report.json has no signature (it was written with --sign=off).",
	core.ErrDiskFull:              "not enough free disk space: need about 2.4 GB, 1.1 GB free in /var/tmp",
	core.ErrNotFound:              "not-found: shop.sql doesn't exist. Check the path you gave.",
	core.ErrUnreachable:           "unreachable: db: connect to mysql://127.0.0.1:3306/shop: dial tcp 127.0.0.1:3306: connect: connection refused. Check that the database or host is running and reachable from here; then check the host and port you gave.",
	core.ErrInterrupted:           "interrupted: the run was stopped by Ctrl-C or a signal. Run it again when you're ready.",
	core.ErrWPAlias:               "wp-alias: WP-CLI alias not found: @nope is in no wp-cli.yml or wp-cli.local.yml from /srv/shop up. Add @nope to wp-cli.yml with ssh: user@host/path (or path: for a local site), or name the site as ssh://user@host/path.",
	core.ErrCodeNotBuilt:          "not built yet",
}

// Every error code exits with its PRD 17 code and prints its message
// once; the screens of all of them are one golden file.
func TestErrorScreens(t *testing.T) {
	var all bytes.Buffer
	for _, code := range core.ErrorCodes() {
		msg, ok := prdErrors[code]
		if !ok {
			t.Errorf("no PRD 17 message for %s in the test", code)
			continue
		}
		exit, _ := core.CodeExit(code)
		r := baseResult("copy")
		r.TargetState = core.Untouched
		if code == core.ErrImportMixed {
			r.TargetState = core.Mixed
		}
		if code == core.ErrUncovered {
			r.Uncovered = []core.Uncovered{{Table: "wp_loyalty_members", Column: "email", Rate: 98}, {Table: "wp_loyalty_members", Column: "phone", Rate: 40}}
		}
		r.Err = core.Errorf(code, exit, "%s", msg)
		useFixture(t, r, nil, nil)
		out := screen(t, "copy", args(copyArgs, "--plain")...)
		all.WriteString(out + "\n")
		if !strings.HasSuffix(out, "--- exit "+strconv.Itoa(exit)+"\n") {
			t.Errorf("%s: want exit %d\n%s", code, exit, out)
		}
		if n := strings.Count(out, msg); n != 1 {
			t.Errorf("%s: message shown %d times\n%s", code, n, out)
		}
		// -q keeps the reason: a CI log says why the run stopped.
		useFixture(t, r, nil, nil)
		if quiet := screen(t, "copy", args(copyArgs, "-q")...); strings.Count(quiet, msg) != 1 {
			t.Errorf("%s -q: message shown %d times\n%s", code, strings.Count(quiet, msg), quiet)
		}
		// With --json the message goes to stderr once and stdout is the report.
		useFixture(t, r, nil, nil)
		stdout, stderr, got := runCmd(t, "copy", args(copyArgs, "--json")...)
		if got != exit || strings.Count(stderr, msg) != 1 || !strings.HasPrefix(stdout, "{") {
			t.Errorf("%s --json: exit %d stderr %q", code, got, stderr)
		}
	}
	testutil.Golden(t, "screen/errors.txt", all.Bytes())
}

// A run that returns an error and no result still gets a screen and an
// exit code.
func TestRunErrorWithoutResult(t *testing.T) {
	useFixture(t, nil, core.Errorf(core.ErrSSH, 3, "ssh: deploy@shop.com refused the key"), nil)
	out, _, code := runCmd(t, "copy", args(copyArgs, "--plain")...)
	if code != 3 || !strings.Contains(out, "ssh: deploy@shop.com refused the key") || !strings.Contains(out, "the target is untouched") {
		t.Errorf("exit %d\n%s", code, out)
	}
	useFixture(t, nil, errors.New("connection reset"), nil)
	if _, _, code := runCmd(t, "copy", args(copyArgs, "--plain")...); code != 3 {
		t.Errorf("plain error: exit %d", code)
	}
}

func TestExitCodes(t *testing.T) {
	for _, c := range []struct {
		name   string
		result *core.Result
		cmd    string
		args   []string
		want   int
	}{
		{"copy pass", copyPass(), "copy", copyArgs, 0},
		{"copy fail", copyFail(), "copy", copyArgs, 1},
		{"copy fail under --fail-on critical", copyFail(), "copy", args(copyArgs, "--fail-on", "critical"), 1},
		{"copy fail with --fail-on none", copyFail(), "copy", args(copyArgs, "--fail-on", "none"), 0},
		{"plan default none", planResult(), "plan", planArgs, 0},
		{"plan would stop with --fail-on", planResult(), "plan", args(planArgs, "--fail-on", "critical"), 1},
		{"plan with --allow-uncovered", planResult(), "plan", args(planArgs, "--fail-on", "high", "--allow-uncovered"), 0},
	} {
		useFixture(t, c.result, nil, nil)
		if _, errOut, code := runCmd(t, c.cmd, c.args...); code != c.want {
			t.Errorf("%s: exit %d, want %d (%s)", c.name, code, c.want, errOut)
		}
	}
	// A verify that read nothing can't pass.
	r := copyPass()
	r.Verify.Coverage = core.Coverage{}
	useFixture(t, r, nil, nil)
	if _, _, code := runCmd(t, "copy", copyArgs...); code != 3 {
		t.Errorf("nothing scanned: exit %d", code)
	}
}

func TestAlsoBlock(t *testing.T) {
	testDeps(t, nil)
	var b bytes.Buffer
	for _, c := range []struct {
		name string
		args []string
		r    *core.Result
	}{
		{"wordpress ssh to site", copyArgs, copyPass()},
		{"wordpress with basic auth and noindex off", args(copyArgs, "--basic-auth", "team", "--noindex=off"), copyPass()},
		{"wordpress dump to dump", []string{"--from", "prod.sql.gz", "--to", "staging.sql.gz", "--url", "https://s.test"}, copyPass()},
		{"wordpress ssh on port 2222 to site", []string{"--from", "ssh://deploy@shop.com:2222/var/www/shop", "--to", "/var/www/staging", "--url", "https://s.test", "--no-config"}, copyPass()},
		{"wordpress ssh to ssh", []string{"--from", "ssh://deploy@shop.com/var/www/shop", "--to", "ssh://web@staging.shop.test/srv/staging", "--url", "https://s.test", "--no-config"}, copyPass()},
		{"wordpress ssh to ssh on ports", []string{"--from", "ssh://deploy@shop.com:2222/var/www/shop", "--to", "ssh://web@staging.shop.test:2200/srv/it's", "--url", "https://s.test", "--no-config"}, copyPass()},
		{"laravel", copyArgs, func() *core.Result {
			r := copyPass()
			r.Stack = core.StackInfo{Name: core.StackLaravel}
			r.Changes = nil
			return r
		}()},
	} {
		p := mustParse(t, "copy", c.args...)
		j, err := p.buildJob(engine.Copy, nil, &b)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString("# " + c.name + "\n")
		for _, l := range alsoLines(c.r, j) {
			b.WriteString(l + "\n")
		}
	}
	testutil.Golden(t, "screen/also.txt", b.Bytes())
	if !strings.Contains(b.String(), "rsync -a --exclude wp-content/debug.log --exclude wp-content/uploads/wc-logs") ||
		!strings.Contains(b.String(), cachesLine[1:]) {
		t.Errorf("ALSO block:\n%s", b.String())
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"a/b": "a/b", "*.sql": "'*.sql'", "it's": `'it'\''s'`, "<production>/": "'<production>/'", "deploy@shop.com:/var/www/": "deploy@shop.com:/var/www/"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// The basic-auth password is printed once after the box and never written
// to a report or an -o file.
func TestBasicAuthPasswordOnlyOnScreen(t *testing.T) {
	r := copyPass()
	r.BasicAuthPassword = "Qx7-pw-" + "only-on-screen"
	t.Chdir(t.TempDir())
	fx := useFixture(t, r, nil, nil)
	out, errOut, code := runCmd(t, "copy", args(copyArgs, "--basic-auth", "team", "-o", "r.json", "-o", "r.md", "-o", "r.html")...)
	if code != 0 || strings.Count(out, r.BasicAuthPassword) != 1 || strings.Contains(errOut, r.BasicAuthPassword) {
		t.Fatalf("exit %d, password shown %d times\n%s\n%s", code, strings.Count(out, r.BasicAuthPassword), out, errOut)
	}
	if !strings.Contains(out, "Basic auth for team: password "+r.BasicAuthPassword) {
		t.Errorf("line: %s", out)
	}
	files := []string{"r.json", "r.md", "r.html"}
	for _, f := range fx.saved {
		files = append(files, filepath.Join(f, "report.json"), filepath.Join(f, "report.html"))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), r.BasicAuthPassword) {
			t.Errorf("%s holds the basic-auth password", f)
		}
	}
	// With --json on stdout it goes to stderr, once.
	useFixture(t, r, nil, nil)
	out, errOut, _ = runCmd(t, "copy", args(copyArgs, "--basic-auth", "team", "--json")...)
	if strings.Contains(out, r.BasicAuthPassword) || strings.Count(errOut, r.BasicAuthPassword) != 1 {
		t.Errorf("--json: stdout has it %v, stderr %d times", strings.Contains(out, r.BasicAuthPassword), strings.Count(errOut, r.BasicAuthPassword))
	}
}

// On GitHub Actions the password never reaches the job log as text: it
// is masked first, then kept in a 0600 file whose path is all the log
// shows.
func TestBasicAuthPasswordOnActions(t *testing.T) {
	r := copyPass()
	r.BasicAuthPassword = "Qx7-pw-" + "on-actions"
	t.Chdir(t.TempDir())
	useFixture(t, r, nil, func(d *deps) {
		d.env = func(k string) (string, bool) {
			if k == "GITHUB_ACTIONS" {
				return "true", true
			}
			return "", false
		}
	})
	// A file from an earlier run, readable by others, is replaced.
	os.MkdirAll(basicAuthDir, 0o700)
	os.WriteFile(basicAuthPasswordFile, []byte("old\n"), 0o644)
	out, errOut, code := runCmd(t, "copy", args(copyArgs, "--basic-auth", "team", "--json")...)
	if code != 0 || strings.Contains(out, r.BasicAuthPassword) {
		t.Fatalf("exit %d, stdout has the password %v", code, strings.Contains(out, r.BasicAuthPassword))
	}
	mask := "::add-mask::" + r.BasicAuthPassword + "\n"
	if !strings.HasPrefix(errOut, mask) || strings.Count(errOut, r.BasicAuthPassword) != 1 {
		t.Errorf("stderr must mask the password first and show it nowhere else:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Basic auth for team: password in .roadie/basic-auth/password (0600; the job log masks it)") {
		t.Errorf("line: %s", errOut)
	}
	b, err := os.ReadFile(basicAuthPasswordFile)
	if err != nil || string(b) != r.BasicAuthPassword+"\n" {
		t.Fatalf("password file: %q %v", b, err)
	}
	if fi, _ := os.Stat(basicAuthPasswordFile); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("password file mode %v, want 0600", fi.Mode().Perm())
	}
	// Off GitHub Actions nothing is masked or saved.
	os.Remove(basicAuthPasswordFile)
	useFixture(t, r, nil, nil)
	_, errOut, _ = runCmd(t, "copy", args(copyArgs, "--basic-auth", "team", "--json")...)
	if strings.Contains(errOut, "::add-mask::") {
		t.Errorf("masked off GitHub Actions: %s", errOut)
	}
	if _, err := os.Stat(basicAuthPasswordFile); err == nil {
		t.Error("saved the password off GitHub Actions")
	}
}

func TestWorkflowEscape(t *testing.T) {
	if got := workflowEscape("a%b\r\n::x"); got != "a%25b%0D%0A::x" {
		t.Errorf("workflowEscape = %q", got)
	}
}

func TestOutputs(t *testing.T) {
	t.Chdir(t.TempDir())
	fx := useFixture(t, copyPass(), nil, nil)
	out, _, code := runCmd(t, "copy", args(copyArgs, "--json", "-o", "a.html", "-o", "a.md")...)
	if code != 0 || !strings.HasPrefix(out, "{\n  \"schema\": \"rhythm-roadie/v1\"") || strings.Contains(out, "PASS") {
		t.Errorf("--json stdout: %s", out)
	}
	// A format flag with an -o file of the same format writes only the
	// file: stdout keeps the live view.
	out, _, code = runCmd(t, "copy", args(copyArgs, "--json", "-o", "a.json")...)
	if code != 0 || strings.Contains(out, "\"schema\"") || !strings.Contains(out, "PASS") {
		t.Errorf("--json -o a.json stdout: %s", out)
	}
	for _, f := range []string{"a.json", "a.md", "a.html"} {
		st, err := os.Stat(f)
		if err != nil || st.Size() == 0 {
			t.Errorf("-o %s: %v", f, err)
		} else if st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
			t.Errorf("-o %s mode %v, want 0600", f, st.Mode().Perm())
		}
	}
	if len(fx.saved) != 2 {
		t.Errorf("two copies saved %d report folders", len(fx.saved))
	}
	// plan writes only what -o asks for.
	fx = useFixture(t, planResult(), nil, nil)
	runCmd(t, "plan", args(planArgs, "-o", "plan.md")...)
	if len(fx.saved) != 0 {
		t.Error("plan saved a report folder")
	}
	if _, err := os.Stat("plan.md"); err != nil {
		t.Error(err)
	}
	// A report that can't be built makes a passing copy exit 3.
	useFixture(t, copyPass(), nil, func(d *deps) {
		d.envelope = func(*core.Result, string, string) (*core.Envelope, error) { return nil, core.ErrNotBuilt }
	})
	if _, errOut, code := runCmd(t, "copy", copyArgs...); code != 3 || !strings.Contains(errOut, "couldn't build the report") {
		t.Errorf("no report: exit %d %q", code, errOut)
	}
	// --sign=off skips the signer.
	signed := 0
	useFixture(t, copyPass(), nil, func(d *deps) {
		d.loadSigner = func(string, func(string) (string, bool)) (signer, bool, error) {
			signed++
			return fakeSigner{"roadie:x"}, false, nil
		}
	})
	runCmd(t, "copy", args(copyArgs, "--sign=off")...)
	runCmd(t, "copy", copyArgs...)
	if signed != 1 {
		t.Errorf("signed %d times, want once (without --sign=off)", signed)
	}
}

func TestOpen(t *testing.T) {
	fx := useFixture(t, copyPass(), nil, func(d *deps) { d.stdoutTerminal = func() bool { return true } })
	runCmd(t, "copy", args(copyArgs, "--open")...)
	if len(fx.opens) != 1 || fx.opens[0] != filepath.Join(".roadie/reports/2026-10-26T14-02-11", "report.html") {
		t.Errorf("opened %q", fx.opens)
	}
	fx = useFixture(t, copyPass(), nil, func(d *deps) {
		d.stdoutTerminal = func() bool { return true }
		d.env = func(n string) (string, bool) { return map[string]string{"CI": "true"}[n], n == "CI" }
	})
	runCmd(t, "copy", args(copyArgs, "--open")...)
	fx2 := useFixture(t, copyPass(), nil, nil) // no terminal
	runCmd(t, "copy", args(copyArgs, "--open")...)
	if len(fx.opens)+len(fx2.opens) != 0 {
		t.Errorf("opened in CI or without a terminal: %q %q", fx.opens, fx2.opens)
	}
}

// copy without --url asks on a terminal and is a usage error without one.
func TestCopyAsksForURL(t *testing.T) {
	noURL := []string{"--from", "prod.sql", "--to", "/var/www/staging", "--no-config"}
	useFixture(t, copyPass(), nil, nil)
	_, errOut, code := runCmd(t, "copy", noURL...)
	if code != 2 || !strings.Contains(errOut, "copy needs --url, the staging site's URL (or url: in .roadie/config.yaml)") {
		t.Errorf("no terminal: exit %d %q", code, errOut)
	}
	fx := useFixture(t, copyPass(), nil, func(d *deps) {
		d.terminal = func() bool { return true }
		d.stdin = strings.NewReader("https://staging.shop.test\nstaging.shop.test\n")
	})
	_, errOut, _ = runCmd(t, "copy", noURL...)
	if len(fx.jobs) != 1 || fx.jobs[0].Opts.URL != "https://staging.shop.test" || !strings.Contains(errOut, "The copy's URL? ") {
		t.Errorf("asked: %q %+v", errOut, fx.jobs)
	}
}

// The inline-password warning is printed once, never with the password.
func TestInlinePasswordWarningOnce(t *testing.T) {
	useFixture(t, copyPass(), nil, nil)
	_, errOut, _ := runCmd(t, "copy", "--from", "mysql://shop:hunter2@db.shop.com/shop", "--to", "staging.sql", "--url", "https://s.test", "--no-config")
	warn := "the password in --from is visible to other users of this machine; use ROADIE_FROM_PASSWORD"
	if strings.Count(errOut, warn) != 1 || strings.Contains(errOut, "hunter2") {
		t.Errorf("stderr %q", errOut)
	}
}

// Text from the data never reaches the terminal with control bytes.
func TestScreenIsSafeText(t *testing.T) {
	r := copyFail()
	r.Verify.Findings[1].Locations[0].Table = "wp_\x1b]0;owned\x07users"
	r.Tables[0].Name = "wp_\x1b[2Jusers"
	useFixture(t, r, nil, nil)
	out, _, _ := runCmd(t, "copy", args(copyArgs, "-v")...)
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control bytes reached the screen: %q", out)
	}
}

func TestProgressTerminal(t *testing.T) {
	now := testNow
	clock := func() time.Time { return now }
	var b bytes.Buffer
	p := newProgress(&b, true, false, clock)
	p.Phase("transform", 2_400_000_000)
	draws := strings.Count(b.String(), "\x1b[2K  Streaming")
	for i := 0; i < 50; i++ { // 50 updates in 49 ms: no redraw
		now = now.Add(time.Millisecond)
		p.Advance(1_000_000)
	}
	if got := strings.Count(b.String(), "\x1b[2K  Streaming"); got != draws {
		t.Errorf("redrew %d times within 100 ms", got-draws)
	}
	for i := 0; i < 20; i++ { // 2 s at 31 MB/s, one update every 100 ms
		now = now.Add(100 * time.Millisecond)
		p.Advance(3_100_000)
		p.Table("wp_wc_orders_meta", 811903)
	}
	if got := strings.Count(b.String(), "\x1b[2K  Streaming") - draws; got < 15 || got > 21 {
		t.Errorf("redrew %d times in 2 s, want about 10 a second", got)
	}
	line := p.line()
	if !strings.HasPrefix(line, "Streaming   █") || !strings.Contains(line, " of 2.4 GB · ") || !strings.Contains(line, "MB/s · ") || !strings.HasSuffix(line, " left") {
		t.Errorf("line %q", line)
	}
	if p.tableLine() != "wp_wc_orders_meta  811,903 rows" {
		t.Errorf("table line %q", p.tableLine())
	}
	now = now.Add(2 * time.Second)
	p.Phase("verify", 0)
	p.Done()
	if tm := p.timings().String(); tm != "transform 2.0s · verify 0.0s" && tm != "transform 4s · verify 0.0s" {
		t.Errorf("timings %q", tm)
	}
}

func TestProgressPlain(t *testing.T) {
	now := testNow
	var b bytes.Buffer
	p := newProgress(&b, false, false, func() time.Time { return now })
	p.Phase("index", 2_400_000_000)
	p.Advance(1 << 30)
	p.Table("wp_users", 10)
	p.Note("spooling the stream (encrypted)")
	p.Phase("transform", 0)
	p.Phase("verify", 0)
	p.Phase("verify", 0) // the engine's and verify's own: one line
	p.Done()
	if got := b.String(); got != "spooling the stream (encrypted)\n" {
		t.Errorf("plain progress %q", got)
	}
	var q bytes.Buffer
	pq := newProgress(&q, true, true, func() time.Time { return now })
	pq.Phase("index", 10)
	pq.Advance(5)
	pq.Done()
	if q.Len() != 0 {
		t.Errorf("-q progress %q", q.String())
	}
}

func TestFormats(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 18402: "18,402", 1210000: "1,210,000", -4120: "-4,120"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q", n, got)
		}
	}
	for n, want := range map[int64]string{512: "512 B", 2_400_000_000: "2.4 GB", 31_000_000: "31.0 MB", 150_000_000: "150 MB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q", n, got)
		}
	}
	for d, want := range map[time.Duration]string{400 * time.Millisecond: "0.4s", 41 * time.Second: "41s", 82 * time.Second: "1m 22s", 2*time.Hour + 5*time.Minute: "2h 5m"} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q", d, got)
		}
	}
	if got := bigNum(1_210_000); got != "1.21 M" {
		t.Errorf("bigNum = %q", got)
	}
	if got := clock(14 * time.Second); got != "0:14" {
		t.Errorf("clock = %q", got)
	}
	if got := counts(core.Summary{Critical: 2, High: 3, Medium: 1}); got != "2 critical · 3 high · 1 medium · 0 low · 0 info" {
		t.Errorf("counts = %q", got)
	}
}

// --no-color and NO_COLOR drop the color only; --plain drops the boxes
// and glyphs too, even with CLICOLOR_FORCE.
func TestColorSwitches(t *testing.T) {
	forced := func(extra map[string]string) func(string) (string, bool) {
		return func(n string) (string, bool) {
			if n == "CLICOLOR_FORCE" {
				return "1", true
			}
			v, ok := extra[n]
			return v, ok
		}
	}
	for _, c := range []struct {
		name      string
		env       map[string]string
		flags     []string
		box, ansi bool
	}{
		{"forced", nil, nil, true, true},
		{"--no-color", nil, []string{"--no-color"}, true, false},
		{"NO_COLOR", map[string]string{"NO_COLOR": ""}, nil, true, false},
		{"--plain", nil, []string{"--plain"}, false, false},
	} {
		useFixture(t, copyPass(), nil, func(d *deps) { d.env = forced(c.env) })
		out, _, _ := runCmd(t, "copy", args(copyArgs, c.flags...)...)
		if strings.Contains(out, "╭") != c.box || strings.Contains(out, "\x1b[") != c.ansi || strings.Contains(out, "✓") != c.box {
			t.Errorf("%s: box %v ansi %v glyphs %v", c.name, strings.Contains(out, "╭"), strings.Contains(out, "\x1b["), strings.Contains(out, "✓"))
		}
	}
}

// Advance once a row keeps a bounded window and the right speed.
func TestProgressSamplesBounded(t *testing.T) {
	now := testNow
	p := newProgress(io.Discard, true, false, func() time.Time { return now })
	p.Phase("transform", 1<<40)
	for i := 0; i < 200_000; i++ { // 20 s at 1 MB/s in 100 µs steps
		now = now.Add(100 * time.Microsecond)
		p.Advance(100)
	}
	if n := len(p.samples); n > 60 {
		t.Errorf("%d samples kept, want about 50", n)
	}
	if r := p.rate(); r < 0.99e6 || r > 1.01e6 {
		t.Errorf("rate %.0f B/s, want 1 MB/s", r)
	}
	// A stall shows in the average within the window.
	now = now.Add(4 * time.Second)
	p.Advance(0)
	if r := p.rate(); r > 0.3e6 {
		t.Errorf("rate after a 4 s stall %.0f B/s", r)
	}
}

// Sizes near int64's limit draw a full bar, not an overflowed one.
func TestProgressHugeSizes(t *testing.T) {
	p := newProgress(io.Discard, true, false, func() time.Time { return testNow })
	p.Phase("transform", 1<<62)
	p.Advance(1 << 62)
	if l := p.line(); !strings.Contains(l, strings.Repeat("█", barWidth)) || strings.Contains(l, "░") {
		t.Errorf("line %q", l)
	}
}

// A file that isn't there and a database that doesn't answer stop with
// a code, an explain page and a next step, not a bare OS error.
func TestPlainErrorsGetCodes(t *testing.T) {
	_, err := os.Open(filepath.Join(t.TempDir(), "shop.sql"))
	ce := asCoreError(fmt.Errorf("engine: %w", err))
	if ce.Code != core.ErrNotFound || ce.Exit != 2 || !strings.HasSuffix(ce.Msg, "shop.sql doesn't exist. Check the path you gave.") {
		t.Errorf("missing file: %+v", ce)
	}
	// 32921 is in the cmd tests' port range and nothing listens on it.
	addr := "127.0.0.1:32921"
	_, err = net.Dial("tcp", addr)
	ce = asCoreError(fmt.Errorf("db: connect to mysql://%s/shop: %w", addr, err))
	if ce.Code != core.ErrUnreachable || ce.Exit != 3 || !strings.HasPrefix(ce.Msg, "unreachable: db: connect to") {
		t.Errorf("refused: %+v", ce)
	}
	// A server that answers with a refusal: a code too, never the bare
	// driver message (an unknown database here).
	ce = asCoreError(fmt.Errorf("site: %w", &db.ConnectError{Display: "mysql://root@127.0.0.1:39001/PROD",
		Err: errors.New("Error 1049 (42000): Unknown database 'PROD'")}))
	if ce.Code != core.ErrUnreachable || ce.Exit != 3 ||
		ce.Msg != "unreachable: mysql://root@127.0.0.1:39001/PROD refused the connection: Error 1049 (42000): Unknown database '•••'. Check the database name, the user and its password in the DSN or the site's config; the target is untouched." {
		t.Errorf("refused login: %+v", ce)
	}
	if ce := asCoreError(errors.New("something else")); ce.Code != "" || ce.Exit != 3 {
		t.Errorf("other: %+v", ce)
	}
	// Ctrl-C: the box names a code, not "STOPPED   ; the target is untouched".
	ce = asCoreError(fmt.Errorf("transform: %w", context.Canceled))
	if ce.Code != core.ErrInterrupted || ce.Exit != 130 || !strings.HasPrefix(ce.Msg, "interrupted: ") {
		t.Errorf("interrupted: %+v", ce)
	}
	var b bytes.Buffer
	v := &view{w: &b, o: viewOpts{plain: true}}
	v.stopView(&core.Result{Err: ce, TargetState: core.Untouched}, engine.Job{Mode: engine.Copy})
	if !strings.Contains(b.String(), "interrupted; the target is untouched") {
		t.Errorf("stop box:\n%s", b.String())
	}
}

// A prepare that stopped after its backup names the backup under the
// box, also with -q; a stop without one says nothing about a backup.
func TestStopViewNamesBackup(t *testing.T) {
	r := &core.Result{Kind: "prepare", TargetState: core.Mixed, Backup: ".roadie/backups/2026-10-28T15-05-04.sql.gz",
		Restore: "gunzip -c .roadie/backups/2026-10-28T15-05-04.sql.gz | mysql -u stg -p staging",
		Err:     core.Errorf(core.ErrImportMixed, 3, "import: x. Restore the backup prepare took first.")}
	want := "Backup: .roadie/backups/2026-10-28T15-05-04.sql.gz; to undo: gunzip -c .roadie/backups/2026-10-28T15-05-04.sql.gz | mysql -u stg -p staging"
	for _, o := range []viewOpts{{plain: true}, {plain: true, quiet: true}} {
		var b bytes.Buffer
		(&view{w: &b, o: o}).stopView(r, engine.Job{Mode: engine.Prepare})
		if !strings.Contains(b.String(), want) {
			t.Errorf("quiet %v:\n%s", o.quiet, b.String())
		}
	}
	var b bytes.Buffer
	r.Backup, r.Restore = "", ""
	(&view{w: &b, o: viewOpts{plain: true}}).stopView(r, engine.Job{Mode: engine.Prepare})
	if strings.Contains(b.String(), "Backup") {
		t.Errorf("no backup:\n%s", b.String())
	}
}

// The plan's URL line runs from production's main URL to the copy's.
func TestPlanURLLine(t *testing.T) {
	var b bytes.Buffer
	v := &view{w: &b, o: viewOpts{plain: true}}
	r := &core.Result{URL: "https://staging.shop.test", Production: []string{"shop.com", "*.shop.com", "fr.shop.com"}}
	v.planView(r, engine.Job{Opts: engine.Options{URL: "https://staging.shop.test"}}, nil)
	if !strings.Contains(b.String(), "URLs: shop.com (and 1 more) → https://staging.shop.test") {
		t.Errorf("plan:\n%s", b.String())
	}
}

// Locations that differ only by row key are one place with the
// finding's count; named rows (an option) stay apart.
func TestLocationsLineMerges(t *testing.T) {
	loc := func(row string) core.LocationCount {
		return core.LocationCount{Location: core.Location{Table: "wp_actionscheduler_actions", Column: "args", Row: row, Path: "json:email"}, Count: 1}
	}
	f := core.Finding{Count: 160, Locations: []core.LocationCount{loc("action_id=1"), loc("action_id=2"), loc("action_id=3")}}
	if got := locationsLine(f); got != "wp_actionscheduler_actions.args → json:email 160" {
		t.Errorf("merged: %q", got)
	}
	f = core.Finding{Count: 2, Locations: []core.LocationCount{
		{Location: core.Location{Table: "wp_options", Column: "option_value", Row: "option_name=a"}, Count: 1},
		{Location: core.Location{Table: "wp_options", Column: "option_value", Row: "option_name=b"}, Count: 1}}}
	if got := locationsLine(f); strings.Count(got, " · ") != 1 {
		t.Errorf("named rows: %q", got)
	}
}

// A copy with --soundcheck shows Soundcheck's note when it didn't run.
func TestCopySoundcheckNote(t *testing.T) {
	r := copyPass()
	r.Verify.Soundcheck = &core.SoundcheckResult{Note: verify.SoundcheckNotRun}
	useFixture(t, r, nil, nil)
	if out := screen(t, "copy", args(copyArgs, "--plain", "--soundcheck")...); !strings.Contains(out, verify.SoundcheckNotRun) {
		t.Errorf("no Soundcheck note:\n%s", out)
	}
	useFixture(t, copyPass(), nil, nil)
	if out := screen(t, "copy", args(copyArgs, "--plain")...); strings.Contains(out, "Soundcheck") {
		t.Errorf("a Soundcheck line without --soundcheck:\n%s", out)
	}
}

// A column several rules touch is listed once, under the action that
// changed most values; a keep rule or a rule that changed nothing never
// makes it look kept or changed.
func TestTableWhatGroupsColumns(t *testing.T) {
	tr := core.TableReport{Name: "wp_usermeta", Action: string(core.Fake)}
	changes := []core.Change{
		{Rule: "a", Table: "wp_usermeta", Column: "meta_value", Action: core.Fake, Values: 400},
		{Rule: "b", Table: "wp_usermeta", Column: "meta_value", Action: core.Fake, Values: 400},
		{Rule: "c", Table: "wp_usermeta", Column: "meta_value", Action: core.Blank, Values: 40},
		{Rule: "d", Table: "wp_usermeta", Column: "meta_value", Action: core.Keep, Values: 0},
		{Rule: "e", Table: "wp_usermeta", Column: "meta_key", Action: core.Blank, Values: 0},
		{Rule: "f", Table: "wp_options", Column: "option_value", Action: core.Blank, Values: 9},
	}
	for _, c := range changes {
		if c.Table == tr.Name {
			tr.Columns = append(tr.Columns, core.ColumnReport{Name: c.Column, Rule: c.Rule, Changed: c.Values})
		}
	}
	if got := tableWhat(tr, changes, true); got != "meta_value → fake" {
		t.Errorf("counted: %q", got)
	}
	// --schema-only: the action most rules take, every rule column listed.
	if got := tableWhat(tr, changes, false); got != "meta_value → fake; meta_key → blank" {
		t.Errorf("schema-only: %q", got)
	}
	// Only keep rules: nothing changes.
	keep := []core.Change{{Rule: "k", Table: "t", Column: "c", Action: core.Keep}}
	if got := tableWhat(core.TableReport{Name: "t", Action: string(core.Fake), Columns: []core.ColumnReport{{Name: "c"}}}, keep, true); got != "no column changes" {
		t.Errorf("keep only: %q", got)
	}
}

// The values a column kept as they were are counted by that column's
// name: 150 empty user_url values never read as 150 passwords kept.
func TestTableWhatKeptByColumn(t *testing.T) {
	tr := core.TableReport{Name: "wp_users", Action: string(core.Fake), Columns: []core.ColumnReport{
		{Name: "user_email", Changed: 151},
		{Name: "user_url", Changed: 1, Kept: 150},
		{Name: "user_pass", Changed: 151},
	}}
	changes := []core.Change{
		{Table: "wp_users", Column: "user_email", Action: core.Fake, Values: 151},
		{Table: "wp_users", Column: "user_url", Action: core.Fake, Values: 1, Kept: 150},
		{Table: "wp_users", Column: "user_pass", Action: core.ResetPassword, Values: 151},
	}
	if got, want := tableWhat(tr, changes, true), "user_email, user_url (150 kept) → fake; user_pass → reset"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An error with no code of its own gets one where Roadie knows it (a
// remote command, a missing table); the stop box never shows a lone
// "; " where the code would be.
func TestStopBoxWithoutCode(t *testing.T) {
	ce := asCoreError(&remote.RunError{Template: remote.TestFile, Exit: 2, Stderr: []byte("sh: 1: test: --: unexpected operator\n")})
	if ce.Code != core.ErrSSH || !strings.HasPrefix(ce.Msg, "ssh: remote test -f exited 2") {
		t.Errorf("remote: %+v", ce)
	}
	ce = asCoreError(&db.NoTableError{DB: "mysql://127.0.0.1/staging", Table: "wp_blogs"})
	if ce.Code != core.ErrSiteConfig || !strings.Contains(ce.Msg, "has no table wp_blogs") {
		t.Errorf("no table: %+v", ce)
	}
	var b bytes.Buffer
	v := &view{w: &b, o: viewOpts{plain: true}}
	v.stopView(&core.Result{Err: &core.Error{Exit: 3, Msg: "boom"}, TargetState: core.Untouched}, engine.Job{Mode: engine.Copy})
	if strings.Contains(b.String(), " ; ") || !strings.Contains(b.String(), "STOPPED   the target is untouched") {
		t.Errorf("stop box:\n%s", b.String())
	}
}

// prepare --only noindex reads no data: its box says what it prepared
// and why verify didn't run, never "copied" or "--no-verify".
func TestPreparedOnlyBox(t *testing.T) {
	var b bytes.Buffer
	v := &view{w: &b, o: viewOpts{plain: true}}
	r := &core.Result{Kind: "prepare", Target: core.EndpointInfo{Kind: core.LocalSite, Display: "./site"}, Stack: core.StackInfo{Name: core.StackWordPress}}
	j := engine.Job{Mode: engine.Prepare, To: core.Endpoint{Kind: core.LocalSite, Display: "./site"},
		Opts: engine.Options{URL: "https://staging.shop.test", Only: []string{"noindex"}}}
	v.verdictBox(r, j)
	out := b.String()
	if !strings.Contains(out, "prepared staging.shop.test: noindex; not checked (--only noindex reads no data)") || strings.Contains(out, "copied") {
		t.Errorf("box:\n%s", out)
	}
	b.Reset()
	j.Opts.Only, j.Opts.NoVerify = []string{"side-effects,keys"}, true
	v.verdictBox(r, j)
	if !strings.Contains(b.String(), "prepared staging.shop.test: side-effects, keys; not checked (--no-verify)") {
		t.Errorf("box:\n%s", b.String())
	}
	// --only side-effects shows no Search line: noindex wasn't touched.
	b.Reset()
	j.Opts.Only, j.Opts.NoVerify = []string{"side-effects"}, false
	v.resultView(r, j, "", nil)
	if strings.Contains(b.String(), "Search") {
		t.Errorf("search line:\n%s", b.String())
	}
}

// A copy from a live PostgreSQL database into a database says up front
// that the target needs the schema; a MySQL source doesn't.
func TestBeforeSaysRowsOnly(t *testing.T) {
	pg := core.Endpoint{Kind: core.DSN, Driver: core.Postgres, Display: "postgres://ro@127.0.0.1:39002/app"}
	to := core.Endpoint{Kind: core.DSN, Driver: core.Postgres, Display: "postgres://rw@127.0.0.1:39003/staging"}
	var b bytes.Buffer
	(&view{w: &b, o: viewOpts{plain: true}}).before(engine.Job{Mode: engine.Copy, From: pg, To: to})
	if !strings.Contains(b.String(), "Schema: rows only: a live PostgreSQL source sends no CREATE TABLE, so every table must already be on the target") {
		t.Errorf("copy:\n%s", b.String())
	}
	b.Reset()
	my := pg
	my.Driver = core.MySQL
	(&view{w: &b, o: viewOpts{plain: true}}).before(engine.Job{Mode: engine.Copy, From: my, To: to})
	if strings.Contains(b.String(), "Schema") {
		t.Errorf("MySQL source:\n%s", b.String())
	}
}
