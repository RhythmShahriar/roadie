// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"errors"
	"testing"
)

// endless is a statement head followed by fill bytes that never end.
type endless struct {
	head string
	fill byte
	read int64
}

func (e *endless) Read(p []byte) (int, error) {
	n := copy(p, e.head)
	e.head = e.head[n:]
	for i := n; i < len(p); i++ {
		p[i] = e.fill
	}
	e.read += int64(len(p))
	return len(p), nil
}

// A statement stops at --max-value while it is read, not after the
// whole of it is held in memory.
func TestStatementBoundedWhileRead(t *testing.T) {
	const limit = 1 << 20
	for _, c := range []struct {
		head string
		fill byte
	}{
		{"CREATE TABLE t (a int", ' '},
		{"COMMENT ON TABLE t IS '", 'x'},
		{"COMMENT ON TABLE t IS E'", '\\'},
		{"CREATE TABLE \"", 'x'},
		{"CREATE FUNCTION f() RETURNS int AS $x$", 'x'},
		{"/* comment ", 'x'},
		{"/* comment ", '*'},
		{"-- line comment ", 'x'},
		{"\\connect ", 'x'},
		{"", '\n'},
		{"", ' '},
	} {
		r := &endless{head: c.head, fill: c.fill}
		l := NewLexer(r, Options{MaxValue: limit})
		var err error
		for i := 0; i < 4 && err == nil; i++ {
			_, err = l.Next()
		}
		var te *TooLargeError
		if !errors.As(err, &te) || te.Limit != limit {
			t.Errorf("%q: err = %v, want a TooLargeError", c.head, err)
		}
		if r.read > 4*limit {
			t.Errorf("%q: read %d bytes before stopping, limit %d", c.head, r.read, limit)
		}
	}
}
