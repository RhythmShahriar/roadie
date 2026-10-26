// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// signedEnvelope is r's envelope signed with the fixed test key, so the
// goldens never change.
func signedEnvelope(t *testing.T, r *core.Result) *core.Envelope {
	t.Helper()
	e := mustEnvelope(t, r)
	if err := testSigner(t, 1).Sign(e); err != nil {
		t.Fatal(err)
	}
	return e
}

type renderer struct {
	ext    string
	render func(*core.Envelope) ([]byte, error)
}

var renderers = []renderer{
	{"md", Markdown},
	{"html", func(e *core.Envelope) ([]byte, error) { return HTML([]*core.Envelope{e}, Brand{}) }},
	{"sarif", SARIF},
	{"xml", JUnit},
}

// Every format is golden for a PASS copy, a FAIL verify and a stopped copy.
func TestRenderGolden(t *testing.T) {
	for name, r := range map[string]func() *core.Result{"pass": copyPass, "fail": verifyFail, "error": copyError} {
		e := signedEnvelope(t, r())
		for _, f := range renderers {
			b, err := f.render(e)
			if err != nil {
				t.Fatalf("%s %s: %v", name, f.ext, err)
			}
			testutil.Golden(t, "render/"+name+"."+f.ext, b)
		}
	}
}

// Every format says the copy is pseudonymised and still personal data,
// carries the Not covered sentence and the footer (PRD 19.1, 19.2) and
// names the key that signed it.
func TestRenderHonestWording(t *testing.T) {
	e := signedEnvelope(t, verifyFail())
	id := ShortKeyID(testSigner(t, 1).KeyID())
	for _, f := range renderers {
		b, err := f.render(e)
		if err != nil {
			t.Fatal(err)
		}
		text := htmlText(b)
		if !strings.Contains(strings.ToLower(text), "pseudonymised") {
			t.Errorf("%s lacks pseudonymised", f.ext)
		}
		for _, want := range []string{StillPersonal, NotCoveredLine(nil)[:60], "It doesn't prove the data holds no personal data.", id} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", f.ext, want)
			}
		}
		if regexp.MustCompile(`(?i)anonymi[sz]ed|compliant|safe to share`).MatchString(text) {
			t.Errorf("%s holds a banned word", f.ext)
		}
	}
	// An unsigned report says so instead of naming a key.
	md, _ := Markdown(mustEnvelope(t, verifyFail()))
	if !bytes.Contains(md, []byte("This report is not signed (--sign=off)")) || bytes.Contains(md, []byte("Signed by")) {
		t.Errorf("unsigned footer:\n%s", md)
	}
}

