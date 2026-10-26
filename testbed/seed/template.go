// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seed

import (
	"crypto/md5" // #nosec G501 -- md5 is a form PII hides in; the testbed plants it
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

// Templates are Go text/template files. The functions:
//
//	{{canary "email" "c1" "wp_users.user_email"}}  a canary; the same kind and name give the same value
//	{{key "stripe_live_secret"}}                   a fake key assembled from parts
//	{{json X}} {{jsonslash X}} {{jsonphp X}} {{jsonu X}}  JSON string contents
//	{{url X}} {{urlpath X}} {{urlall X}}           percent-encodings
//	{{html X}} {{htmlnum X}} {{htmlhex X}}          HTML escapes
//	{{b64 X...}} {{b64url X...}}                   base64 of the arguments joined
//	{{ser X}}                                      PHP serialized string framing, s:N:"X";
//	{{sarr "k" V "k2" V2}}                         a PHP array of string keys to serialized values
//	{{md5 X}} {{sha256 X}} {{lower X}} {{upper X}}
//	{{sql X}}                                      escape for a MySQL single-quoted string
//	{{seq N}} {{add A B}} {{sub A B}} {{mul A B}} {{mod A B}}  1 to N for range; arithmetic for IDs
//	{{repeat N S}}                                 S N times, to pad a value to a length
//	{{cr}} {{bom}}                                 a carriage return and a UTF-8 byte order mark
//	{{list A B ...}} {{dict "k" V ...}}             arguments for a shared {{template}}
//	{{include "name" DATA}}                        a template's output as a string
//
// Git checks fixtures out with LF line endings, so a CRLF or BOM fixture
// spells them with {{cr}} and {{bom}} instead of holding the bytes.
//
// An encoding function given a canary records the encoding on it (in the
// oracle's names), so canaries.json says how each one was planted. A
// canary printed as it is (in a file tree, say) is recorded as raw when
// nothing else was.

// cval is a canary inside a template: it prints as its value and tells an
// encoding function which canary it is encoding.
type cval struct{ c *Canary }

func (v cval) String() string { return v.c.Value }

// renderer fills one scenario's templates.
type renderer struct {
	shared   []string // testbed/fixtures/_shared/*.tmpl
	scenario string
	gen      *generator
	byName   map[string]*Canary
	list     []*Canary
	counts   map[string]int
	values   map[string]bool // every value made in this run, lower-cased, across scenarios
}

func (r *renderer) canary(kind, name string, where ...string) (cval, error) {
	k := kind + "/" + name
	if c, ok := r.byName[k]; ok {
		return cval{c}, nil
	}
	var v string
	for {
		var err error
		if v, err = r.gen.value(kind); err != nil {
			return cval{}, err
		}
		if !r.clashes(v) {
			break
		}
	}
	r.values[strings.ToLower(v)] = true
	r.counts[kind]++
	c := &Canary{ID: fmt.Sprintf("%s-%s-%04d", r.scenario, kind, r.counts[kind]), Kind: kind, Scenario: r.scenario, Value: v}
	if len(where) > 0 {
		c.Table, c.Column, _ = strings.Cut(where[0], ".")
	}
	r.byName[k] = c
	r.list = append(r.list, c)
	return cval{c}, nil
}

// clashes reports whether v equals, contains or sits inside a value made
// earlier: one canary inside another would make a survivor of the outer
// one count twice.
func (r *renderer) clashes(v string) bool {
	lv := strings.ToLower(v)
	if r.values[lv] {
		return true
	}
	for lo := range r.values {
		if strings.Contains(lo, lv) || strings.Contains(lv, lo) {
			return true
		}
	}
	return false
}

func (r *renderer) key(name string) (string, error) {
	k := "key/" + name
	if c, ok := r.byName[k]; ok {
		return c.Value, nil
	}
	v, live, err := r.gen.key(name)
	if err != nil {
		return "", err
	}
	c := &Canary{ID: r.scenario + "-key-" + name, Kind: "key", Scenario: r.scenario, Value: v, Live: live}
	r.byName[k] = c
	r.list = append(r.list, c)
	return v, nil
}

// placed is a canary at a byte offset inside a rendered string.
type placed struct {
	c   *Canary
	off int
}

// cstr is a rendered string that still holds canaries as they are, such
// as the output of ser or sarr. It carries them on to the function that
// encodes it, so a canary serialized and then base64 encoded is recorded
// as base64 too.
type cstr struct {
	s      string
	inside []placed
}

func (v cstr) String() string { return v.s }

// collect prints args and lists every canary among them with its offset,
// canaries inside a cstr included.
func collect(args []any) (string, []placed) {
	var b strings.Builder
	var in []placed
	for _, a := range args {
		switch v := a.(type) {
		case cval:
			in = append(in, placed{v.c, b.Len()})
		case cstr:
			for _, p := range v.inside {
				in = append(in, placed{p.c, b.Len() + p.off})
			}
		}
		fmt.Fprint(&b, a)
	}
	return b.String(), in
}

// record adds enc to c's encodings. For base64 the name carries the
// canary's offset modulo 3.
func record(c *Canary, enc string, off int) {
	if enc == "base64" || enc == "base64url" {
		enc += "-" + strconv.Itoa(off%3)
	}
	if enc != "" && !slices.Contains(c.Encodings, enc) {
		c.Encodings = append(c.Encodings, enc)
	}
}

// join prints args and records enc on every canary among them. A canary
// held inside a cstr is recorded too, unless enc is raw: sql of a
// serialized canary plants it serialized, not as itself.
func join(enc string, args []any) string {
	s, in := collect(args)
	for _, p := range in {
		if enc == "raw" && !direct(p.c, args) {
			continue
		}
		record(p.c, enc, p.off)
	}
	return s
}

// direct reports whether c is one of args itself, not inside a cstr.
func direct(c *Canary, args []any) bool {
	for _, a := range args {
		if v, ok := a.(cval); ok && v.c == c {
			return true
		}
	}
	return false
}

// keep wraps s, rendered from args with prefix bytes before them, as a
// cstr when a canary sits inside it, so an outer encoding sees it.
func keep(s, prefix string, in []placed) any {
	if len(in) == 0 {
		return s
	}
	out := cstr{s: s}
	for _, p := range in {
		out.inside = append(out.inside, placed{p.c, len(prefix) + p.off})
	}
	return out
}

func jsonContents(s string) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}

