// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// The fixed values of the placeholders (manifest.Placeholders).
const (
	stagingURL    = "http://staging-wp"
	productionURL = "https://shop.example.com"
	confirmName   = "staging-wp"
	sshSourceHost = "ssh://roadie@127.0.0.1:18925/var/www/html"
)

// Inside a container the harness keeps everything under one folder.
const (
	ctrRoot = "/tmp/roadie"
	ctrBin  = ctrRoot + "/bin/rhythm-roadie"
	ctrIn   = ctrRoot + "/in"
	ctrWork = ctrRoot + "/work"
)

// RunEnv is where one case's Roadie runs: this machine or a container.
// Vars fill the command's placeholders as Roadie sees them; Local fills
// the same placeholders as the harness sees them (for the oracle).
type RunEnv interface {
	Name() string
	Vars() map[string]string
	// Prepare puts the binary and the seeded input in place.
	Prepare(ctx context.Context) error
	// Roadie runs the binary with args. sig, when not nil, is called
	// with a function that signals the running process.
	Roadie(ctx context.Context, args []string, env []string, sig func(signal func(os.Signal) error)) (*Run, error)
	// Collect copies what the run wrote under ${work} back to the local
	// work folder.
	Collect(ctx context.Context) error
	// Absent lists which of the globs exist where Roadie ran.
	Absent(ctx context.Context, globs []string) ([]string, error)
}

// hostEnv runs Roadie on this machine against the gate's ports. Its
// environment is built from scratch: HOME, XDG and RHYTHM folders under
// the work folder, no update check, offline, no color. The caller's own
// configuration and keys never leak in.
type hostEnv struct {
	bin, in, work string
	path          string // PATH, for ssh
}

func (h *hostEnv) Name() string { return manifest.RunHost }

func (h *hostEnv) Vars() map[string]string {
	return map[string]string{
		"in": h.in, "work": h.work, "site": filepath.Join(h.work, "site"),
		"staging_url": stagingURL, "production_url": productionURL, "confirm": confirmName,
		"ssh_source": sshSourceHost, "staging_dsn": "mysql://shop@127.0.0.1:18927/shop",
	}
}

func (h *hostEnv) Prepare(context.Context) error {
	return os.MkdirAll(filepath.Join(h.work, "tmp"), 0o700)
}

// isolatedEnv is the environment of every Roadie run, under home.
func isolatedEnv(home, path string, extra []string) []string {
	env := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"RHYTHM_CONFIG_DIR=" + filepath.Join(home, ".config", "rhythm"),
		"RHYTHM_CACHE_DIR=" + filepath.Join(home, ".cache", "rhythm"),
		"RHYTHM_NO_UPDATE_CHECK=1",
		"RHYTHM_OFFLINE=1",
		"NO_COLOR=1",
		"LANG=C.UTF-8",
		"PATH=" + path,
	}
	return append(env, extra...)
}

func (h *hostEnv) Roadie(ctx context.Context, args []string, env []string, sig func(func(os.Signal) error)) (*Run, error) {
	home := filepath.Join(h.work, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, h.bin, args...) // #nosec G204 -- the binary under test with the manifest's arguments
	cmd.Dir = h.work
	cmd.Env = isolatedEnv(home, h.path, append([]string{"TMPDIR=" + filepath.Join(h.work, "tmp")}, env...))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if sig != nil {
		sig(cmd.Process.Signal)
	}
	err := cmd.Wait()
	r := &Run{Args: args, Stdout: out.Bytes(), Stderr: errb.Bytes(), Wall: time.Since(start)}
	r.MaxRSSKB = maxRSSKB(cmd.ProcessState)
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.Exit = exitCode(ee)
	default:
		return r, err
	}
	return r, nil
}

func (h *hostEnv) Collect(context.Context) error { return nil }

func (h *hostEnv) Absent(_ context.Context, globs []string) ([]string, error) {
	var found []string
	for _, g := range globs {
		m, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		found = append(found, m...)
	}
	return found, nil
}

// containerEnv runs Roadie inside a testbed container (staging-wp or
// laravel) as user, on the sealed network: the binary, the seeded input
// and the work folder are copied in, the work folder copied back after.
type containerEnv struct {
	name    string // manifest.RunStagingWP or manifest.RunLaravel
	ctr     string
	user    string
	site    string
	bin     string // the binary on this machine (Linux)
	in      string // the seeded input on this machine
	work    string // the local work folder
	ex      Execer
	dsnHost string
}

func (c *containerEnv) Name() string { return c.name }

func (c *containerEnv) Vars() map[string]string {
	return map[string]string{
		"in": ctrIn, "work": ctrWork, "site": c.site,
		"staging_url": stagingURL, "production_url": productionURL, "confirm": confirmName,
		"ssh_source": "ssh://roadie@sshd/var/www/html", "staging_dsn": "mysql://shop@" + c.dsnHost + "/shop",
	}
}

func (c *containerEnv) Prepare(ctx context.Context) error {
	if _, err := c.ex.Exec(ctx, c.ctr, "root", nil, nil, "sh", "-c",
		"rm -rf "+ctrRoot+" && mkdir -p "+ctrRoot+"/bin "+ctrIn+" "+ctrWork+"/home "+ctrWork+"/tmp"); err != nil {
		return fmt.Errorf("%s: making %s: %w", c.ctr, ctrRoot, err)
	}
	if _, err := docker(ctx, nil, "cp", c.bin, c.ctr+":"+ctrBin); err != nil {
		return err
	}
	if c.in != "" {
		if _, err := docker(ctx, nil, "cp", c.in+"/.", c.ctr+":"+ctrIn); err != nil {
			return err
		}
	}
	if _, err := c.ex.Exec(ctx, c.ctr, "root", nil, nil, "sh", "-c",
		"chmod 755 "+ctrBin+" && chown -R "+c.user+" "+ctrRoot+" && chmod -R go-rwx "+ctrIn+" "+ctrWork); err != nil {
		return fmt.Errorf("%s: %w", c.ctr, err)
	}
	return nil
}

