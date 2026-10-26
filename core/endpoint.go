// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

// EndpointKind is the kind of a --from or --to value.
type EndpointKind string

// The endpoint kinds of PRD 6.2.
const (
	DumpFile  EndpointKind = "dump-file"
	Stdio     EndpointKind = "stdio"
	LocalSite EndpointKind = "local-site"
	SSHSite   EndpointKind = "ssh-site"
	WPAlias   EndpointKind = "wp-alias"
	DSN       EndpointKind = "dsn"
)

// Endpoint is a parsed source or target. Raw and Password may hold a
// secret, so neither is ever printed or written to JSON; String gives Display.
type Endpoint struct {
	Kind        EndpointKind
	Raw         string `json:"-"` // as typed; never printed (may hold a password)
	Display     string // password replaced by "•••"
	Path        string // file or site path (local or remote)
	User        string
	Host        string
	Port        int
	Alias       string // "@prod"
	Driver      Dialect
	DSN         string // without password
	Password    string `json:"-"` // from the DSN or ROADIE_FROM_/TO_PASSWORD
	Compression string // "", "gzip", "zstd", "bzip2" (bzip2 read only)
}

// String returns Display, so printing an Endpoint with %v never shows Raw
// or Password.
func (e Endpoint) String() string { return e.Display }

// GoString returns Display too, for %#v.
func (e Endpoint) GoString() string { return "core.Endpoint(" + e.Display + ")" }
