// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"sort"
	"strings"
)

// Define is one define() call as the file holds it.
type Define struct {
	Name  string
	Value string // decoded; "true" or "false" for a bool, the digits for a number
	// Literal is false when the value is built from getenv(), $_ENV,
	// $_SERVER, another constant or any other expression: Roadie can't
	// know it without running PHP, so Value is the source text.
	Literal bool
	Quoted  bool // a string, not a bool or number
	// Conditional is true inside an if, a block, after "or" or anywhere
	// PHP may skip the call.
	Conditional bool
	// BeforeSettings is true when the call comes before the
	// require_once of wp-settings.php (or the file has none), which is
	// when WordPress still reads it.
	BeforeSettings bool
	Line           int
}

// def is a Define plus the offsets an edit needs.
type def struct {
	Define
	quote            byte // '\'' or '"' when Quoted
	callStart        int
	valStart, valEnd int  // the value's bytes; valStart < 0 when there is none
	stmt             *gap // the safe gap before the top-level statement holding the call
	// members are the string literals inside a value built from an
	// expression, such as serialize( array( 'key' => 'value' ) ).
	members []member
}

// member is one string literal inside a define's expression.
type member struct {
	key        string // the array key before "=>", "" when there is none
	value      string
	start, end int // the literal's bytes, quotes included
	quote      byte
	line       int
}

// readMembers finds the string literals of an expression value, each with
// the array key before it. A key itself is not a member.
func readMembers(toks []token, val []int, text func(int) string) []member {
	var out []member
	for j, ti := range val {
		t := toks[ti]
		if t.kind != tString || !t.literal {
			continue
		}
		if j+1 < len(val) && toks[val[j+1]].kind == tPunct && text(val[j+1]) == "=>" {
			continue
		}
		m := member{value: t.value, start: t.start, end: t.end, quote: t.quote}
		if j >= 2 && toks[val[j-1]].kind == tPunct && text(val[j-1]) == "=>" {
			if k := toks[val[j-2]]; k.kind == tString && k.literal {
				m.key = k.value
			} else if k.kind == tNumber {
				m.key = text(val[j-2])
			}
		}
		out = append(out, m)
	}
	return out
}

// gap is the space between two top-level statements, where a new define
// can go: from the end of one statement to the start of the next.
type gap struct {
	start, end int
	comments   []token // comments in the gap
	next       int     // index of the next significant token, -1 at the end
}

// parsed is what parse finds in one file.
type parsed struct {
	toks        []token
	defs        []def
	gaps        []*gap
	settings    *gap // before require_once ... wp-settings.php
	settingsAt  int  // its offset, -1 when there is none
	stopEditing *gap // the gap holding the "stop editing" comment
	stopAt      int  // that comment's offset
	end         *gap // the safe gap before the end of the PHP code
	prefix      string
	prefixOK    bool
	lines       []int // offset of each line start
}

// controlWords open a statement whose body may be a block, a ':' block
// or one statement.
var controlWords = map[string]bool{"if": true, "elseif": true, "while": true, "for": true, "foreach": true, "switch": true, "declare": true}

// altOpeners are the controls whose "(...):" opens an alternative-syntax
// block closed by an end keyword. elseif and else continue one.
var altOpeners = map[string]bool{"if": true, "while": true, "for": true, "foreach": true, "switch": true, "declare": true}

var altEnds = map[string]bool{"endif": true, "endwhile": true, "endfor": true, "endforeach": true, "endswitch": true, "enddeclare": true}

// notAfterBlock are words that continue the statement a '}' or ';' ends,
// so nothing can be inserted before them.
var notAfterBlock = map[string]bool{"else": true, "elseif": true, "catch": true, "finally": true, "while": true}

