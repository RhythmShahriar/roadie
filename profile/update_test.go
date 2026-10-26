// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/update"
)

// entry is one file of a fixture tar.
type entry struct {
	name string
	body []byte
	typ  byte
	link string
}

// makeTar builds a gzipped tar of the entries.
func makeTar(t testing.TB, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// builtinFile returns one built-in profile's YAML.
func builtinFile(t testing.TB, id string) []byte {
	t.Helper()
	b, err := builtinFS.ReadFile("builtin/" + id + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// renamed is a profile file with another display name, so a test can
// tell the pack's profile from the built-in one.
func renamed(t testing.TB, id, name string) []byte {
	t.Helper()
	b := builtinFile(t, id)
	d, err := Parse(id+".yaml", b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Replace(b, []byte("name: "+d.Name), []byte("name: "+name), 1)
}

// memSource serves releases from memory.
type memSource struct {
	rels   map[string]map[string][]byte // version -> asset -> bytes
	latest string
	opened []string
}

func (m *memSource) release(v string) (*update.Release, error) {
	assets, ok := m.rels[v]
	if !ok {
		return nil, &update.ErrNoRelease{}
	}
	r := &update.Release{Version: v, Tag: "v" + v, Assets: map[string]string{}}
	for name := range assets {
		r.Assets[name] = "https://example.invalid/" + name
	}
	return r, nil
}

func (m *memSource) Latest(context.Context) (*update.Release, error) { return m.release(m.latest) }
func (m *memSource) ByTag(_ context.Context, v string) (*update.Release, error) {
	return m.release(v)
}
func (m *memSource) Open(_ context.Context, rel *update.Release, asset string) (io.ReadCloser, int64, error) {
	m.opened = append(m.opened, rel.Version+"/"+asset)
	b := m.rels[rel.Version][asset]
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

// fakeVerifier accepts a bundle that reads "signed <version>".
type fakeVerifier struct{ calls int }

func (f *fakeVerifier) VerifyChecksums(_ context.Context, repo, version string, _, bundle []byte) (string, error) {
	f.calls++
	if repo != packRepo || string(bundle) != "signed "+version {
		return "", errors.New("signature not valid")
	}
	return "https://github.com/RhythmShahriar/.github/.github/workflows/release-go.yml@refs/heads/master", nil
}

// release builds a release's assets around an archive.
func release(version string, archive []byte) map[string][]byte {
	sum := sha256.Sum256(archive)
	return map[string][]byte{
		packAsset:   archive,
		sumsAsset:   []byte(hex.EncodeToString(sum[:]) + "  " + packAsset + "\n" + strings.Repeat("0", 64) + "  rhythm-roadie_linux_amd64.tar.gz\n"),
		bundleAsset: []byte("signed " + version),
	}
}

// usePack points the package at a memory source, a fake verifier and a
// temporary cache.
func usePack(t *testing.T, src *memSource) (*fakeVerifier, string) {
	t.Helper()
	v := &fakeVerifier{}
	dir := filepath.Join(t.TempDir(), "profiles")
	old, oldBin := deps, binaryVersion
	deps = packDeps{source: src, verifier: v, cacheDir: func() (string, error) { return dir, nil }, offline: func() bool { return false }}
	binaryVersion = ""
	t.Cleanup(func() { deps, binaryVersion = old, oldBin })
	return v, dir
}

// A signed pack newer than the built-in profiles installs and loads; its
// profiles replace the built-in ones with the same ID.
func TestUpdateInstallsAndLoads(t *testing.T) {
	archive := makeTar(t, []entry{
		{name: "profile/", typ: tar.TypeDir},
		{name: "profile/builtin/", typ: tar.TypeDir},
		{name: "profile/builtin/wordpress.yaml", body: renamed(t, "wordpress", "WordPress from the pack")},
		{name: "woocommerce.yaml", body: builtinFile(t, "woocommerce")},
	})
	src := &memSource{latest: "9.9.9", rels: map[string]map[string][]byte{"9.9.9": release("9.9.9", archive)}}
	v, dir := usePack(t, src)
	var out bytes.Buffer
	packs, err := Update(context.Background(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 1 || packs[0].Name != "profiles" || packs[0].Version != "9.9.9" || len(packs[0].SHA256) != 64 || v.calls != 1 {
		t.Fatalf("packs %+v calls %d", packs, v.calls)
	}
	if out.String() != "Installed profile pack 9.9.9 (2 profiles).\n" {
		t.Errorf("output %q", out.String())
	}
	fi, err := os.Stat(filepath.Join(dir, "9.9.9", "wordpress.yaml"))
	if err != nil {
		t.Errorf("stored file: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		// Windows reports 0666 for any writable file.
		t.Errorf("stored file mode %v", fi.Mode().Perm())
	}
	defs, ver, err := Load(context.Background(), false)
	if err != nil || ver != "9.9.9" {
		t.Fatalf("load %s %v", ver, err)
	}
	wp, _ := ByID(defs, "wordpress")
	if wp.Name != "WordPress from the pack" {
		t.Errorf("wordpress from %q", wp.Name)
	}
	if _, ok := ByID(defs, "laravel"); !ok {
		t.Error("a built-in profile the pack lacks went missing")
	}
	// offline uses the built-in profiles even with a pack cached.
	if _, ver, _ := Load(context.Background(), true); ver != BuiltinVersion {
		t.Errorf("offline load %s", ver)
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ver, _ := Load(context.Background(), false); ver != BuiltinVersion {
		t.Errorf("after Clear %s", ver)
	}
}

// Every unsafe or unverified pack is refused and nothing is stored.
func TestUpdateRefuses(t *testing.T) {
	good := builtinFile(t, "wordpress")
	cases := map[string]struct {
		entries []entry
		edit    func(assets map[string][]byte)
		want    string
	}{
		"bad checksum": {entries: []entry{{name: "wordpress.yaml", body: good}},
			edit: func(a map[string][]byte) { a[packAsset] = append(a[packAsset], 0) }, want: "where checksums.txt says"},
		"bad signature": {entries: []entry{{name: "wordpress.yaml", body: good}},
			edit: func(a map[string][]byte) { a[bundleAsset] = []byte("forged") }, want: "doesn't verify"},
		"no bundle": {entries: []entry{{name: "wordpress.yaml", body: good}},
			edit: func(a map[string][]byte) { delete(a, bundleAsset) }, want: "has no checksums.txt.sigstore.json"},
		"not in checksums": {entries: []entry{{name: "wordpress.yaml", body: good}},
			edit: func(a map[string][]byte) { a[sumsAsset] = []byte("abc  other\n") }, want: "no valid line"},
		"traversal":    {entries: []entry{{name: "../wordpress.yaml", body: good}}, want: "a path outside the pack"},
		"deep":         {entries: []entry{{name: "profile/builtin/../../../x/wordpress.yaml", body: good}}, want: "a path outside the pack"},
		"absolute":     {entries: []entry{{name: "/etc/wordpress.yaml", body: good}}, want: "a path outside the pack"},
		"other folder": {entries: []entry{{name: "other/wordpress.yaml", body: good}}, want: "with no folders"},
		"symlink":      {entries: []entry{{name: "wordpress.yaml", typ: tar.TypeSymlink, link: "/etc/passwd"}}, want: "links aren't allowed"},
		"hard link":    {entries: []entry{{name: "wordpress.yaml", typ: tar.TypeLink, link: "x.yaml"}}, want: "links aren't allowed"},
		"non-yaml":     {entries: []entry{{name: "wordpress.yaml", body: good}, {name: "install.sh", body: []byte("#!/bin/sh\n")}}, want: "isn't a profile"},
		"oversize":     {entries: []entry{{name: "wordpress.yaml", body: append(bytes.Clone(good), bytes.Repeat([]byte("#"), maxEntry)...)}}, want: "over 1 MiB"},
		"unparseable":  {entries: []entry{{name: "wordpress.yaml", body: []byte("id: wordpress\nfuture_key: 1\n")}}, want: "doesn't parse"},
		"wrong id":     {entries: []entry{{name: "other.yaml", body: good}}, want: "differs from its file name"},
		"empty":        {entries: []entry{{name: "profile/", typ: tar.TypeDir}}, want: "holds no profiles"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assets := release("9.9.9", makeTar(t, c.entries))
			if c.edit != nil {
				c.edit(assets)
			}
			src := &memSource{latest: "9.9.9", rels: map[string]map[string][]byte{"9.9.9": assets}}
			_, dir := usePack(t, src)
			_, err := Update(context.Background(), io.Discard)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("stored %v", entries)
			}
		})
	}
	// No verifier set up: fail closed.
	src := &memSource{latest: "9.9.9", rels: map[string]map[string][]byte{"9.9.9": release("9.9.9", makeTar(t, []entry{{name: "wordpress.yaml", body: good}}))}}
	usePack(t, src)
	deps.verifier = nil
	if _, err := Update(context.Background(), io.Discard); !errors.Is(err, errNoVerifier) {
		t.Errorf("no verifier: %v", err)
	}
}

// RHYTHM_OFFLINE never touches the network; a latest release no newer
// than the built-in profiles downloads nothing; a pinned version
// installs by tag; an older cached pack is ignored.
func TestUpdateOfflineAndVersions(t *testing.T) {
	archive := makeTar(t, []entry{{name: "wordpress.yaml", body: renamed(t, "wordpress", "Old pack")}})
	src := &memSource{latest: "0.9.0", rels: map[string]map[string][]byte{"0.9.0": release("0.9.0", archive)}}
	_, dir := usePack(t, src)
	deps.offline = func() bool { return true }
	var out bytes.Buffer
	packs, err := Update(context.Background(), &out)
	if err != nil || len(src.opened) != 0 || packs[0].Version != BuiltinVersion {
		t.Fatalf("offline: %v %v %+v", err, src.opened, packs)
	}
	if out.String() != "RHYTHM_OFFLINE is set: kept the profiles in use ("+BuiltinVersion+").\n" {
		t.Errorf("offline output %q", out.String())
	}
	deps.offline = func() bool { return false }
	out.Reset()
	if packs, err := Update(context.Background(), &out); err != nil || len(src.opened) != 0 || packs[0].Version != BuiltinVersion {
		t.Errorf("not newer: %v %v %+v", err, src.opened, packs)
	}
	if out.String() != "The built-in profiles ("+BuiltinVersion+") are the newest; nothing to download.\n" {
		t.Errorf("not newer output %q", out.String())
	}
	out.Reset()
	packs, err = UpdatePinned(context.Background(), &out, map[string]string{"profiles": "v0.9.0"})
	if err != nil || packs[0].Version != "0.9.0" {
		t.Fatalf("pinned: %v %+v", err, packs)
	}
	if !strings.Contains(out.String(), "The built-in profiles ("+BuiltinVersion+") are newer, so Roadie keeps using them.") {
		t.Errorf("pinned output %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "0.9.0", "wordpress.yaml")); err != nil {
		t.Fatal(err)
	}
	defs, ver, err := Load(context.Background(), false)
	if err != nil || ver != BuiltinVersion {
		t.Fatalf("older cache used: %s %v", ver, err)
	}
	if wp, _ := ByID(defs, "wordpress"); wp.Name == "Old pack" {
		t.Error("an older pack's profile loaded")
	}
	if _, err := UpdatePinned(context.Background(), io.Discard, map[string]string{"profiles": "7.0.0"}); err == nil {
		t.Error("a missing pinned release installed")
	}
}

// Version prefers the binary's release version over BuiltinVersion.
func TestVersion(t *testing.T) {
	old := binaryVersion
	t.Cleanup(func() { binaryVersion = old })
	SetBinaryVersion("v1.2.3")
	if Version() != "1.2.3" {
		t.Errorf("release build: %s", Version())
	}
	SetBinaryVersion("dev")
	if Version() != BuiltinVersion {
		t.Errorf("dev build: %s", Version())
	}
}

// FuzzExtract feeds arbitrary bytes to the pack reader: it must never
// panic; anything it accepts holds only flat *.yaml names.
func FuzzExtract(f *testing.F) {
	f.Add(makeTar(f, []entry{{name: "wordpress.yaml", body: builtinFile(f, "wordpress")}}))
	f.Add(makeTar(f, []entry{{name: "../x.yaml", body: []byte("id: x")}}))
	f.Add([]byte("not gzip"))
	f.Fuzz(func(t *testing.T, b []byte) {
		files, err := extract(b)
		if err != nil {
			return
		}
		for name := range files {
			if strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".yaml") {
				t.Fatalf("accepted %q", name)
			}
		}
	})
}

// FuzzChecksumOf reads arbitrary checksums.txt text: a found sum is
// always 64 lower-case hex characters.
func FuzzChecksumOf(f *testing.F) {
	f.Add([]byte(strings.Repeat("a", 64) + "  " + packAsset + "\n"))
	f.Add([]byte("zz  " + packAsset))
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := checksumOf(b, packAsset)
		if err != nil {
			return
		}
		if _, derr := hex.DecodeString(h); derr != nil || len(h) != 64 || h != strings.ToLower(h) {
			t.Fatalf("sum %q", h)
		}
	})
}
