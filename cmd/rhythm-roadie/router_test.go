// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// The router (rhythm 0.9.0) reads two answers from every tool built on
// kit/cli: version --json (rhythm list, update and doctor) and update
// --json (rhythm doctor's data check and rhythm lock). The fixtures in
// testdata/router are what the router's own test tool prints
// (rhythm/cmd/rhythm/testdata/kittool), so Roadie's answers must have the
// same fields with the same JSON types.

// fieldTypes maps each field of a JSON object to its JSON type.
func fieldTypes(t *testing.T, b []byte) map[string]string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, b)
	}
	types := map[string]string{}
	for k, v := range m {
		types[k] = reflect.TypeOf(v).String()
	}
	return types
}

// fixture reads a router fixture.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "router", name)) // #nosec G304 -- the package's own fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// buildRoadie builds rhythm-roadie at version into a temporary folder:
// the binary the router runs, not the test binary.
func buildRoadie(t *testing.T, version string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "rhythm-roadie")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go on PATH to build with: %v", err)
	}
	cmd := exec.Command(gobin, "build", "-ldflags", "-X main.version="+version, "-o", bin, ".") // #nosec G204 -- the Go toolchain on this package
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// runBuilt runs the built binary the way the router's probes do: no
// terminal, offline, in folders of its own.
func runBuilt(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) // #nosec G204 -- the binary this test built
	cmd.Env = append(os.Environ(), "RHYTHM_OFFLINE=1", "CI=1", "RHYTHM_CONFIG_DIR="+t.TempDir(), "RHYTHM_CACHE_DIR="+t.TempDir(),
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(), "APPDATA="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return stdout.String(), stderr.String(), ee.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), 0
}

// semver is what the router's versionFromJSON and semverToken accept.
var semver = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func TestRouterReadsTheBinary(t *testing.T) {
	bin := buildRoadie(t, "1.0.0")

	// version --json: rhythm-version/v1 with a kit version, which tells
	// the router the tool runs on kit/cli and so has update --json.
	out, errOut, code := runBuilt(t, bin, "version", "--json")
	if code != 0 {
		t.Fatalf("version --json: exit %d: %s", code, errOut)
	}
	if got, want := fieldTypes(t, []byte(out)), fieldTypes(t, fixture(t, "version.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("version --json fields %v, want the router's %v", got, want)
	}
	var v struct {
		Schema     string `json:"schema"`
		Name       string `json:"name"`
		Version    string `json:"version"`
		KitVersion string `json:"kit_version"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.Schema != "rhythm-version/v1" || v.Name != "rhythm-roadie" || v.Version != "1.0.0" || !semver.MatchString(v.Version) || v.KitVersion == "" {
		t.Errorf("version --json = %s", out)
	}

	// --version: the router's fallback reads the second word.
	if out, _, code := runBuilt(t, bin, "--version"); code != 0 || strings.Fields(out)[1] != "1.0.0" {
		t.Errorf("--version = %q, exit %d", out, code)
	}

	// rhythm help roadie runs rhythm-roadie --help: the top help on
	// stdout, exit 0.
	out, errOut, code = runBuilt(t, bin, "--help")
	if code != 0 || errOut != "" || !strings.HasPrefix(out, "♪ rhythm roadie  1.0.0\n") || !strings.Contains(out, "rhythm-roadie copy --from <source> --to <target>") {
		t.Errorf("--help: exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}

	// update --json --offline: rhythm-data/v1, one entry per pack, with
	// the fields rhythm doctor and rhythm lock read.
	if _, _, err := profile.Load(context.Background(), true); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("update --json --offline needs the engine lane")
	}
	out, errOut, code = runBuilt(t, bin, "update", "--json", "--offline")
	if code != 0 {
		t.Fatalf("update --json --offline: exit %d: %s", code, errOut)
	}
	checkDataReport(t, []byte(out))
}

// checkDataReport compares an update --json answer with the router's
// fixture, field by field and pack by pack.
func checkDataReport(t *testing.T, out []byte) {
	t.Helper()
	if got, want := fieldTypes(t, out), fieldTypes(t, fixture(t, "update.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("update --json fields %v, want the router's %v", got, want)
	}
	var rep, ref struct {
		Schema string           `json:"schema"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, "update.json"), &ref); err != nil {
		t.Fatal(err)
	}
	if rep.Schema != "rhythm-data/v1" || len(rep.Data) != 1 {
		t.Fatalf("update --json = %s", out)
	}
	for k, v := range ref.Data[0] {
		if reflect.TypeOf(rep.Data[0][k]) != reflect.TypeOf(v) {
			t.Errorf("pack field %q is %v, want %T", k, rep.Data[0][k], v)
		}
	}
	if rep.Data[0]["name"] != profilesPack || rep.Data[0]["version"] == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(rep.Data[0]["sha256"].(string)) {
		t.Errorf("pack = %v", rep.Data[0])
	}
}

