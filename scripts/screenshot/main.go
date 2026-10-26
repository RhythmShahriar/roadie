// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command screenshot turns a color terminal screen (text with ANSI SGR
// escapes) into the SVG the README shows, in a terminal window frame with
// DejaVu Sans Mono embedded so it looks the same everywhere. The screen
// is the copy PASS golden, which the CLI tests render with
// CLICOLOR_FORCE=1 COLORTERM=truecolor, so the picture is what Roadie
// draws:
//
//	go run ./scripts/screenshot -readme \
//	  -font /usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf \
//	  -o docs/assets/copy.svg cmd/rhythm-roadie/testdata/golden/screen/copy-pass-color.txt
//
// -readme sets the README's prompt, title and version; -prompt, -title and
// -version set them one by one. It reads only the files it is given and
// writes only -o. A test fails when docs/assets/copy.svg no longer matches
// the golden, so a changed screen means running this again.
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// The frame's geometry, in pixels. cell is DejaVu Sans Mono's advance
// width (1233/2048 em) at size.
const (
	size    = 14.0
	cell    = size * 1233 / 2048
	line    = 20.0
	pad     = 24.0
	titleH  = 36.0
	radius  = 10.0
	bg      = "#100f0d"
	fg      = "#e7e4df"
	dotSize = 6.0
)

// The README's picture: the command that made the golden, wrapped to fit,
// its accessible name and the version the banner shows.
var (
	readmePrompt = []string{
		`rhythm roadie copy --from ssh://deploy@shop.com/var/www/shop \`,
		`    --to /var/www/staging --url https://staging.shop.test`,
	}
	readmeTitle   = "A Roadie copy from production to staging: the plan header, each step ticked, verify in exact mode with every check passed, the PASS box, the honesty line and the next steps"
	readmeVersion = "1.0.0"
)

// listFlag collects a repeated string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, "\n") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run is main with its arguments, for the tests.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the SVG to this file")
	font := fs.String("font", "", "TrueType font to embed (DejaVu Sans Mono)")
	version := fs.String("version", "", "show this version where the screen says dev")
	title := fs.String("title", "rhythm roadie copy", "the picture's accessible name")
	var prompt listFlag
	fs.Var(&prompt, "prompt", "the command line shown first, one flag per line (replaces a $ line)")
	readme := fs.Bool("readme", false, "the README's prompt, title and version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *readme {
		prompt, *title, *version = readmePrompt, readmeTitle, readmeVersion
	}
	if *out == "" || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: screenshot -o <file.svg> [-font f.ttf] [-version v] [-prompt line]... <screen.txt>")
		return 2
	}
	screen, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "screenshot: %v\n", err)
		return 1
	}
	var fontData []byte
	if *font != "" {
		if fontData, err = os.ReadFile(*font); err != nil {
			fmt.Fprintf(stderr, "screenshot: %v\n", err)
			return 1
		}
	}
	lines := prepare(string(screen), prompt, *version)
	svg := render(lines, *title, fontData)
	if err := os.WriteFile(*out, svg, 0o644); err != nil { //nolint:gosec // a picture for the README, meant to be read
		fmt.Fprintf(stderr, "screenshot: %v\n", err)
		return 1
	}
	return 0
}

// prepare splits the screen into lines, drops the golden's "--- exit"
// trailer, puts the prompt in place of the golden's own "$ " line and
// shows version where the banner says dev.
func prepare(screen string, prompt []string, version string) []string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], "--- exit") {
		lines = lines[:n-1]
	}
	if len(prompt) > 0 {
		if len(lines) > 0 && strings.HasPrefix(lines[0], "$ ") {
			lines = lines[1:]
		}
		shown := make([]string, 0, len(prompt))
		for i, p := range prompt {
			lead := "  "
			if i == 0 {
				lead = "$ "
			}
			shown = append(shown, "\x1b[38;2;154;150;144m"+lead+"\x1b[0m"+p)
		}
		lines = append(shown, lines...)
	}
	if version != "" {
		for i, l := range lines {
			// The banner pads the version to align the hosts on the right:
			// a longer version takes the place of as many spaces.
			if j := strings.Index(l, "dev\x1b[0m"); j >= 0 && strings.Contains(l, "rhythm roadie") {
				rest := l[j+len("dev\x1b[0m"):]
				trim := len(version) - len("dev")
				for trim > 0 && strings.HasPrefix(rest, " ") {
					rest = rest[1:]
					trim--
				}
				lines[i] = l[:j] + version + "\x1b[0m" + rest
				break
			}
		}
	}
	return lines
}

// style is the SGR state of a run of text.
type style struct {
	color string // "" is the default foreground
	bold  bool
	faint bool
}

// span is text in one style.
type span struct {
	text string
	style
}

// basic is the 16-color palette, for 30-37 and 90-97.
var basic = []string{
	"#1c1a17", "#ef4444", "#10b981", "#eab308", "#60a5fa", "#c084fc", "#22d3ee", "#e7e4df",
	"#6b7180", "#f87171", "#34d399", "#facc15", "#93c5fd", "#d8b4fe", "#67e8f9", "#ffffff",
}

