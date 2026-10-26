// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package core holds the types every part of Roadie shares: the schema
// and row stream between dump readers, the engine and writers, endpoints
// and sites, rules, findings and their IDs, the result and JSON envelope,
// errors with their exit codes, the masks every printed value goes
// through and the reserved ranges fakes and detectors agree on.
//
// core imports only the standard library and two kit packages (cli for
// Severity, secrets for Mask), so every lane can depend on it without
// pulling in another lane. It is frozen once the skeleton lands: a change
// here is a small pull request to master that every lane rebases on.
package core
