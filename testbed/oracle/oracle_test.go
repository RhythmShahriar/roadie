// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/build"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// The oracle may not use Roadie's code, directly or through another
// package of the module: only its own packages.
func TestOracleImportsNoRoadie(t *testing.T) {
	const module = "github.com/RhythmShahriar/roadie"
	const self = module + "/testbed/oracle"
	seen := map[string]bool{}
	var walk func(dir, path string)
	walk = func(dir, path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		// Every file, whatever its build tags (testbed, docker): the
		// default context would skip a tagged file's imports.
		bc := build.Default
		bc.UseAllFiles = true
		pkg, err := bc.ImportDir(dir, build.ImportComment)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		imports := append(append(append([]string{}, pkg.Imports...), pkg.TestImports...), pkg.XTestImports...)
		for _, imp := range imports {
			if !strings.HasPrefix(imp, module+"/") && imp != module {
				continue
			}
			if imp != self && !strings.HasPrefix(imp, self+"/") {
				t.Errorf("%s imports %s: the oracle must not use Roadie's code", path, imp)
				continue
			}
			walk(filepath.Join(".", strings.TrimPrefix(imp, self)), imp)
		}
	}
	walk(".", self)
	walk("cmd/oracle", self+"/cmd/oracle")
	if len(seen) < 2 {
		t.Fatal("walked no packages")
	}
}

