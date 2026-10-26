// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package phpser

import "testing"

func TestRewriteScalars(t *testing.T) {
	in := `a:4:{s:7:"enabled";b:1;s:10:"sandbox_on";b:0;s:5:"count";i:-3;s:4:"deep";a:1:{i:7;b:0;}}`
	seen := map[string]string{}
	out, changed, err := RewriteScalars([]byte(in), func(p string, tok []byte) ([]byte, bool) {
		seen[p] = string(tok)
		switch p {
		case "sandbox_on":
			return []byte("b:1;"), true
		case "count":
			return []byte("i:5;"), true
		}
		return tok, false
	})
	if err != nil || !changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	want := `a:4:{s:7:"enabled";b:1;s:10:"sandbox_on";b:1;s:5:"count";i:5;s:4:"deep";a:1:{i:7;b:0;}}`
	if string(out) != want || !Valid(out) {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	for p, tok := range map[string]string{"enabled": "b:1;", "sandbox_on": "b:0;", "count": "i:-3;", "deep[7]": "b:0;"} {
		if seen[p] != tok {
			t.Errorf("%s: saw %q, want %q", p, seen[p], tok)
		}
	}
	if len(seen) != 4 {
		t.Errorf("saw %v: keys are no values", seen)
	}
	// Nothing changed: the input itself comes back.
	out, changed, err = RewriteScalars([]byte(in), func(_ string, tok []byte) ([]byte, bool) { return tok, true })
	if err != nil || changed || string(out) != in {
		t.Errorf("identity: %q %v %v", out, changed, err)
	}
	if _, _, err := RewriteScalars([]byte(`a:1:{b:1;}`), func(string, []byte) ([]byte, bool) { return nil, false }); err == nil {
		t.Error("invalid input passed")
	}
}

func FuzzRewriteScalars(f *testing.F) {
	f.Add([]byte(`a:2:{s:1:"a";b:0;i:3;i:-4;}`))
	f.Add([]byte(`O:8:"stdClass":1:{s:1:"x";b:1;}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		out, _, err := RewriteScalars(b, func(_ string, tok []byte) ([]byte, bool) {
			if tok[0] == 'b' {
				return []byte("b:1;"), true
			}
			return []byte("i:0;"), true
		})
		if err == nil && !Valid(out) {
			t.Fatalf("invalid output %q from %q", out, b)
		}
	})
}
