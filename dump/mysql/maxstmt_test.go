// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"errors"
	"io"
	"strings"
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

// A statement passed through whole stops at --max-value while it is read,
// not after the whole of it is held in memory.
func TestStatementBoundedWhileRead(t *testing.T) {
	const limit = 1 << 20
	for _, c := range []struct {
		head string
		fill byte
	}{
		{"CREATE TABLE t (a int", ' '},
		{"CREATE TABLE t (a varchar(10) DEFAULT '", 'x'},
		{"CREATE TABLE t (a varchar(10) DEFAULT '", '\\'},
		{"SET @a = `", 'x'},
		{"/*!40101 SET NAMES utf8mb4 ", 'x'},
		{"/* plain comment ", 'x'},
		{"-- line comment ", 'x'},
		{"\\! ", 'x'},
		{"INSERT INTO t SELECT ", 'x'},
		{"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = '", 'x'},
		{"INSERT INTO t (", ' '},
		{"INSERT INTO t VALUES ", ' '},
		{"INSERT INTO t VALUES (1), ", ' '},
		{"INSERT INTO t VALUES (1,", ' '},
		{"INSERT INTO t VALUES (1, CONCAT(", ' '},
		{"INSERT INTO t VALUES (1)", ' '},
		{"INSERT INTO t VALUES (1, 0x", 'a'},
		{"INSERT INTO t VALUES (1, X'", 'a'},
		{"INSERT INTO t VALUES (1, 1", '1'},
		{"INSERT INTO t VALUES (1, 'a' ", ' '},
		{"INSERT INTO t VALUES (1, 'a' /*", ' '},
		{"INSERT INTO t VALUES (1, _utf8mb4", ' '},
		{"INSERT INTO t VALUES (1, _binary 0x", 'f'},
		{"", '\n'},
	} {
		r := &endless{head: c.head, fill: c.fill}
		l := NewLexer(r, Options{MaxValue: limit})
		var err error
		for i := 0; i < 4 && err == nil; i++ {
			var st *Stmt
			st, err = l.Next()
			if err == nil && st.Rows {
				for err == nil {
					var ok bool
					if _, ok, err = l.Row(&Arena{}); !ok {
						break
					}
				}
				if err == nil {
					_, err = l.Tail()
				}
			}
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

// A value at the limit, with the spaces and quotes around it, still reads.
func TestValueAtLimitReads(t *testing.T) {
	const limit = 1 << 20
	v := strings.Repeat("v", limit)
	for _, in := range []string{
		"INSERT INTO t VALUES (1,   _utf8mb4'" + v + "'  ), (2, '" + v + "');\n",
		"CREATE TABLE t (a int);\n",
	} {
		if _, _, err := lexAll(t, strings.NewReader(in), Options{MaxValue: limit}); err != nil {
			t.Errorf("%.40q: %v", in, err)
		}
	}
}

var _ io.Reader = (*endless)(nil)
