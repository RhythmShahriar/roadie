// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- phpass portable hashes are md5 by definition
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/RhythmShahriar/roadie/core"
)

// itoa64 is the phpass alphabet, used for its salts and hash encoding.
const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// saltAlphabet is what wp_generate_password(64, true, true) draws from,
// the characters WordPress salts are made of. It has no quote or
// backslash, so a salt fits in wp-config.php's single-quoted defines.
const saltAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" +
	"!@#$%^&*()" + "-_ []{}<>~`+=,.;:/?|"

const (
	// resetPrefix starts every WordPress reset hash. phpass reads the
	// byte after "$P$" as the iteration count; 'r' is out of its range,
	// so phpass refuses the hash and no password ever matches it.
	resetPrefix = "$P$roadie"
	// phpassLog2 is WordPress's PasswordHash(8, true) count: 2^13 rounds,
	// written as itoa64[13], the 'B' in "$P$B".
	phpassLog2 = 13
	// resetCost and passwordCost are the bcrypt costs of a hash nobody
	// logs in with and of one somebody does.
	resetCost    = 10
	passwordCost = 12
	// maxPasswordLen is bcrypt's limit. phpass would take more; one
	// limit for every stack keeps --admin-password-env predictable.
	maxPasswordLen = 72
)

// random fills b from crypto/rand. Since Go 1.24 Read never fails (the
// runtime aborts first), so the panic is only a guard.
func random(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("pseudo: crypto/rand failed: " + err.Error())
	}
}

// randomFrom returns n characters of alphabet picked by crypto/rand,
// rejecting bytes past the last whole multiple so every character is
// equally likely.
func randomFrom(alphabet string, n int) []byte {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n+n/2)
	for len(out) < n {
		random(buf)
		for _, c := range buf {
			if int(c) < limit && len(out) < n {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return out
}

func resetHash(stack core.Stack) ([]byte, error) {
	switch stack {
	case core.StackWordPress:
		return append([]byte(resetPrefix), randomFrom(itoa64, 22)...), nil
	case core.StackLaravel, core.StackSQL:
		pw := make([]byte, 32)
		random(pw)
		return bcryptHash(pw, resetCost)
	}
	return nil, unknownStack(stack)
}

// bcryptB64 is bcrypt's own base64: its alphabet, no padding.
var bcryptB64 = base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789").WithPadding(base64.NoPadding)

func resetHashFor(stack core.Stack, key Key, original []byte) ([]byte, error) {
	// Two HMAC blocks give the 39 bytes a bcrypt string holds (16 of
	// salt and 23 of digest); WordPress uses the first 16.
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte("reset-password\x00" + string(stack) + "\x00"))
	mac.Write(original)
	d := mac.Sum(nil)
	mac.Reset()
	mac.Write(d)
	d = mac.Sum(d)
	switch stack {
	case core.StackWordPress:
		return append([]byte(resetPrefix), phpassEncode(d[:16])...), nil
	case core.StackLaravel, core.StackSQL:
		return fmt.Appendf(nil, "$2y$%02d$%s%s", resetCost, bcryptB64.EncodeToString(d[:16]), bcryptB64.EncodeToString(d[16:39])), nil
	}
	return nil, unknownStack(stack)
}

func passwordHash(stack core.Stack, password []byte) ([]byte, error) {
	if len(password) == 0 {
		return nil, core.Errorf(core.ErrConfig, 2, "admin password: the variable is empty; set it to the password kept users log in with")
	}
	if len(password) > maxPasswordLen {
		return nil, core.Errorf(core.ErrConfig, 2, "admin password: longer than %d bytes, which bcrypt can't hash; use a shorter one", maxPasswordLen)
	}
	switch stack {
	case core.StackWordPress:
		salt := make([]byte, 6)
		random(salt)
		return phpassHash(password, phpassEncode(salt), phpassLog2), nil
	case core.StackLaravel, core.StackSQL:
		return bcryptHash(password, passwordCost)
	}
	return nil, unknownStack(stack)
}

func unknownStack(stack core.Stack) error {
	return core.Errorf(core.ErrStack, 2, "no password hash for the stack %q; Roadie knows wordpress, laravel and sql", string(stack))
}

// bcryptHash returns a bcrypt hash written with the "$2y$" prefix PHP
// writes. Go writes "$2a$" for the same algorithm; PHP's
// password_get_info doesn't name it bcrypt, so Laravel's Hash::check
// (verify on by default since Laravel 11) would refuse it.
func bcryptHash(pw []byte, cost int) ([]byte, error) {
	h, err := bcrypt.GenerateFromPassword(pw, cost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return nil, core.Errorf(core.ErrConfig, 2, "admin password: longer than %d bytes, which bcrypt can't hash; use a shorter one", maxPasswordLen)
		}
		return nil, core.Errorf(core.ErrConfig, 2, "password hash: %v", err)
	}
	if bytes.HasPrefix(h, []byte("$2a$")) {
		h[2] = 'y'
	}
	return h, nil
}

// phpassHash is phpass's crypt_private for a new portable hash: "$P$",
// the count character, the 8-character salt, then the md5 of salt and
// password stretched 2^log2 times, in phpass's base64.
func phpassHash(password, salt []byte, log2 int) []byte {
	h := md5.New() // #nosec G401 -- the phpass portable format, which WordPress checks
	h.Write(salt)
	h.Write(password)
	sum := h.Sum(nil)
	for range 1 << log2 {
		h.Reset()
		h.Write(sum)
		h.Write(password)
		sum = h.Sum(sum[:0])
	}
	out := make([]byte, 0, 34)
	out = append(out, "$P$"...)
	out = append(out, itoa64[log2])
	out = append(out, salt...)
	return append(out, phpassEncode(sum)...)
}

// phpassEncode is phpass's encode64: little-endian groups of three bytes
// into four characters, with a short last group written short.
func phpassEncode(in []byte) []byte {
	var out []byte
	for i := 0; i < len(in); {
		v := uint(in[i])
		i++
		out = append(out, itoa64[v&0x3f])
		if i < len(in) {
			v |= uint(in[i]) << 8
		}
		out = append(out, itoa64[(v>>6)&0x3f])
		if i >= len(in) {
			break
		}
		i++
		if i < len(in) {
			v |= uint(in[i]) << 16
		}
		out = append(out, itoa64[(v>>12)&0x3f])
		if i >= len(in) {
			break
		}
		i++
		out = append(out, itoa64[(v>>18)&0x3f])
	}
	return out
}

func salt() string { return string(randomFrom(saltAlphabet, 64)) }

func appKey() string {
	b := make([]byte, 32)
	random(b)
	return "base64:" + base64.StdEncoding.EncodeToString(b)
}