func escapeEach(s string, keep func(r rune) bool, esc func(r rune) string) string {
	var b strings.Builder
	for _, r := range s {
		if keep(r) {
			b.WriteRune(r)
		} else {
			b.WriteString(esc(r))
		}
	}
	return b.String()
}

func alnumRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func jsonPHP(s string) string {
	return escapeEach(s, func(r rune) bool { return r >= 0x20 && r < 0x7f && r != '"' && r != '\\' && r != '/' }, func(r rune) string {
		if r == '"' || r == '\\' || r == '/' {
			return `\` + string(r)
		}
		if r > 0xffff {
			r -= 0x10000
			return fmt.Sprintf(`\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		}
		return fmt.Sprintf(`\u%04x`, r)
	})
}

// SQLEscape escapes s for a MySQL single-quoted string, as mysqldump does.
func SQLEscape(s string) string {
	return strings.NewReplacer("\\", `\\`, "'", `\'`, `"`, `\"`, "\x00", `\0`, "\n", `\n`, "\r", `\r`, "\x1a", `\Z`).Replace(s)
}

func (r *renderer) funcs() template.FuncMap {
	enc := func(name string, f func(string) string) func(...any) string {
		return func(args ...any) string { return f(join(name, args)) }
	}
	id := func(s string) string { return s }
	return template.FuncMap{
		"canary": r.canary,
		"key":    r.key,
		"raw":    enc("raw", id),
		"lower":  enc("lower", strings.ToLower),
		"upper":  enc("upper", strings.ToUpper),
		"json":   enc("json", jsonContents),
		"jsonslash": enc("json-slash", func(s string) string {
			return strings.ReplaceAll(jsonContents(s), "/", `\/`)
		}),
		"jsonphp": enc("json-php", jsonPHP),
		"jsonu": enc("json-unicode", func(s string) string {
			return escapeEach(s, alnumRune, func(r rune) string { return fmt.Sprintf(`\u%04x`, r) })
		}),
		"url":     enc("url", url.QueryEscape),
		"urlpath": enc("url-path", url.PathEscape),
		"urlall": enc("url-all", func(s string) string {
			var b strings.Builder
			for i := range len(s) {
				if alnumRune(rune(s[i])) {
					b.WriteByte(s[i])
				} else {
					fmt.Fprintf(&b, "%%%02X", s[i])
				}
			}
			return b.String()
		}),
		"html": enc("html", strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;").Replace),
		"htmlnum": enc("html-entity", func(s string) string {
			return escapeEach(s, alnumRune, func(r rune) string { return fmt.Sprintf("&#%d;", r) })
		}),
		"htmlhex": enc("html-hex", func(s string) string {
			return escapeEach(s, alnumRune, func(r rune) string { return fmt.Sprintf("&#x%x;", r) })
		}),
		"b64":    enc("base64", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }),
		"b64url": enc("base64url", func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }),
		"ser": func(args ...any) any {
			v, in := collect(args)
			for _, p := range in {
				record(p.c, "serialized", p.off)
			}
			prefix := "s:" + strconv.Itoa(len(v)) + `:"`
			return keep(prefix+v+`";`, prefix, in)
		},
		"md5": enc("md5", func(s string) string {
			m := md5.Sum([]byte(s)) // #nosec G401 -- planting the md5 form of a canary
			return hex.EncodeToString(m[:])
		}),
		"sha256": enc("sha256", func(s string) string {
			m := sha256.Sum256([]byte(s))
			return hex.EncodeToString(m[:])
		}),
		"sarr": func(kv ...any) (any, error) {
			if len(kv)%2 != 0 {
				return "", fmt.Errorf("sarr needs key and value pairs, got %d arguments", len(kv))
			}
			var b strings.Builder
			var in []placed
			fmt.Fprintf(&b, "a:%d:{", len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k := fmt.Sprint(kv[i])
				fmt.Fprintf(&b, `s:%d:"%s";`, len(k), k)
				v, inside := collect(kv[i+1 : i+2])
				for _, p := range inside {
					in = append(in, placed{p.c, b.Len() + p.off})
				}
				b.WriteString(v)
			}
			b.WriteByte('}')
			return keep(b.String(), "", in), nil
		},
		// A canary straight inside sql is planted as itself.
		"sql":    func(args ...any) string { return SQLEscape(join("raw", args)) },
		"add":    func(a, b int) int { return a + b },
		"sub":    func(a, b int) int { return a - b },
		"mul":    func(a, b int) int { return a * b },
		"repeat": func(n int, s string) string { return strings.Repeat(s, max(n, 0)) },
		"mod":    func(a, b int) int { return a % b },
		"cr":     func() string { return "\r" },
		"bom":    func() string { return "\ufeff" },
		"list":   func(args ...any) []any { return args },
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict needs key and value pairs, got %d arguments", len(kv))
			}
			m := map[string]any{}
			for i := 0; i < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m, nil
		},
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
	}
}
