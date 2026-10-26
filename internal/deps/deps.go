// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package deps imports every third-party module Roadie's lanes will use
// that no skeleton code imports yet, so go mod tidy keeps them in go.mod
// and no lane edits go.mod. Integration deletes it.
package deps

import (
	_ "github.com/RhythmShahriar/echo/client"               // detect: verify --echo
	_ "github.com/RhythmShahriar/echo/connectors/wordpress" // site: the Echo mu-plugin
	_ "github.com/charmbracelet/huh"                        // cli: picker and prompts
	_ "github.com/charmbracelet/lipgloss"                   // cli: terminal views
	_ "github.com/charmbracelet/x/term"                     // cli: terminal detection
	_ "github.com/go-sql-driver/mysql"                      // io: MySQL and MariaDB
	_ "github.com/jackc/pgx/v5/stdlib"                      // io: PostgreSQL
	_ "github.com/klauspost/compress/zstd"                  // io: zstd dumps
	_ "github.com/muesli/termenv"                           // cli: terminal color profile
	_ "golang.org/x/crypto/bcrypt"                          // values: Laravel passwords
	_ "golang.org/x/text/unicode/norm"                      // values: name normalising
	_ "gopkg.in/yaml.v3"                                    // engine: config and profiles
	_ "modernc.org/sqlite"                                  // io: SQLite DSNs
)
