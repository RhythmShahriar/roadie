// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- md5 is one of the forms PII hides in (Gravatar, AS args)
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The encodings the oracle searches, in the order a match is named when
// two give the same bytes (a lower-case email is its own lower-case form:
// it is reported as raw).
const (
	EncRaw         = "raw"
	EncLower       = "lower"
	EncUpper       = "upper"
	EncJSON        = "json"
	EncJSONSlash   = "json-slash"
	EncJSONPHP     = "json-php"
	EncJSONUnicode = "json-unicode"
	EncURL         = "url"
	EncURLPath     = "url-path"
	EncURLAll      = "url-all"
	EncHTML        = "html"
	EncHTMLDec     = "html-entity"
	EncHTMLHex     = "html-hex"
	EncBase64      = "base64"    // std alphabet, "base64-0" to "base64-2" by offset
	EncBase64URL   = "base64url" // url alphabet, "base64url-0" to "base64url-2"
	EncSerialized  = "serialized"
	EncMD5         = "md5"
	EncSHA256      = "sha256"
)

// Encodings lists every encoding name a report can carry.
func Encodings() []string {
	return []string{
		EncRaw, EncLower, EncUpper, EncJSON, EncJSONSlash, EncJSONPHP, EncJSONUnicode,
		EncURL, EncURLPath, EncURLAll, EncHTML, EncHTMLDec, EncHTMLHex,
		EncBase64 + "-0", EncBase64 + "-1", EncBase64 + "-2",
		EncBase64URL + "-0", EncBase64URL + "-1", EncBase64URL + "-2",
		EncSerialized, EncMD5, EncSHA256,
	}
}

// form is one byte pattern standing for a canary in one encoding.
type form struct {
	enc string
	b   []byte
}

// forms returns every encoded form of v, each also in the spelling a
// dump writes it in (a JSON escape's backslash is doubled in a mysqldump
// string). Forms equal to an earlier one are left out, so the first
// encoding that gives some bytes names them. Inside a dump (inDump) the
// SQL spellings come first: there a \" is a quote escaped by the dump,
// in a plain file it is a JSON escape.
func forms(v string, inDump bool) []form {
	var plain []form
	add := func(enc, s string) { plain = append(plain, form{enc, []byte(s)}) }
	add(EncRaw, v)
	add(EncLower, strings.ToLower(v))
	add(EncUpper, strings.ToUpper(v))
	j := jsonString(v)
	add(EncJSON, j)
	add(EncJSONSlash, strings.ReplaceAll(j, "/", `\/`))
	add(EncJSONPHP, jsonPHP(v))
	add(EncJSONUnicode, jsonUnicode(v))
	add(EncURL, url.QueryEscape(v))
	add(EncURLPath, url.PathEscape(v))
	add(EncURLAll, percentAll(v))
	add(EncHTML, htmlSpecial(v))
	add(EncHTMLDec, htmlNumeric(v, false))
	add(EncHTMLHex, htmlNumeric(v, true))
	for off := range 3 {
		if s := base64Core(base64.RawStdEncoding, v, off); s != "" {
			add(EncBase64+"-"+strconv.Itoa(off), s)
		}
		if s := base64Core(base64.RawURLEncoding, v, off); s != "" {
			add(EncBase64URL+"-"+strconv.Itoa(off), s)
		}
	}
	add(EncSerialized, "s:"+strconv.Itoa(len(v))+`:"`+v+`";`)
	m := md5.Sum([]byte(v)) // #nosec G401 -- searching for md5 of PII, not hashing anything
	add(EncMD5, hex.EncodeToString(m[:]))
	add(EncMD5, strings.ToUpper(hex.EncodeToString(m[:])))
	s := sha256.Sum256([]byte(v))
	add(EncSHA256, hex.EncodeToString(s[:]))
	add(EncSHA256, strings.ToUpper(hex.EncodeToString(s[:])))
	escaped := make([]form, len(plain))
	for i, f := range plain {
		escaped[i] = form{f.enc, []byte(sqlEscape(string(f.b)))}
	}
	order := append(plain, escaped...)
	if inDump {
		order = append(escaped, plain...)
	}
	var out []form
	seen := map[string]bool{}
	for _, f := range order {
		if len(f.b) > 0 && !seen[string(f.b)] {
			seen[string(f.b)] = true
			out = append(out, f)
		}
	}
	return out
}

// jsonString is v as JSON writes it inside quotes, without the quotes
// and without HTML escaping (PHP's json_encode doesn't escape <, > or &).
func jsonString(v string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return v
	}
	s := strings.TrimSuffix(b.String(), "\n")
	return s[1 : len(s)-1]
}

// jsonPHP is v as PHP's json_encode writes it with no flags (as
// WordPress's wp_json_encode does): "/" escaped and every non-ASCII rune
// as a lower-case \uXXXX.
func jsonPHP(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '"' || r == '\\' || r == '/':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// jsonUnicode escapes every byte that isn't a letter or digit as \u00XX
// and every non-ASCII rune as \uXXXX (or a surrogate pair), as a JSON
// encoder told to escape everything does.
func jsonUnicode(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case isAlnum(r):
			b.WriteRune(r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

func isAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// percentAll percent-encodes every byte that isn't a letter or digit.
func percentAll(v string) string {
	var b strings.Builder
	for i := range len(v) {
		if c := v[i]; isAlnum(rune(c)) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// htmlSpecial is PHP's htmlspecialchars with ENT_QUOTES.
func htmlSpecial(v string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;").Replace(v)
}

// htmlNumeric writes every rune that isn't a letter or digit as a numeric
// character reference, decimal or hex.
func htmlNumeric(v string, hexForm bool) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case isAlnum(r):
			b.WriteRune(r)
		case hexForm:
			fmt.Fprintf(&b, "&#x%x;", r)
		default:
			fmt.Fprintf(&b, "&#%d;", r)
		}
	}
	return b.String()
}

// base64Core returns the base64 characters v always produces when it
// starts at a byte offset of off modulo 3 inside a longer encoded value:
// the whole 3-byte groups that hold only bytes of v. The characters of the
// partial groups at either end depend on the bytes around v, so they
// are left out.
func base64Core(enc *base64.Encoding, v string, off int) string {
	skip := (3 - off) % 3
	if skip >= len(v) {
		return ""
	}
	n := (len(v) - skip) / 3 * 3
	if n == 0 {
		return ""
	}
	return enc.EncodeToString([]byte(v[skip : skip+n]))
}

// sqlEscape escapes s as mysqldump does inside a quoted string.
func sqlEscape(s string) string {
	return strings.NewReplacer("\\", `\\`, "'", `\'`, `"`, `\"`, "\x00", `\0`, "\n", `\n`, "\r", `\r`, "\x1a", `\Z`).Replace(s)
}
