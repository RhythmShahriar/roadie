// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// fingerprintPrefix starts every key fingerprint.
const fingerprintPrefix = "roadie-key:"

func newKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, core.Errorf(core.ErrConfig, 2, "consistency key: no random bytes from the system: %v", err)
	}
	return k, nil
}

func parseKey(s string) (Key, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(Key{}) {
		// The value is never quoted: it may be a real key with a typo.
		return Key{}, core.Errorf(core.ErrConfig, 2, "consistency key: not base64 of 32 bytes; make a new one with rhythm-roadie config key rotate")
	}
	var k Key
	copy(k[:], b)
	return k, nil
}

func (k Key) encode() string { return base64.StdEncoding.EncodeToString(k[:]) }

func (k Key) fingerprint() string {
	sum := sha256.Sum256(k[:])
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return fingerprintPrefix + strings.ToLower(b32[:12])
}
