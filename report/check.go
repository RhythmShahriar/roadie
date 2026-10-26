// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/roadie/core"
)

// Check verifies a report's signature (against pinned when set) and the
// input fingerprint against input when given: *core.Error report-edited or
// report-unsigned.
func Check(b []byte, pinned []byte, input io.Reader) error {
	_, err := CheckReport("report.json", b, pinned, input)
	return err
}

// CheckReport is Check for a report file called name, as the messages
// name it; it returns the ID of the key that signed the report. Every
// failure is a *core.Error: report-unsigned (exit 3) for a report without
// a signature, report-edited (exit 1) for a changed report, another
// signer than pinned or another input; config (exit 2) for a pinned
// key that isn't one.
func CheckReport(name string, b []byte, pinned []byte, input io.Reader) (string, error) {
	name = term.SafeText(name)
	edited := func(format string, a ...any) error {
		return core.Errorf(core.ErrReportEdited, cli.ExitFindings, "report check: "+name+" "+format, a...)
	}
	v, err := parseJSON(b)
	if err != nil {
		return "", edited("isn't a report Roadie can check (%s).", strings.TrimPrefix(err.Error(), "canonical JSON: "))
	}
	env, _ := v.(map[string]any)
	if env == nil || env["schema"] != core.EnvelopeSchema {
		return "", edited("isn't a %s report.", core.EnvelopeSchema)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		return "", edited("has no data object.")
	}
	raw, ok := data["signature"]
	if !ok || raw == nil {
		return "", core.Errorf(core.ErrReportUnsigned, cli.ExitUnavailable,
			"report check: %s has no signature (it was written with --sign=off).", name)
	}
	sig, ok := readSignature(raw)
	if !ok {
		return "", edited("has a signature Roadie can't read.")
	}
	delete(data, "signature")
	var msg bytes.Buffer
	writeCanonical(&msg, env)
	if KeyID(sig.pub) != sig.keyID || !ed25519.Verify(sig.pub, msg.Bytes(), sig.sig) {
		if diff := summaryDiff(env, data); diff != "" {
			return "", edited("was changed after it was signed (%s).", diff)
		}
		return "", edited("was changed after it was signed.")
	}
	if pinned != nil {
		ids, err := pinnedKeyIDs(pinned)
		if err != nil {
			return "", err
		}
		if !slices.Contains(ids, sig.keyID) {
			return "", edited("is signed by %s, not by the key you pinned (%s).", sig.keyID, ids[0])
		}
	}
	if input != nil {
		if err := checkInput(name, data, input); err != nil {
			return "", err
		}
	}
	return sig.keyID, nil
}

// CheckedLine is what report check prints for a valid report (PRD 6.9).
func CheckedLine(keyID string) string {
	return "signed by " + ShortKeyID(keyID) + " and unchanged since. It shows this Roadie run found these results; it doesn't prove the checks covered everything."
}

// parsedSignature is data.signature, decoded.
type parsedSignature struct {
	keyID string
	pub   ed25519.PublicKey
	sig   []byte
}

func readSignature(v any) (parsedSignature, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return parsedSignature{}, false
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	if str("alg") != "ed25519" {
		return parsedSignature{}, false
	}
	pub, err := base64.StdEncoding.DecodeString(str("public_key"))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return parsedSignature{}, false
	}
	sig, err := base64.StdEncoding.DecodeString(str("sig"))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return parsedSignature{}, false
	}
	return parsedSignature{keyID: str("key_id"), pub: pub, sig: sig}, str("key_id") != ""
}

// severityFields are the summary counts a reader can recompute from
// data.findings.
var severityFields = []string{"critical", "high", "medium", "low", "info"}

// summaryDiff compares the summary's severity counts with the counts of
// data.findings and writes the ones that differ as "summary.high 3 → 0":
// what the findings hold, then what the summary now says. An edit that
// changes both the same way shows nothing here; the signature still fails.
func summaryDiff(env, data map[string]any) string {
	summary, _ := env["summary"].(map[string]any)
	findings, _ := data["findings"].([]any)
	counted := map[string]int64{}
	for _, f := range findings {
		if m, ok := f.(map[string]any); ok {
			if s, ok := m["severity"].(string); ok {
				counted[s]++
			}
		}
	}
	var parts []string
	for _, k := range severityFields {
		said, ok := summary[k].(int64)
		if !ok {
			continue
		}
		if said != counted[k] {
			parts = append(parts, fmt.Sprintf("summary.%s %d → %d", k, counted[k], said))
		}
	}
	return strings.Join(parts, ", ")
}

// pinnedKeyIDs reads the key report check --key pins: a key ID
// ("roadie:" and 16 characters), a report's public_key (base64), raw
// public key bytes or a PEM public or private key. A bare 32-byte value
// is only ever a public key: reading it as a seed too would let anyone
// sign as the key that seed makes, since a report's public_key is public.
// A private key pins only as PEM, whose PRIVATE KEY label says what it is.
func pinnedKeyIDs(pinned []byte) ([]string, error) {
	bad := core.Errorf(core.ErrConfig, cli.ExitUsage,
		"report check: --key isn't a public key: give a key ID (roadie:...), a report's public_key or a PEM Ed25519 key")
	text := strings.TrimSpace(string(pinned))
	switch {
	case strings.HasPrefix(text, "roadie:"):
		if len(text) != len("roadie:")+16 {
			return nil, core.Errorf(core.ErrConfig, cli.ExitUsage,
				"report check: --key %s isn't a whole key ID; give all 16 characters after roadie:", term.SafeText(text))
		}
		return []string{text}, nil
	case strings.HasPrefix(text, "-----BEGIN"):
		block, _ := pem.Decode([]byte(text))
		if block == nil {
			return nil, bad
		}
		switch block.Type {
		case "PUBLIC KEY":
			k, err := x509.ParsePKIXPublicKey(block.Bytes)
			if pub, ok := k.(ed25519.PublicKey); err == nil && ok {
				return []string{KeyID(pub)}, nil
			}
		case "PRIVATE KEY":
			if s, err := parseSigningKey([]byte(text)); err == nil {
				return []string{s.keyID}, nil
			}
		}
		return nil, bad
	}
	raw := pinned
	if b, ok := decodeBase64(text); ok {
		raw = b
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, bad
	}
	return []string{KeyID(raw)}, nil
}

// checkInput compares data.input with the SHA-256 of input.
func checkInput(name string, data map[string]any, input io.Reader) error {
	in, _ := data["input"].(map[string]any)
	kind, _ := in["kind"].(string)
	want, _ := in["value"].(string)
	switch {
	case in == nil || want == "":
		return core.Errorf(core.ErrReportEdited, cli.ExitFindings, "report check: %s names no input, so --input has nothing to compare.", name)
	case kind != "sha256-file":
		return core.Errorf(core.ErrReportEdited, cli.ExitFindings,
			"report check: %s describes a database (%s), not a dump file; --input checks dump files.", name, term.SafeText(kind))
	}
	h := sha256.New()
	if _, err := io.Copy(h, input); err != nil {
		return core.Errorf(core.ErrReportEdited, cli.ExitUnavailable, "report check: couldn't read --input: %v", err)
	}
	want = strings.ToLower(strings.TrimPrefix(want, "sha256:"))
	if hex.EncodeToString(h.Sum(nil)) != want {
		short := term.SafeText(want)
		if len(short) > 12 {
			short = short[:12]
		}
		return core.Errorf(core.ErrReportEdited, cli.ExitFindings, "report check: %s describes another input (sha256 %s…).", name, short)
	}
	return nil
}
