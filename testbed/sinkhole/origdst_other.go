// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux

package main

import "net"

// originalPort needs Linux netfilter; elsewhere a connection is taken
// to have come to the port it arrived on.
func originalPort(net.Conn) int { return 0 }
