// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"strconv"
	"strings"
)

// Location says where a finding or a key is: a file and line, or a table,
// column, row key and path inside a nested value.
type Location struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Table  string `json:"table,omitempty"`
	Column string `json:"column,omitempty"`
	// Row holds the primary key or option_name/meta_key, as MaskRow
	// builds it, e.g. "option_name=woocommerce_stripe_settings"; never a data value.
	Row string `json:"row,omitempty"`
	// Path is the leaf inside a nested value: "serialized:secret_key",
	// "json:billing.email", "serialized:a.json:b".
	Path string `json:"path,omitempty"`
}

// String renders the location for the terminal:
// "wp_options woocommerce_stripe_settings → serialized:secret_key",
// "wp_users.user_email" or "wp-config.php line 41". A row key shows only
// its values, which MaskRow already made safe to print.
func (l Location) String() string {
	var b strings.Builder
	switch {
	case l.File != "":
		b.WriteString(l.File)
		if l.Line > 0 {
			b.WriteString(" line ")
			b.WriteString(strconv.Itoa(l.Line))
		}
	case l.Table != "":
		b.WriteString(l.Table)
		if l.Row != "" {
			b.WriteByte(' ')
			b.WriteString(rowValues(l.Row))
		} else if l.Column != "" {
			b.WriteByte('.')
			b.WriteString(l.Column)
		}
	}
	if l.Path != "" {
		if b.Len() > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(l.Path)
	}
	return b.String()
}

// rowValues turns "option_name=x,ID=4" into "x 4".
func rowValues(row string) string {
	parts := strings.Split(row, ",")
	for i, p := range parts {
		if _, v, ok := strings.Cut(p, "="); ok {
			parts[i] = v
		}
	}
	return strings.Join(parts, " ")
}
