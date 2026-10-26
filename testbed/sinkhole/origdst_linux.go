// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"
)

// soOriginalDst is SO_ORIGINAL_DST from linux/netfilter_ipv4.h.
const soOriginalDst = 80

// originalPort returns the port a connection redirected by iptables
// (REDIRECT) was really dialed on, or 0 when it wasn't redirected.
func originalPort(c net.Conn) int {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return 0
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return 0
	}
	port := 0
	_ = raw.Control(func(fd uintptr) {
		var addr syscall.RawSockaddrInet4
		size := uint32(unsafe.Sizeof(addr))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.IPPROTO_IP, soOriginalDst,
			uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0) // #nosec G103 -- getsockopt needs the raw pointers
		if errno == 0 {
			p := (*[2]byte)(unsafe.Pointer(&addr.Port)) // #nosec G103 -- network byte order port
			port = int(binary.BigEndian.Uint16(p[:]))
		}
	})
	return port
}
