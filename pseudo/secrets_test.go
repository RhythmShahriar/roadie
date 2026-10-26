// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bufio"
	"bytes"
	"crypto/md5" // #nosec G501 -- the reference phpass the hashes are checked with
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// refCheck is phpass CheckPassword written apart from secrets.go, so a
// shared mistake can't hide: crypt_private and its encode64 read as 24-bit
// little-endian groups.
func refCheck(password, stored string) bool {
	if len(stored) != 34 || (stored[:3] != "$P$" && stored[:3] != "$H$") {
		return false
	}
	log2 := strings.IndexByte(itoa64, stored[3])
	if log2 < 7 || log2 > 30 {
		return false
	}
	salt := stored[4:12]
	sum := md5.Sum([]byte(salt + password)) // #nosec G401 -- phpass
	for range 1 << log2 {
		sum = md5.Sum(append(sum[:], password...)) // #nosec G401 -- phpass
	}
	var enc strings.Builder
	for i := 0; i < 16; i += 3 {
		var v uint32
		n := min(3, 16-i)
		for j := range n {
			v |= uint32(sum[i+j]) << (8 * j)
		}
		for j := 0; j < n+1; j++ {
			enc.WriteByte(itoa64[(v>>(6*j))&0x3f])
		}
	}
	return stored[12:] == enc.String()
}

// The vector PHP's crypt_private gives for "correct horse" and the
// setting $P$B12345678 (php -n testdata/check_password.php, mode make).
func TestPhpassVector(t *testing.T) {
	const want = "$P$B1234567841hqyZ5rENHR9W.8Ac4Pi0"
	got := phpassHash([]byte("correct horse"), []byte("12345678"), phpassLog2)
	if string(got) != want {
		t.Fatalf("phpassHash = %s, want %s", got, want)
	}
	if !refCheck("correct horse", want) || refCheck("correct horsf", want) {
		t.Fatal("reference phpass disagrees with the PHP vector")
	}
}

func TestPhpassEncodeLengths(t *testing.T) {
	// encode64 writes ceil(n*8/6) characters for n bytes.
	for n, want := range map[int]int{1: 2, 2: 3, 3: 4, 6: 8, 16: 22} {
		if got := len(phpassEncode(make([]byte, n))); got != want {
			t.Errorf("phpassEncode(%d bytes) has %d characters, want %d", n, got, want)
		}
	}
}