func (c *containerEnv) Roadie(ctx context.Context, args []string, env []string, sig func(func(os.Signal) error)) (*Run, error) {
	home := ctrWork + "/home"
	full := isolatedEnv(home, "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", append([]string{"TMPDIR=" + ctrWork + "/tmp"}, env...))
	argv := []string{"exec", "-u", c.user, "-w", ctrWork}
	for _, kv := range full {
		k, _, _ := strings.Cut(kv, "=")
		argv = append(argv, "-e", k)
	}
	argv = append(argv, c.ctr, ctrBin)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...) // #nosec G204 -- the binary under test with the manifest's arguments
	// The values reach docker through its own environment, never argv.
	cmd.Env = append(os.Environ(), full...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if sig != nil {
		// docker exec doesn't pass a signal on: send it to Roadie inside.
		sig(func(s os.Signal) error {
			name := "INT"
			if s == os.Kill {
				name = "KILL"
			}
			_, err := c.ex.Exec(context.Background(), c.ctr, "root", nil, nil, "pkill", "-"+name, "-f", ctrBin)
			return err
		})
	}
	err := cmd.Wait()
	r := &Run{Args: args, Stdout: out.Bytes(), Stderr: errb.Bytes(), Wall: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.Exit = exitCode(ee)
	default:
		return r, err
	}
	return r, nil
}

func (c *containerEnv) Collect(ctx context.Context) error {
	out, err := c.ex.Exec(ctx, c.ctr, "root", nil, nil, "tar", "-C", ctrWork, "-cf", "-", ".")
	if err != nil {
		return fmt.Errorf("%s: reading %s: %w", c.ctr, ctrWork, err)
	}
	_, err = untar(bytes.NewReader(out), c.work)
	return err
}

func (c *containerEnv) Absent(ctx context.Context, globs []string) ([]string, error) {
	return absentIn(ctx, c.ex, c.ctr, globs)
}

// absentIn lists which globs match in a container.
func absentIn(ctx context.Context, ex Execer, ctr string, globs []string) ([]string, error) {
	var found []string
	for _, g := range globs {
		out, err := ex.Exec(ctx, ctr, "root", nil, nil, "sh", "-c", "for f in "+g+"; do [ -e \"$f\" ] && echo \"$f\"; done; true")
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				found = append(found, ctr+":"+l)
			}
		}
	}
	return found, nil
}

// runnerEnv runs Roadie in a throwaway container with no network at all
// and the target folder on a small tmpfs (r20's disk-full case).
type runnerEnv struct {
	bin, in, work string
	tmpfs         int64 // bytes
}

func (r *runnerEnv) Name() string { return manifest.RunRunner }

func (r *runnerEnv) Vars() map[string]string {
	return map[string]string{
		"in": "/in", "work": "/out", "site": "/out/site",
		"staging_url": stagingURL, "production_url": productionURL, "confirm": confirmName,
		"ssh_source": sshSourceHost, "staging_dsn": "mysql://shop@127.0.0.1:18927/shop",
	}
}

func (r *runnerEnv) Prepare(context.Context) error {
	return os.MkdirAll(filepath.Join(r.work, "home"), 0o700)
}

func (r *runnerEnv) Roadie(ctx context.Context, args []string, env []string, _ func(func(os.Signal) error)) (*Run, error) {
	full := isolatedEnv("/work/home", "/usr/local/bin:/usr/bin:/bin", append([]string{"TMPDIR=/out"}, env...))
	argv := []string{"run", "--rm", "--network", "none", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--tmpfs", fmt.Sprintf("/out:size=%d,mode=1777", r.tmpfs),
		"-v", r.bin + ":/usr/local/bin/rhythm-roadie:ro", "-v", r.in + ":/in:ro", "-v", r.work + ":/work", "-w", "/work"}
	for _, kv := range full {
		k, _, _ := strings.Cut(kv, "=")
		argv = append(argv, "-e", k)
	}
	argv = append(argv, "roadie-testbed-runner:1", "rhythm-roadie")
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...) // #nosec G204 -- the binary under test with the manifest's arguments
	cmd.Env = append(os.Environ(), full...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	run := &Run{Args: args, Stdout: out.Bytes(), Stderr: errb.Bytes(), Wall: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		run.Exit = exitCode(ee)
	default:
		return run, err
	}
	return run, nil
}

func (r *runnerEnv) Collect(context.Context) error { return nil }

func (r *runnerEnv) Absent(context.Context, []string) ([]string, error) { return nil, nil }

// untar extracts a tar stream into dir. Names that climb out of dir and
// links are skipped: the stream comes from a container Roadie wrote to.
// It returns the links it skipped, by name, so a caller judging the tree
// can say what went unread.
func untar(r io.Reader, dir string) (links []string, err error) {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return links, nil
		}
		if err != nil {
			return links, err
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			continue
		}
		dst := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			links = append(links, filepath.ToSlash(name))
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return links, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return links, err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- dst is inside dir (checked above)
			if err != nil {
				return links, err
			}
			_, err = io.Copy(f, io.LimitReader(tr, 16<<30))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return links, err
			}
		}
	}
}
