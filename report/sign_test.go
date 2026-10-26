// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
)

// seedOf is a fixed test seed, so key IDs in goldens never change.
func seedOf(b byte) []byte { return bytes.Repeat([]byte{b}, ed25519.SeedSize) }

func noEnv(string) (string, bool) { return "", false }

// envWith answers ROADIE_SIGNING_KEY with v.
func envWith(v string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == EnvSigningKey {
			return v, true
		}
		return "", false
	}
}

func testSigner(t *testing.T, b byte) *Signer {
	t.Helper()
	s, created, err := LoadSigner("", envWith(base64.StdEncoding.EncodeToString(seedOf(b))))
	if err != nil || created {
		t.Fatalf("LoadSigner: %v created=%v", err, created)
	}
	return s
}

// signed is a signed verify FAIL report as report.json holds it.
func signed(t *testing.T, s *Signer, r *core.Result) []byte {
	t.Helper()
	e := mustEnvelope(t, r)
	if err := s.Sign(e); err != nil {
		t.Fatal(err)
	}
	return indent(t, e)
}

func TestKeyID(t *testing.T) {
	s := testSigner(t, 1)
	if !regexp.MustCompile(`^roadie:[A-Za-z0-9_-]{16}$`).MatchString(s.KeyID()) {
		t.Errorf("key ID %q", s.KeyID())
	}
	if s.KeyID() != KeyID(s.PublicKey()) || testSigner(t, 2).KeyID() == s.KeyID() {
		t.Error("key IDs don't follow the key")
	}
	if ShortKeyID(s.KeyID()) != s.KeyID()[:14]+"…" {
		t.Errorf("short %q", ShortKeyID(s.KeyID()))
	}
	var none *Signer
	if none.KeyID() != "" || none.Sign(&core.Envelope{}) == nil {
		t.Error("a nil signer signed")
	}
}

func TestLoadSignerSources(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvConfigDir, dir)
	want := testSigner(t, 7)

	// A --key file in base64 or PKCS#8 PEM.
	b64 := filepath.Join(dir, "b64.key")
	os.WriteFile(b64, []byte(base64.RawURLEncoding.EncodeToString(seedOf(7))+"\n"), 0o600)
	der, _ := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(seedOf(7)))
	pemFile := filepath.Join(dir, "pem.key")
	os.WriteFile(pemFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	for _, f := range []string{b64, pemFile} {
		s, created, err := LoadSigner(f, noEnv)
		if err != nil || created || s.KeyID() != want.KeyID() {
			t.Errorf("%s: %v %v %v", f, s.KeyID(), created, err)
		}
	}
	// ROADIE_SIGNING_KEY wins over --key.
	if s, _, _ := LoadSigner(b64, envWith(base64.StdEncoding.EncodeToString(seedOf(8)))); s.KeyID() == want.KeyID() {
		t.Error("--key won over ROADIE_SIGNING_KEY")
	}
	// Bad keys are errors that never show the key.
	short := base64.StdEncoding.EncodeToString(seedOf(9)[:16])
	if _, _, err := LoadSigner("", envWith(short)); err == nil || strings.Contains(err.Error(), short) {
		t.Errorf("short env key: %v", err)
	}
	junk := filepath.Join(dir, "junk.key")
	os.WriteFile(junk, []byte("not a key"), 0o600)
	pub := filepath.Join(dir, "pub.pem")
	pubDER, _ := x509.MarshalPKIXPublicKey(want.PublicKey())
	os.WriteFile(pub, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600)
	for _, f := range []string{junk, pub, filepath.Join(dir, "missing.key")} {
		if _, _, err := LoadSigner(f, noEnv); err == nil {
			t.Errorf("%s loaded", f)
		}
	}
}

// The default key is made once (0600 in a 0700 folder), then read back
// after that.
func TestLoadSignerCreatesDefault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv(config.EnvConfigDir, dir)
	s, created, err := LoadSigner("", nil)
	if err != nil || !created {
		t.Fatalf("first: %v created=%v", err, created)
	}
	again, created, err := LoadSigner("", nil)
	if err != nil || created || again.KeyID() != s.KeyID() {
		t.Fatalf("second: %v created=%v %s %s", err, created, again.KeyID(), s.KeyID())
	}
	path := DefaultKeyPath(dir)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("key file mode %v, want 0600", fi.Mode().Perm())
		}
		di, _ := os.Stat(filepath.Dir(path))
		if di.Mode().Perm() != 0o700 {
			t.Errorf("key folder mode %v, want 0700", di.Mode().Perm())
		}
	}
	// The report holds the public key only, never the seed.
	seed, _ := os.ReadFile(path)
	rep := signed(t, s, verifyFail())
	if bytes.Contains(rep, bytes.TrimSpace(seed)) {
		t.Error("the signing key is in the report")
	}
}

