// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package size formats byte counts for the dump messages of PRD 17, such
// as "ends inside table wp_postmeta at 1.7 GB".
package size

import "strconv"

// Human formats n in decimal units with one decimal place: "1.7 GB",
// "64.0 MB", "512 B". Decimal units match what file managers and df -H
// show, which is what a user compares the message with.
func Human(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n) / 1000
	u := 0
	for v >= 999.95 && u < len(units)-1 {
		v /= 1000
		u++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[u]
}

// GB formats n in gigabytes with one decimal place, "1.7 GB", for the
// offsets in dump-truncated messages.
func GB(n int64) string {
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + " GB"
}

// MiB is the binary mebibyte, for the --max-value wording ("256 MiB").
const MiB = 1 << 20

// Binary formats n as a whole number of binary units when it is one,
// "256 MiB" or "1 GiB", and in bytes otherwise; it names the --max-value
// limit in the unit the user passed.
func Binary(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + " GiB"
	case n >= MiB && n%MiB == 0:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// Count formats n with thousands separators, "4,120", as PRD 17 writes
// line numbers.
func Count(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return neg + s
}
