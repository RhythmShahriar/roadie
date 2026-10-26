// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// Services the setup steps touch.
var (
	ctrStagingWP = container(stagingHost)
	ctrStagingDB = container("staging-db")
	ctrProdDB    = container("prod-db")
	ctrLaravel   = container("laravel")
	ctrLaravelDB = container("laravel-db")
	ctrSSHD      = container("sshd")
)

// The staging docroot and the fresh-install copies the reset restores.
const (
	docroot         = "/var/www/html"
	pristineDocroot = "/tmp/testbed-pristine-docroot.tar"
)

// sqlIn runs SQL from stdin (or the client with args) in a database
// container as its application user; passwords come from the container's
// own environment.
func (h *Harness) sqlIn(ctx context.Context, ctr string, stdin io.Reader, root bool, args ...string) ([]byte, error) {
	var client string
	switch ctr {
	case ctrStagingDB:
		client = `MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb -ushop`
		if root {
			client = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot`
		}
	case ctrProdDB:
		client = `MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -ushop`
		if root {
			client = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot`
		}
	case ctrLaravelDB:
		client = `MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -ularavel`
		if root {
			client = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot`
		}
	default:
		return nil, fmt.Errorf("no SQL client for %s", ctr)
	}
	for _, a := range args {
		client += " " + shellQuote(a)
	}
	return h.Ex.Exec(ctx, ctr, "", nil, stdin, "sh", "-c", client)
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// loadInto loads a dump file into a database.
func (h *Harness) loadInto(ctx context.Context, ctr, db, dump string) error {
	f, err := os.Open(dump) // #nosec G304 -- a seeded fixture
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := h.sqlIn(ctx, ctr, f, false, db); err != nil {
		return fmt.Errorf("loading %s into %s: %w", filepath.Base(dump), ctr, errDetail(err))
	}
	return nil
}

// errDetail adds a container command's stderr to its error. Database
// clients don't print passwords, so this is safe to log.
func errDetail(err error) error {
	var ee *ExitError
	if errors.As(err, &ee) && ee.Stderr != "" {
		return fmt.Errorf("%w: %s", err, firstLine(ee.Stderr))
	}
	return err
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// dumpDB dumps a database with the real dump tool in its container, with
// no dump date, so the same data gives the same file.
func (h *Harness) dumpDB(ctx context.Context, service, out string) error {
	var args []string
	ctr := container(service)
	switch service {
	case manifest.OutStagingDB:
		args = []string{"sh", "-c", `MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb-dump -ushop --single-transaction --skip-dump-date shop`}
	case manifest.OutLaravelDB:
		args = []string{"sh", "-c", `MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump -ularavel --single-transaction --no-tablespaces --skip-dump-date laravel`}
	case manifest.OutPG:
		args = []string{"pg_dump", "-U", "app", "--no-owner", "--no-privileges", "app"}
	default:
		return fmt.Errorf("no dump for service %s", service)
	}
	b, err := h.Ex.Exec(ctx, ctr, "", nil, nil, args...)
	if err != nil {
		return fmt.Errorf("dumping %s: %w", service, errDetail(err))
	}
	return os.WriteFile(out, b, 0o600)
}

// setupScenario runs the scenario's setup steps once.
func (h *Harness) setupScenario(ctx context.Context, st *scenarioState) error {
	for _, step := range st.s.Setup {
		if err := h.setupStep(ctx, st, step, st.work); err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}
	return nil
}

// teardown undoes what setup started for the whole scenario.
func (h *Harness) teardown(st *scenarioState) {
	if slices.Contains(st.s.Setup, manifest.SetupEchoDown) {
		_, _ = docker(context.Background(), nil, "start", container("echo"))
		// Wait until Echo answers again, so the next scenario's window
		// can clear it.
		e := Echo{Base: gateEcho, Token: h.echoTok}
		for i := 0; i < 60; i++ {
			if _, err := e.Messages(context.Background()); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (h *Harness) setupStep(ctx context.Context, st *scenarioState, step, work string) error {
	switch step {
	case manifest.SetupStagingSite:
		return h.resetStaging(ctx, st)
	case manifest.SetupStagingDB:
		return h.loadInto(ctx, ctrStagingDB, "shop", filepath.Join(st.in, "db.sql"))
	case manifest.SetupProdDB:
		return h.loadInto(ctx, ctrProdDB, "shop", filepath.Join(st.in, "db.sql"))
	case manifest.SetupLaravelDB:
		return h.loadInto(ctx, ctrLaravelDB, "laravel", filepath.Join(st.in, "db.sql"))
	case manifest.SetupSiteDir:
		return h.siteDir(st, work)
	case manifest.SetupLaravelSite:
		return nil // after the run environment is prepared (prepareCase)
	case manifest.SetupConfig, manifest.SetupProductionConfig:
		return writeConfig(work, step == manifest.SetupProductionConfig)
	case manifest.SetupHostCron:
		st.cron = true
	case manifest.SetupTraffic:
		st.traffic = true
	case manifest.SetupEchoDown:
		_, err := docker(ctx, nil, "stop", container("echo"))
		return err
	case manifest.SetupEgressPlugin:
		return h.overlay(ctx, ctrStagingWP, docroot, filepath.Join(h.Runtime, "fixtures", "r23", "files"), h.dbValues(true, false))
	case manifest.SetupSnapshot:
		s, err := h.snapshot(ctx)
		st.snapshot = s
		return err
	case manifest.SetupReadOnlyUser:
		return h.readOnlyUser(ctx)
	case manifest.SetupEnvProduction:
		_, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, nil, "sed", "-i",
			`s/define( *'WP_ENVIRONMENT_TYPE', *'staging' *);/define( 'WP_ENVIRONMENT_TYPE', 'production' );/`, docroot+"/wp-config.php")
		return err
	case manifest.SetupUnknownHost:
		return nil // sshFiles leaves known_hosts empty
	default:
		return fmt.Errorf("unknown setup step %q", step)
	}
	return nil
}

// resetStaging gives staging its fresh install back: the docroot from
// the tar taken the first time, the database from the dump taken the
// first time, then the fixture's files and a plain wp-config.php.
func (h *Harness) resetStaging(ctx context.Context, st *scenarioState) error {
	if _, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, nil, "sh", "-c",
		"[ -f "+pristineDocroot+" ] || tar -C "+docroot+" -cf "+pristineDocroot+" ."); err != nil {
		return err
	}
	if _, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, nil, "sh", "-c",
		"find "+docroot+" -mindepth 1 -delete && tar -C "+docroot+" -xf "+pristineDocroot); err != nil {
		return fmt.Errorf("restoring the docroot: %w", errDetail(err))
	}
	pristine := filepath.Join(h.Runtime, "pristine-staging.sql")
	if _, err := os.Stat(pristine); err != nil {
		if err := h.dumpDB(ctx, manifest.OutStagingDB, pristine); err != nil {
			return err
		}
	}
	if _, err := h.sqlIn(ctx, ctrStagingDB, strings.NewReader("DROP DATABASE IF EXISTS shop; CREATE DATABASE shop; GRANT ALL ON shop.* TO shop;"), true); err != nil {
		return fmt.Errorf("emptying staging-db: %w", errDetail(err))
	}
	if err := h.loadInto(ctx, ctrStagingDB, "shop", pristine); err != nil {
		return err
	}
	vals := h.dbValues(true, false)
	files := filepath.Join(st.in, "files")
	if _, err := os.Stat(filepath.Join(files, "wp-config.php")); err != nil {
		cfg := stagingConfig(vals, st.s.Multisite)
		if _, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, strings.NewReader(cfg), "sh", "-c",
			"umask 027; cat > "+docroot+"/wp-config.php"); err != nil {
			return err
		}
	}
	if err := h.overlay(ctx, ctrStagingWP, docroot, files, vals); err != nil {
		return err
	}
	_, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, nil, "chown", "-R", "www-data:www-data", docroot)
	return err
}

// dbValues are the @@DB_*@@ placeholder values of the fixture files: the
// staging database (or Laravel's) as the run sees it, on the sealed
// network or through the gate.
func (h *Harness) dbValues(sealed, laravel bool) map[string]string {
	v := map[string]string{"@@DB_NAME@@": "shop", "@@DB_USER@@": "shop", "@@DB_PASSWORD@@": h.db["MYSQL_PASSWORD"]}
	switch {
	case laravel && sealed:
		v["@@DB_NAME@@"], v["@@DB_USER@@"], v["@@DB_HOST@@"], v["@@DB_PORT@@"] = "laravel", "laravel", "laravel-db", "3306"
	case laravel:
		v["@@DB_NAME@@"], v["@@DB_USER@@"], v["@@DB_HOST@@"], v["@@DB_PORT@@"] = "laravel", "laravel", "127.0.0.1", "18928"
	case sealed:
		v["@@DB_HOST@@"], v["@@DB_PORT@@"] = "staging-db", "3306"
	default:
		v["@@DB_HOST@@"], v["@@DB_PORT@@"] = "127.0.0.1:18927", "18927"
	}
	return v
}

// fill replaces the @@DB_*@@ placeholders.
func fill(b []byte, vals map[string]string) []byte {
	for k, v := range vals {
		b = bytes.ReplaceAll(b, []byte(k), []byte(v))
	}
	return b
}

// stagingConfig is the plain wp-config.php the harness gives a staging
// site whose fixture has none: literal defines a text parser can read,
// WP_ENVIRONMENT_TYPE staging, testbed salts (not secrets).
func stagingConfig(vals map[string]string, multisite string) string {
	var b strings.Builder
	b.WriteString("<?php\n// Written by the Roadie testbed harness for staging-wp.\n")
	for _, k := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"} {
		fmt.Fprintf(&b, "define( '%s', '%s' );\n", k, vals["@@"+k+"@@"])
	}
	b.WriteString("define( 'DB_CHARSET', 'utf8mb4' );\ndefine( 'DB_COLLATE', '' );\n")
	for _, k := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		fmt.Fprintf(&b, "define( '%s', 'testbed-staging-%s' );\n", k, strings.ToLower(k))
	}
	b.WriteString("define( 'WP_ENVIRONMENT_TYPE', 'staging' );\n")
	if multisite != "" {
		fmt.Fprintf(&b, "define( 'WP_ALLOW_MULTISITE', true );\ndefine( 'MULTISITE', true );\ndefine( 'SUBDOMAIN_INSTALL', %v );\n", multisite == "subdomain")
		b.WriteString("define( 'DOMAIN_CURRENT_SITE', 'staging-wp' );\ndefine( 'PATH_CURRENT_SITE', '/' );\ndefine( 'SITE_ID_CURRENT_SITE', 1 );\ndefine( 'BLOG_ID_CURRENT_SITE', 1 );\n")
	}
	b.WriteString("$table_prefix = 'wp_';\nif ( ! defined( 'ABSPATH' ) ) {\n\tdefine( 'ABSPATH', __DIR__ . '/' );\n}\nrequire_once ABSPATH . 'wp-settings.php';\n")
	return b.String()
}

// overlay copies a seeded files/ tree into dir in a container, with the
// @@DB_*@@ placeholders filled. The tar goes over stdin, so the database
// password never reaches a command line.
func (h *Harness) overlay(ctx context.Context, ctr, dir, files string, vals map[string]string) error {
	if _, err := os.Stat(files); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(files, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(files, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path) // #nosec G304 G122 -- the testbed's own seeded files
		if err != nil {
			return err
		}
		b = fill(b, vals)
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o640, Size: int64(len(b)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	// The site's files belong to the user the web server runs as.
	owner := "www-data:www-data"
	if ctr != ctrStagingWP {
		owner = "root:root"
	}
	if _, err := h.Ex.Exec(ctx, ctr, "root", nil, &buf, "sh", "-c", "mkdir -p "+shellQuote(dir)+" && tar -C "+shellQuote(dir)+" --no-same-owner -xf - && chown -R "+owner+" "+shellQuote(dir)); err != nil {
		return fmt.Errorf("copying files into %s: %w", ctr, errDetail(err))
	}
	return nil
}

// siteDir renders the fixture's files into work/site for a run on this
// machine: the database through the gate's ports.
func (h *Harness) siteDir(st *scenarioState, work string) error {
	laravel := st.s.Stack == "laravel"
	vals := h.dbValues(false, laravel)
	site := filepath.Join(work, "site")
	if err := os.MkdirAll(site, 0o700); err != nil {
		return err
	}
	files := filepath.Join(st.in, "files")
	err := filepath.WalkDir(files, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == files {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(files, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path) // #nosec G304 G122 -- the testbed's own seeded files
		if err != nil {
			return err
		}
		dst := filepath.Join(site, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dst, fill(b, vals), 0o600) // #nosec G703 -- dst is the seeded tree's own path under work/site
	})
	if err != nil {
		return err
	}
	if !laravel {
		if _, err := os.Stat(filepath.Join(site, "wp-config.php")); errors.Is(err, fs.ErrNotExist) {
			return os.WriteFile(filepath.Join(site, "wp-config.php"), []byte(stagingConfig(vals, st.s.Multisite)), 0o600)
		}
	}
	return nil
}

// writeConfig writes work/roadie.yaml: the staging URL and production;
// for r25 production also lists the staging host, which Roadie must
// refuse to write to.
func writeConfig(work string, stagingIsProduction bool) error {
	prod := `["shop.example.com"]`
	if stagingIsProduction {
		prod = `["shop.example.com", "staging-wp"]`
	}
	cfg := "# Written by the Roadie testbed harness.\nurl: " + stagingURL + "\nproduction: " + prod + "\n"
	return os.WriteFile(filepath.Join(work, "roadie.yaml"), []byte(cfg), 0o600)
}

// snapshot hashes the staging database (a dump without its date) and the
// docroot's files outside WordPress core and the plugins.
func (h *Harness) snapshot(ctx context.Context) (string, error) {
	db, err := h.Ex.Exec(ctx, ctrStagingDB, "", nil, nil, "sh", "-c",
		`MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb-dump -ushop --single-transaction --skip-dump-date shop`)
	if err != nil {
		return "", fmt.Errorf("snapshot of staging-db: %w", errDetail(err))
	}
	files, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, nil, "sh", "-c",
		"cd "+docroot+" && find . -type f ! -path './wp-includes/*' ! -path './wp-admin/*' ! -path './wp-content/plugins/*' -print0 | sort -z | xargs -0 sha256sum")
	if err != nil {
		return "", fmt.Errorf("snapshot of the docroot: %w", errDetail(err))
	}
	sum := sha256.New()
	sum.Write(db)
	sum.Write(files)
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// readOnlyUser points staging's wp-config.php at a user with SELECT only
// (r27), with a new random password written into the file only.
func (h *Harness) readOnlyUser(ctx context.Context) error {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	pw := hex.EncodeToString(raw)
	sql := "DROP USER IF EXISTS 'reader'@'%'; CREATE USER 'reader'@'%' IDENTIFIED BY '" + pw + "'; GRANT SELECT ON shop.* TO 'reader'@'%';"
	if _, err := h.sqlIn(ctx, ctrStagingDB, strings.NewReader(sql), true); err != nil {
		return fmt.Errorf("making the read-only user: %w", errDetail(err))
	}
	vals := h.dbValues(true, false)
	vals["@@DB_USER@@"], vals["@@DB_PASSWORD@@"] = "reader", pw
	cfg := stagingConfig(vals, "")
	_, err := h.Ex.Exec(ctx, ctrStagingWP, "root", nil, strings.NewReader(cfg), "sh", "-c",
		"umask 027; cat > "+docroot+"/wp-config.php && chown www-data:www-data "+docroot+"/wp-config.php")
	return err
}

// sshFiles gives a host run its own ssh setup: a wrapper first on PATH
// that runs the real ssh with a config of its own (the client key and
// known_hosts from runtime/, strict host key checking, batch mode), so
// the operator's ~/.ssh is never read. unknown leaves known_hosts empty.
func (h *Harness) sshFiles(work string, unknown bool) error {
	if h.sshPath == "" {
		return errors.New("ssh isn't on PATH")
	}
	dir := filepath.Join(work, "home", ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(h.Runtime, "ssh", "id_ed25519")) // #nosec G304 -- the testbed's own client key
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), key, 0o600); err != nil { // #nosec G703 -- the harness's own work folder
		return err
	}
	var known []byte
	if !unknown {
		if known, err = os.ReadFile(filepath.Join(h.Runtime, "known_hosts")); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), known, 0o600); err != nil { // #nosec G703 -- the harness's own work folder
		return err
	}
	cfg := fmt.Sprintf("Host *\n  IdentityFile %s\n  IdentitiesOnly yes\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking yes\n  BatchMode yes\n",
		filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "known_hosts"))
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		return err
	}
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	wrapper := fmt.Sprintf("#!/bin/sh\n# The testbed's ssh: its own config, never ~/.ssh.\nexec %s -F %s \"$@\"\n", shellQuote(h.sshPath), shellQuote(filepath.Join(dir, "config")))
	return os.WriteFile(filepath.Join(bin, "ssh"), []byte(wrapper), 0o700) // #nosec G306 -- a script must be executable
}

// bombLines is how many 1 KiB lines the gzip bomb holds: 64 MiB that
// gzip to about 64 KiB, far past the 20x Roadie allows by default.
const bombLines = 64 * 1024

// makeFile writes a file a case needs that no template can hold: a gzip
// bomb (r24) or an All-in-One WP Migration archive named .sql.
func makeFile(in, kind string) error {
	switch kind {
	case manifest.MakeGzipBomb:
		f, err := os.OpenFile(filepath.Join(in, "bomb.sql.gz"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- the seeded input folder
		if err != nil {
			return err
		}
		zw, _ := gzip.NewWriterLevel(f, gzip.DefaultCompression)
		_, err = io.WriteString(zw, "-- MySQL dump 10.13  Distrib 8.4.3, for Linux (x86_64)\n")
		chunk := bytes.Repeat([]byte("-- "+strings.Repeat("0", 1021)+"\n"), 1024) // 1 MiB
		for i := 0; err == nil && i < bombLines/1024; i++ {
			_, err = zw.Write(chunk)
		}
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	case manifest.MakeWpress:
		return os.WriteFile(filepath.Join(in, "archive.sql"), wpressArchive(), 0o600)
	}
	return fmt.Errorf("unknown make %q", kind)
}

// wpressArchive is a minimal .wpress: one 4,377-byte header (name 255,
// size 14, mtime 12, path 4,096, NUL padded) and the file, then an empty
// header as the end.
func wpressArchive() []byte {
	body := []byte("-- not really a dump\n")
	field := func(s string, n int) []byte {
		b := make([]byte, n)
		copy(b, s)
		return b
	}
	var b bytes.Buffer
	b.Write(field("database.sql", 255))
	b.Write(field(fmt.Sprint(len(body)), 14))
	b.Write(field("1790000000", 12))
	b.Write(field(".", 4096))
	b.Write(body)
	b.Write(make([]byte, 4377))
	return b.Bytes()
}