func TestSignAndCheck(t *testing.T) {
	s := testSigner(t, 1)
	rep := signed(t, s, verifyFail())
	id, err := CheckReport("report.json", rep, nil, nil)
	if err != nil || id != s.KeyID() {
		t.Fatalf("a fresh report: %v %s", err, id)
	}
	if err := Check(rep, nil, nil); err != nil {
		t.Fatal(err)
	}
	e, _ := ReadEnvelope(rep)
	d, _ := ParseData(e)
	if d.Signature == nil || d.Signature.Alg != "ed25519" || d.Signature.KeyID != s.KeyID() ||
		d.Signature.PublicKey != base64.StdEncoding.EncodeToString(s.PublicKey()) {
		t.Fatalf("signature %+v", d.Signature)
	}
	// Signing again with another key replaces the signature.
	other := testSigner(t, 2)
	if err := other.Sign(e); err != nil {
		t.Fatal(err)
	}
	if id, err := CheckReport("report.json", indent(t, e), nil, nil); err != nil || id != other.KeyID() {
		t.Fatalf("re-signed: %v %s", err, id)
	}
	// The compact and the indented forms check alike: whitespace isn't signed.
	compact, _ := json.Marshal(e)
	if err := Check(compact, nil, nil); err != nil {
		t.Fatal(err)
	}
	if want := "signed by " + ShortKeyID(s.KeyID()) + " and unchanged since. It shows this Roadie run found these results; it doesn't prove the checks covered everything."; CheckedLine(s.KeyID()) != want {
		t.Errorf("checked line %q", CheckedLine(s.KeyID()))
	}
}

func wantCode(t *testing.T, err error, code string, exit int) *core.Error {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != code || ce.Exit != exit {
		t.Fatalf("got %v, want %s exit %d", err, code, exit)
	}
	return ce
}

// Flipping one byte anywhere in report.json makes the check fail.
func TestCheckFlipEveryByte(t *testing.T) {
	rep := signed(t, testSigner(t, 1), verifyFail())
	for i := range rep {
		b := bytes.Clone(rep)
		b[i] ^= 0x01
		if err := Check(b, nil, nil); err == nil {
			t.Fatalf("byte %d flipped (%q) still checks", i, rep[max(0, i-20):min(len(rep), i+20)])
		}
	}
}

