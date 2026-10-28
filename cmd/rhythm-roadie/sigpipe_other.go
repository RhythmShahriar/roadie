// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix

package main

// catchSIGPIPE does nothing where there is no SIGPIPE: a closed pipe is
// already a write error there.
func catchSIGPIPE() {}
