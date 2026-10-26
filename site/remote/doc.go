// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package remote is the only place Roadie starts ssh. It validates hosts,
// builds every remote command from a fixed allowlist (PRD 6.13) with one
// quoting function, passes database credentials on stdin and maps ssh and
// WP-CLI failures to the error codes of PRD 17.
package remote
