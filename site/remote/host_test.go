// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"testing"
)

func TestCheckHost(t *testing.T) {
	for _, ok := range []string{"shop.example", "db-1.internal", "a_b", "10.0.0.5", "[2001:db8::1]", "[fe80::1%eth0]"} {
		if err := CheckHost(ok); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=touch /tmp/x", "-p", "a b", "a\tb", "a\nb", "a\x00b", "a;id", "$(id)", "a`id`", "a|b", "a&b", "a>b", "a'b", `a"b`, "a\\b", "a*", "h:22", "[]", "a/b", "ä.example"} {
		err := CheckHost(bad)
		if !errors.Is(err, ErrBadHost) {
			t.Errorf("CheckHost(%q) = %v, want ErrBadHost", bad, err)
		}
	}
}

func TestCheckUser(t *testing.T) {
	for _, ok := range []string{"", "deploy", "web-1", "a.b_c"} {
		if err := CheckUser(ok); err != nil {
			t.Errorf("CheckUser(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"-l", "a b", "de$(id)", "a:b", "[x]", "a%b"} {
		if err := CheckUser(bad); !errors.Is(err, ErrBadHost) {
			t.Errorf("CheckUser(%q) = %v, want ErrBadHost", bad, err)
		}
	}
}