func TestPasswordHashWordPress(t *testing.T) {
	h, err := PasswordHash(core.StackWordPress, []byte("correct horse"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(h)
	if len(s) != 34 || !strings.HasPrefix(s, "$P$B") {
		t.Fatalf("hash %q isn't a $P$B portable hash of 34 bytes", s)
	}
	for _, c := range []byte(s[4:]) {
		if strings.IndexByte(itoa64, c) < 0 {
			t.Fatalf("hash %q has %q outside the phpass alphabet", s, c)
		}
	}
	if !refCheck("correct horse", s) {
		t.Fatalf("reference phpass refuses %q", s)
	}
	if refCheck("Correct horse", s) || refCheck("", s) {
		t.Fatalf("reference phpass accepts the wrong password for %q", s)
	}
	h2, _ := PasswordHash(core.StackWordPress, []byte("correct horse"))
	if bytes.Equal(h, h2) {
		t.Fatal("two hashes of one password share a salt")
	}
}

func TestPasswordHashBcrypt(t *testing.T) {
	for _, stack := range []core.Stack{core.StackLaravel, core.StackSQL} {
		h, err := PasswordHash(stack, []byte("correct horse"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(h, []byte("$2y$12$")) || len(h) != 60 {
			t.Fatalf("%s hash %q isn't $2y$ bcrypt with cost 12", stack, h)
		}
		if err := bcrypt.CompareHashAndPassword(h, []byte("correct horse")); err != nil {
			t.Fatalf("%s: %v", stack, err)
		}
		if bcrypt.CompareHashAndPassword(h, []byte("correct horsf")) == nil {
			t.Fatalf("%s hash accepts the wrong password", stack)
		}
	}
}

func TestPasswordHashErrors(t *testing.T) {
	secret := "hunter2-" + strings.Repeat("x", 80)
	for _, tc := range []struct {
		stack core.Stack
		pw    string
		code  string
	}{
		{core.StackWordPress, "", core.ErrConfig},
		{core.StackLaravel, "", core.ErrConfig},
		{core.StackWordPress, secret, core.ErrConfig},
		{core.StackLaravel, secret, core.ErrConfig},
		{"drupal", "correct horse", core.ErrStack},
	} {
		_, err := PasswordHash(tc.stack, []byte(tc.pw))
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != tc.code || ce.Exit != 2 {
			t.Fatalf("PasswordHash(%s, %d bytes) = %v, want code %s exit 2", tc.stack, len(tc.pw), err, tc.code)
		}
		if tc.pw != "" && strings.Contains(err.Error(), tc.pw) {
			t.Fatalf("error quotes the password: %v", err)
		}
	}
	// 72 bytes is still fine.
	if _, err := PasswordHash(core.StackWordPress, bytes.Repeat([]byte("p"), 72)); err != nil {
		t.Fatal(err)
	}
}

func TestResetHash(t *testing.T) {
	wp, err := ResetHash(core.StackWordPress)
	if err != nil {
		t.Fatal(err)
	}
	if len(wp) != len(resetPrefix)+22 || !bytes.HasPrefix(wp, []byte(resetPrefix)) {
		t.Fatalf("WordPress reset hash %q", wp)
	}
	for _, c := range wp[len(resetPrefix):] {
		if strings.IndexByte(itoa64, c) < 0 {
			t.Fatalf("reset hash %q has %q outside the phpass alphabet", wp, c)
		}
	}
	wp2, _ := ResetHash(core.StackWordPress)
	if bytes.Equal(wp, wp2) {
		t.Fatal("two reset hashes are the same")
	}
	for _, stack := range []core.Stack{core.StackLaravel, core.StackSQL} {
		h, err := ResetHash(stack)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(h, []byte("$2y$10$")) || len(h) != 60 {
			t.Fatalf("%s reset hash %q isn't $2y$ bcrypt with cost 10", stack, h)
		}
	}
	if _, err := ResetHash("drupal"); err == nil {
		t.Fatal("ResetHash of an unknown stack gave no error")
	}
}

// The keyed reset hash is the same for one key and original, differs
// for another key or original and keeps ResetHash's shapes.
func TestResetHashFor(t *testing.T) {
	k1, k2 := Key{1}, Key{2}
	for _, stack := range []core.Stack{core.StackWordPress, core.StackLaravel, core.StackSQL} {
		a, err := ResetHashFor(stack, k1, []byte("old-hash"))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ResetHashFor(stack, k1, []byte("old-hash"))
		if !bytes.Equal(a, b) {
			t.Errorf("%s: one key gave %q and %q", stack, a, b)
		}
		if c, _ := ResetHashFor(stack, k2, []byte("old-hash")); bytes.Equal(a, c) {
			t.Errorf("%s: two keys gave %q", stack, a)
		}
		if c, _ := ResetHashFor(stack, k1, []byte("other-hash")); bytes.Equal(a, c) {
			t.Errorf("%s: two originals gave %q", stack, a)
		}
		if stack == core.StackWordPress {
			if len(a) != len(resetPrefix)+22 || !bytes.HasPrefix(a, []byte(resetPrefix)) || bytes.ContainsFunc(a[len(resetPrefix):], func(r rune) bool { return !strings.ContainsRune(itoa64, r) }) {
				t.Errorf("WordPress keyed reset hash %q", a)
			}
			for _, pw := range randomPasswords(200) {
				if refCheck(pw, string(a)) {
					t.Fatalf("keyed reset hash %q matches %q", a, pw)
				}
			}
			continue
		}
		if cost, err := bcrypt.Cost(a); err != nil || cost != resetCost || len(a) != 60 || !bytes.HasPrefix(a, []byte("$2y$10$")) {
			t.Errorf("%s keyed reset hash %q: cost %d %v", stack, a, cost, err)
		}
		if bcrypt.CompareHashAndPassword(a, []byte("old-hash")) == nil {
			t.Errorf("%s keyed reset hash matches its original", stack)
		}
	}
	if _, err := ResetHashFor("drupal", k1, nil); err == nil {
		t.Fatal("ResetHashFor of an unknown stack gave no error")
	}
}

// randomPasswords returns n passwords: the obvious ones and random bytes.
func randomPasswords(n int) []string {
	pws := []string{"", "password", "admin", "roadie", "correct horse", resetPrefix}
	for len(pws) < n {
		pws = append(pws, string(randomFrom(saltAlphabet, 1+len(pws)%40)))
	}
	return pws
}

func TestResetHashNeverMatches(t *testing.T) {
	wp, _ := ResetHash(core.StackWordPress)
	for _, pw := range randomPasswords(1000) {
		if refCheck(pw, string(wp)) {
			t.Fatalf("reset hash %q matches %q", wp, pw)
		}
	}
	// bcrypt is slow on purpose: a sample is enough to show the reset
	// hash is a real bcrypt of a password nobody has.
	bc, _ := ResetHash(core.StackLaravel)
	for _, pw := range randomPasswords(6) {
		if bcrypt.CompareHashAndPassword(bc, []byte(pw)) == nil {
			t.Fatalf("bcrypt reset hash matches %q", pw)
		}
	}
}

// phpChecker runs testdata/check_password.php once and asks it about
// many hashes.
type phpChecker struct {
	t   *testing.T
	in  *bytes.Buffer
	exp []string
}

func (c *phpChecker) ask(mode, pw string, hash []byte, want bool) {
	b64 := base64.StdEncoding.EncodeToString
	fmt.Fprintf(c.in, "%s %s %s\n", mode, b64([]byte(pw)), b64(hash))
	c.exp = append(c.exp, fmt.Sprintf("%s %q %q want %v", mode, pw, hash, want))
}

func (c *phpChecker) run(php string) {
	c.t.Helper()
	cmd := exec.Command(php, "-n", filepath.Join("testdata", "check_password.php")) // #nosec G204 -- php from PATH running the test's own script
	cmd.Stdin = c.in
	out, err := cmd.Output()
	if err != nil {
		c.t.Fatalf("check_password.php: %v", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	i := 0
	for ; sc.Scan(); i++ {
		if i >= len(c.exp) {
			c.t.Fatalf("PHP gave more answers than questions")
		}
		want := strings.HasSuffix(c.exp[i], "want true")
		if got := sc.Text() == "1"; got != want {
			c.t.Errorf("PHP disagrees: %s", c.exp[i])
		}
	}
	if i != len(c.exp) {
		c.t.Fatalf("PHP answered %d of %d", i, len(c.exp))
	}
}

// PHP itself checks the hashes: phpass CheckPassword for WordPress and
// password_verify with Laravel's bcrypt algorithm check.
func TestPasswordHashPHP(t *testing.T) {
	php := testutil.RequirePHP(t)
	c := &phpChecker{t: t, in: &bytes.Buffer{}}
	for _, pw := range []string{"correct horse", "Zoë ünïcode", "p@ss w0rd!'\"\\"} {
		wp, err := PasswordHash(core.StackWordPress, []byte(pw))
		if err != nil {
			t.Fatal(err)
		}
		c.ask("phpass", pw, wp, true)
		c.ask("phpass", pw+"x", wp, false)
		bc, err := PasswordHash(core.StackLaravel, []byte(pw))
		if err != nil {
			t.Fatal(err)
		}
		c.ask("bcrypt", pw, bc, true)
		c.ask("bcrypt", pw+"x", bc, false)
	}
	wp, _ := ResetHash(core.StackWordPress)
	for _, pw := range randomPasswords(1000) {
		c.ask("phpass", pw, wp, false)
	}
	bc, _ := ResetHash(core.StackLaravel)
	for _, pw := range randomPasswords(6) {
		c.ask("bcrypt", pw, bc, false)
	}
	kwp, _ := ResetHashFor(core.StackWordPress, Key{7}, []byte("$P$Bold"))
	kbc, _ := ResetHashFor(core.StackLaravel, Key{7}, []byte("$2y$10$old"))
	for _, pw := range randomPasswords(6) {
		c.ask("phpass", pw, kwp, false)
		c.ask("bcrypt", pw, kbc, false)
	}
	c.run(php)
}

func TestSalt(t *testing.T) {
	seen := map[string]bool{}
	counts := map[byte]int{}
	for range 1000 {
		s := Salt()
		if len(s) != 64 {
			t.Fatalf("salt %q has %d characters", s, len(s))
		}
		for i := range len(s) {
			if strings.IndexByte(saltAlphabet, s[i]) < 0 {
				t.Fatalf("salt has %q outside the WordPress alphabet", s[i])
			}
			counts[s[i]]++
		}
		if strings.ContainsAny(s, "'\"\\") {
			t.Fatal("salt can't go in a single-quoted define")
		}
		if seen[s] {
			t.Fatal("two salts are the same")
		}
		seen[s] = true
	}
	// 64,000 characters over 92: every one shows up, none dominates.
	if len(counts) != len(saltAlphabet) {
		t.Fatalf("salts used %d of %d characters", len(counts), len(saltAlphabet))
	}
	for c, n := range counts {
		if n < 400 || n > 1000 {
			t.Fatalf("character %q came %d times, want about 696", c, n)
		}
	}
}

func TestAppKey(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		k := AppKey()
		raw, ok := strings.CutPrefix(k, "base64:")
		if !ok {
			t.Fatalf("APP_KEY %q has no base64: prefix", k)
		}
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(b) != 32 {
			t.Fatalf("APP_KEY %q isn't base64 of 32 bytes", k)
		}
		if seen[k] {
			t.Fatal("two APP_KEYs are the same")
		}
		seen[k] = true
	}
}

func TestRandomFromAlphabet(t *testing.T) {
	// A two-letter alphabet still fills n characters.
	if got := randomFrom("ab", 100); len(got) != 100 || strings.Trim(string(got), "ab") != "" {
		t.Fatalf("randomFrom(ab, 100) = %q", got)
	}
}
