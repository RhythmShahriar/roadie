// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"bytes"
	"go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

const bom = "\xef\xbb\xbf"

// fixtures returns every fixture by name, the BOM and lone-CR ones built
// at test time.
func fixtures(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, dir := range []string{"testdata", filepath.Join("testdata", "crlf")} {
		names, err := filepath.Glob(filepath.Join(dir, "*.php"))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				t.Fatal(err)
			}
			key := filepath.Base(n)
			if dir != "testdata" {
				key = "crlf/" + key
			}
			out[key] = b
		}
	}
	std := out["standard.php"]
	if len(std) == 0 || len(out["crlf/standard.php"]) == 0 {
		t.Fatal("fixtures missing")
	}
	out["bom/standard.php"] = append([]byte(bom), std...)
	out["cr/standard.php"] = bytes.ReplaceAll(std, []byte("\n"), []byte("\r"))
	return out
}

func mustParse(t *testing.T, b []byte) *File {
	t.Helper()
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// prepare makes the edits the kill switch makes.
func prepare(t *testing.T, f *File) {
	t.Helper()
	if err := f.Set("WP_ENVIRONMENT_TYPE", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetBool("DISABLE_WP_CRON", true); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("AUTH_KEY", `new'key\with$"quotes`); err != nil {
		t.Fatal(err)
	}
}

func TestIdentity(t *testing.T) {
	for name, b := range fixtures(t) {
		if got := mustParse(t, b).Bytes(); !bytes.Equal(got, b) {
			t.Errorf("%s: Bytes() differs from the input with no edit", name)
		}
	}
}

func TestStandard(t *testing.T) {
	f := mustParse(t, fixtures(t)["standard.php"])
	name, user, pass, host, ok := f.DB()
	if !ok || name != "shop" || user != "shop_user" || pass != "fixture-pass-1" || host != "localhost" {
		t.Errorf("DB() = %q %q %q %q %v", name, user, pass, host, ok)
	}
	if got := f.TablePrefix(); got != "wp_" {
		t.Errorf("TablePrefix %q", got)
	}
	pairs := f.Pairs()
	if len(pairs) != 16 {
		t.Errorf("%d pairs, want 16: %v", len(pairs), pairs)
	}
	if p, ok := f.Get("WP_DEBUG"); !ok || p.Value != "false" || p.Quoted || p.Line != 26 {
		t.Errorf("WP_DEBUG = %+v %v", p, ok)
	}
	if p, ok := f.Get("WP_MEMORY_LIMIT"); !ok || p.Value != "256" || p.Quoted {
		t.Errorf("WP_MEMORY_LIMIT = %+v %v", p, ok)
	}
	if p, ok := f.Get("DB_COLLATE"); !ok || p.Value != "" || !p.Quoted {
		t.Errorf("DB_COLLATE = %+v %v", p, ok)
	}
	if got := f.Unresolved(); !slices.Equal(got, []string{"ABSPATH"}) {
		t.Errorf("Unresolved %v, want [ABSPATH]", got)
	}
	d, ok := f.Lookup("ABSPATH")
	if !ok || !d.Conditional || d.Literal || !d.BeforeSettings {
		t.Errorf("ABSPATH %+v", d)
	}
}

func TestGetenv(t *testing.T) {
	f := mustParse(t, fixtures(t)["getenv.php"])
	want := []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST", "WP_HOME", "WP_CACHE_KEY_SALT"}
	if got := f.Unresolved(); !slices.Equal(got, want) {
		t.Errorf("Unresolved %v, want %v", got, want)
	}
	if _, _, _, _, ok := f.DB(); ok {
		t.Error("DB() ok with getenv values")
	}
	if _, ok := f.Get("DB_PASSWORD"); ok {
		t.Error("Get gave a value built from getenv")
	}
	if len(f.Pairs()) != 0 {
		t.Errorf("Pairs %v, want none", f.Pairs())
	}
	if f.TablePrefix() != "" {
		t.Errorf("TablePrefix %q from getenv", f.TablePrefix())
	}
	d, _ := f.Lookup("DB_PASSWORD")
	if d.Value != "getenv('DB_PASS')" || d.Conditional {
		t.Errorf("DB_PASSWORD %+v", d)
	}
}

func TestConditional(t *testing.T) {
	f := mustParse(t, fixtures(t)["conditional.php"])
	cond := map[string]bool{}
	for _, d := range f.Defines() {
		cond[d.Name] = d.Conditional
	}
	want := map[string]bool{
		"DB_NAME": false, "DB_USER": false, "DB_PASSWORD": false, "DB_HOST": false,
		"WP_ENVIRONMENT_TYPE": true, "WP_DEBUG": true, "DISABLE_WP_CRON": true, "WP_CACHE": true, "FS_METHOD": true,
	}
	for k, v := range want {
		if c, ok := cond[k]; !ok || c != v {
			t.Errorf("%s conditional=%v (found %v), want %v", k, c, ok, v)
		}
	}
	if f.TablePrefix() != "shop_" {
		t.Errorf("TablePrefix %q", f.TablePrefix())
	}
	if err := f.Set("WP_ENVIRONMENT_TYPE", "staging"); err != nil {
		t.Fatal(err)
	}
	out := string(f.Bytes())
	// The new define goes before the if, so PHP sees it first.
	if !strings.Contains(out, "$table_prefix  = \"shop_\";\n\ndefine( 'WP_ENVIRONMENT_TYPE', 'staging' );\nif ( isset(") {
		t.Errorf("define not inserted before the conditional one:\n%s", out)
	}
}

func TestProductionReplacedInPlace(t *testing.T) {
	for _, name := range []string{"production.php", "crlf/production.php"} {
		f := mustParse(t, fixtures(t)[name])
		prepare(t, f)
		out := string(f.Bytes())
		if n := strings.Count(out, "WP_ENVIRONMENT_TYPE"); n != 1 {
			t.Errorf("%s: %d WP_ENVIRONMENT_TYPE defines, want 1", name, n)
		}
		if !strings.Contains(out, `define( "WP_ENVIRONMENT_TYPE" , "staging" );`) {
			t.Errorf("%s: quotes or spacing not kept:\n%s", name, out)
		}
		if !strings.Contains(out, `define( 'DISABLE_WP_CRON', true );`) || strings.Count(out, "DISABLE_WP_CRON") != 1 {
			t.Errorf("%s: DISABLE_WP_CRON not replaced in place:\n%s", name, out)
		}
		if p, _ := f.Get("DB_PASSWORD"); p.Value != `fixture$pass"3` {
			t.Errorf("%s: double-quoted escapes decoded to %q", name, p.Value)
		}
	}
}

func TestCommentsAndStringsIgnored(t *testing.T) {
	f := mustParse(t, fixtures(t)["commented.php"])
	var names []string
	for _, d := range f.Defines() {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"}) {
		t.Errorf("defines %v: a define in a comment, string or nowdoc was read", names)
	}
}

func TestInsertionPoints(t *testing.T) {
	tests := []struct {
		fixture, before string
	}{
		{"standard.php", "/* That's all, stop editing! Happy publishing. */"},
		{"nostop.php", "require_once(ABSPATH . 'wp-settings.php');"},
		{"closing.php", "?>"},
		{"commented.php", "/* That's all, stop editing! */"},
		{"system.php", "/* That's all, stop editing! */"},
	}
	for _, tt := range tests {
		f := mustParse(t, fixtures(t)[tt.fixture])
		if err := f.Set("WP_ENVIRONMENT_TYPE", "staging"); err != nil {
			t.Fatalf("%s: %v", tt.fixture, err)
		}
		if !strings.Contains(string(f.Bytes()), "define( 'WP_ENVIRONMENT_TYPE', 'staging' );\n"+tt.before) {
			t.Errorf("%s: define not on its own line before %q:\n%s", tt.fixture, tt.before, f.Bytes())
		}
	}
}

func TestInsertionEdgeCases(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"end with newline", "<?php\n$a = 1;\n", "<?php\n$a = 1;\ndefine( 'X', 'y' );\n"},
		{"end without newline", "<?php\n$a = 1;", "<?php\n$a = 1;\ndefine( 'X', 'y' );"},
		{"one line", "<?php require_once ABSPATH . 'wp-settings.php';", "<?php define( 'X', 'y' );\nrequire_once ABSPATH . 'wp-settings.php';"},
		{"close tag on the same line", "<?php $a = 1; ?>\n", "<?php $a = 1; define( 'X', 'y' );\n?>\n"},
		{"stop editing after wp-settings", "<?php\nrequire_once ABSPATH . 'wp-settings.php';\n/* That's all, stop editing! */\n",
			"<?php\ndefine( 'X', 'y' );\nrequire_once ABSPATH . 'wp-settings.php';\n/* That's all, stop editing! */\n"},
		{"define after wp-settings is too late", "<?php\nrequire_once ABSPATH . 'wp-settings.php';\ndefine('X', 'old');\n",
			"<?php\ndefine( 'X', 'y' );\nrequire_once ABSPATH . 'wp-settings.php';\ndefine('X', 'old');\n"},
		{"guarded conditional first", "<?php\nif (!defined('X')) define('X', 'old');\ndefine('X', 'old2');\n",
			"<?php\nif (!defined('X')) define('X', 'y');\ndefine('X', 'y');\n"},
		{"multi-line comment before", "<?php\n$a = 1; /* one\ntwo */ require 'wp-settings.php';\n",
			"<?php\n$a = 1; /* one\ntwo */ define( 'X', 'y' );\nrequire 'wp-settings.php';\n"},
		{"after a block", "<?php\nif ($a) { $b = 1; }\n", "<?php\nif ($a) { $b = 1; }\ndefine( 'X', 'y' );\n"},
		{"namespaced call", "<?php\n\\define('X', 'old');\n", "<?php\n\\define('X', 'y');\n"},
	}
	for _, tt := range tests {
		f := mustParse(t, []byte(tt.in))
		if err := f.Set("X", "y"); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := string(f.Bytes()); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestSetRefused(t *testing.T) {
	for _, tt := range []struct{ name, in, key string }{
		{"no php", "just html", "X"},
		{"bad name", "<?php\n", "1X"},
		{"bad name with quote", "<?php\n", "X'); system('id"},
		{"only inside a block", "<?php\nif ($a) {\n", "X"},
		{"wp-settings inside an if", "<?php\nif ($a) require 'wp-settings.php';\n", "X"},
	} {
		f := mustParse(t, []byte(tt.in))
		if err := f.Set(tt.key, "v"); err == nil {
			t.Errorf("%s: Set worked:\n%s", tt.name, f.Bytes())
		}
		if !bytes.Equal(f.Bytes(), []byte(tt.in)) {
			t.Errorf("%s: a refused Set changed the file", tt.name)
		}
	}
}

func TestParseRefused(t *testing.T) {
	for _, in := range []string{"<?php /* never ends", "<?php $a = 'never ends", "<?php $a = \"never ends", "<?php $a = <<<EOT\nnever ends\n"} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) worked", in)
		}
	}
}