// htmlText undoes the escapes of the four formats well enough to search
// for sentences: HTML and XML entities, Markdown backslashes.
func htmlText(b []byte) string {
	s := string(b)
	s = strings.NewReplacer("&#39;", "'", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&quot;", `"`, "&#xA;", "\n", "&#x9;", "\t").Replace(s)
	return regexp.MustCompile(`\\([\\`+"`"+`*_\[\]()!<>|#~&{}:.@+=-])`).ReplaceAllString(s, "$1")
}

// hostile is a result whose table names, titles, fixes and comments try
// to break out of every format.
func hostile() *core.Result {
	r := verifyFail()
	evil := "<script>alert(1)</script>|x](javascript:x)<img src=x onerror=alert(2)>"
	r.Verify.Findings[0].Title = "emails " + evil
	r.Verify.Findings[0].Fix = "rhythm-roadie prepare `x` " + evil
	r.Verify.Findings[0].Locations[0].Table = "wp_" + evil
	r.Verify.Findings[0].Detail = "a comment: " + evil + "\n| fake | row |"
	r.Verify.Uncovered[0].Table = evil
	r.Target.Display = evil + ".sql"
	r.NotCovered = append(r.NotCovered, evil)
	r.Tables = []core.TableReport{{Name: evil, Rows: 1, Action: "pseudonymise", Columns: []core.ColumnReport{{Name: evil, Rule: evil, Changed: 1}}}}
	return r
}

func TestRenderEscapesHostileValues(t *testing.T) {
	e := signedEnvelope(t, hostile())
	html, err := HTML([]*core.Envelope{e}, Brand{Name: "Agency <script>x</script>"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "<img", "javascript:x)<"} {
		if bytes.Contains(bytes.ToLower(html), []byte(bad)) {
			t.Errorf("HTML holds %q unescaped", bad)
		}
	}
	if !bytes.Contains(html, []byte("&lt;script&gt;alert(1)&lt;/script&gt;")) {
		t.Error("the hostile text isn't shown escaped")
	}
	md, err := Markdown(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(md), "\n") {
		// Text inside a code span is shown as it is; outside one every
		// special character must be escaped.
		text := stripCodeSpans(line)
		if strings.Contains(text, "](javascript") {
			t.Errorf("Markdown opens a link: %s", line)
		}
		if regexp.MustCompile(`(^|[^\\])<(script|img)`).MatchString(text) {
			t.Errorf("Markdown holds raw HTML: %s", line)
		}
		// A table row keeps its own cells: every | inside a value is escaped.
		if strings.HasPrefix(line, "| ") {
			if cells := len(regexp.MustCompile(`(^|[^\\])\|`).FindAllString(line, -1)); cells != 3 && cells != 4 && cells != 5 {
				t.Errorf("a value split a table row (%d cells): %s", cells, line)
			}
		}
	}
	// SARIF is JSON: the text stays text there. JUnit is XML.
	jb, _ := JUnit(e)
	if bytes.Contains(jb, []byte("<script")) {
		t.Error("JUnit holds raw markup")
	}
	if err := xml.Unmarshal(jb, new(junitSuites)); err != nil {
		t.Errorf("JUnit isn't XML: %v", err)
	}
}

// The HTML page loads nothing: no scripts, no external assets, a strict
// CSP and no http URL except text from the data itself.
func TestHTMLSelfContained(t *testing.T) {
	logo := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	b, err := HTML([]*core.Envelope{signedEnvelope(t, copyPass()), signedEnvelope(t, verifyFail())}, Brand{Name: "Acme Agency", Logo: logo})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`) {
		t.Error("no CSP meta")
	}
	for _, bad := range []string{"<script", "<link", "<iframe", "<object", "@import", "url("} {
		if strings.Contains(s, bad) {
			t.Errorf("HTML holds %s", bad)
		}
	}
	for _, m := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(s, -1) {
		if !strings.HasPrefix(m[1], "#") && !strings.HasPrefix(m[1], "data:image/png;base64,") {
			t.Errorf("an attribute loads %q", m[1])
		}
	}
	for _, u := range regexp.MustCompile(`https?://[^\s"<&]*`).FindAllString(s, -1) {
		if u != "https://staging.shop.test" && u != "ssh://deploy@shop.com/var/www/shop" {
			t.Errorf("HTML holds the URL %q", u)
		}
	}
	// Several reports: one summary page, worst first.
	fail, pass := strings.Index(s, `id="r1"`), strings.Index(s, `id="r2"`)
	if !strings.Contains(s, "Summary, worst first") || fail < 0 || pass < fail ||
		!strings.Contains(s[fail:pass], "FAIL") || !strings.Contains(s[pass:], "PASS") {
		t.Error("the summary page isn't worst first")
	}
	if !strings.Contains(s, "Acme Agency") || !strings.Contains(s, `<img src="data:image/png;base64,`) {
		t.Error("no brand")
	}
	md, _ := MarkdownAll([]*core.Envelope{signedEnvelope(t, copyPass()), signedEnvelope(t, verifyFail())})
	if f, p := bytes.Index(md, []byte("## Roadie: FAIL")), bytes.Index(md, []byte("## Roadie: PASS")); f < 0 || p < f {
		t.Error("MarkdownAll isn't worst first")
	}
}

func TestLogo(t *testing.T) {
	for name, logo := range map[string]string{
		"jpeg": "\xff\xd8\xff\xe0rest",
		"svg":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#f00"/></svg>`,
	} {
		uri, err := LogoDataURI([]byte(logo))
		if err != nil || !strings.HasPrefix(uri, "data:image/") {
			t.Errorf("%s: %v %q", name, err, uri)
		}
	}
	for name, logo := range map[string][]byte{
		"script": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"href":   []byte(`<svg xmlns="http://www.w3.org/2000/svg"><a href="https://x.example"><rect/></a></svg>`),
		"style":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:url(https://x.example)"/></svg>`),
		"gif":    []byte("GIF89a"),
		"big":    append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoSize)...),
	} {
		if _, err := LogoDataURI(logo); err == nil {
			t.Errorf("%s logo accepted", name)
		}
		if _, err := HTML([]*core.Envelope{mustEnvelope(t, copyPass())}, Brand{Logo: logo}); err == nil {
			t.Errorf("%s logo rendered", name)
		}
	}
}

func TestSARIFValid(t *testing.T) {
	schema := loadSchema(t, "testdata/sarif-2.1.0-subset.json")
	for name, r := range map[string]func() *core.Result{"pass": copyPass, "fail": verifyFail, "error": copyError, "hostile": hostile} {
		b, err := SARIF(signedEnvelope(t, r()))
		if err != nil {
			t.Fatal(err)
		}
		if errs := validate(schema, b); len(errs) > 0 {
			t.Errorf("%s isn't valid SARIF:\n%s", name, strings.Join(errs, "\n"))
		}
	}
	b, _ := SARIF(signedEnvelope(t, verifyFail()))
	var log sarifLog
	if err := json.Unmarshal(b, &log); err != nil {
		t.Fatal(err)
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "rhythm-roadie" || len(run.Tool.Driver.Rules) != 3 {
		t.Fatalf("driver %+v", run.Tool.Driver)
	}
	for _, rule := range run.Tool.Driver.Rules {
		if rule.HelpURI != WikiBase+"#"+rule.ID {
			t.Errorf("helpUri %s", rule.HelpURI)
		}
	}
	// A database location is the dump file plus the column as a logical
	// location; a file finding keeps its path and line.
	var db, file bool
	for _, res := range run.Results {
		l := res.Locations[0]
		switch {
		case l.PhysicalLocation.ArtifactLocation.URI == "staging.sql.gz" && len(l.LogicalLocations) == 1 &&
			l.LogicalLocations[0] == (sarifLogical{Name: "user_email", FullyQualifiedName: "wp_users.user_email", Kind: "member"}):
			db = true
		case l.PhysicalLocation.ArtifactLocation.URI == "wp-config.php" && l.PhysicalLocation.Region != nil && l.PhysicalLocation.Region.StartLine == 41:
			file = true
		}
	}
	if !db || !file {
		t.Errorf("locations: db %v file %v\n%s", db, file, b)
	}
	// A site target's tables are db://<host>/<table>.
	b, _ = SARIF(signedEnvelope(t, func() *core.Result {
		r := copyPass()
		r.Verify.Findings = verifyFail().Verify.Findings
		return r
	}()))
	if !bytes.Contains(b, []byte(`"uri": "db://staging.shop.test/wp_users"`)) {
		t.Errorf("no db:// location:\n%s", b)
	}
}

func TestFileURI(t *testing.T) {
	for in, want := range map[string]string{
		"staging.sql.gz":            "staging.sql.gz",
		"dumps/a b.sql":             "dumps/a%20b.sql",
		`wp-content\themes\x.php`:   "wp-content/themes/x.php",
		"/srv/dumps/staging #1.sql": "file:///srv/dumps/staging%20%231.sql",
		`C:\dumps\staging.sql`:      "file:///C:/dumps/staging.sql",
		"d:/x.sql":                  "file:///d:/x.sql",
		`\\nas\share\staging.sql`:   "file://nas/share/staging.sql",
		"ab:c.sql":                  "./ab:c.sql",
	} {
		if got := fileURI(in); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJUnit(t *testing.T) {
	b, err := JUnit(signedEnvelope(t, verifyFail()))
	if err != nil {
		t.Fatal(err)
	}
	var s junitSuites
	if err := xml.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	suite := s.Suites[0]
	if suite.Name != "roadie.verify" || suite.Tests != 4 || suite.Fails != 2 || suite.Skipped != 1 {
		t.Fatalf("suite %+v", suite)
	}
	if f := suite.Cases[0].Failure; f == nil || f.Message != "3 real-looking emails" {
		t.Errorf("failure %+v", f)
	}
	if f := suite.Cases[2].Failure; f == nil || f.Message != "Stripe live key" {
		t.Errorf("keys failure %+v", f)
	}
	b, _ = JUnit(signedEnvelope(t, copyError()))
	var stopped junitSuites
	_ = xml.Unmarshal(b, &stopped)
	if c := stopped.Suites[0].Cases[0]; c.Error == nil || c.Error.Type != core.ErrTargetIsProduction {
		t.Errorf("error case %+v", c)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	e := signedEnvelope(t, copyPass())
	folder, err := Save(dir, e, []byte("<!doctype html>"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".roadie", "reports", "2026-10-26T14-02-11"); folder != want {
		t.Errorf("folder %s, want %s", folder, want)
	}
	again, err := Save(dir, e, nil)
	if err != nil || again != filepath.Join(dir, ".roadie", "reports", "2026-10-26T14-02-11-2") {
		t.Errorf("second save %s %v", again, err)
	}
	js, _ := os.ReadFile(filepath.Join(folder, "report.json"))
	if err := Check(js, nil, nil); err != nil {
		t.Errorf("the saved report doesn't check: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Join(folder, "report.json"), filepath.Join(folder, "report.html")} {
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v", p, fi.Mode().Perm())
			}
		}
		for _, p := range []string{folder, filepath.Dir(folder)} {
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o700 {
				t.Errorf("%s mode %v", p, fi.Mode().Perm())
			}
		}
	}
	// No start time: the clock names the folder.
	now = func() time.Time { return started.Add(time.Hour) }
	t.Cleanup(func() { now = time.Now })
	if f, err := Save(dir, mustEnvelope(t, &core.Result{Kind: "verify"}), nil); err != nil || filepath.Base(f) != "2026-10-26T15-02-11" {
		t.Errorf("clock folder %s %v", f, err)
	}
	if _, err := Save(dir, nil, nil); err == nil {
		t.Error("saved a nil envelope")
	}
}

// A project folder that can't be written sends the report to the cache.
func TestSaveFallsBackToCache(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the user can't write to")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	cache := t.TempDir()
	t.Setenv(config.EnvCacheDir, cache)
	folder, err := Save(ro, signedEnvelope(t, copyPass()), nil)
	if err != nil || folder != filepath.Join(cache, "roadie", "reports", "2026-10-26T14-02-11") {
		t.Fatalf("fallback %s %v", folder, err)
	}
}

// stripCodeSpans replaces each Markdown code span in line (a run of n
// backticks up to the next run of exactly n) with CODE.
func stripCodeSpans(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		end := -1
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				end = j + m
				break
			}
			j += m
		}
		if end < 0 {
			b.WriteString(line[i:])
			break
		}
		b.WriteString("CODE")
		i = end
	}
	return b.String()
}
