// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package exposure

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"golang.org/x/crypto/bcrypt"
)

// BasicAuthDir is where the basic auth files go, relative to the folder
// Roadie runs in. Roadie never edits server config: the user copies the
// lines into place.
const BasicAuthDir = ".roadie/basic-auth"

// PasswdFile is the password file path the snippets name. It is outside
// any web root on purpose: a .htpasswd inside one is served by nginx and
// Caddy to anyone who asks.
const PasswdFile = "/etc/roadie/staging.htpasswd" // #nosec G101 -- a path, not a credential

// The files BasicAuth returns.
const (
	FileApache = "apache.htaccess"
	FilePasswd = ".htpasswd"
	FileNginx  = "nginx.conf"
	FileCaddy  = "Caddyfile"
)

const (
	passwordLen = 20
	passwordABC = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	// bcryptRounds is htpasswd -B's default. The servers check the hash on
	// every request (each image of a page); a 20-character random password
	// needs no slow hash to resist guessing.
	bcryptRounds = 5
)

// newPassword and hashPassword are swapped in tests for golden files.
var (
	newPassword  = randomPassword
	hashPassword = func(pw string) (string, error) {
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptRounds)
		return string(h), err
	}
)

// randomPassword returns passwordLen letters and digits from crypto/rand,
// each drawn evenly from the 62 (about 119 bits in all).
func randomPassword() (string, error) {
	b := make([]byte, passwordLen)
	n := big.NewInt(int64(len(passwordABC)))
	for i := range b {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", fmt.Errorf("basic-auth: no random password: %w", err)
		}
		b[i] = passwordABC[k.Int64()]
	}
	return string(b), nil
}

// validUser reports a user name every server reads the same way: no ':'
// (the htpasswd separator), no space, quote, brace or control character.
func validUser(u string) bool {
	if u == "" || len(u) > 64 {
		return false
	}
	for _, c := range []byte(u) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte(":\"'`\\{}#;$", c) >= 0 {
			return false
		}
	}
	return true
}

func basicAuth(user, passwdFile string) (map[string][]byte, string, error) {
	if !validUser(user) {
		return nil, "", cli.Usagef("basic-auth: user %q can't be used: give 1 to 64 letters, digits or . _ - @ (no ':', spaces or quotes)", user)
	}
	pw, err := newPassword()
	if err != nil {
		return nil, "", err
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return nil, "", fmt.Errorf("basic-auth: %w", err)
	}
	// Apache and the C crypt() nginx uses read bcrypt as $2y$; Go's $2a$
	// is the same algorithm. Caddy checks with Go's bcrypt, which reads both.
	apr := "$2y$" + strings.TrimPrefix(hash, "$2a$")
	files := map[string][]byte{
		FilePasswd: []byte(user + ":" + apr + "\n"),
		FileApache: []byte(`# Roadie basic auth for a staging copy (Apache 2.4).
# Copy .htpasswd to ` + passwdFile + `
# (outside the web root), then put these lines at the top of the site's
# .htaccess or in its <Directory> block.
# Loopback requests pass without a password, so WP-Cron spawns and Site
# Health keep working; add the server's own address to "Require ip" when
# they go out through its public IP.
# Behind a reverse proxy on this host every request is loopback: remove
# the loopback allow lines there.
AuthType Basic
AuthName "Staging copy"
AuthUserFile ` + passwdFile + `
<RequireAny>
    Require ip 127.0.0.1 ::1
    Require valid-user
</RequireAny>
`),
		FileNginx: []byte(`# Roadie basic auth for a staging copy (nginx).
# Copy .htpasswd to ` + passwdFile + `
# (outside the web root), then put these lines in the site's server block
# and reload nginx.
# Loopback requests pass without a password (satisfy any), so WP-Cron
# spawns and Site Health keep working.
# Behind a reverse proxy on this host every request is loopback: remove
# the loopback allow lines there.
satisfy any;
allow 127.0.0.1;
allow ::1;
deny all;
auth_basic "Staging copy";
auth_basic_user_file ` + passwdFile + `;
`),
		FileCaddy: []byte(`# Roadie basic auth for a staging copy (Caddy 2).
# Put these lines in the site's block and reload Caddy. The hash is in
# the file itself; Caddy needs no .htpasswd.
# Loopback requests pass without a password, so WP-Cron spawns and Site
# Health keep working.
# Behind a reverse proxy on this host every request is loopback: remove
# the loopback allow lines there.
@notLocal not remote_ip 127.0.0.1 ::1
basicauth @notLocal {
	` + user + ` ` + hash + `
}
`),
	}
	return files, pw, nil
}

// WriteBasicAuth writes files under dir/BasicAuthDir: the folder 0700 and
// each file 0600, since .htpasswd holds a password hash. It returns the
// folder it wrote.
func WriteBasicAuth(dir string, files map[string][]byte) (string, error) {
	out := filepath.Join(dir, filepath.FromSlash(BasicAuthDir))
	if err := os.MkdirAll(out, 0o700); err != nil {
		return "", fmt.Errorf("basic-auth: %w", err)
	}
	if err := os.Chmod(out, 0o700); err != nil { // #nosec G302 -- a folder needs its x bit; files are 0600
		return "", fmt.Errorf("basic-auth: %w", err)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if n != filepath.Base(n) || n == "." || n == ".." {
			return "", fmt.Errorf("basic-auth: %q is not a file name", n)
		}
		if err := os.WriteFile(filepath.Join(out, n), files[n], 0o600); err != nil {
			return "", fmt.Errorf("basic-auth: %w", err)
		}
		// WriteFile keeps the mode of a file from an earlier run.
		if err := os.Chmod(filepath.Join(out, n), 0o600); err != nil {
			return "", fmt.Errorf("basic-auth: %w", err)
		}
	}
	return out, nil
}