// edit changes a signed report through its JSON.
func edit(t *testing.T, rep []byte, change func(env map[string]any)) []byte {
	t.Helper()
	var env map[string]any
	d := json.NewDecoder(bytes.NewReader(rep))
	d.UseNumber()
	if err := d.Decode(&env); err != nil {
		t.Fatal(err)
	}
	change(env)
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCheckTamper(t *testing.T) {
	rep := signed(t, testSigner(t, 1), verifyFail())
	data := func(env map[string]any) map[string]any { return env["data"].(map[string]any) }
	cases := map[string]func(map[string]any){
		"verdict":  func(env map[string]any) { env["verdict"] = "ok" },
		"result":   func(env map[string]any) { data(env)["result"] = "pass" },
		"version":  func(env map[string]any) { env["version"] = "1.0.1" },
		"coverage": func(env map[string]any) { data(env)["coverage"].(map[string]any)["tables_total"] = json.Number("58") },
		"input":    func(env map[string]any) { data(env)["input"].(map[string]any)["value"] = strings.Repeat("0", 64) },
		"key id": func(env map[string]any) {
			data(env)["signature"].(map[string]any)["key_id"] = "roadie:AAAAAAAAAAAAAAAA"
		},
		"added field": func(env map[string]any) { data(env)["note"] = "fine" },
		"drop finding": func(env map[string]any) {
			d := data(env)
			d["findings"] = d["findings"].([]any)[1:]
		},
	}
	for name, change := range cases {
		err := Check(edit(t, rep, change), nil, nil)
		ce := wantCode(t, err, core.ErrReportEdited, 1)
		if !strings.HasPrefix(ce.Msg, "report check: report.json was changed after it was signed") {
			t.Errorf("%s: %s", name, ce.Msg)
		}
	}
	// A summary that no longer matches the findings names the field.
	err := Check(edit(t, rep, func(env map[string]any) { env["summary"].(map[string]any)["high"] = json.Number("0") }), nil, nil)
	if ce := wantCode(t, err, core.ErrReportEdited, 1); ce.Msg != "report check: report.json was changed after it was signed (summary.high 1 → 0)." {
		t.Errorf("summary: %s", ce.Msg)
	}
	// Not JSON, not a Roadie report, a key twice.
	for _, b := range [][]byte{[]byte("{"), []byte(`{"schema":"rhythm-echo/v1","data":{}}`), append(bytes.TrimSuffix(bytes.TrimSpace(bytes.Clone(rep)), []byte("}")), []byte(`,"verdict":"ok"}`)...)} {
		wantCode(t, Check(b, nil, nil), core.ErrReportEdited, 1)
	}
	// A signature Roadie can't read.
	bad := edit(t, rep, func(env map[string]any) { data(env)["signature"].(map[string]any)["sig"] = "AAAA" })
	if ce := wantCode(t, Check(bad, nil, nil), core.ErrReportEdited, 1); !strings.Contains(ce.Msg, "can't read") {
		t.Error(ce.Msg)
	}
}

func TestCheckUnsigned(t *testing.T) {
	rep := indent(t, mustEnvelope(t, verifyFail()))
	ce := wantCode(t, Check(rep, nil, nil), core.ErrReportUnsigned, 3)
	if ce.Msg != "report check: report.json has no signature (it was written with --sign=off)." {
		t.Error(ce.Msg)
	}
	if _, err := CheckReport("a\x1b[2J.json", rep, nil, nil); strings.Contains(err.Error(), "\x1b") {
		t.Error("the file name reached the message unescaped")
	}
}

func TestCheckPinned(t *testing.T) {
	ours, theirs := testSigner(t, 1), testSigner(t, 2)
	rep := signed(t, theirs, verifyFail())
	ce := wantCode(t, Check(rep, []byte(ours.KeyID()), nil), core.ErrReportEdited, 1)
	if ce.Msg != "report check: report.json is signed by "+theirs.KeyID()+", not by the key you pinned ("+ours.KeyID()+")." {
		t.Error(ce.Msg)
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(theirs.PublicKey())
	privDER, _ := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(seedOf(2)))
	for name, pin := range map[string][]byte{
		"key id":      []byte(theirs.KeyID() + "\n"),
		"public_key":  []byte(base64.StdEncoding.EncodeToString(theirs.PublicKey())),
		"raw public":  theirs.PublicKey(),
		"signing.key": []byte(base64.StdEncoding.EncodeToString(seedOf(2)) + "\n"),
		"PEM public":  pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}),
		"PEM private": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}),
	} {
		if err := Check(rep, pin, nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, pin := range []string{"roadie:7Q3kXc2…", "hello", "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"} {
		wantCode(t, Check(rep, []byte(pin), nil), core.ErrConfig, 2)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestCheckInput(t *testing.T) {
	r := verifyFail()
	dump := []byte("-- MySQL dump\nINSERT INTO t VALUES (1);\n")
	sum := sha256Hex(dump)
	r.Input.Value = sum
	rep := signed(t, testSigner(t, 1), r)
	if err := Check(rep, nil, bytes.NewReader(dump)); err != nil {
		t.Fatalf("the right dump: %v", err)
	}
	ce := wantCode(t, Check(rep, nil, strings.NewReader("another dump")), core.ErrReportEdited, 1)
	if ce.Msg != "report check: report.json describes another input (sha256 "+sum[:12]+"…)." {
		t.Error(ce.Msg)
	}
	db := verifyFail()
	db.Input = &core.Fingerprint{Kind: "db-tables", Value: "61 tables"}
	ce = wantCode(t, Check(signed(t, testSigner(t, 1), db), nil, bytes.NewReader(dump)), core.ErrReportEdited, 1)
	if !strings.Contains(ce.Msg, "describes a database (db-tables)") {
		t.Error(ce.Msg)
	}
	none := verifyFail()
	none.Input = nil
	wantCode(t, Check(signed(t, testSigner(t, 1), none), nil, bytes.NewReader(dump)), core.ErrReportEdited, 1)
}
