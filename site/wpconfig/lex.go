// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tokKind is the kind of one lexical token. The lexer knows only what it
// needs to find define() calls and safe places to insert one: it never
// evaluates anything.
type tokKind int

const (
	tHTML     tokKind = iota // text outside <?php ... ?>
	tOpen                    // <?php or <?=
	tClose                   // ?> (with the one newline PHP eats after it)
	tSpace                   // whitespace
	tComment                 // //, # and /* */
	tString                  // '...' and "..."
	tHeredoc                 // <<<ID ... ID and <<<'ID' ... ID
	tBacktick                // `...`, a shell command: never a literal
	tIdent                   // a name, possibly namespaced (\define)
	tVar                     // $name
	tNumber                  // 12, 0x1F, 1.5e3
	tPunct                   // anything else, one or more bytes
)

// token is one token of the source. start and end are byte offsets.
type token struct {
	kind       tokKind
	start, end int
	// value is the decoded content of a literal string or heredoc;
	// literal is false when the content interpolates variables or holds
	// escapes this reader doesn't decode.
	value   string
	literal bool
	quote   byte // '\'' or '"' for tString
}

// errSyntax is wrapped by every lexer error.
var errSyntax = errors.New("wp-config.php: unreadable PHP")

// lex splits src into tokens. It fails only on a construct that never
// ends (a comment, string or heredoc running to the end of the file),
// since an edit after one would land inside it.
func lex(src []byte) ([]token, error) {
	var toks []token
	i, n := 0, len(src)
	php := false
	for i < n {
		if !php {
			j := findOpenTag(src, i)
			if j < 0 {
				toks = append(toks, token{kind: tHTML, start: i, end: n})
				break
			}
			if j > i {
				toks = append(toks, token{kind: tHTML, start: i, end: j})
			}
			k := j + 2
			if k < n && src[k] == '=' {
				k++
			} else {
				k += 3 // "php"
			}
			toks = append(toks, token{kind: tOpen, start: j, end: k})
			i, php = k, true
			continue
		}
		c := src[i]
		switch {
		case c == '?' && i+1 < n && src[i+1] == '>':
			k := i + 2
			if k < n && src[k] == '\n' {
				k++
			} else if k+1 < n && src[k] == '\r' && src[k+1] == '\n' {
				k += 2
			}
			toks = append(toks, token{kind: tClose, start: i, end: k})
			i, php = k, false
		case isSpace(c):
			k := i
			for k < n && isSpace(src[k]) {
				k++
			}
			toks = append(toks, token{kind: tSpace, start: i, end: k})
			i = k
		case c == '#' || c == '/' && i+1 < n && src[i+1] == '/':
			// A line comment ends at the line end or at ?>, which closes PHP.
			k := i + 1
			for k < n && src[k] != '\n' && src[k] != '\r' && (src[k] != '?' || k+1 >= n || src[k+1] != '>') {
				k++
			}
			toks = append(toks, token{kind: tComment, start: i, end: k})
			i = k
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("%w: a /* comment at line %d never ends", errSyntax, lineAt(src, i))
			}
			k := i + 2 + end + 2
			toks = append(toks, token{kind: tComment, start: i, end: k})
			i = k
		case c == '\'':
			t, err := lexSingle(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
			i = t.end
		case c == '"':
			t, err := lexDouble(src, i, '"')
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
			i = t.end
		case c == '`':
			t, err := lexDouble(src, i, '`')
			if err != nil {
				return nil, err
			}
			t.kind, t.literal = tBacktick, false
			toks = append(toks, t)
			i = t.end
		case c == '<' && bytes.HasPrefix(src[i:], []byte("<<<")):
			t, ok, err := lexHeredoc(src, i)
			if err != nil {
				return nil, err
			}
			if ok {
				toks = append(toks, t)
				i = t.end
				continue
			}
			toks = append(toks, token{kind: tPunct, start: i, end: i + 3})
			i += 3
		case c == '$' && i+1 < n && isIdentStart(src[i+1]):
			k := i + 2
			for k < n && isIdentByte(src[k]) {
				k++
			}
			toks = append(toks, token{kind: tVar, start: i, end: k})
			i = k
		case isIdentStart(c) || c == '\\' && i+1 < n && isIdentStart(src[i+1]):
			k := i + 1
			for k < n && (isIdentByte(src[k]) || src[k] == '\\' && k+1 < n && isIdentStart(src[k+1])) {
				k++
			}
			toks = append(toks, token{kind: tIdent, start: i, end: k})
			i = k
		case c >= '0' && c <= '9' || c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9':
			k := i + 1
			for k < n && (isIdentByte(src[k]) || src[k] == '.' ||
				(src[k] == '+' || src[k] == '-') && (src[k-1] == 'e' || src[k-1] == 'E') && !isHexPrefix(src[i:k])) {
				k++
			}
			toks = append(toks, token{kind: tNumber, start: i, end: k})
			i = k
		default:
			k := i + 1
			// Two-byte operators the parser looks at as one token.
			if k < n {
				switch string(src[i : k+1]) {
				case "->", "::", "=>", "==", "!=", "<=", ">=", "||", "&&", "??":
					k++
				}
			}
			toks = append(toks, token{kind: tPunct, start: i, end: k})
			i = k
		}
	}
	return toks, nil
}

