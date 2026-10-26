// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package demo

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// WPConfig returns the demo shop's wp-config.php. It defines no
// WP_ENVIRONMENT_TYPE, as most production sites don't, so the demo shows
// Roadie adding one. The salts come from the seed: the same Options give
// the same file. Leaky does not change it.
func WPConfig(o Options) []byte {
	// #nosec G404 G115 -- seeded fake salts for a fixture, never used to protect anything
	r := rand.New(rand.NewPCG(uint64(o.Seed), 0x7770636667))
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#%()*+,-.:;<=>?@[]^_{|}~"
	salt := func() string {
		b := make([]byte, 64)
		for i := range b {
			b[i] = chars[r.IntN(len(chars))]
		}
		return string(b)
	}
	var b strings.Builder
	b.WriteString("<?php\n")
	b.WriteString("/**\n * The base configuration for WordPress (Bean & Leaf Coffee).\n */\n\n")
	b.WriteString("// ** Database settings ** //\n")
	b.WriteString("define( 'DB_NAME', 'shop' );\n")
	b.WriteString("define( 'DB_USER', 'shop' );\n")
	fmt.Fprintf(&b, "define( 'DB_PASSWORD', '%s' );\n", salt()[:20])
	b.WriteString("define( 'DB_HOST', 'localhost' );\n")
	b.WriteString("define( 'DB_CHARSET', 'utf8mb4' );\n")
	b.WriteString("define( 'DB_COLLATE', '' );\n\n")
	for _, k := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		fmt.Fprintf(&b, "define( '%s', %s'%s' );\n", k, strings.Repeat(" ", 17-len(k)), salt())
	}
	b.WriteString("\n$table_prefix = 'wp_';\n\n")
	b.WriteString("define( 'WP_DEBUG', false );\n\n")
	b.WriteString("/* That's all, stop editing! Happy publishing. */\n\n")
	b.WriteString("if ( ! defined( 'ABSPATH' ) ) {\n\tdefine( 'ABSPATH', __DIR__ . '/' );\n}\n\n")
	b.WriteString("require_once ABSPATH . 'wp-settings.php';\n")
	return []byte(b.String())
}
