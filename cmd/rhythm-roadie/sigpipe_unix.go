// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// catchSIGPIPE asks for SIGPIPE, so Go turns a write to a closed stdout
// into an EPIPE error the run handles; nothing reads the channel.
func catchSIGPIPE() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
}
