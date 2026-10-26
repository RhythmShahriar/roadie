// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package nested

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/phpser"
)

const fake = "u0123456789abcdef@example.invalid"

// emails replaces every leaf holding an '@' with fake and records the
// paths it saw.
func emails(paths *[]string) func(string, []byte) ([]byte, bool) {
	return func(path string, leaf []byte) ([]byte, bool) {
		if paths != nil {
			*paths = append(*paths, path)
		}
		if bytes.IndexByte(leaf, '@') >= 0 {
			return []byte(fake), true
		}
		return nil, false
	}
}

func rewrite(t *testing.T, in string) (string, []string) {
	t.Helper()
	var paths []string
	out, _, err := Rewrite([]byte(in), emails(&paths), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return string(out), paths
}

func ser(t *testing.T, s string) string {
	t.Helper()
	if !phpser.Valid([]byte(s)) {
		t.Fatalf("fixture not valid serialized: %s", s)
	}
	return s
}

func TestFormat(t *testing.T) {
	session := base64.StdEncoding.EncodeToString([]byte(`a:1:{s:5:"email";s:16:"jane@example.com";}`))
	cases := []struct{ in, want string }{
		{`a:0:{}`, FormatSerialized},
		{`s:3:"abc";`, FormatSerialized},
		{`{"a":1}`, FormatJSON},
		{" \n[1,2]", FormatJSON},
		{`"just a string"`, FormatText},
		{`42`, FormatText},
		{`{"a":`, FormatText},
		{session, FormatBase64Serialized},
		{"YWJjZGVmZ2hpams=", FormatText}, // base64 of text
		{"TjsK", FormatText},             // under 12 bytes
		{"A teaching post: s:3:\"abc\"; is a serialized string.", FormatText},
		{`a:1:{s:1:"x";s:5:"broken";}`, FormatText},
		{"", FormatText},
	}
	for _, c := range cases {
		if got := Format([]byte(c.in)); got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Action Scheduler stores its args as JSON; plugins put serialized data
// in there.
func TestActionSchedulerArgs(t *testing.T) {
	inner := ser(t, `a:1:{s:5:"email";s:16:"jane@example.com";}`)
	b, _ := json.Marshal([]any{map[string]any{"order_id": 123, "data": inner}})
	out, paths := rewrite(t, string(b))
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON broken: %v\n%s", err, out)
	}
	data := got[0]["data"].(string)
	if want := `a:1:{s:5:"email";s:33:"` + fake + `";}`; data != want {
		t.Errorf("inner %q, want %q", data, want)
	}
	if !phpser.Valid([]byte(data)) {
		t.Error("inner serialized invalid")
	}
	if strings.Join(paths, " ") != "json:[0].data.serialized:email" {
		t.Errorf("paths %q", paths)
	}
}

// A Laravel queue payload is JSON with the job serialized in
// data.command (Queue::createObjectPayload).
func TestLaravelQueuePayload(t *testing.T) {
	cmd := ser(t, `O:17:"App\Jobs\SendMail":1:{s:5:"email";s:16:"jane@example.com";}`)
	payload := map[string]any{
		"uuid": "8a7c", "displayName": `App\Jobs\SendMail`, "job": `Illuminate\Queue\CallQueuedHandler@call`,
		"data": map[string]any{"commandName": `App\Jobs\SendMail`, "command": cmd},
	}
	b, _ := json.Marshal(payload)
	out, paths := rewrite(t, string(b))
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	command := got["data"].(map[string]any)["command"].(string)
	if !phpser.Valid([]byte(command)) || !strings.Contains(command, `s:33:"`+fake) {
		t.Errorf("command %q", command)
	}
	// the job class with its '@' is a leaf too; the test fn replaces it,
	// which a real rule wouldn't
	if !strings.Contains(strings.Join(paths, " "), "json:data.command.serialized:email") {
		t.Errorf("paths %q", paths)
	}
}

// Laravel's database session payload is base64 of serialized data.
func TestLaravelSession(t *testing.T) {
	in := base64.StdEncoding.EncodeToString([]byte(ser(t, `a:2:{s:6:"_token";s:4:"abcd";s:5:"email";s:16:"jane@example.com";}`)))
	out, paths := rewrite(t, in)
	dec, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := `a:2:{s:6:"_token";s:4:"abcd";s:5:"email";s:33:"` + fake + `";}`; string(dec) != want {
		t.Errorf("got %q, want %q", dec, want)
	}
	if strings.Join(paths, " ") != "base64:serialized:_token base64:serialized:email" {
		t.Errorf("paths %q", paths)
	}
}

// Elementor stores _elementor_data as JSON with "\/" and \u escapes; a
// changed string keeps that style and the rest keeps its bytes.
func TestElementorStyle(t *testing.T) {
	in := `[{"id":"1a2b","settings":{"title":"Café","link":{"url":"https:\/\/shop.example\/page"},"mail":"jane@example.com"},"n":1.50}]`
	out, _, err := Rewrite([]byte(in), func(path string, leaf []byte) ([]byte, bool) {
		switch path {
		case "json:[0].settings.mail":
			return []byte("Zoë/ü@example.invalid"), true
		case "json:[0].settings.link.url":
			if string(leaf) != "https://shop.example/page" {
				t.Errorf("url decoded as %q", leaf)
			}
		}
		return nil, false
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"1a2b","settings":{"title":"Café","link":{"url":"https:\/\/shop.example\/page"},"mail":"Zoë\/ü@example.invalid"},"n":1.50}]`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// A document with raw UTF-8 and plain slashes keeps those.
	out, _, _ = Rewrite([]byte(`{"a":"é/","m":"x@y.z"}`), emails(nil), Options{})
	if string(out) != `{"a":"é/","m":"`+fake+`"}` {
		t.Errorf("raw style: %s", out)
	}
}

func TestJSONEscapes(t *testing.T) {
	in := `{"k.d":"a\"b\\c\n\t\u0001😀 \ud800","e":"x@y.z"}`
	var leaves []string
	var paths []string
	if err := Leaves([]byte(in), func(path string, leaf []byte) {
		paths = append(paths, path)
		leaves = append(leaves, string(leaf))
	}, Options{}); err != nil {
		t.Fatal(err)
	}
	if leaves[0] != "a\"b\\c\n\t\x01😀 �" {
		t.Errorf("decoded %q", leaves[0])
	}
	if strings.Join(paths, " ") != `json:k\.d json:e` {
		t.Errorf("paths %q", paths)
	}
	out, _, _ := Rewrite([]byte(`{"a":"é","b":"q@x.y"}`), func(_ string, leaf []byte) ([]byte, bool) {
		if bytes.IndexByte(leaf, '@') < 0 {
			return nil, false
		}
		return []byte("\"\\\n\x01😀\xff"), true
	}, Options{})
	if want := `{"a":"é","b":"\"\\\n\u0001😀�"}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
	if !json.Valid(out) {
		t.Error("output not JSON")
	}
}

// Serialized in JSON in serialized: every length on the way out is right.
func TestSerializedJSONSerialized(t *testing.T) {
	inner := ser(t, `a:1:{s:5:"email";s:16:"jane@example.com";}`)
	j, _ := json.Marshal(map[string]string{"blob": inner})
	outer := `a:1:{s:4:"data";s:` + itoa(len(j)) + `:"` + string(j) + `";}`
	ser(t, outer)
	out, paths := rewrite(t, outer)
	if !phpser.Valid([]byte(out)) {
		t.Fatalf("outer invalid: %s", out)
	}
	if paths[0] != "serialized:data.json:blob.serialized:email" {
		t.Errorf("path %q", paths[0])
	}
	var mid map[string]string
	err := phpser.Leaves([]byte(out), func(_ string, leaf []byte, isKey bool) {
		if !isKey {
			if err := json.Unmarshal(leaf, &mid); err != nil {
				t.Errorf("middle JSON: %v", err)
			}
		}
	})
	if err != nil || !phpser.Valid([]byte(mid["blob"])) || !strings.Contains(mid["blob"], fake) {
		t.Errorf("inner %q (%v)", mid["blob"], err)
	}
}

func itoa(n int) string {
	var b []byte
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Text that only mentions serialized syntax is one text leaf; a broken
// serialized value is text too, counted.
func TestTextAndBroken(t *testing.T) {
	post := `To store a string PHP writes s:3:"abc"; mail me at jane@example.com`
	out, paths := rewrite(t, post)
	if out != fake || len(paths) != 1 || paths[0] != "" {
		t.Errorf("post: %q %q", out, paths)
	}
	var st Stats
	broken := `a:1:{s:5:"email";s:99:"jane@example.com";}`
	got, changed, _ := Rewrite([]byte(broken), func(string, []byte) ([]byte, bool) { return nil, false }, Options{Stats: &st})
	if changed || string(got) != broken || st.Broken != 1 {
		t.Errorf("broken: %q %v %+v", got, changed, st)
	}
}

func TestDepthAndBudget(t *testing.T) {
	// Each level is a JSON array holding the level below as a string.
	v := `["x@y.z"]`
	for range 20 {
		b, _ := json.Marshal([]string{v})
		v = string(b)
	}
	var st Stats
	var seen int
	out, _, _ := Rewrite([]byte(v), func(_ string, leaf []byte) ([]byte, bool) {
		seen++
		return []byte("changed"), true
	}, Options{Stats: &st})
	if st.TooDeep != 1 || seen != 1 || !json.Valid(out) {
		t.Errorf("depth: %+v seen %d", st, seen)
	}
	st = Stats{}
	_, _, _ = Rewrite([]byte(v), emails(nil), Options{MaxDepth: 64, Budget: int64(len(v)) * 3, Stats: &st})
	if st.OverBudget != 1 {
		t.Errorf("budget: %+v", st)
	}
	st = Stats{}
	_, _, _ = Rewrite([]byte(v), emails(nil), Options{MaxDepth: 64, Stats: &st})
	if st != (Stats{}) {
		t.Errorf("default budget hit on 21 levels: %+v", st)
	}
}

func TestUnchangedIsInput(t *testing.T) {
	for _, in := range []string{`{"a":"b"}`, `a:1:{i:0;s:1:"x";}`, "plain", base64.StdEncoding.EncodeToString([]byte(`a:1:{i:0;s:1:"x";}`))} {
		out, changed, err := Rewrite([]byte(in), func(string, []byte) ([]byte, bool) { return nil, false }, Options{})
		if err != nil || changed || string(out) != in {
			t.Errorf("%q: %q %v %v", in, out, changed, err)
		}
	}
	if out, changed, err := Rewrite([]byte("x"), nil, Options{}); err != nil || changed || string(out) != "x" {
		t.Error("nil fn")
	}
	if Leaves([]byte("x"), nil, Options{}) != nil {
		t.Error("nil fn leaves")
	}
}

func FuzzNested(f *testing.F) {
	inner := `a:1:{s:5:"email";s:16:"jane@example.com";}`
	j, _ := json.Marshal(map[string]string{"blob": inner, "u": "https://shop.example/é"})
	f.Add([]byte(inner))
	f.Add(j)
	f.Add([]byte(`a:1:{s:4:"data";s:` + itoa(len(j)) + `:"` + string(j) + `";}`))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte(inner))))
	f.Add([]byte(`[{"a":"Café","b":"https:\/\/x"}]`))
	f.Add([]byte(`plain text s:3:"abc";`))
	f.Fuzz(func(t *testing.T, b []byte) {
		wasSer, wasJSON := phpser.Valid(b), Format(b) == FormatJSON
		var st Stats
		calls := 0
		out, _, err := Rewrite(b, func(_ string, leaf []byte) ([]byte, bool) {
			calls++
			if calls > len(b)+1 {
				t.Fatal("more leaves than bytes")
			}
			return append([]byte("€\"/"), leaf...), true
		}, Options{Stats: &st})
		if err != nil {
			t.Fatal(err)
		}
		if wasSer && !phpser.Valid(out) {
			t.Fatalf("serialized %q became invalid: %q", b, out)
		}
		if wasJSON && !json.Valid(out) {
			t.Fatalf("JSON %q became invalid: %q", b, out)
		}
		// A small budget never panics and always ends.
		_ = Leaves(b, func(string, []byte) {}, Options{Budget: 1, MaxDepth: 1})
	})
}

func BenchmarkNested(b *testing.B) {
	inner := `a:2:{s:5:"email";s:16:"jane@example.com";s:4:"name";s:8:"Jane Roe";}`
	row, _ := json.Marshal(map[string]any{"id": 1, "data": inner, "note": strings.Repeat("n", 80)})
	in := []byte("[" + strings.Repeat(string(row)+",", 499) + string(row) + "]")
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := Rewrite(in, emails(nil), Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
