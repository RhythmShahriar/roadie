// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func TestSettings(t *testing.T) {
	tests := []struct {
		in   string
		want map[string]string
	}{
		{`a:3:{s:7:"enabled";s:3:"yes";s:8:"testmode";s:2:"no";s:10:"secret_key";s:0:"";}`,
			map[string]string{"enabled": "yes", "testmode": "no", "secret_key": ""}},
		{`a:2:{s:10:"sandbox_on";b:1;s:5:"count";i:-3;}`, map[string]string{"sandbox_on": "1", "count": "-3"}},
		{`a:1:{s:4:"smtp";a:2:{s:4:"host";s:9:"127.0.0.1";s:4:"pass";N;}}`, map[string]string{"smtp.host": "127.0.0.1", "smtp.pass": ""}},
		{`a:1:{s:11:"connections";a:1:{i:0;a:1:{s:8:"password";s:3:"abc";}}}`, map[string]string{"connections[0].password": "abc"}},
		{`a:1:{s:3:"a.b";d:1.5;}`, map[string]string{`a\.b`: "1.5"}},
		{"O:8:\"stdClass\":1:{s:6:\"\x00*\x00key\";s:1:\"v\";}", map[string]string{"key": "v"}},
		{`{"mode":"live","nested":{"on":true,"n":2},"list":["a",null]}`,
			map[string]string{"mode": "live", "nested.on": "1", "nested.n": "2", "list[0]": "a", "list[1]": ""}},
	}
	for _, tt := range tests {
		got, ok := Settings([]byte(tt.in))
		if !ok || !maps.Equal(got, tt.want) {
			t.Errorf("Settings(%q) = %v, %v; want %v", tt.in, got, ok, tt.want)
		}
	}
	for _, bad := range []string{"", "plain text", `a:2:{s:1:"a";s:1:"b";}`, `a:1:{s:5:"a";s:1:"b";}`, `s:3:"abc";`,
		`a:1:{s:1:"a";s:1:"b";}x`, `a:999999999:{}`, `C:3:"Foo":0:{}`, `{"a":1} {}`, `a:1:{b:1;s:1:"x";}`} {
		if _, ok := Settings([]byte(bad)); ok {
			t.Errorf("Settings(%q) is ok", bad)
		}
	}
}

func TestSettingsDepthAndSize(t *testing.T) {
	deep := strings.Repeat(`a:1:{i:0;`, 40) + `s:1:"x";` + strings.Repeat("}", 40)
	if _, ok := Settings([]byte(deep)); ok {
		t.Error("40 levels deep is ok")
	}
	start := time.Now()
	Settings([]byte("a:99999:{" + strings.Repeat(`i:0;a:0:{}`, 1<<17)))
	if d := time.Since(start); d > time.Second {
		t.Errorf("1 MB of arrays took %v", d)
	}
}

func TestSiblings(t *testing.T) {
	s := map[string]string{"enabled": "yes", "testmode": "no", "smtp.host": "h", "smtp.pass": "p", "mail.mailer": "smtp"}
	got := Siblings(s, "smtp.pass")
	if got["host"] != "h" || got["pass"] != "p" || got["enabled"] != "yes" || got["mailer"] != "" {
		t.Errorf("Siblings(smtp.pass) = %v", got)
	}
	got = Siblings(s, "testmode")
	if got["enabled"] != "yes" || got["host"] != "" {
		t.Errorf("Siblings(testmode) = %v", got)
	}
	if Siblings(nil, "x") != nil {
		t.Error("nil settings give siblings")
	}
}

// FuzzSettings: never panics, stays linear and every path it returns is
// one splitPath can read back.
func FuzzSettings(f *testing.F) {
	for _, s := range []string{`a:1:{s:1:"a";b:1;}`, `{"a":[1,{"b":"c"}]}`, `O:1:"A":1:{s:1:"x";i:1;}`, `a:1:{i:0;N;}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		m, ok := Settings(b)
		if !ok {
			return
		}
		for p := range m {
			if _, last := splitPath(p); last == "" && p != "" && !strings.HasSuffix(p, ".") {
				t.Fatalf("path %q has no last key", p)
			}
		}
	})
}
