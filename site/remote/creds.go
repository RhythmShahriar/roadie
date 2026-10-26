// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Creds are database credentials for a remote mysqldump, mysql, pg_dump
// or psql. They travel on the command's stdin, never in an argv on
// either host (security N3).
type Creds struct {
	User, Password, Host string
	Port                 int
}

// ErrCredential is wrapped when a credential can't be sent safely.
var ErrCredential = errors.New("credential can't be sent on stdin")

// MySQLDefaults is the [client] option file mysqldump reads from
// /dev/stdin and the mysql wrapper reads up to its end line. A value is
// quoted with whichever quote it doesn't hold.
func MySQLDefaults(c Creds) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("[client]\n")
	for _, kv := range [][2]string{{"user", c.User}, {"password", c.Password}, {"host", c.Host}} {
		if kv[1] == "" {
			continue
		}
		v, err := optionValue(kv[1])
		if err != nil {
			return nil, fmt.Errorf("%w: the MySQL %s %w", ErrCredential, kv[0], err)
		}
		b.WriteString(kv[0] + "=" + v + "\n")
	}
	if c.Port != 0 {
		b.WriteString("port=" + strconv.Itoa(c.Port) + "\n")
	}
	b.WriteString(credsEnd + "\n")
	return b.Bytes(), nil
}

// optionValue quotes v for a MySQL option file, where a backslash starts
// an escape inside quotes.
func optionValue(v string) (string, error) {
	if strings.ContainsAny(v, "\x00\n\r") {
		return "", errors.New("holds a NUL or a line break")
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	switch {
	case !strings.Contains(v, `"`):
		return `"` + v + `"`, nil
	case !strings.Contains(v, `'`):
		return `'` + v + `'`, nil
	}
	return "", errors.New("holds both quote characters")
}

// PGPassword is the first stdin line the pg_dump and psql wrappers read
// into PGPASSWORD.
func PGPassword(c Creds) ([]byte, error) {
	if strings.ContainsAny(c.Password, "\x00\n\r") {
		return nil, fmt.Errorf("%w: the PostgreSQL password holds a NUL or a line break", ErrCredential)
	}
	return []byte(c.Password + "\n"), nil
}

// WithCreds puts creds before body on stdin; body may be nil.
func WithCreds(creds []byte, body io.Reader) io.Reader {
	if body == nil {
		return bytes.NewReader(creds)
	}
	return io.MultiReader(bytes.NewReader(creds), body)
}
