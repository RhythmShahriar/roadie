// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
)

// EnvSigningKey holds a base64 Ed25519 seed that signs every report, so
// CI runs share one identity (PRD 16.4).
const EnvSigningKey = "ROADIE_SIGNING_KEY"

// Signer signs envelopes with an Ed25519 key. The private key never
// leaves it: a report holds only the public key and its ID.
type Signer struct {
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
	keyID string
}

// maxKeyFile is the largest key file Roadie reads; a PEM key is a few
// hundred bytes.
const maxKeyFile = 64 << 10

// LoadSigner loads the signing key from ROADIE_SIGNING_KEY (base64 of a
// 32-byte seed), else keyFile (the same, or a PKCS#8 PEM Ed25519 key),
// else <config.Dir()>/roadie/signing.key, which it creates with
// crypto/rand on first use: the file 0600 in a 0700 folder. created
// reports that, so the caller can say it once. env reads the
// environment; nil means os.LookupEnv. No error message holds key bytes.
func LoadSigner(keyFile string, env func(string) (string, bool)) (*Signer, bool, error) {
	if env == nil {
		env = os.LookupEnv
	}
	if v, ok := env(EnvSigningKey); ok && strings.TrimSpace(v) != "" {
		s, err := parseSigningKey([]byte(v))
		if err != nil {
			return nil, false, fmt.Errorf("%s isn't an Ed25519 signing key: want base64 of a 32-byte seed", EnvSigningKey)
		}
		return s, false, nil
	}
	if keyFile != "" {
		s, err := readSigningKey(keyFile)
		return s, false, err
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, false, fmt.Errorf("no folder for the signing key: %w", err)
	}
	path := DefaultKeyPath(dir)
	s, err := readSigningKey(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return s, false, err
	}
	return createSigningKey(path)
}

// DefaultKeyPath is where LoadSigner keeps the signing key under the
// Rhythm config folder.
func DefaultKeyPath(configDir string) string {
	return filepath.Join(configDir, "roadie", "signing.key")
}

// readSigningKey reads a key file. A missing file is fs.ErrNotExist.
func readSigningKey(path string) (*Signer, error) {
	f, err := os.Open(path) // #nosec G304 -- the user's own key file or Roadie's default path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("signing key %s: %w", path, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("signing key: %w", err)
	}
	defer func() { _ = f.Close() }()
	var b bytes.Buffer
	if _, err := b.ReadFrom(io.LimitReader(f, maxKeyFile+1)); err != nil {
		return nil, fmt.Errorf("signing key %s: %w", path, err)
	}
	if b.Len() > maxKeyFile {
		return nil, fmt.Errorf("signing key %s is too large to be a key", path)
	}
	s, err := parseSigningKey(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("signing key %s: %w", path, err)
	}
	return s, nil
}

// parseSigningKey reads base64 of a 32-byte seed (standard or URL
// alphabet, padded or not) or a PKCS#8 PEM Ed25519 private key.
func parseSigningKey(b []byte) (*Signer, error) {
	text := strings.TrimSpace(string(b))
	if strings.HasPrefix(text, "-----BEGIN") {
		block, _ := pem.Decode([]byte(text))
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, errors.New("want a PKCS#8 PEM block (BEGIN PRIVATE KEY)")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("the PEM block isn't a PKCS#8 private key")
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("the PEM key isn't an Ed25519 key")
		}
		return newSigner(priv.Seed()), nil
	}
	seed, ok := decodeBase64(text)
	if !ok || len(seed) != ed25519.SeedSize {
		return nil, errors.New("want base64 of a 32-byte Ed25519 seed or a PKCS#8 PEM key")
	}
	return newSigner(seed), nil
}