func TestLineEndingsKept(t *testing.T) {
	fx := fixtures(t)
	for _, name := range []string{"crlf/standard.php", "cr/standard.php", "bom/standard.php"} {
		f := mustParse(t, fx[name])
		prepare(t, f)
		out := f.Bytes()
		switch name {
		case "crlf/standard.php":
			if bytes.Count(out, []byte("\n")) != bytes.Count(out, []byte("\r\n")) {
				t.Errorf("%s: a bare LF was written", name)
			}
			out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
		case "cr/standard.php":
			if bytes.Contains(out, []byte("\n")) {
				t.Errorf("%s: an LF was written", name)
			}
			out = bytes.ReplaceAll(out, []byte("\r"), []byte("\n"))
		case "bom/standard.php":
			if !bytes.HasPrefix(out, []byte(bom)) {
				t.Errorf("%s: BOM lost", name)
			}
			out = out[len(bom):]
		}
		lf := mustParse(t, fx["standard.php"])
		prepare(t, lf)
		if !bytes.Equal(out, lf.Bytes()) {
			t.Errorf("%s: edits differ from the LF file's beyond line endings:\n%s", name, out)
		}
	}
}

// Every fixture after the kill switch's edits: golden, idempotent and
// valid PHP.
func TestGoldenEdits(t *testing.T) {
	var edited [][]byte
	for name, b := range fixtures(t) {
		if strings.Contains(name, "/") {
			continue // the line-ending variants are compared with their LF file
		}
		f := mustParse(t, b)
		prepare(t, f)
		testutil.Golden(t, name+".golden", f.Bytes())
		once := f.Bytes()
		prepare(t, f)
		if !bytes.Equal(once, f.Bytes()) {
			t.Errorf("%s: a second prepare changed the file", name)
		}
		if d, _ := f.Lookup("WP_ENVIRONMENT_TYPE"); d.Value != "staging" || d.Conditional || !d.BeforeSettings {
			t.Errorf("%s: WP_ENVIRONMENT_TYPE %+v", name, d)
		}
		edited = append(edited, once)
	}
	for name, b := range fixtures(t) {
		if strings.Contains(name, "/") {
			f := mustParse(t, b)
			prepare(t, f)
			edited = append(edited, f.Bytes())
		}
	}
	phpLint(t, edited...)
}