// findOpenTag finds the next <?php (followed by whitespace or the end)
// or <?= at or after i.
func findOpenTag(src []byte, i int) int {
	for {
		j := bytes.Index(src[i:], []byte("<?"))
		if j < 0 {
			return -1
		}
		j += i
		rest := src[j+2:]
		if len(rest) > 0 && rest[0] == '=' {
			return j
		}
		if len(rest) >= 3 && strings.EqualFold(string(rest[:3]), "php") && (len(rest) == 3 || isSpace(rest[3])) {
			return j
		}
		i = j + 2
	}
}

func lexSingle(src []byte, i int) (token, error) {
	var b strings.Builder
	for k := i + 1; k < len(src); k++ {
		switch src[k] {
		case '\\':
			if k+1 < len(src) && (src[k+1] == '\\' || src[k+1] == '\'') {
				b.WriteByte(src[k+1])
				k++
				continue
			}
			b.WriteByte('\\')
		case '\'':
			return token{kind: tString, start: i, end: k + 1, value: b.String(), literal: true, quote: '\''}, nil
		default:
			b.WriteByte(src[k])
		}
	}
	return token{}, fmt.Errorf("%w: a string at line %d never ends", errSyntax, lineAt(src, i))
}

// lexDouble reads a double-quoted (or backtick) string and decodes its
// escapes. A '$' that starts a variable makes it not literal.
func lexDouble(src []byte, i int, q byte) (token, error) {
	var b strings.Builder
	literal := true
	for k := i + 1; k < len(src); k++ {
		c := src[k]
		switch {
		case c == q:
			return token{kind: tString, start: i, end: k + 1, value: b.String(), literal: literal, quote: '"'}, nil
		case c == '$' && k+1 < len(src) && (isIdentStart(src[k+1]) || src[k+1] == '{'):
			literal = false
			b.WriteByte(c)
		case c == '{' && k+1 < len(src) && src[k+1] == '$':
			literal = false
			b.WriteByte(c)
		case c == '\\' && k+1 < len(src):
			k += decodeEscape(src, k, q, &b) - 1
		default:
			b.WriteByte(c)
		}
	}
	return token{}, fmt.Errorf("%w: a string at line %d never ends", errSyntax, lineAt(src, i))
}

