// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// wellFormed fails the test when svg isn't well-formed XML.
func wellFormed(t testing.TB, svg []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(svg))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v\n%s", err, svg)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name, in string
		want     []span
	}{
		{"plain", "PASS   ok", []span{{"PASS   ok", style{}}}},
		{"truecolor and bold", "\x1b[1;38;2;16;185;129m✓\x1b[0m Data",
			[]span{{"✓", style{color: "#10b981", bold: true}}, {" Data", style{}}}},
		{"basic and bright colors", "\x1b[31mred\x1b[92mgreen\x1b[39mplain",
			[]span{{"red", style{color: basic[1]}}, {"green", style{color: basic[10]}}, {"plain", style{}}}},
		{"256 colors below 16", "\x1b[38;5;4mblue", []span{{"blue", style{color: basic[4]}}}},
		{"faint then normal", "\x1b[2mdim\x1b[22mnormal", []span{{"dim", style{faint: true}}, {"normal", style{}}}},
		// Negative: escapes it doesn't draw and controls are dropped,
		// never printed.
		{"cursor moves dropped", "a\x1b[2Kb\x1b[1;1Hc", []span{{"abc", style{}}}},
		{"controls dropped", "a\x07b\rc\x1b]0;title\x07d", []span{{"ab", style{}}, {"c]0;titled", style{}}}},
		{"bad truecolor ignored", "\x1b[38;2;300;0;0mx", []span{{"x", style{}}}},
		{"unfinished sequence ends the line", "ok\x1b[38;2", []span{{"ok", style{}}}},
	}
	for _, c := range cases {
		got, _ := parse(c.in, style{})
		// "controls dropped" splits where the stray ESC was: compare text.
		if c.name == "controls dropped" {
			var b strings.Builder
			for _, s := range got {
				b.WriteString(s.text)
			}
			if b.String() != "abc]0;titled" {
				t.Errorf("%s: %q", c.name, b.String())
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// The style carries from one line to the next, as on a terminal.
func TestStyleCarries(t *testing.T) {
	svg := render([]string{"\x1b[31mred", "still red\x1b[0m", "plain"}, "t", nil)
	wellFormed(t, svg)
	if !strings.Contains(string(svg), `<tspan fill="#ef4444">still red</tspan>`) {
		t.Errorf("the color didn't carry:\n%s", svg)
	}
	if !strings.Contains(string(svg), `xml:space="preserve">plain</text>`) {
		t.Errorf("the reset didn't end it:\n%s", svg)
	}
}

// Text that looks like markup stays text.
func TestEscapes(t *testing.T) {
	svg := render([]string{`</text><script>alert(1)</script> & "q" 'a'` + "\uFFFE"}, `a"b<c`, []byte("font"))
	wellFormed(t, svg)
	s := string(svg)
	if strings.Contains(s, "<script>") || !strings.Contains(s, "&lt;/text&gt;&lt;script&gt;") || strings.Contains(s, "\uFFFE") {
		t.Errorf("markup got through:\n%s", s)
	}
	if !strings.Contains(s, `aria-label="a&quot;b&lt;c"`) || !strings.Contains(s, "base64,Zm9udA==") {
		t.Errorf("title or font:\n%s", s)
	}
}

func TestPrepare(t *testing.T) {
	screen := "$ rhythm-roadie copy --long\n\n  \x1b[1mrhythm roadie\x1b[0m \x1b[38;2;107;113;128mdev\x1b[0m      \x1b[38;2;1;2;3mshop.com\n--- exit 0\n"
	got := prepare(screen, []string{"rhythm roadie copy \\", "    --to x"}, "1.0.0")
	if len(got) != 4 {
		t.Fatalf("%d lines: %q", len(got), got)
	}
	if !strings.HasSuffix(got[0], "$ \x1b[0mrhythm roadie copy \\") || !strings.HasSuffix(got[1], "  \x1b[0m    --to x") {
		t.Errorf("prompt: %q", got[:2])
	}
	if got[2] != "" {
		t.Errorf("the golden's own $ line stays: %q", got[2])
	}
	if want := "  \x1b[1mrhythm roadie\x1b[0m \x1b[38;2;107;113;128m1.0.0\x1b[0m    \x1b[38;2;1;2;3mshop.com"; got[3] != want {
		t.Errorf("banner %q, want %q (two spaces fewer)", got[3], want)
	}
	// Negative: without a prompt or version the screen stays as it is,
	// less the trailer.
	if got := prepare("a\nrhythm roadie dev\x1b[0m\n", nil, ""); !reflect.DeepEqual(got, []string{"a", "rhythm roadie dev\x1b[0m"}) {
		t.Errorf("untouched: %q", got)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "screen.txt")
	if err := os.WriteFile(in, []byte("\x1b[32m✓\x1b[0m ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "shot.svg")
	if code := run([]string{"-o", out, in}, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	wellFormed(t, b)
	for _, args := range [][]string{nil, {"-o", out}, {in}, {"-o", out, filepath.Join(dir, "missing.txt")}, {"-o", out, "-font", filepath.Join(dir, "none.ttf"), in}} {
		if code := run(args, io.Discard); code == 0 {
			t.Errorf("%q: exit 0", args)
		}
	}
}

// The README's picture is the golden it says it is: regenerating it gives
// the same file.
func TestAssetIsCurrent(t *testing.T) {
	const font = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
	asset, err := os.ReadFile("../../docs/assets/copy.svg")
	if err != nil {
		t.Fatal(err)
	}
	fontData, err := os.ReadFile(font)
	if err != nil {
		t.Skipf("needs %s: %v", font, err)
	}
	screen, err := os.ReadFile("../../cmd/rhythm-roadie/testdata/golden/screen/copy-pass-color.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := render(prepare(string(screen), readmePrompt, readmeVersion), readmeTitle, fontData)
	if !bytes.Equal(got, asset) {
		t.Errorf("docs/assets/copy.svg is out of date: run the command in scripts/screenshot/main.go's doc")
	}
}

func FuzzRender(f *testing.F) {
	f.Add("\x1b[1;38;2;16;185;129m✓\x1b[0m Data <&>")
	f.Add("\x1b[38;5;300m\x1b[38;2;;;m\x1b[")
	f.Add("\xff\xfe\x00]]>")
	f.Fuzz(func(t *testing.T, s string) {
		wellFormed(t, render(strings.Split(s, "\n"), s, nil))
	})
}
