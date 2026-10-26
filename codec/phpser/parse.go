// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package phpser

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// MaxDepth is the most arrays and objects nested inside each other that
// the walker follows. PHP allows 4096 by default; real site data never
// comes close and a lower limit keeps the recursion cheap.
const MaxDepth = 512

// Reasons a value is invalid that Roadie treats differently from PHP on
// purpose. Both wrap ErrInvalid, so callers only need errors.Is(err,
// ErrInvalid); the differential test uses them to skip known differences.
var (
	errTooDeep = errors.New("nested deeper than 512 levels")
	errEscaped = errors.New("escaped S: strings are not supported")
	errBadByte = errors.New("unexpected byte")
	errTrail   = errors.New("bytes after the value")
	// errSeparator is a missing ":{" after an object's count, which PHP
	// before 8.3 didn't check.
	errSeparator = errors.New(`no ":{" after the count`)
)

// parseError says where the bytes stopped being serialized data.
type parseError struct {
	pos int
	why error
}

func (e *parseError) Error() string {
	return fmt.Sprintf("%s at byte %d: %s", ErrInvalid, e.pos, e.why)
}

func (e *parseError) Unwrap() []error { return []error{ErrInvalid, e.why} }

// slot types, for the r: and R: checks PHP makes.
const (
	tOther  uint8 = iota // any value that isn't an object
	tObject              // O:, C:, E: and r: values
	tUndef               // a bucket whose new value is still being read
)

// parser walks one serialized value. It never builds a tree: Valid only
// moves pos, Leaves calls walk, Rewrite copies the bytes between changed
// leaves into out.
type parser struct {
	b     []byte
	pos   int
	depth int
	why   error
	at    int

	fn   LeafFunc                                   // Rewrite
	walk func(path string, leaf []byte, isKey bool) // Leaves
	path []byte
	// pathStr is string(path) when pathOK; a key and its value share one
	// path, so the string is built once for both.
	pathStr string
	pathOK  bool
	out     []byte
	last    int   // first byte of b not yet copied to out
	err     error // the error fn returned

	// PHP numbers every value except R: in the order it reads them; r:
	// and R: name such a slot. A slot points at the bucket (array element
	// or property) the value lives in; a later duplicate key puts a new
	// value into that same bucket. refs is off when b holds no "r:"
	// or "R:" at all, which is nearly always, so no slot is tracked then.
	refs    bool
	slots   []int32
	buckets []uint8

	// onEnum sees the span of every E: value; tests use it to swap enums
	// for objects before asking PHP, which can't load an unknown enum.
	onEnum func(start, end int)
	// scalar sees every b: and i: value whole (RewriteScalars).
	scalar func(path string, tok []byte) ([]byte, bool)
}

func (p *parser) fail(why error) bool {
	if p.why == nil {
		p.why, p.at = why, p.pos
	}
	return false
}

func (p *parser) failure() error {
	if p.err != nil {
		return p.err
	}
	return &parseError{pos: p.at, why: p.why}
}

// run parses one value from the start of b and reports how many bytes it
// used. Bytes after the value are the caller's business.
func (p *parser) run(b []byte) (int, error) {
	p.b = b
	p.refs = bytes.Contains(b, []byte("r:")) || bytes.Contains(b, []byte("R:"))
	if p.refs {
		p.buckets = append(p.buckets, tUndef)
	}
	if !p.value(0) {
		return 0, p.failure()
	}
	return p.pos, nil
}

// whole parses b as exactly one value. PHP itself ignores bytes after the
// value; a field with more bytes after it isn't one serialized value, so
// Roadie treats it as text and leaves it alone.
func (p *parser) whole(b []byte) error {
	n, err := p.run(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		p.pos = n
		p.fail(errTrail)
		return p.failure()
	}
	if p.out != nil {
		p.out = append(p.out, b[p.last:]...)
	}
	return nil
}

func (p *parser) lit(s string) bool {
	if len(p.b)-p.pos < len(s) || string(p.b[p.pos:p.pos+len(s)]) != s {
		return p.fail(errBadByte)
	}
	p.pos += len(s)
	return true
}

func (p *parser) peek(c byte) bool { return p.pos < len(p.b) && p.b[p.pos] == c }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// uiv reads [0-9]+ the way PHP's parse_uiv does: as a size_t that wraps
// on overflow. PHP really accepts s:18446744073709551619:"abc"; as a
// three-byte string, so Roadie does too.
func (p *parser) uiv() (uint64, bool) {
	start := p.pos
	var n uint64
	for p.pos < len(p.b) && isDigit(p.b[p.pos]) {
		n = n*10 + uint64(p.b[p.pos]-'0')
		p.pos++
	}
	if p.pos == start {
		return 0, p.fail(errBadByte)
	}
	return n, true
}