// decodeEscape writes the escape at src[k] ('\\') to b and returns how
// many bytes it used. An unknown escape keeps its backslash, as PHP does.
func decodeEscape(src []byte, k int, q byte, b *strings.Builder) int {
	e := src[k+1]
	simple := map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', 'v': '\v', 'e': 0x1b, 'f': '\f', '\\': '\\', '$': '$'}
	if r, ok := simple[e]; ok {
		b.WriteByte(r)
		return 2
	}
	if e == q {
		b.WriteByte(q)
		return 2
	}
	switch {
	case e >= '0' && e <= '7':
		j := k + 1
		for j < len(src) && j < k+4 && src[j] >= '0' && src[j] <= '7' {
			j++
		}
		v, _ := strconv.ParseUint(string(src[k+1:j]), 8, 16)
		b.WriteByte(byte(v)) // #nosec G115 -- PHP keeps the low byte of an octal escape over \377
		return j - k
	case e == 'x' && k+2 < len(src) && isHex(src[k+2]):
		j := k + 2
		for j < len(src) && j < k+4 && isHex(src[j]) {
			j++
		}
		v, _ := strconv.ParseUint(string(src[k+2:j]), 16, 8)
		b.WriteByte(byte(v))
		return j - k
	case e == 'u' && k+2 < len(src) && src[k+2] == '{':
		end := bytes.IndexByte(src[k+3:], '}')
		if end > 0 {
			v, err := strconv.ParseUint(string(src[k+3:k+3+end]), 16, 32)
			if err == nil && v <= utf8.MaxRune {
				b.WriteRune(rune(v))
				return 3 + end + 1
			}
		}
	}
	b.WriteByte('\\')
	return 1
}

// lexHeredoc reads <<<ID, <<<"ID" or <<<'ID' up to its closing line. ok
// is false when src[i:] is not a heredoc start at all (a shift operator).
func lexHeredoc(src []byte, i int) (token, bool, error) {
	k := i + 3
	for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
		k++
	}
	quote := byte(0)
	if k < len(src) && (src[k] == '\'' || src[k] == '"') {
		quote = src[k]
		k++
	}
	idStart := k
	if k >= len(src) || !isIdentStart(src[k]) {
		return token{}, false, nil
	}
	for k < len(src) && isIdentByte(src[k]) {
		k++
	}
	id := string(src[idStart:k])
	if quote != 0 {
		if k >= len(src) || src[k] != quote {
			return token{}, false, nil
		}
		k++
	}
	switch {
	case k < len(src) && src[k] == '\n':
		k++
	case k+1 < len(src) && src[k] == '\r' && src[k+1] == '\n':
		k += 2
	case k < len(src) && src[k] == '\r':
		k++
	default:
		return token{}, false, nil
	}
	bodyStart := k
	for line := k; line <= len(src); {
		end := line
		for end < len(src) && src[end] != '\n' && src[end] != '\r' {
			end++
		}
		ind := line
		for ind < end && (src[ind] == ' ' || src[ind] == '\t') {
			ind++
		}
		if bytes.HasPrefix(src[ind:end], []byte(id)) && (ind+len(id) == len(src) || !isIdentByte(src[ind+len(id)])) {
			body := ""
			if line > bodyStart {
				body = string(src[bodyStart:line])
				body = strings.TrimSuffix(strings.TrimSuffix(body, "\n"), "\r")
			}
			indent := string(src[line:ind])
			value, literal := heredocValue(body, indent, quote == '\'')
			return token{kind: tHeredoc, start: i, end: ind + len(id), value: value, literal: literal}, true, nil
		}
		if end >= len(src) {
			break
		}
		if src[end] == '\r' && end+1 < len(src) && src[end+1] == '\n' {
			end++
		}
		line = end + 1
	}
	return token{}, false, fmt.Errorf("%w: a heredoc at line %d never ends", errSyntax, lineAt(src, i))
}

// heredocValue removes the closing line's indentation from every line. A
// heredoc (not a nowdoc) with a '$' or a backslash is not taken as a
// literal: this reader doesn't decode it.
func heredocValue(body, indent string, nowdoc bool) (string, bool) {
	if indent != "" {
		lines := strings.SplitAfter(body, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(l, indent)
		}
		body = strings.Join(lines, "")
	}
	if !nowdoc && strings.ContainsAny(body, "$\\") {
		return body, false
	}
	return body, true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 0x80
}

func isIdentByte(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func isHexPrefix(b []byte) bool { return len(b) >= 2 && b[0] == '0' && (b[1] == 'x' || b[1] == 'X') }

// lineAt is the 1-based line of offset i, counting \n, \r\n and a lone \r.
func lineAt(src []byte, i int) int {
	line := 1
	for k := 0; k < i && k < len(src); k++ {
		if src[k] == '\n' || src[k] == '\r' && (k+1 >= len(src) || src[k+1] != '\n') {
			line++
		}
	}
	return line
}