// phpLint runs php -l on each file; it skips without php unless
// ROADIE_REQUIRE_PHP=1. php only checks the syntax, never runs the file.
func phpLint(t *testing.T, files ...[]byte) {
	t.Helper()
	php := testutil.RequirePHP(t)
	dir := t.TempDir()
	for i, b := range files {
		path := filepath.Join(dir, "wp-config-"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".php")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(php, "-n", "-l", path).CombinedOutput()
		if err != nil {
			t.Errorf("php -l failed: %v\n%s\n%s", err, out, b)
		}
	}
}

// A hostile wp-config.php is read as text: the process its system() call
// would start never runs; the package can't start one.
func TestSystemCallNeverRuns(t *testing.T) {
	dir := t.TempDir()
	b := bytes.ReplaceAll(fixtures(t)["system.php"], []byte("__DIR__ . '/pwned'"), []byte("'"+filepath.ToSlash(filepath.Join(dir, "pwned"))+"'"))
	f := mustParse(t, b)
	prepare(t, f)
	if err := os.WriteFile(filepath.Join(dir, "wp-config.php"), f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("the fixture's system() call ran: %v", err)
	}
	names, _ := filepath.Glob("*.go")
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(gotoken.NewFileSet(), n, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"os/exec"` || imp.Path.Value == `"syscall"` {
				t.Errorf("%s imports %s; the wp-config reader must never start a process", n, imp.Path.Value)
			}
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		in   string
		q    byte
		want string
	}{
		{"staging", '\'', "'staging'"},
		{`a'b\c`, '\'', `'a\'b\\c'`},
		{`a"b$c\d`, '"', `"a\"b\$c\\d"`},
		{"line\nbreak", '\'', `"line\nbreak"`},
		{"nul\x00", '"', `"nul\x00"`},
	}
	for _, tt := range tests {
		got := Quote(tt.in, tt.q)
		if got != tt.want {
			t.Errorf("Quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
		f := mustParse(t, []byte("<?php define('X', "+got+");"))
		if p, ok := f.Get("X"); !ok || p.Value != tt.in {
			t.Errorf("Quote(%q) reads back as %q %v", tt.in, p.Value, ok)
		}
	}
}

func TestStringDecoding(t *testing.T) {
	tests := []struct {
		src, want string
		literal   bool
	}{
		{`'it\'s \n'`, `it's \n`, true},
		{`"tab\tnew\nhex\x41oct\101u\u{1F600}"`, "tab\tnew\nhexAoctAu\U0001F600", true},
		{`"unknown \q"`, `unknown \q`, true},
		{`"var $x"`, `"var $x"`, false},
		{`"var {$x}"`, `"var {$x}"`, false},
		{"<<<'EOT'\n  raw $x\n  EOT", "raw $x", true},
		{"<<<EOT\nplain\nEOT", "plain", true},
		{"<<<\"EOT\"\nhas $x\nEOT", "<<<\"EOT\"\nhas $x\nEOT", false},
		{"-1.5", "-1.5", true},
		{"0x1F", "0x1F", true},
		{"TRUE", "true", true},
		{"PHP_INT_MAX", "PHP_INT_MAX", false},
		{"`id`", "`id`", false},
	}
	for _, tt := range tests {
		f := mustParse(t, []byte("<?php\ndefine('X', "+tt.src+");\n"))
		d, ok := f.Lookup("X")
		if !ok || d.Value != tt.want || d.Literal != tt.literal {
			t.Errorf("%s: got %q literal=%v (%v), want %q literal=%v", tt.src, d.Value, d.Literal, ok, tt.want, tt.literal)
		}
	}
}

func TestNotTheBuiltin(t *testing.T) {
	f := mustParse(t, []byte("<?php\n$o->define('A', 1);\nFoo::define('B', 2);\nfunction define2() {}\n$o?->define('C', 3);\n"))
	if len(f.Defines()) != 0 {
		t.Errorf("method calls read as define(): %+v", f.Defines())
	}
}

func FuzzWPConfigEdit(f *testing.F) {
	for _, name := range []string{"standard.php", "conditional.php", "production.php", "nostop.php", "closing.php", "commented.php", "getenv.php"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, "staging")
	}
	f.Add([]byte("<?php ?><?php define('WP_ENVIRONMENT_TYPE', 1) ?>"), "a'b")
	f.Fuzz(func(t *testing.T, b []byte, value string) {
		cfg, err := Parse(b)
		if err != nil {
			return
		}
		if !bytes.Equal(cfg.Bytes(), b) {
			t.Fatal("identity edit is not byte-identical")
		}
		_ = cfg.Pairs()
		_ = cfg.Unresolved()
		if err := cfg.Set("WP_ENVIRONMENT_TYPE", value); err != nil {
			if !bytes.Equal(cfg.Bytes(), b) {
				t.Fatal("a failed Set changed the file")
			}
			return
		}
		once := cfg.Bytes()
		if p, ok := cfg.Get("WP_ENVIRONMENT_TYPE"); !ok || p.Value != value {
			t.Fatalf("Set then Get = %q %v, want %q", p.Value, ok, value)
		}
		if err := cfg.Set("WP_ENVIRONMENT_TYPE", value); err != nil {
			t.Fatalf("second Set failed: %v", err)
		}
		if !bytes.Equal(once, cfg.Bytes()) {
			t.Fatalf("Set twice differs from Set once:\n%q\n%q", once, cfg.Bytes())
		}
	})
}