// iv reads [+-]?[0-9]* the way PHP's parse_iv2 does: leading zeros are
// skipped and a number out of range clamps to the int64 limit. need says
// whether at least one digit is required.
func (p *parser) iv(need bool) (int64, bool) {
	neg := false
	if p.peek('-') {
		neg = true
		p.pos++
	} else if p.peek('+') {
		p.pos++
	}
	any := false
	for p.peek('0') {
		p.pos++
		any = true
	}
	start := p.pos
	var n uint64
	for p.pos < len(p.b) && isDigit(p.b[p.pos]) {
		n = n*10 + uint64(p.b[p.pos]-'0')
		p.pos++
	}
	if need && !any && p.pos == start {
		return 0, p.fail(errBadByte)
	}
	limit := uint64(math.MaxInt64)
	if neg {
		limit++
	}
	if p.pos-start > 19 || n > limit {
		if neg {
			return math.MinInt64, true
		}
		return math.MaxInt64, true
	}
	if neg {
		return -int64(n), true //nolint:gosec // n is at most 2^63 here, which wraps to MinInt64 as PHP's does
	}
	return int64(n), true
}

// remain is how many bytes are left, as a uint64 for comparing with
// declared lengths before they are used.
func (p *parser) remain() uint64 { return uint64(len(p.b) - p.pos) } //nolint:gosec // pos never passes len(b)

// value parses the value whose slot points at bucket.
func (p *parser) value(bucket int32) bool {
	if p.pos >= len(p.b) {
		return p.fail(errBadByte)
	}
	c := p.b[p.pos]
	if p.refs {
		if c != 'R' {
			p.slots = append(p.slots, bucket)
		}
		p.buckets[bucket] = tUndef
	}
	t := tOther
	var ok bool
	switch c {
	case 'N':
		ok = p.lit("N;")
	case 'b':
		start := p.pos
		ok = p.lit("b:") && p.pos+1 < len(p.b) && (p.b[p.pos] == '0' || p.b[p.pos] == '1') && p.b[p.pos+1] == ';'
		if ok {
			p.pos += 2
			p.scalarLeaf(start)
		} else {
			p.fail(errBadByte)
		}
	case 'i':
		start := p.pos
		if ok = p.integer(); ok {
			p.scalarLeaf(start)
		}
	case 'd':
		ok = p.float()
	case 's':
		_, ok = p.str(false)
	case 'S':
		return p.fail(errEscaped)
	case 'a':
		if p.refs {
			p.buckets[bucket] = tOther
		}
		ok = p.array()
	case 'O', 'C':
		if p.refs {
			p.buckets[bucket] = tObject
		}
		t = tObject
		ok = p.object(c == 'C')
	case 'E':
		t = tObject
		ok = p.enum()
	case 'r', 'R':
		t, ok = p.ref(c == 'r')
	default:
		return p.fail(errBadByte)
	}
	if ok && p.refs {
		p.buckets[bucket] = t
	}
	return ok
}

// scalarLeaf hands the b: or i: value from start to pos to the scalar
// function and splices in what it returns.
func (p *parser) scalarLeaf(start int) {
	if p.scalar == nil {
		return
	}
	if !p.pathOK {
		p.pathStr, p.pathOK = string(p.path), true
	}
	tok := p.b[start:p.pos]
	out, changed := p.scalar(p.pathStr, tok)
	if !changed || bytes.Equal(out, tok) {
		return
	}
	if p.out == nil {
		p.out = make([]byte, 0, len(p.b)+len(out))
	}
	p.out = append(p.out, p.b[p.last:start]...)
	p.out = append(p.out, out...)
	p.last = p.pos
}

