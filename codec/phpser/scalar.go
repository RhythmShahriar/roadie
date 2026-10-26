// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package phpser

// RewriteScalars returns b with the boolean and integer values fn
// changed, verbatim otherwise.
//
// fn sees each b: and i: value as written ("b:0;", "i:-3;") at its path,
// named as Rewrite names them, and returns the value to write in its
// place, which must itself be one serialized value. Rewrite only sees
// strings, yet a setting such as PayPal's sandbox_on is stored as a
// boolean. When nothing changed, out is b itself.
func RewriteScalars(b []byte, fn func(path string, tok []byte) ([]byte, bool)) (out []byte, changed bool, err error) {
	p := parser{scalar: fn, walk: func(string, []byte, bool) {}}
	if err := p.whole(b); err != nil {
		return nil, false, err
	}
	if p.out == nil {
		return b, false, nil
	}
	return p.out, true, nil
}
