// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dotenv

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustParse(t *testing.T, b []byte) *File {
	t.Helper()
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// privateKey builds a multi-line PEM-shaped value at test time, so no
// key-shaped block is committed.
func privateKey() string {
	return testutil.J("-----BEGIN ", "PRIVATE KEY-----\n", "MIIfixturefixturefixture\n", "bm90LWEtcmVhbC1rZXk=\n", "-----END ", "PRIVATE KEY-----")
}

func TestRead(t *testing.T) {
	for _, name := range []string{"laravel.env", "crlf/laravel.env"} {
		f := mustParse(t, read(t, name))
		if !bytes.Equal(f.Bytes(), read(t, name)) {
			t.Errorf("%s: Bytes() not byte-identical", name)
		}
		want := map[string]string{
			"APP_NAME":          "Roadie Shop",
			"APP_ENV":           "production",
			"DB_CONNECTION":     "mysql",
			"DB_HOST":           "127.0.0.1",
			"DB_PASSWORD":       "fixture#pass with spaces",
			"MAIL_HOST":         "smtp.mail.example",
			"MAIL_PASSWORD":     "",
			"MAIL_FROM_ADDRESS": "hello@${APP_DOMAIN}",
			"QUEUE_CONNECTION":  "database",
			"VITE_APP_NAME":     "${APP_NAME}",
		}
		for k, v := range want {
			if p, ok := f.Get(k); !ok || p.Value != v {
				t.Errorf("%s: %s = %q %v, want %q", name, k, p.Value, ok, v)
			}
		}
		chain, _ := f.Get("CERT_CHAIN")
		if want := "line one\nline two # not a comment\nline three"; strings.ReplaceAll(chain.Value, "\r\n", "\n") != want {
			t.Errorf("%s: CERT_CHAIN %q", name, chain.Value)
		}
		if p, _ := f.Get("VITE_APP_NAME"); p.Line != 25 || !p.Quoted {
			t.Errorf("%s: VITE_APP_NAME %+v, want line 25 quoted", name, p)
		}
		if got := f.Unresolved(); !slices.Equal(got, []string{"MAIL_FROM_ADDRESS", "VITE_APP_NAME"}) {
			t.Errorf("%s: Unresolved %v", name, got)
		}
		if n := len(f.Pairs()); n != 19 {
			t.Errorf("%s: %d pairs, want 19", name, n)
		}
	}
}

func TestSetGolden(t *testing.T) {
	for _, name := range []string{"laravel.env", "crlf/laravel.env"} {
		f := mustParse(t, read(t, name))
		edits := [][2]string{
			{"APP_ENV", "staging"},
			{"APP_URL", "https://staging.shop.test"},
			{"DB_HOST", "db.staging.internal"},
			{"DB_PASSWORD", "new pass \"$x\""},
			{"MAIL_HOST", "127.0.0.1"},
			{"MAIL_PASSWORD", ""},
			{"QUEUE_CONNECTION", "sync"},
			{"RHYTHM_ECHO_URL", "http://127.0.0.1:8025"},
			{"RHYTHM_ECHO_MODE", "seal"},
			{"APP_PRIVATE_KEY", privateKey()},
		}
		for _, e := range edits {
			if err := f.Set(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		out := f.Bytes()
		for _, e := range edits {
			if p, _ := f.Get(e[0]); p.Value != e[1] {
				t.Errorf("%s: %s reads back %q", name, e[0], p.Value)
			}
		}
		if !bytes.Contains(out, []byte("BEGIN PRIVATE")) || bytes.Count(out, []byte("QUEUE_CONNECTION=")) != 2 {
			t.Errorf("%s: duplicate key changed or key missing", name)
		}
		if name == "crlf/laravel.env" {
			if bytes.Count(out, []byte("\n")) != bytes.Count(out, []byte("\r\n")) {
				t.Errorf("%s: a bare LF was written", name)
			}
			out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
		}
		// The key is masked in the golden file so it holds no PEM block.
		out = bytes.ReplaceAll(out, []byte(strings.ReplaceAll(privateKey(), "\n", `\n`)), []byte("<private key>"))
		testutil.Golden(t, "laravel.env.golden", out)
	}
}

func TestSetRules(t *testing.T) {
	tests := []struct{ name, in, key, value, want string }{
		{"in place keeps the comment", "A=1 # c\n", "A", "2", "A=2 # c\n"},
		{"last duplicate", "A=1\nA=2\n", "A", "3", "A=1\nA=3\n"},
		{"export kept", "export A=1\n", "A", "x y", "export A=\"x y\"\n"},
		{"append with newline", "A=1\n", "B", "2", "A=1\nB=2\n"},
		{"append without trailing newline", "A=1", "B", "2", "A=1\nB=2"},
		{"append to empty", "", "B", "2", "B=2\n"},
		{"append CRLF", "A=1\r\n", "B", "2", "A=1\r\nB=2\r\n"},
		{"append lone CR", "A=1\r", "B", "2", "A=1\rB=2\r"},
		{"empty value", "A=old\n", "A", "", "A=\n"},
		{"quoted to plain", "A='x y'\n", "A", "plain", "A=plain\n"},
		{"multi-line replaced", "A=\"one\ntwo\"\nB=1\n", "A", "x", "A=x\nB=1\n"},
		{"plain characters", "", "A", "base64:abc+/=", "A=\"base64:abc+/=\"\n"},
		{"url stays plain", "", "A", "http://127.0.0.1:8025", "A=http://127.0.0.1:8025\n"},
		{"escapes", "", "A", "a\\b\"c$d\ne\tf", "A=\"a\\\\b\\\"c\\$d\\ne\\tf\"\n"},
		{"hash", "", "A", "a#b", "A=\"a#b\"\n"},
		{"spaces around =", "A = 1\n", "A", "2", "A = 2\n"},
		// Fuzz finds: text after the old value must not join or become the new one.
		{"text right after the value", `B=""""`, "B", "", `B=""""`},
		{"quoted text after the value", `B="" ""`, "B", "", `B="" ""`},
	}
	for _, tt := range tests {
		f := mustParse(t, []byte(tt.in))
		if err := f.Set(tt.key, tt.value); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := string(f.Bytes()); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
		if p, _ := f.Get(tt.key); p.Value != tt.value {
			t.Errorf("%s: reads back %q", tt.name, p.Value)
		}
	}
}

func TestRefused(t *testing.T) {
	if _, err := Parse([]byte("A=\"never ends\n")); err == nil {
		t.Error("unterminated double quote parsed")
	}
	if _, err := Parse([]byte("A='never ends\n")); err == nil {
		t.Error("unterminated single quote parsed")
	}
	f := mustParse(t, []byte("A=1\n"))
	for _, bad := range []string{"", "1A", "A B", "A=B", "A\nB"} {
		if err := f.Set(bad, "x"); err == nil {
			t.Errorf("Set(%q) worked", bad)
		}
	}
	if err := f.Set("A", "nul\x00"); err == nil {
		t.Error("a NUL value was written")
	}
	if string(f.Bytes()) != "A=1\n" {
		t.Errorf("a refused Set changed the file: %q", f.Bytes())
	}
}

func TestNotAssignments(t *testing.T) {
	in := "# A=1\n\n  # comment\nnot an assignment\n=x\n1A=2\n"
	f := mustParse(t, []byte(in))
	if len(f.Pairs()) != 0 {
		t.Errorf("Pairs %v from lines that aren't assignments", f.Pairs())
	}
}

func FuzzDotenv(f *testing.F) {
	f.Add(read(f, "laravel.env"), "APP_ENV", "staging")
	f.Add(read(f, "crlf/laravel.env"), "NEW_KEY", "a b")
	f.Add([]byte("export A='x'\rB=\"y\\n\"\n"), "B", "")
	f.Fuzz(func(t *testing.T, b []byte, key, value string) {
		env, err := Parse(b)
		if err != nil {
			return
		}
		if !bytes.Equal(env.Bytes(), b) {
			t.Fatal("not byte-identical without an edit")
		}
		before := env.Pairs()
		if err := env.Set(key, value); err != nil {
			if !bytes.Equal(env.Bytes(), b) {
				t.Fatal("a failed Set changed the file")
			}
			return
		}
		once := env.Bytes()
		if p, ok := env.Get(key); !ok || p.Value != value {
			t.Fatalf("Get after Set = %q %v, want %q", p.Value, ok, value)
		}
		after := env.Pairs()
		for i, p := range before {
			if after[i].Name != p.Name || (p.Name != key && after[i].Value != p.Value) {
				t.Fatalf("Set %s changed %s", key, p.Name)
			}
		}
		if err := env.Set(key, value); err != nil || !bytes.Equal(env.Bytes(), once) {
			t.Fatalf("Set twice differs from once: %v", err)
		}
	})
}
