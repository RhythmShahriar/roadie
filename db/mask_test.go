// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"strings"
	"testing"
)

// Driver messages keep their words and the schema names at the end but
// never a value, even one with quotes in it or one the server cut short.
func TestMask(t *testing.T) {
	for _, c := range []struct {
		num      uint16 // MySQL error number; 0 for any other message
		in, want string
	}{
		{1062, "Duplicate entry 'jane@example.invalid' for key 'wp_users.user_email'", "Duplicate entry '•••' for key 'wp_users.user_email'"},
		{1062, "Duplicate entry 'o'brien@example.test' for key 'wp_users.user_email'", "Duplicate entry '•••' for key 'wp_users.user_email'"},
		{1062, "Duplicate entry 'x' for key 'evil' for key 'PRIMARY'", "Duplicate entry '•••' for key 'PRIMARY'"},
		{1062, "Duplicate entry '' for key 'PRIMARY'", "Duplicate entry '•••' for key 'PRIMARY'"},
		{1366, "Incorrect string value: '\\xF0\\x9F' for column `shop`.`t`.`c` at row 1", "Incorrect string value: '•••' for column `shop`.`t`.`c` at row 1"},
		{1292, "Incorrect datetime value: '2026-13-01' for column 'd' at row 4", "Incorrect datetime value: '•••' for column 'd' at row 4"},
		{1292, "Truncated incorrect DOUBLE value: 'it's'", "Truncated incorrect DOUBLE value: '•••'"},
		{1048, "Column 'user_email' cannot be null", "Column '•••' cannot be null"},
		{1406, "Data too long for column 'c' at row 2", "Data too long for column '•••' at row 2"},
		{0, `invalid input syntax for type integer: "abc"`, "invalid input syntax for type integer: '•••'"},
		{0, "no quotes at all", "no quotes at all"},
		// A value that spells the end of another message shape.
		{1292, "Truncated incorrect DOUBLE value: 'x' for key 'leak'", "Truncated incorrect DOUBLE value: '•••'"},
		{1292, "Truncated incorrect DOUBLE value: 'x' for column 'leak'", "Truncated incorrect DOUBLE value: '•••'"},
		{0, "value 'x' for key 'leak'", "value '•••'"},
		// Unbalanced: the server cut the message inside the value.
		{1062, "Duplicate entry 'o'brien@example.te", "Duplicate entry '•••'"},
		{1062, "Duplicate entry 'o'brien smith", "Duplicate entry '•••'"},
		{1062, "Duplicate entry 'x' for key 'secret-value-cut-sh", "Duplicate entry '•••'"},
		{1366, "Incorrect string value: 'a' for column 'b' at row 1 tail' x'", "Incorrect string value: '•••'"},
	} {
		if got := mask(c.in, mysqlTails[c.num]); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// FuzzMask: a value quoted in a MySQL or PostgreSQL message shape never
// survives masking, whatever bytes it holds.
func FuzzMask(f *testing.F) {
	f.Add("o'brien@example.test", uint8(0))
	f.Add("x' for key 'k", uint8(3))
	f.Add("x' for column 'c' at row 1", uint8(1))
	f.Add(`a"b`, uint8(2))
	f.Add("' cannot be null", uint8(4))
	shapes := []struct {
		num  uint16
		text string
	}{
		{1062, "Duplicate entry '%s' for key 'wp_users.user_email'"},
		{1366, "Incorrect string value: '%s' for column `shop`.`t`.`c` at row 1"},
		{0, `invalid input syntax for type integer: "%s"`},
		{1292, "Truncated incorrect DOUBLE value: '%s'"},
		{1062, "Duplicate entry '%s"},
		{1048, "Column '%s' cannot be null"},
	}
	f.Fuzz(func(t *testing.T, v string, shape uint8) {
		v = "SECRET" + v + "TERCES"
		sh := shapes[int(shape)%len(shapes)]
		out := mask(strings.Replace(sh.text, "%s", v, 1), mysqlTails[sh.num])
		if strings.Contains(out, "SECRET") || strings.Contains(out, "TERCES") {
			t.Fatalf("value left in %q", out)
		}
	})
}