// The test plants each encoding with encoders of its own, not forms(),
// so a bug in forms() shows up as a miss.
func plant(enc, v string) string {
	switch enc {
	case EncRaw:
		return v
	case EncLower:
		return strings.ToLower(v)
	case EncUpper:
		return strings.ToUpper(v)
	case EncJSON, EncJSONSlash:
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(map[string]string{"v": v})
		if enc == EncJSONSlash {
			return strings.ReplaceAll(b.String(), "/", `\/`)
		}
		return b.String()
	case EncJSONPHP, EncJSONUnicode:
		var b strings.Builder
		b.WriteString(`{"v":"`)
		for _, r := range v {
			alnum := r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
			switch {
			case alnum, enc == EncJSONPHP && r < 128 && r >= 0x20 && r != '"' && r != '\\' && r != '/':
				b.WriteRune(r)
			case enc == EncJSONPHP && (r == '"' || r == '\\' || r == '/'):
				b.WriteByte('\\')
				b.WriteRune(r)
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
		b.WriteString(`"}`)
		return b.String()
	case EncURL:
		return "https://x.example/?next=" + url.QueryEscape(v) + "&a=1"
	case EncURLPath:
		return "https://x.example/" + url.PathEscape(v) + "/"
	case EncURLAll:
		var b strings.Builder
		for i := range len(v) {
			c := v[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		return "q=" + b.String()
	case EncHTML:
		return "<p>" + strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;").Replace(v) + "</p>"
	case EncHTMLDec, EncHTMLHex:
		var b strings.Builder
		for _, r := range v {
			switch {
			case r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
				b.WriteRune(r)
			case enc == EncHTMLHex:
				fmt.Fprintf(&b, "&#x%x;", r)
			default:
				fmt.Fprintf(&b, "&#%d;", r)
			}
		}
		return b.String()
	case EncSerialized:
		return `a:1:{s:5:"email";s:` + strconv.Itoa(len(v)) + `:"` + v + `";}`
	case EncMD5:
		m := md5.Sum([]byte(v))
		return "gravatar/" + hex.EncodeToString(m[:])
	case EncSHA256:
		s := sha256.Sum256([]byte(v))
		return `{"em":"` + hex.EncodeToString(s[:]) + `"}`
	}
	for off := range 3 {
		prefix := strings.Repeat("{", off+3) // off+3 bytes: offset off modulo 3
		blob := []byte(prefix + v + `"}`)
		switch enc {
		case EncBase64 + "-" + strconv.Itoa(off):
			return base64.StdEncoding.EncodeToString(blob)
		case EncBase64URL + "-" + strconv.Itoa(off):
			return base64.URLEncoding.EncodeToString(blob)
		}
	}
	panic("no planter for " + enc)
}

// testSQLEscape is the test's own mysqldump string escape.
func testSQLEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch c {
		case '\\', '\'', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// canaryFor makes a value whose every encoding differs from its raw form
// (it has upper and lower case, a space, punctuation and non-ASCII).
// "/" and "+" and "?" make the base64 and url alphabets differ.
func canaryFor(i int) string {
	return fmt.Sprintf("Bavotel Mürisun%02d <b.m%02d@kovarin.de>/~&\"q\" ?>?+", i, i)
}

func writeDump(t *testing.T, path string, rows map[string][]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("-- MySQL dump 10.13\n\n")
	for _, table := range []string{"wp_options", "wp_postmeta", "wp_users"} {
		vals := rows[table]
		fmt.Fprintf(&b, "CREATE TABLE `%s` (\n  `id` bigint NOT NULL,\n  `v` longtext,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB;\n", table)
		if len(vals) == 0 {
			continue
		}
		fmt.Fprintf(&b, "INSERT INTO `%s` VALUES ", table)
		for i, v := range vals {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "(%d,'%s')", i+1, testSQLEscape(v))
		}
		b.WriteString(";\n")
	}
	b.WriteString("-- Dump completed on 2026-10-01 12:00:00\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOracleFindsEveryEncoding(t *testing.T) {
	dir := t.TempDir()
	var canaries []Canary
	rows := map[string][]string{}
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "wp-content"), 0o750); err != nil {
		t.Fatal(err)
	}
	encs := Encodings()
	for i, enc := range encs {
		c := Canary{ID: fmt.Sprintf("t-text-%04d", i), Kind: "text", Scenario: "t", Value: canaryFor(i)}
		canaries = append(canaries, c)
		rows["wp_postmeta"] = append(rows["wp_postmeta"], "before "+plant(enc, c.Value)+" after")
	}
	// The same encodings again in files, from a second set of canaries.
	var files strings.Builder
	for i, enc := range encs {
		c := Canary{ID: fmt.Sprintf("t-file-%04d", i), Kind: "text", Scenario: "t", Value: canaryFor(100 + i)}
		canaries = append(canaries, c)
		files.WriteString(plant(enc, c.Value) + "\n")
	}
	if err := os.WriteFile(filepath.Join(tree, "wp-content", "debug.log"), []byte(files.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, "out.sql")
	writeDump(t, dump, rows)
	rep, err := Run(context.Background(), Options{Canaries: canaries, Dump: dump, Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range rep.Survivors {
		if prev, ok := got[s.ID]; ok {
			t.Errorf("%s found twice: %s and %s in %s", s.ID, prev, s.Encoding, s.Where)
		}
		got[s.ID] = s.Encoding + " " + s.Where
	}
	for i, enc := range encs {
		if want := enc + " db:wp_postmeta"; got[fmt.Sprintf("t-text-%04d", i)] != want {
			t.Errorf("dump, %s: got %q, want %q", enc, got[fmt.Sprintf("t-text-%04d", i)], want)
		}
		if want := enc + " file:wp-content/debug.log"; got[fmt.Sprintf("t-file-%04d", i)] != want {
			t.Errorf("file, %s: got %q, want %q", enc, got[fmt.Sprintf("t-file-%04d", i)], want)
		}
	}
	if rep.Clean() {
		t.Error("a report with survivors is clean")
	}
	out, _ := json.Marshal(rep)
	for _, c := range canaries {
		if bytes.Contains(out, []byte(c.Value)) || bytes.Contains(out, []byte(jsonString(c.Value))) {
			t.Fatalf("the report holds the value of %s", c.ID)
		}
	}

	// A clean output with near misses reports nothing.
	clean := filepath.Join(dir, "clean.sql")
	near := []Canary{
		{ID: "n-ip-1", Kind: "ip", Scenario: "n", Value: "81.123.45.167"},
		{ID: "n-email-1", Kind: "email", Scenario: "n", Value: "bavotel.murisun.r04x17@gmail.com"},
		{ID: "n-phone-1", Kind: "phone", Scenario: "n", Value: "+44 7911 123456"},
	}
	writeDump(t, clean, map[string][]string{
		"wp_options": {"181.123.45.1670", "fake-email-1@example.com", "bavotel.murisun.r04x18@gmail.com", "+44 7911 1234567"},
		"wp_users":   {"nothing to see"},
	})
	rep, err = Run(context.Background(), Options{Canaries: near, Dump: clean, Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Survivors) != 0 || !rep.Clean() {
		t.Errorf("clean output: %+v", rep.Survivors)
	}
	// The same IP standing alone is found.
	writeDump(t, clean, map[string][]string{"wp_options": {"ip 81.123.45.167."}})
	if rep, _ = Run(context.Background(), Options{Canaries: near, Dump: clean}); len(rep.Survivors) != 1 {
		t.Errorf("ip on its own: %+v", rep.Survivors)
	}
}

// A canary inside a zip backup is found, deflated or stored, plain or
// gzip inside; a link in the tree is reported, not followed.
func TestOracleReadsZipAndReportsLinks(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "backups"), 0o750); err != nil {
		t.Fatal(err)
	}
	cs := []Canary{
		{ID: "z-email-1", Kind: "email", Scenario: "z", Value: "deflated.canary.z01@gmail.com"},
		{ID: "z-email-2", Kind: "email", Scenario: "z", Value: "stored.canary.z02@gmail.com"},
		{ID: "z-email-3", Kind: "email", Scenario: "z", Value: "gzipped.canary.z03@gmail.com"},
	}
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	// Enough repetition that deflate compresses rather than stores it.
	_, _ = gw.Write([]byte(strings.Repeat("-- padding padding\n", 40) + "INSERT INTO `wp_users` VALUES (1,'" + cs[2].Value + "');\n"))
	_ = gw.Close()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, m := range []struct {
		name   string
		method uint16
		body   []byte
	}{
		{"site/debug.log", zip.Deflate, []byte(strings.Repeat("padding ", 50) + cs[0].Value)},
		{"site/notes.txt", zip.Store, []byte("note " + cs[1].Value)},
		{"db.sql.gz", zip.Store, gz.Bytes()},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: m.name, Method: m.method})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(m.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "backups", "site.zip"), zb.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.log")
	if err := os.WriteFile(outside, []byte(cs[0].Value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "backups", "latest.log")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	rep, err := Run(context.Background(), Options{Canaries: cs, Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range rep.Survivors {
		if prev, ok := got[s.ID]; ok {
			t.Errorf("%s found twice: %s and %s (%s)", s.ID, prev, s.Where, s.Encoding)
		}
		got[s.ID] = s.Where
	}
	want := map[string]string{
		"z-email-1": "file:backups/site.zip!site/debug.log",
		"z-email-2": "file:backups/site.zip",
		"z-email-3": "file:backups/site.zip!db.sql.gz:wp_users",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: found in %q, want %q", id, got[id], w)
		}
	}
	if len(rep.Links) != 1 || rep.Links[0] != "backups/latest.log" {
		t.Errorf("links %q", rep.Links)
	}

	// No zip, no link: nothing reported.
	if err := os.RemoveAll(filepath.Join(tree, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "fake.zip"), []byte("PK\x03\x04 not a zip "+cs[1].Value[:5]), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), Options{Canaries: cs, Tree: tree})
	if err != nil || len(rep.Survivors) != 0 || len(rep.Links) != 0 || !rep.Clean() {
		t.Errorf("clean tree: %+v %v", rep, err)
	}
}

// A link alone in a tree leaves the output unclean: whatever it points
// to went unread.
func TestOracleLinkIsNotClean(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "readme.txt"), []byte("nothing here"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Tree: tree})
	if err != nil || len(rep.Links) != 0 || !rep.Clean() {
		t.Fatalf("no link: %+v %v", rep, err)
	}
	if err := os.Symlink(filepath.Join(dir, "prod-backup.sql"), filepath.Join(tree, "backup.sql")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	rep, err = Run(context.Background(), Options{Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Survivors) != 0 || len(rep.Links) != 1 || rep.Clean() {
		t.Errorf("a link must make the output unclean: %+v", rep)
	}
}

// bzipped holds "INSERT INTO `wp_users` VALUES (1,'<z-email-5>');\n"
// compressed by bzip2 -9: the standard library reads bzip2 but can't
// write it.
const bzipped = "425a683931415926535943ff6eea00000adf80001040e5620862259f00fea7dab0200041500620d34c8326834186a1801934d06430434c468c1509d52db064bf76c40d89d0c3b137e8a164a16cc9595ba132bb72f917a31b7c5b9efa2d02545a621184e60fc5dc914e142410ffdbba80"

// zstdBytes compresses b with zstd.
func zstdBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	return enc.EncodeAll(b, nil)
}

// A canary inside zstd or bzip2 is found by the magic bytes, whatever
// the file is called, in a tree, in a zip and as the output dump; bytes
// that only start like one are no error and no finding.
func TestOracleReadsZstdAndBzip2(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o750); err != nil {
		t.Fatal(err)
	}
	cs := []Canary{
		{ID: "z-email-4", Kind: "email", Scenario: "z", Value: "zstded.canary.z04@gmail.com"},
		{ID: "z-email-5", Kind: "email", Scenario: "z", Value: "bzipped.canary.z05@gmail.com"},
		{ID: "z-email-6", Kind: "email", Scenario: "z", Value: "zipzstd.canary.z06@gmail.com"},
	}
	row := func(v string) []byte { return []byte("INSERT INTO `wp_users` VALUES (1,'" + v + "');\n") }
	bz, err := hex.DecodeString(bzipped)
	if err != nil {
		t.Fatal(err)
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "db.sql.zst", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(zstdBytes(t, row(cs[2].Value)))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"db.sql.zst":  zstdBytes(t, row(cs[0].Value)),
		"archive.dat": bz,
		"site.zip":    zb.Bytes(),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(tree, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Run(context.Background(), Options{Canaries: cs, Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, sv := range rep.Survivors {
		got[sv.ID] = sv.Where
	}
	want := map[string]string{
		"z-email-4": "file:db.sql.zst:wp_users",
		"z-email-5": "file:archive.dat",
		"z-email-6": "file:site.zip!db.sql.zst:wp_users",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: found in %q, want %q", id, got[id], w)
		}
	}

	// The output dump itself as zstd: the canary and the row count.
	dump := filepath.Join(dir, "out.sql.zst")
	if err := os.WriteFile(dump, files["db.sql.zst"], 0o600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "in.sql")
	if err := os.WriteFile(input, row("someone@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), Options{Canaries: cs, Dump: dump, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Survivors) != 1 || rep.Survivors[0].Where != "db:wp_users" || rep.RowsDiffer {
		t.Errorf("zstd dump: %+v", rep)
	}

	// A clean compressed file: nothing found.
	clean := filepath.Join(dir, "clean")
	if err := os.MkdirAll(clean, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clean, "db.sql.zst"), zstdBytes(t, row("someone@example.test")), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), Options{Canaries: cs, Tree: clean})
	if err != nil || len(rep.Survivors) != 0 || !rep.Clean() {
		t.Errorf("clean tree: %+v %v", rep, err)
	}

	// Bytes that only start like zstd or bzip2 can't be read: an error,
	// never a clean verdict.
	for name, b := range map[string][]byte{
		"fake.zst": append([]byte{0x28, 0xb5, 0x2f, 0xfd}, []byte("not zstd "+cs[0].Value[:5])...),
		"fake.bz2": []byte("BZh9 not bzip2 " + cs[1].Value[:5]),
	} {
		bad := filepath.Join(dir, "bad-"+name)
		if err := os.MkdirAll(bad, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bad, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(context.Background(), Options{Canaries: cs, Tree: bad}); err == nil {
			t.Errorf("%s: no error for bytes that don't decode", name)
		}
	}
}

func TestKeys(t *testing.T) {
	dir := t.TempDir()
	live := "sk" + "_live_" + "Q3vX9mP2kL7wR4tY8nB6cD1f"
	test := "sk" + "_test_" + "A7bC9dE2fG4hJ6kL8mN0pQ3r"
	keys := []Canary{
		{ID: "k-key-live", Kind: "key", Scenario: "k", Value: live, Live: true},
		{ID: "k-key-test", Kind: "key", Scenario: "k", Value: test},
	}
	dump := filepath.Join(dir, "a.sql")
	writeDump(t, dump, map[string][]string{"wp_options": {`a:1:{s:10:"secret_key";s:32:"` + live + `";}`}})
	rep, err := Run(context.Background(), Options{Canaries: keys, Dump: dump})
	if err != nil {
		t.Fatal(err)
	}
	if rep.LiveKeys != 1 || len(rep.TestKeysMissing) != 1 || rep.TestKeysMissing[0] != "k-key-test" || rep.Clean() {
		t.Errorf("live in, test gone: %+v", rep)
	}
	writeDump(t, dump, map[string][]string{"wp_options": {`s:32:"` + test + `";`, "blank"}})
	rep, _ = Run(context.Background(), Options{Canaries: keys, Dump: dump})
	if rep.LiveKeys != 0 || len(rep.TestKeysMissing) != 0 || !rep.Clean() {
		t.Errorf("live blanked, test kept: %+v", rep)
	}
}

// A canary across the matcher's chunk boundary, at the very start and at
// the very end of a stream, inside gzip, is still found.
func TestMatcherEdges(t *testing.T) {
	v := "dovarin.kelmas.q81z03@outlook.com"
	m, err := newMatcher(patterns([]Canary{{ID: "e", Kind: "email", Value: v}}, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int{0, chunkSize - 10, chunkSize - len(v), chunkSize, 3*chunkSize - 1} {
		data := bytes.Repeat([]byte("x"), at)
		data = append(data, v...)
		var hits []int64
		if err := m.scan(bytes.NewReader(data), func(off int64, p *pattern, _ []byte) {
			if p.canary == 0 {
				hits = append(hits, off)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0] != int64(at) {
			t.Errorf("canary at %d: hits %v", at, hits)
		}
	}
	dir := t.TempDir()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	fmt.Fprintf(zw, "INSERT INTO `wp_users` VALUES (1,'%s');\n", v)
	_ = zw.Close()
	path := filepath.Join(dir, "out.sql.gz")
	if err := os.WriteFile(path, gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Canaries: []Canary{{ID: "e", Kind: "email", Value: v}}, Dump: path})
	if err != nil || len(rep.Survivors) != 1 || rep.Survivors[0].Where != "db:wp_users" {
		t.Errorf("gzip dump: %+v %v", rep.Survivors, err)
	}
}

func TestBase64Core(t *testing.T) {
	v := "anything at all, 31 bytes long."
	for off := range 3 {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
			for lead := off; lead < off+9; lead += 3 {
				blob := enc.EncodeToString([]byte(strings.Repeat("\xff", lead) + v + "\x00\x01"))
				raw := base64.RawStdEncoding
				if enc == base64.URLEncoding {
					raw = base64.RawURLEncoding
				}
				if core := base64Core(raw, v, off); core == "" || !strings.Contains(blob, core) {
					t.Errorf("offset %d lead %d: core %q not in %q", off, lead, core, blob)
				}
			}
		}
	}
	if base64Core(base64.RawStdEncoding, "ab", 1) != "" {
		t.Error("a 2-byte value has no whole group at offset 1")
	}
}

func TestCleanAndIntegrity(t *testing.T) {
	php := requirePHP(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.sql")
	good := `a:2:{s:2:"ip";s:9:"127.0.0.1";s:2:"ua";s:3:"cli";}`
	writeDump(t, in, map[string][]string{
		"wp_options":  {good, "plain", `O:8:"stdClass":1:{s:1:"a";i:1;}`, "b:0;"},
		"wp_postmeta": {`a:1:{i:0;s:3:"one";}`},
		"wp_users":    {`a:1:{s:3:"bad";s:8:"too short";}`}, // broken already: never listed
	})
	rc, err := OpenDump(in)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := SerializedCells(context.Background(), rc, PHP{Cmd: []string{php}})
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := []Location{
		{"wp_options", "id=1", "v"}, {"wp_options", "id=3", "v"}, {"wp_options", "id=4", "v"},
		{"wp_postmeta", "id=1", "v"},
	}
	if fmt.Sprint(cells) != fmt.Sprint(want) {
		t.Fatalf("serialized cells %v, want %v", cells, want)
	}

	// The same rows: clean.
	rep, err := Run(context.Background(), Options{Dump: in, Input: in, Serialized: cells, PHP: PHP{Cmd: []string{php}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() || rep.Integrity.Checked != 4 || rep.Integrity.Failures != 0 || rep.RowsDiffer {
		t.Errorf("unchanged output: %+v %+v", rep, rep.Integrity)
	}

	// A rewrite that forgot the length plus a dropped row.
	out := filepath.Join(dir, "out.sql")
	writeDump(t, out, map[string][]string{
		"wp_options":  {strings.Replace(good, "127.0.0.1", "203.0.113.7", 1), "plain", `O:8:"stdClass":1:{s:1:"a";i:1;}`, "b:0;"},
		"wp_postmeta": {},
		"wp_users":    {`a:1:{s:3:"bad";s:8:"too short";}`},
	})
	rep, err = Run(context.Background(), Options{Dump: out, Input: in, Serialized: cells, PHP: PHP{Cmd: []string{php}}})
	if err != nil {
		t.Fatal(err)
	}
	// The dropped row's cell is gone with its row, which the row counts
	// judge; Missing is for a cell whose row is still there.
	if rep.Clean() || rep.Integrity.Failures != 1 || rep.Integrity.Missing != 0 || rep.Integrity.Gone["wp_postmeta"] != 1 || !rep.RowsDiffer {
		t.Errorf("broken output: %+v %+v", rep, rep.Integrity)
	}
	if len(rep.Integrity.Failed) != 1 || rep.Integrity.Failed[0] != (Location{"wp_options", "id=1", "v"}) {
		t.Errorf("failed cells %v", rep.Integrity.Failed)
	}
}

// requirePHP finds php, or skips (fails under ROADIE_REQUIRE_PHP=1). The
// oracle has its own copy of the helper: it may not import testutil.
func requirePHP(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("php")
	if err != nil {
		if os.Getenv("ROADIE_REQUIRE_PHP") == "1" {
			t.Fatal("php not on PATH and ROADIE_REQUIRE_PHP=1")
		}
		t.Skip("php not on PATH")
	}
	return p
}

// BenchmarkScan reports the matcher's speed with 2,000 canaries, so the
// oracle keeps up with the large-dump runs (r17).
func BenchmarkScan(b *testing.B) {
	var cs []Canary
	for i := range 2000 {
		cs = append(cs, Canary{ID: strconv.Itoa(i), Kind: "email", Value: fmt.Sprintf("bavotel.murisun.q%04dz@gmail.com", i)})
	}
	m, err := newMatcher(patterns(cs, true))
	if err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte("INSERT INTO `wp_usermeta` VALUES (1,2,'billing_email','fake-email-1@example.com'),(3,4,'k','a:1:{s:2:\"ip\";s:9:\"127.0.0.1\";}');\n"), 64<<10)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		if err := m.scan(bytes.NewReader(data), func(int64, *pattern, []byte) {}); err != nil {
			b.Fatal(err)
		}
	}
}

// The tree scan never panics or hangs on a file of any bytes, archive
// or not. A corrupt gzip, zstd or bzip2 stream is an error on purpose:
// unread bytes can't be called clean.
func FuzzScanFile(f *testing.F) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("a.sql")
	_, _ = w.Write([]byte("INSERT INTO `t` VALUES ('fuzz.canary.f01@gmail.com');\n"))
	_ = zw.Close()
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	_, _ = gw.Write([]byte("fuzz.canary.f01@gmail.com"))
	_ = gw.Close()
	f.Add(zb.Bytes())
	f.Add(gz.Bytes())
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{0x1f, 0x8b})
	f.Add([]byte{0x28, 0xb5, 0x2f, 0xfd})
	f.Add([]byte("BZh9"))
	cs := []Canary{{ID: "f-email-1", Kind: "email", Scenario: "f", Value: "fuzz.canary.f01@gmail.com"}}
	f.Fuzz(func(t *testing.T, data []byte) {
		tree := t.TempDir()
		if err := os.WriteFile(filepath.Join(tree, "x.zip"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = Run(context.Background(), Options{Canaries: cs, Tree: tree})
	})
}