// The offline update in process, with a pack of fake profiles.
func TestUpdateOffline(t *testing.T) {
	testDeps(t, func(d *deps) {
		d.loadProfiles = func(_ context.Context, offline bool) ([]profile.Def, string, error) {
			if !offline {
				t.Error("update --offline loaded the profiles online")
			}
			return fakeProfiles(), "2026.10.26", nil
		}
	})
	var stdout, stderr bytes.Buffer
	if code := entry([]string{"update", "--json", "--offline"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	checkDataReport(t, stdout.Bytes())
	first := stdout.String()

	// RHYTHM_OFFLINE does the same; the digest stays with the pack.
	dep.env = func(n string) (string, bool) {
		return map[string]string{"RHYTHM_OFFLINE": "1"}[n], n == "RHYTHM_OFFLINE"
	}
	stdout.Reset()
	if code := entry([]string{"update", "--json"}, &stdout, &stderr); code != 0 || stdout.String() != first {
		t.Errorf("RHYTHM_OFFLINE=1: exit %d, %s want %s", code, stdout.String(), first)
	}
	stdout.Reset()
	stderr.Reset()
	if code := entry([]string{"update"}, &stdout, &stderr); code != 0 || stdout.String() != "profiles 2026.10.26\n" ||
		!strings.Contains(stderr.String(), "Offline: nothing was downloaded.") {
		t.Errorf("text: exit %d %q %q", code, stdout.String(), stderr.String())
	}

	// Negative: a pin needs a download, a stray argument is a usage
	// error, a pack that can't load couldn't run.
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"update", "--offline", "--pin", "profiles=2026.10.20"}, 2, "--pin downloads the pinned pack, so it can't run offline"},
		{[]string{"update", "--offline", "now"}, 2, `unexpected argument "now"`},
		{[]string{"update", "--offline", "--nope"}, 2, "flag provided but not defined"},
	} {
		stderr.Reset()
		if code := entry(c.args, &stdout, &stderr); code != c.code || !strings.Contains(stderr.String(), c.msg) {
			t.Errorf("%q: exit %d %q, want %d with %q", c.args, code, stderr.String(), c.code, c.msg)
		}
	}
	dep.loadProfiles = func(context.Context, bool) ([]profile.Def, string, error) {
		return nil, "", errors.New("pack unreadable")
	}
	stderr.Reset()
	if code := entry([]string{"update", "--offline"}, &stdout, &stderr); code != 3 || !strings.Contains(stderr.String(), "update failed: pack unreadable") {
		t.Errorf("unreadable pack: exit %d %q", code, stderr.String())
	}

	// RHYTHM_OFFLINE=0 is online: kit's update runs (here the stub pack
	// fails, which is still not the offline path).
	dep.env = func(n string) (string, bool) {
		return map[string]string{"RHYTHM_OFFLINE": "0"}[n], n == "RHYTHM_OFFLINE"
	}
	if offlineUpdate([]string{"--json"}) {
		t.Error("RHYTHM_OFFLINE=0 counted as offline")
	}
	if !offlineUpdate([]string{"--json", "--offline"}) {
		t.Error("--offline didn't count")
	}
}

// The next-steps line names the other tools by their router commands
// (PRD 16.1), so a user copies it as it is.
func TestNextStepsNameTheSet(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", "screen", "copy-pass.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "next: rhythm echo up, then rhythm soundcheck run https://staging.shop.test\n") {
		t.Errorf("copy-pass.txt lacks the next-steps line naming rhythm echo and rhythm soundcheck")
	}
}
