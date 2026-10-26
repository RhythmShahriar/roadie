// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/rhythm/kit/update"
)

// The profile pack of PRD 6.10: the YAML files of profile/builtin as a
// release asset, signed through checksums.txt like the binary.
const (
	// PackName is the pack's name in rhythm update and rhythm lock.
	PackName = "profiles"
	// packRepo is the repository whose releases carry the pack.
	packRepo = "RhythmShahriar/roadie"
	// packAsset is the pack's archive in a release.
	packAsset = "rhythm-roadie_profiles.tar.gz"
	// sumsAsset and bundleAsset are the signed checksums and their
	// Sigstore bundle.
	sumsAsset   = "checksums.txt"
	bundleAsset = "checksums.txt.sigstore.json"
	// maxEntry caps one profile file; maxArchive the whole download.
	maxEntry   = 1 << 20
	maxTotal   = 16 << 20 // every profile file of a pack together
	maxArchive = 32 << 20
	maxSmall   = 1 << 20 // checksums.txt and the bundle
	maxEntries = 256
)

// Verifier checks the Sigstore bundle over a release's checksums.txt, as
// kit sigverify does for the binary. Tests swap it: a real bundle needs
// Sigstore.
type Verifier interface {
	VerifyChecksums(ctx context.Context, repo, version string, checksums, bundle []byte) (signer string, err error)
}

// packDeps are what Update reaches outside the package: the release
// source, the signature check and the cache folder. Tests replace them.
type packDeps struct {
	source   update.Source
	verifier Verifier
	cacheDir func() (string, error)
	offline  func() bool
}

// deps is the package's packDeps; nil fields take the defaults.
var deps packDeps

func (d packDeps) src() update.Source {
	if d.source != nil {
		return d.source
	}
	return &githubSource{u: &update.Updater{Repo: packRepo, Binary: "rhythm-roadie", Current: "0.0.0", API: os.Getenv("RHYTHM_GITHUB_API")}}
}

// errNoVerifier fails a pack install closed when no signature check is
// set up.
var errNoVerifier = errors.New("no signature check is set up in this build, so nothing was installed")

func (d packDeps) verify() (Verifier, error) {
	if d.verifier != nil {
		return d.verifier, nil
	}
	if packVerifier != nil {
		return packVerifier, nil
	}
	return nil, errNoVerifier
}

// packVerifier is the signature check SetVerifier installs.
var packVerifier Verifier

// SetVerifier sets the check Update runs over the pack's checksums.txt:
// the CLI passes kit sigverify's Verifier (sigverify.New with no
// options), the same trust path as the binary: the shared release
// workflow at refs/heads/master, this repository and the release's tag.
// Without one Update installs nothing.
func SetVerifier(v Verifier) { packVerifier = v }

func (d packDeps) dir() (string, error) {
	if d.cacheDir != nil {
		return d.cacheDir()
	}
	dir, err := config.CacheDir("roadie")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles"), nil
}

func (d packDeps) isOffline() bool {
	if d.offline != nil {
		return d.offline()
	}
	v := os.Getenv("RHYTHM_OFFLINE")
	return v != "" && v != "0"
}

// binaryVersion is the running binary's version, set by the CLI.
var binaryVersion string

// SetBinaryVersion tells the package the running binary's version (the
// CLI's ldflags version), which the built-in profiles carry.
func SetBinaryVersion(v string) { binaryVersion = strings.TrimPrefix(v, "v") }

// Version returns the version of the built-in profiles: the binary's
// release version when it is one, else BuiltinVersion.
func Version() string {
	if v := binaryVersion; update.IsRelease(v) {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimPrefix(bi.Main.Version, "v"); update.IsRelease(v) {
			return v
		}
	}
	return BuiltinVersion
}

// Clear removes the cached pack; Roadie uses its built-in profiles until
// the next update.
func Clear() error {
	dir, err := deps.dir()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// loadWith is Load over d: the built-in profiles, with a newer cached
// pack over them unless offline.
func loadWith(d packDeps, offline bool) ([]Def, string, error) {
	defs, err := builtin()
	if err != nil {
		return nil, "", err
	}
	if offline || d.isOffline() {
		return defs, Version(), nil
	}
	if pack, v, ok := d.load(); ok {
		return merge(defs, pack), v, nil
	}
	return defs, Version(), nil
}

// load returns the cached pack when it is newer than the built-in
// profiles: its profiles over the built-in ones with its version.
func (d packDeps) load() ([]Def, string, bool) {
	dir, err := d.dir()
	if err != nil {
		return nil, "", false
	}
	v, ok := newestCached(dir)
	if !ok || !update.Newer(Version(), v) {
		return nil, "", false
	}
	defs, err := readPack(filepath.Join(dir, v))
	if err != nil {
		return nil, "", false
	}
	return defs, v, true
}

// newestCached is the highest version folder in dir.
func newestCached(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() || !update.IsRelease(e.Name()) {
			continue
		}
		if best == "" || update.Newer(best, e.Name()) {
			best = e.Name()
		}
	}
	return best, best != ""
}