// parse splits one line into styled spans. SGR codes it knows change the
// style; any other escape sequence and control character is dropped, so
// nothing but text reaches the SVG.
func parse(s string, st style) ([]span, style) {
	var spans []span
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			spans = append(spans, span{b.String(), st})
			b.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				break // an unfinished sequence ends the line
			}
			if s[j] == 'm' {
				flush()
				st = sgr(s[i+2:j], st)
			}
			i = j + 1
			continue
		}
		if c < 0x20 || c == 0x7f {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	flush()
	return spans, st
}

// sgr applies one SGR parameter list to st.
func sgr(params string, st style) style {
	if params == "" {
		return style{}
	}
	ps := strings.Split(params, ";")
	num := func(k int) int {
		if k >= len(ps) {
			return -1
		}
		n, err := strconv.Atoi(ps[k])
		if err != nil {
			return -1
		}
		return n
	}
	for k := 0; k < len(ps); k++ {
		switch n := num(k); {
		case n == 0:
			st = style{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.faint = true
		case n == 22:
			st.bold, st.faint = false, false
		case n == 39:
			st.color = ""
		case n >= 30 && n <= 37:
			st.color = basic[n-30]
		case n >= 90 && n <= 97:
			st.color = basic[n-90+8]
		case n == 38 && num(k+1) == 2:
			r, g, bl := num(k+2), num(k+3), num(k+4)
			if r >= 0 && r < 256 && g >= 0 && g < 256 && bl >= 0 && bl < 256 {
				st.color = fmt.Sprintf("#%02x%02x%02x", r, g, bl)
			}
			k += 4
		case n == 38 && num(k+1) == 5:
			if c := num(k + 2); c >= 0 && c < 16 {
				st.color = basic[c]
			}
			k += 2
		}
	}
	return st
}

// width is the number of terminal cells s takes: one per rune, which
// holds for the box drawing, arrows and ticks Roadie prints.
func width(spans []span) int {
	n := 0
	for _, s := range spans {
		n += len([]rune(s.text))
	}
	return n
}

// render draws the lines in a window frame.
func render(lines []string, title string, font []byte) []byte {
	var rows [][]span
	cols := 0
	var st style
	for _, l := range lines {
		var spans []span
		spans, st = parse(l, st)
		rows = append(rows, spans)
		cols = max(cols, width(spans))
	}
	w := pad*2 + float64(cols)*cell
	h := titleH + pad + float64(len(rows))*line + pad/2

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.2f" height="%.2f" viewBox="0 0 %.2f %.2f" role="img" aria-label="%s">`+"\n", w, h, w, h, escape(title))
	b.WriteString("<style>\n")
	if len(font) > 0 {
		fmt.Fprintf(&b, "@font-face { font-family: 'DejaVu Sans Mono'; src: url(data:font/ttf;base64,%s); }\n", base64.StdEncoding.EncodeToString(font))
	}
	fmt.Fprintf(&b, "text { font-family: 'DejaVu Sans Mono', monospace; font-size: %.0fpx; fill: %s; white-space: pre; }\n", size, fg)
	b.WriteString(".b { font-weight: bold; } .f { opacity: 0.7; }\n</style>\n")
	fmt.Fprintf(&b, `<rect width="%.2f" height="%.2f" rx="%.0f" fill="%s"/>`+"\n", w, h, radius, bg)
	for i, c := range []string{"#ff5f57", "#febc2e", "#28c840"} {
		fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="%.0f" fill="%s"/>`+"\n", pad+float64(i)*20, titleH/2+4, dotSize, c)
	}
	for i, spans := range rows {
		y := titleH + pad + float64(i)*line
		fmt.Fprintf(&b, `<text x="%.2f" y="%.2f" xml:space="preserve">`, pad, y)
		for _, s := range spans {
			var attrs []string
			if s.color != "" {
				attrs = append(attrs, `fill="`+s.color+`"`)
			}
			var class []string
			if s.bold {
				class = append(class, "b")
			}
			if s.faint {
				class = append(class, "f")
			}
			if len(class) > 0 {
				attrs = append(attrs, `class="`+strings.Join(class, " ")+`"`)
			}
			if len(attrs) == 0 {
				b.WriteString(escape(s.text))
				continue
			}
			fmt.Fprintf(&b, "<tspan %s>%s</tspan>", strings.Join(attrs, " "), escape(s.text))
		}
		b.WriteString("</text>\n")
	}
	b.WriteString("</svg>\n")
	return b.Bytes()
}

// escape makes text safe inside an XML element or attribute.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\uFFFD', '\uFFFE', '\uFFFF':
			// Invalid UTF-8 and the two non-characters XML forbids never
			// reach the file.
		case '\t':
			b.WriteRune(r)
		default:
			if r >= 0x20 { // XML has no other control characters
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