// decodeBase64 reads standard or URL base64, padded or not.
func decodeBase64(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

func newSigner(seed []byte) *Signer {
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Signer{priv: priv, pub: pub, keyID: KeyID(pub)}
}

// createSigningKey makes a new key at path. O_EXCL keeps two first runs
// from writing two keys: the second one reads the first one's.
func createSigningKey(path string) (*Signer, bool, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("signing key folder: %w", err)
	}
	if runtime.GOOS != "windows" {
		// MkdirAll leaves an existing folder as it was; the key's folder
		// is Roadie's own, so it is made private either way.
		if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a folder needs x to be entered; 0700 is owner only
			return nil, false, fmt.Errorf("signing key folder: %w", err)
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, false, fmt.Errorf("signing key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- Roadie's default key path
	if errors.Is(err, fs.ErrExist) {
		s, err := readSigningKey(path)
		return s, false, err
	}
	if err != nil {
		return nil, false, fmt.Errorf("signing key: %w", err)
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(seed) + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return nil, false, fmt.Errorf("signing key: %w", err)
	}
	return newSigner(seed), true, nil
}

// KeyID returns "roadie:" and the first 16 characters of the URL base64
// of the SHA-256 of the raw public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "roadie:" + base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}

// KeyID returns "roadie:" and 16 characters of the public key's hash.
func (s *Signer) KeyID() string {
	if s == nil {
		return ""
	}
	return s.keyID
}

// PublicKey returns the raw 32-byte public key.
func (s *Signer) PublicKey() ed25519.PublicKey {
	if s == nil {
		return nil
	}
	return s.pub
}

// Sign adds data.signature to e: Ed25519 over the canonical JSON of the
// whole envelope with data.signature removed (PRD 16.4). Signing again
// replaces the old signature.
func (s *Signer) Sign(e *core.Envelope) error {
	if s == nil || len(s.priv) == 0 {
		return errors.New("report: no signing key")
	}
	if e == nil {
		return errors.New("report: no envelope")
	}
	msg, data, err := signedBytes(e)
	if err != nil {
		return err
	}
	sig := Signature{
		Alg:       "ed25519",
		KeyID:     s.keyID,
		PublicKey: base64.StdEncoding.EncodeToString(s.pub),
		Sig:       base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, msg)),
	}
	sb, err := marshal(sig)
	if err != nil {
		return err
	}
	e.Data, err = withSignature(e.Data, data, sb)
	return err
}

// signedBytes returns the canonical JSON a signature covers: the
// envelope with data.signature removed. data is the envelope's data
// object as parsed, with the signature already gone.
func signedBytes(e *core.Envelope) ([]byte, map[string]any, error) {
	b, err := marshal(e)
	if err != nil {
		return nil, nil, fmt.Errorf("report: %w", err)
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, nil, fmt.Errorf("report: %w", err)
	}
	env, _ := v.(map[string]any)
	data, ok := env["data"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("report: the envelope's data isn't an object")
	}
	delete(data, "signature")
	var out bytes.Buffer
	writeCanonical(&out, env)
	return out.Bytes(), data, nil
}

// withSignature puts the signature object into the data object. When
// data holds no signature yet, it goes at the end and the rest of the
// bytes stay as Envelope wrote them; an old signature is replaced in a
// fresh encoding of the parsed data.
func withSignature(raw json.RawMessage, parsed map[string]any, sig []byte) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	if _, had := probe["signature"]; !had {
		trimmed := bytes.TrimRight(raw, " \t\r\n")
		if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
			return nil, errors.New("report: the envelope's data isn't an object")
		}
		out := append([]byte(nil), trimmed[:len(trimmed)-1]...)
		if len(probe) > 0 {
			out = append(out, ',')
		}
		out = append(out, `"signature":`...)
		out = append(out, sig...)
		return append(out, '}'), nil
	}
	var sv any
	if err := json.Unmarshal(sig, &sv); err != nil {
		return nil, err
	}
	parsed["signature"] = sv
	var out bytes.Buffer
	writeCanonical(&out, parsed)
	return out.Bytes(), nil
}