func parse(src []byte) (*parsed, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parsed{toks: toks, settingsAt: -1, stopAt: -1, lines: lineStarts(src)}
	sig := make([]int, 0, len(toks))
	for i, t := range toks {
		if t.kind != tSpace && t.kind != tComment {
			sig = append(sig, i)
		}
	}
	text := func(i int) string { return string(src[toks[i].start:toks[i].end]) }
	word := func(i int) string {
		if i < 0 || toks[i].kind != tIdent {
			return ""
		}
		return strings.ToLower(strings.TrimPrefix(text(i), "\\"))
	}
	punct := func(i int, s string) bool { return i >= 0 && toks[i].kind == tPunct && text(i) == s }

	var (
		brace, alt, paren int
		pending           = []int{0} // brace-less bodies waiting for their ';', per brace depth
		controlParen      = -1       // paren depth at which a control's '(' opened
		control           string     // the control word whose '(' or body is next
		afterControl      bool       // the next token starts a control's body
		lastSafe          *gap
	)
	commentsFrom := 0 // first token index after the previous significant token
	lastOpen := -1    // the last <?php: a ?> after it ends the PHP code
	for i, t := range toks {
		if t.kind == tOpen {
			lastOpen = i
		}
	}
	for si := 0; si <= len(sig); si++ {
		ti := -1
		if si < len(sig) {
			ti = sig[si]
		}
		prev := -1
		if si > 0 {
			prev = sig[si-1]
		}
		// Is the space before this token a safe place for a statement?
		safe := brace == 0 && alt == 0 && paren == 0 && pending[0] == 0 && !afterControl && control == ""
		if safe {
			switch {
			case prev < 0:
				safe = false // before any <?php
			case toks[prev].kind == tOpen:
			case punct(prev, ";"):
				safe = !notAfterBlock[word(ti)] || word(ti) == "while"
			case punct(prev, "}"):
				safe = ti < 0 || toks[ti].kind == tClose || (toks[ti].kind == tIdent || toks[ti].kind == tVar) && !notAfterBlock[word(ti)]
			default:
				safe = false
			}
			if ti >= 0 && toks[ti].kind == tHTML {
				safe = false
			}
		}
		end := len(src)
		if ti >= 0 {
			end = toks[ti].start
		}
		start := 0
		if prev >= 0 {
			start = toks[prev].end
		}
		var g *gap
		if safe {
			g = &gap{start: start, end: end, next: ti}
			for k := commentsFrom; k < len(toks) && (ti < 0 || k < ti); k++ {
				if toks[k].kind == tComment {
					g.comments = append(g.comments, toks[k])
				}
			}
			p.gaps = append(p.gaps, g)
			lastSafe = g
			for _, c := range g.comments {
				if p.stopEditing == nil && isStopEditing(src[c.start:c.end]) {
					p.stopEditing, p.stopAt = g, c.start
				}
			}
		}
		if ti < 0 {
			if g != nil {
				p.end = g
			}
			break
		}
		commentsFrom = ti + 1
		t := toks[ti]
		if t.kind == tClose && g != nil && ti > lastOpen {
			p.end = g
		}
		w := word(ti)

		switch {
		case w == "require_once" || w == "require" || w == "include" || w == "include_once":
			// The first wp-settings.php line counts even inside an if:
			// nothing may go after it. It is only an anchor when safe.
			if p.settingsAt < 0 && statementHasSettings(toks, sig, si, text) {
				p.settings, p.settingsAt = g, t.start
			}
		case g != nil && t.kind == tVar && text(ti) == "$table_prefix" && !p.prefixOK:
			if v, ok := prefixAssign(toks, sig, si, text); ok {
				p.prefix, p.prefixOK = v, true
			}
		}
		if w == "define" && si+1 < len(sig) && punct(sig[si+1], "(") && !isMember(prev, text) {
			if d, ok := readDefine(src, toks, sig, si, text); ok {
				d.Conditional = g == nil || d.Conditional
				d.stmt = lastSafe
				d.Line = p.lineOf(t.start)
				for k := range d.members {
					d.members[k].line = p.lineOf(d.members[k].start)
				}
				p.defs = append(p.defs, d)
			}
		}

		// Move the nesting state past this token.
		if afterControl {
			afterControl = false
			switch {
			case punct(ti, "{"):
			case punct(ti, ":"):
				if altOpeners[control] {
					alt++
				}
				control = ""
				continue
			case punct(ti, ";"):
				control = ""
				continue
			case w == "if":
				// "else if": the if is the body; it opens its own.
			default:
				pending[brace]++
			}
			control = ""
		}
		switch {
		case controlWords[w]:
			control, controlParen = w, -1
		case w == "else":
			control, afterControl = "else", true
		case altEnds[w]:
			alt = max(alt-1, 0)
		case punct(ti, "("):
			if control != "" && controlParen < 0 {
				controlParen = paren
			}
			paren++
		case punct(ti, ")"):
			paren = max(paren-1, 0)
			if control != "" && controlParen == paren {
				afterControl, controlParen = true, -1
			}
		case punct(ti, "{"):
			brace++
			pending = append(pending, 0)
		case punct(ti, "}"):
			if brace > 0 {
				brace--
				pending = pending[:len(pending)-1]
			}
			pending[brace] = 0
		case punct(ti, ";") && paren == 0:
			pending[brace] = 0
		}
	}
	p.markSettings()
	return p, nil
}

// isStopEditing reports the "That's all, stop editing!" comment of
// wp-config-sample.php, with a straight or curly apostrophe. The comment
// above it that mentions "the stop editing line" doesn't count.
func isStopEditing(c []byte) bool {
	s := strings.ToLower(string(c))
	return strings.Contains(s, "stop editing") && (strings.Contains(s, "that's all") || strings.Contains(s, "that’s all") || strings.Contains(s, "thats all"))
}

