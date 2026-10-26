// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBadHost is wrapped by every error CheckHost returns.
var ErrBadHost = errors.New("refused ssh host")

// CheckHost refuses a host or user that ssh could read as an option or a
// remote shell could expand (security N1): a leading '-', whitespace,
// control bytes and shell characters. Only letters, digits, '.', '_' and
// '-' pass, plus ':' and '%' inside the brackets of an IPv6 address.
func CheckHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: the host is empty", ErrBadHost)
	}
	if host[0] == '-' {
		return fmt.Errorf("%w: a host can't start with '-'", ErrBadHost)
	}
	name := host
	ipv6 := strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]")
	if ipv6 {
		name = host[1 : len(host)-1]
		if name == "" {
			return fmt.Errorf("%w: the host is empty", ErrBadHost)
		}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		case ipv6 && (c == ':' || c == '%'):
		case c <= ' ' || c == 0x7f:
			return fmt.Errorf("%w: a host can't hold whitespace or control characters", ErrBadHost)
		default:
			return fmt.Errorf("%w: a host can't hold %q", ErrBadHost, string(c))
		}
	}
	return nil
}

// CheckUser applies the host rules to the user name before '@'.
func CheckUser(user string) error {
	if user == "" {
		return nil
	}
	if strings.ContainsAny(user, "[]:%") {
		return fmt.Errorf("%w: a user name can't hold '[', ']', ':' or '%%'", ErrBadHost)
	}
	if err := CheckHost(user); err != nil {
		return fmt.Errorf("%w (in the user name)", err)
	}
	return nil
}