func (p *parser) integer() bool {
	if !p.lit("i:") {
		return false
	}
	if p.peek('+') || p.peek('-') {
		p.pos++
	}
	start := p.pos
	for p.pos < len(p.b) && isDigit(p.b[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return p.fail(errBadByte)
	}
	return p.lit(";")
}

// float accepts what PHP's d: rule does: NAN, INF, -INF, or a decimal
// with an optional sign, an optional fraction and an optional exponent.
func (p *parser) float() bool {
	if !p.lit("d:") {
		return false
	}
	for _, s := range []string{"NAN;", "INF;", "-INF;"} {
		if bytes.HasPrefix(p.b[p.pos:], []byte(s)) {
			p.pos += len(s)
			return true
		}
	}
	if p.peek('+') || p.peek('-') {
		p.pos++
	}
	digits := p.digits()
	if p.peek('.') {
		p.pos++
		digits += p.digits()
	}
	if digits == 0 {
		return p.fail(errBadByte)
	}
	if p.peek('e') || p.peek('E') {
		p.pos++
		if p.peek('+') || p.peek('-') {
			p.pos++
		}
		if p.digits() == 0 {
			return p.fail(errBadByte)
		}
	}
	return p.lit(";")
}

func (p *parser) digits() int {
	start := p.pos
	for p.pos < len(p.b) && isDigit(p.b[p.pos]) {
		p.pos++
	}
	return p.pos - start
}

// quoted reads <len>:"<bytes>" after the type letter and its colon and
// returns the bytes. The length is checked against what is left before
// anything is read.
func (p *parser) quoted() ([]byte, bool) {
	n, ok := p.uiv()
	if !ok || !p.lit(":\"") {
		return nil, false
	}
	if n > p.remain() {
		return nil, p.fail(errBadByte)
	}
	start := p.pos
	p.pos += int(n) //nolint:gosec // n is at most the bytes left, so it fits in an int
	if !p.peek('"') {
		return nil, p.fail(errBadByte)
	}
	p.pos++
	return p.b[start : p.pos-1 : p.pos-1], true
}

// str parses s:<len>:"<bytes>"; and hands the bytes to the leaf function.
// It returns the bytes for keys.
func (p *parser) str(isKey bool) ([]byte, bool) {
	start := p.pos
	if !p.lit("s:") {
		return nil, false
	}
	leaf, ok := p.quoted()
	if !ok || !p.lit(";") {
		return nil, false
	}
	if p.walk == nil && p.fn == nil {
		return leaf, true
	}
	if !p.pathOK {
		p.pathStr, p.pathOK = string(p.path), true
	}
	if p.walk != nil {
		p.walk(p.pathStr, leaf, isKey)
	}
	if p.fn != nil {
		out, changed, err := p.fn(p.pathStr, leaf, isKey)
		if err != nil {
			p.err = err
			return nil, false
		}
		// A leaf handed back unchanged keeps its original prefix, so an
		// identity fn returns the input byte for byte.
		if changed && !bytes.Equal(out, leaf) {
			if p.out == nil {
				p.out = make([]byte, 0, len(p.b)+len(out)+16)
			}
			p.out = append(p.out, p.b[p.last:start]...)
			p.out = append(p.out, "s:"...)
			p.out = strconv.AppendInt(p.out, int64(len(out)), 10)
			p.out = append(p.out, ":\""...)
			p.out = append(p.out, out...)
			p.out = append(p.out, "\";"...)
			p.last = p.pos
		}
	}
	return leaf, true
}

// key reads an array key or property name: only i: and s: are keys. It
// returns the segment for the path and the key as PHP stores it, which
// the reference tracking needs.
func (p *parser) key(object bool, parent int) (canon string, ok bool) {
	if p.pos >= len(p.b) {
		return "", p.fail(errBadByte)
	}
	switch p.b[p.pos] {
	case 'i':
		start := p.pos + 2
		if !p.integer() {
			return "", false
		}
		q := parser{b: p.b[:p.pos-1], pos: start}
		n, _ := q.iv(true)
		if p.fn != nil || p.walk != nil {
			p.pathOK = false
			p.path = append(p.path, '[')
			p.path = strconv.AppendInt(p.path, n, 10)
			p.path = append(p.path, ']')
		}
		if p.refs {
			return strconv.FormatInt(n, 10), true
		}
		return "", true
	case 's':
		// The key goes into the path before the leaf function sees it,
		// so a key and its value share one path.
		save := p.pos
		q := parser{b: p.b, pos: p.pos + 2}
		raw, ok := q.quoted()
		if !ok {
			p.pos = q.pos
			return "", p.fail(errBadByte)
		}
		if p.fn != nil || p.walk != nil {
			p.pathOK = false
			p.segment(raw, object, parent)
		}
		p.pos = save
		if _, ok := p.str(true); !ok {
			return "", false
		}
		if p.refs {
			if n, num := numericKey(raw); num {
				return strconv.FormatInt(n, 10), true
			}
			return "s" + string(raw), true
		}
		return "", true
	case 'S':
		return "", p.fail(errEscaped)
	}
	return "", p.fail(errBadByte)
}

// segment appends a string key to the path: a property's visible name
// (PHP stores protected ones as \0*\0name and private ones as
// \0Class\0name), with any '.' escaped as '\.'.
func (p *parser) segment(raw []byte, object bool, parent int) {
	if object && len(raw) > 0 && raw[0] == 0 {
		if i := bytes.IndexByte(raw[1:], 0); i >= 0 {
			raw = raw[i+2:]
		}
	}
	if parent > 0 {
		p.path = append(p.path, '.')
	}
	for _, c := range raw {
		if c == '.' {
			p.path = append(p.path, '\\')
		}
		p.path = append(p.path, c)
	}
}

// numericKey reports whether PHP turns a string key into an integer key:
// "0" or an optional '-' and digits without a leading zero, within int64.
func numericKey(s []byte) (int64, bool) {
	d := s
	if len(d) > 0 && d[0] == '-' {
		d = d[1:]
	}
	if len(d) == 0 || len(d) > 19 || (d[0] == '0' && (len(d) > 1 || len(s) > 1)) {
		return 0, false
	}
	for _, c := range d {
		if !isDigit(c) {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(string(s), 10, 64)
	return n, err == nil
}

// elements reads count key and value pairs and the closing brace.
func (p *parser) elements(count int64, object bool) bool {
	if count < 0 || uint64(count) > p.remain() {
		return p.fail(errBadByte)
	}
	p.depth++
	if p.depth > MaxDepth {
		return p.fail(errTooDeep)
	}
	var seen map[string]int32
	for range count {
		parent := len(p.path)
		canon, ok := p.key(object, parent)
		if !ok {
			return false
		}
		bucket := int32(-1)
		if p.refs {
			if seen == nil {
				seen = make(map[string]int32)
			}
			if b, dup := seen[canon]; dup {
				bucket = b
			} else {
				bucket = int32(len(p.buckets)) //nolint:gosec // one bucket per element read, far below 2^31
				p.buckets = append(p.buckets, tUndef)
				seen[canon] = bucket
			}
		}
		if !p.value(bucket) {
			return false
		}
		if len(p.path) != parent {
			p.path, p.pathOK = p.path[:parent], false
		}
	}
	p.depth--
	return p.lit("}")
}

// array parses a:<count>:{...}. Unlike objects, PHP reads the count with
// its strict grammar: digits only, no sign.
func (p *parser) array() bool {
	if !p.lit("a:") {
		return false
	}
	start := p.pos
	if _, ok := p.uiv(); !ok {
		return false
	}
	q := parser{b: p.b[:p.pos], pos: start}
	count, _ := q.iv(true)
	if !p.lit(":{") {
		return false
	}
	return p.elements(count, false)
}

// className reads <len>:"<name>": and checks the name the way PHP does:
// not empty, [A-Za-z0-9_\\] or a byte of 0x80 and above, no leading '\'.
func (p *parser) className() bool {
	name, ok := p.quoted()
	if !ok {
		return false
	}
	if len(name) == 0 || name[0] == '\\' {
		return p.fail(errBadByte)
	}
	for _, c := range name {
		if !classByte(c) {
			return p.fail(errBadByte)
		}
	}
	return p.lit(":")
}

func classByte(c byte) bool {
	return c >= 0x80 || c == '_' || c == '\\' || isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// object parses O:<len>:"<class>":<count>:{...} and, when custom,
// C:<len>:"<class>":<len>:{<bytes>}, whose bytes stay opaque. PHP reads
// the count with parse_iv2 (sign and leading zeros allowed, even no
// digits). PHP before 8.3 then skipped the two bytes after the count
// without looking at them; 8.3 requires ":{" there and so does Roadie.
func (p *parser) object(custom bool) bool {
	p.pos++ // 'O' or 'C', seen by value
	if !p.lit(":") || !p.className() {
		return false
	}
	n, ok := p.iv(false)
	if !ok {
		return false
	}
	if !bytes.HasPrefix(p.b[p.pos:], []byte(":{")) {
		return p.fail(errSeparator)
	}
	p.pos += 2
	if !custom {
		return p.elements(n, true)
	}
	if n < 0 || uint64(n) >= p.remain() {
		return p.fail(errBadByte)
	}
	p.pos += int(n)
	return p.lit("}")
}

// enum parses E:<len>:"<Enum>:<Case>";. PHP needs the enum's class
// loaded to accept one; Roadie has no classes, so any well-formed name
// passes.
func (p *parser) enum() bool {
	start := p.pos
	if !p.lit("E:") {
		return false
	}
	name, ok := p.quoted()
	if !ok || !p.lit(";") {
		return false
	}
	i := bytes.IndexByte(name, ':')
	if i <= 0 || i == len(name)-1 {
		p.pos = start
		return p.fail(errBadByte)
	}
	if p.onEnum != nil {
		p.onEnum(start, p.pos)
	}
	return true
}

// ref parses r:<n>; (an object handle, which must point at an object) and
// R:<n>; (a PHP reference to any value already read).
func (p *parser) ref(object bool) (uint8, bool) {
	p.pos++ // 'r' or 'R', seen by value
	if !p.lit(":") {
		return 0, false
	}
	n, ok := p.uiv()
	if !ok || !p.lit(";") {
		return 0, false
	}
	if !p.refs {
		return 0, p.fail(errBadByte) // unreachable: refs is on whenever r: or R: is present
	}
	id := n - 1
	if n == 0 || id >= uint64(len(p.slots)) {
		return 0, p.fail(errBadByte)
	}
	t := p.buckets[p.slots[id]]
	if t == tUndef || (object && t != tObject) {
		return 0, p.fail(errBadByte)
	}
	return t, true
}