// isMember reports a call like $x->define( or Foo::define( or a function
// named define, which are not the define() built-in. ?-> lexes as '?'
// then '->', so it is caught by the '->'.
func isMember(prev int, text func(int) string) bool {
	if prev < 0 {
		return false
	}
	switch strings.ToLower(text(prev)) {
	case "->", "::", "function", "new", "const":
		return true
	}
	return false
}

// readDefine reads define( name , value [, flag] ) starting at sig[si].
func readDefine(src []byte, toks []token, sig []int, si int, text func(int) string) (def, bool) {
	depth := 0
	var args [][]int
	cur := []int{}
	k := si + 1
	for ; k < len(sig); k++ {
		ti := sig[k]
		s := text(ti)
		if toks[ti].kind == tPunct {
			switch s {
			case "(", "[", "{":
				depth++
				if depth == 1 {
					continue
				}
			case ")", "]", "}":
				depth--
				if depth == 0 {
					args = append(args, cur)
					goto done
				}
			case ",":
				if depth == 1 {
					args = append(args, cur)
					cur = []int{}
					continue
				}
			}
		}
		if toks[ti].kind == tClose || toks[ti].kind == tHTML || toks[ti].kind == tOpen {
			return def{}, false
		}
		cur = append(cur, ti)
	}
	return def{}, false
done:
	if len(args) < 2 || len(args[0]) != 1 || toks[args[0][0]].kind != tString || !toks[args[0][0]].literal {
		return def{}, false
	}
	d := def{callStart: toks[sig[si]].start, valStart: -1}
	d.Name = toks[args[0][0]].value
	val := args[1]
	if len(val) > 0 {
		d.valStart, d.valEnd = toks[val[0]].start, toks[val[len(val)-1]].end
		d.Value = string(src[d.valStart:d.valEnd])
	}
	switch {
	case len(val) == 1 && (toks[val[0]].kind == tString || toks[val[0]].kind == tHeredoc):
		t := toks[val[0]]
		d.Value, d.Literal, d.Quoted, d.quote = t.value, t.literal, true, t.quote
		if !t.literal {
			d.Value = string(src[t.start:t.end])
		}
	case len(val) == 1 && toks[val[0]].kind == tIdent && (strings.EqualFold(text(val[0]), "true") || strings.EqualFold(text(val[0]), "false")):
		d.Value, d.Literal = strings.ToLower(text(val[0])), true
	case len(val) == 1 && toks[val[0]].kind == tNumber:
		d.Literal = true
	case len(val) == 2 && (text(val[0]) == "-" || text(val[0]) == "+") && toks[val[1]].kind == tNumber:
		d.Value, d.Literal = text(val[0])+text(val[1]), true
	}
	if !d.Literal && len(val) > 1 {
		d.members = readMembers(toks, val, text)
	}
	// define(...) must end its statement; "define(...) or die()" is conditional.
	if k+1 >= len(sig) || (text(sig[k+1]) != ";" && toks[sig[k+1]].kind != tClose) {
		d.Conditional = true
	}
	return d, true
}

// statementHasSettings reports a require/include statement naming
// wp-settings.php.
func statementHasSettings(toks []token, sig []int, si int, text func(int) string) bool {
	for k := si + 1; k < len(sig); k++ {
		t := toks[sig[k]]
		if t.kind == tClose || t.kind == tPunct && text(sig[k]) == ";" {
			return false
		}
		if t.kind == tString && strings.HasSuffix(t.value, "wp-settings.php") {
			return true
		}
	}
	return false
}

// prefixAssign reads $table_prefix = 'x';.
func prefixAssign(toks []token, sig []int, si int, text func(int) string) (string, bool) {
	if si+3 >= len(sig) || text(sig[si+1]) != "=" {
		return "", false
	}
	v := toks[sig[si+2]]
	if v.kind != tString || !v.literal || text(sig[si+3]) != ";" {
		return "", false
	}
	return v.value, true
}

// markSettings sets BeforeSettings on every define.
func (p *parsed) markSettings() {
	for i := range p.defs {
		p.defs[i].BeforeSettings = p.settingsAt < 0 || p.defs[i].callStart < p.settingsAt
	}
}

// effective is the define PHP keeps for name: the first unconditional
// one, else the first conditional one.
func (p *parsed) effective(name string) *def {
	var first *def
	for i := range p.defs {
		d := &p.defs[i]
		if d.Name != name {
			continue
		}
		if !d.Conditional {
			return d
		}
		if first == nil {
			first = d
		}
	}
	return first
}

func lineStarts(src []byte) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			starts = append(starts, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineOf is the 1-based line of offset i.
func (p *parsed) lineOf(i int) int {
	return sort.Search(len(p.lines), func(k int) bool { return p.lines[k] > i })
}

// lineStart is the offset of the start of the line holding offset i.
func (p *parsed) lineStart(i int) int {
	return p.lines[p.lineOf(i)-1]
}