// readPack parses every profile of an installed pack.
func readPack(dir string) ([]Def, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("empty pack")
	}
	defs := make([]Def, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n) // #nosec G304 -- a file of the verified pack in the cache
		if err != nil {
			return nil, err
		}
		d, err := parsePackFile(filepath.Base(n), b)
		if err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	return defs, nil
}

// parsePackFile parses one profile and checks its ID is its file name.
func parsePackFile(name string, b []byte) (Def, error) {
	d, err := Parse(name, b)
	if err != nil {
		return Def{}, err
	}
	if want := strings.TrimSuffix(name, ".yaml"); d.ID != want {
		return Def{}, fmt.Errorf("profile %s: id %q differs from its file name", name, d.ID)
	}
	return d, nil
}

// merge puts the pack's profiles over the built-in ones by ID, sorted.
func merge(builtin, pack []Def) []Def {
	out := slices.Clone(pack)
	for _, b := range builtin {
		if _, ok := ByID(pack, b.ID); !ok {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Def) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// install downloads, checks and caches one release's pack: the latest
// when version is "".
func (d packDeps) install(ctx context.Context, w io.Writer, version string) ([]cli.DataPack, error) {
	if w == nil {
		w = io.Discard
	}
	if d.isOffline() {
		_, v, err := loadWith(d, true)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(w, "RHYTHM_OFFLINE is set: kept the profiles in use (%s).\n", v)
		return []cli.DataPack{{Name: PackName, Version: v}}, nil
	}
	src := d.src()
	var rel *update.Release
	var err error
	if version == "" {
		rel, err = src.Latest(ctx)
	} else {
		rel, err = src.ByTag(ctx, version)
	}
	if err != nil {
		return nil, fmt.Errorf("profile pack: %w", err)
	}
	// The version names the cache folder; it comes from the release API
	// before anything is verified, so it must be a plain version.
	if !update.IsRelease(rel.Version) || strings.ContainsAny(rel.Version, `/\`) {
		return nil, fmt.Errorf("profile pack: the release's version %q isn't a release version, so nothing was installed", rel.Version)
	}
	if version == "" && !update.Newer(Version(), rel.Version) {
		_, _ = fmt.Fprintf(w, "The built-in profiles (%s) are the newest; nothing to download.\n", Version())
		return []cli.DataPack{{Name: PackName, Version: Version()}}, nil
	}
	sums, err := fetch(ctx, src, rel, sumsAsset, maxSmall)
	if err != nil {
		return nil, err
	}
	bundle, err := fetch(ctx, src, rel, bundleAsset, maxSmall)
	if err != nil {
		return nil, err
	}
	v, err := d.verify()
	if err != nil {
		return nil, err
	}
	if _, err := v.VerifyChecksums(ctx, packRepo, rel.Version, sums, bundle); err != nil {
		return nil, fmt.Errorf("profile pack %s: the signature over %s doesn't verify, so nothing was installed: %w", rel.Version, sumsAsset, err)
	}
	want, err := checksumOf(sums, packAsset)
	if err != nil {
		return nil, fmt.Errorf("profile pack %s: %w", rel.Version, err)
	}
	archive, err := fetch(ctx, src, rel, packAsset, maxArchive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return nil, fmt.Errorf("profile pack %s: %s has SHA-256 %s where checksums.txt says %s; nothing was installed", rel.Version, packAsset, got, want)
	}
	files, err := extract(archive)
	if err != nil {
		return nil, fmt.Errorf("profile pack %s: %w; nothing was installed", rel.Version, err)
	}
	dir, err := d.dir()
	if err != nil {
		return nil, err
	}
	if err := store(dir, rel.Version, files); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(w, "Installed profile pack %s (%d profiles).\n", rel.Version, len(files))
	if !update.Newer(Version(), rel.Version) {
		_, _ = fmt.Fprintf(w, "The built-in profiles (%s) are newer, so Roadie keeps using them.\n", Version())
	}
	return []cli.DataPack{{Name: PackName, Version: rel.Version, SHA256: got}}, nil
}

// fetch downloads one release asset, at most limit bytes.
func fetch(ctx context.Context, src update.Source, rel *update.Release, asset string, limit int64) ([]byte, error) {
	if _, ok := rel.Assets[asset]; !ok {
		return nil, fmt.Errorf("profile pack %s: the release has no %s", rel.Version, asset)
	}
	rc, _, err := src.Open(ctx, rel, asset)
	if err != nil {
		return nil, fmt.Errorf("profile pack %s: downloading %s: %w", rel.Version, asset, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("profile pack %s: downloading %s: %w", rel.Version, asset, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("profile pack %s: %s is over %d bytes", rel.Version, asset, limit)
	}
	return b, nil
}

// checksumOf reads one file's SHA-256 from checksums.txt
// ("<hex>  <name>" lines).
func checksumOf(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			h := strings.ToLower(f[0])
			if len(h) != 64 {
				break
			}
			if _, err := hex.DecodeString(h); err != nil {
				break
			}
			return h, nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no valid line for %s", name)
}

// extract reads the pack's tar.gz: only regular *.yaml files, flat or
// under profile/builtin/ as GoReleaser stores them, each under 1 MiB and
// each a profile this binary can parse. Anything else refuses the pack.
func extract(archive []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("%s isn't gzip: %w", packAsset, err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	var total int
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s is damaged: %w", packAsset, err)
		}
		name, err := entryName(h)
		if err != nil {
			return nil, err
		}
		if name == "" {
			continue // a folder
		}
		if len(out) >= maxEntries {
			return nil, fmt.Errorf("%s holds more than %d files", packAsset, maxEntries)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s holds %s twice", packAsset, name)
		}
		if h.Size > maxEntry {
			return nil, fmt.Errorf("%s in %s is over 1 MiB", name, packAsset)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxEntry+1))
		if err != nil {
			return nil, fmt.Errorf("%s is damaged: %w", packAsset, err)
		}
		if len(b) > maxEntry {
			return nil, fmt.Errorf("%s in %s is over 1 MiB", name, packAsset)
		}
		if total += len(b); total > maxTotal {
			return nil, fmt.Errorf("%s holds over 16 MiB of profiles", packAsset)
		}
		if _, err := parsePackFile(name, b); err != nil {
			return nil, fmt.Errorf("%s doesn't parse (it may need a newer rhythm-roadie): %w", name, err)
		}
		out[name] = b
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no profiles", packAsset)
	}
	return out, nil
}

// entryName checks one tar entry and returns the file name to store, ""
// for a folder entry on the allowed path.
func entryName(h *tar.Header) (string, error) {
	raw := h.Name
	clean := path.Clean(strings.TrimPrefix(raw, "./"))
	if raw == "" || strings.Contains(raw, "\\") || path.IsAbs(raw) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s holds %q, a path outside the pack", packAsset, raw)
	}
	dir, file := path.Split(clean)
	dir = strings.TrimSuffix(dir, "/")
	okDir := dir == "" || dir == "profile/builtin"
	switch h.Typeflag {
	case tar.TypeDir:
		if clean == "profile" || clean == "profile/builtin" || clean == "." {
			return "", nil
		}
		return "", fmt.Errorf("%s holds the folder %q; a pack holds only profiles", packAsset, raw)
	case tar.TypeReg:
	default:
		return "", fmt.Errorf("%s holds %q, which isn't a regular file (links aren't allowed)", packAsset, raw)
	}
	if !okDir {
		return "", fmt.Errorf("%s holds %q; a pack holds only profiles, with no folders", packAsset, raw)
	}
	if !strings.HasSuffix(file, ".yaml") || file == ".yaml" {
		return "", fmt.Errorf("%s holds %q, which isn't a profile (.yaml)", packAsset, raw)
	}
	return file, nil
}

// store writes the pack to <dir>/<version>/ through a temporary folder
// and removes older packs, so a crash never leaves half a pack in use.
func store(dir, version string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, ".pack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if err := cache.WriteFile(filepath.Join(tmp, n), files[n]); err != nil {
			return err
		}
	}
	final := filepath.Join(dir, version)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != version {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// githubSource reads releases through kit update and downloads assets
// over https.
type githubSource struct{ u *update.Updater }

func (g *githubSource) Latest(ctx context.Context) (*update.Release, error) { return g.u.Latest(ctx) }
func (g *githubSource) ByTag(ctx context.Context, v string) (*update.Release, error) {
	return g.u.ByTag(ctx, v)
}

// Open downloads an asset. Only https is used: the checksums are signed
// but the download should not be readable or changeable on the way.
func (g *githubSource) Open(ctx context.Context, rel *update.Release, asset string) (io.ReadCloser, int64, error) {
	raw := rel.Assets[asset]
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return nil, 0, fmt.Errorf("refusing to download %s from a URL that isn't https", asset)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "rhythm-update/rhythm-roadie")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}
