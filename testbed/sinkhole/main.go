// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command sinkhole is the testbed's stand-in for the internet. On the
// sealed network it is every container's DNS server: it answers every
// name with its own address (the payment and messaging API hosts with
// the stubs' address) and accepts TCP on every port (iptables in its
// container redirects them all to -redirect), recording the client, the
// port, the TLS server name, the HTTP Host and the first 256 bytes (with
// the values of credential headers such as Authorization and Cookie
// masked) to -log, then closing without answering anything useful. Every connection
// it records during a scenario is a side effect Roadie did not stop.
//
// It is built on the host (make -C testbed binaries) and runs in the
// testbed's runner image; it never runs anywhere else.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// smtpPorts get a greeting first: an SMTP client sends nothing until the
// server speaks.
var smtpPorts = map[int]bool{25: true, 465: true, 587: true, 2525: true}

// fallbackPorts are listened on directly when iptables is not there to
// redirect every port: the usual mail, web, database and queue ports.
var fallbackPorts = []int{21, 22, 25, 80, 110, 143, 443, 465, 587, 993, 995, 1025, 2525, 3306, 5432, 5672, 6379, 8080, 8443, 9200, 11211, 27017}

const firstBytes = 256

type sinkhole struct {
	rec     *recorder
	res     resolver
	timeout time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sinkhole:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("sinkhole", flag.ContinueOnError)
	self := fl.String("self", "", "the address every name resolves to (this container's)")
	stubs := fl.String("stubs", "", "the stubs container's address")
	stubHosts := fl.String("stub-hosts", "api.stripe.com,api.paypal.com,api-m.paypal.com,api.twilio.com,hooks.zapier.com,*.api.mailchimp.com,*.mailchimp.com", "names that resolve to -stubs")
	owner := fl.String("owner", "", "uid:gid to own the log, so the harness on the host reads it (the container runs as root)")
	logPath := fl.String("log", "/runtime/sinkhole.jsonl", "where the records go")
	dnsAddr := fl.String("dns", ":53", "the DNS listen address (UDP and TCP)")
	redirect := fl.Int("redirect", 9999, "the port iptables redirects every other TCP port to; 0 listens on the fallback ports instead")
	if err := fl.Parse(args); err != nil {
		return err
	}
	selfIP, err := netip.ParseAddr(*self)
	if err != nil {
		return fmt.Errorf("-self: %w", err)
	}
	stubIP, err := netip.ParseAddr(*stubs)
	if err != nil {
		return fmt.Errorf("-stubs: %w", err)
	}
	f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- the testbed's own log
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := chownLog(*logPath, *owner); err != nil {
		return err
	}
	s := &sinkhole{rec: &recorder{w: f, now: time.Now}, res: resolver{self: selfIP, stubs: stubIP, stubHosts: strings.Split(*stubHosts, ",")}, timeout: 2 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	udp, err := net.ListenPacket("udp", *dnsAddr)
	if err != nil {
		return err
	}
	go s.serveDNSUDP(udp)
	dnsTCP, err := net.Listen("tcp", *dnsAddr)
	if err != nil {
		return err
	}
	go s.serveDNSTCP(dnsTCP)
	ports := fallbackPorts
	if *redirect != 0 {
		ports = []int{*redirect}
	}
	for _, p := range ports {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			return err
		}
		go s.serveTCP(ln, p, *redirect)
	}
	fmt.Fprintf(os.Stderr, "sinkhole: answering every name with %s (stub hosts with %s), recording to %s\n", selfIP, stubIP, *logPath)
	<-ctx.Done()
	return nil
}

func clientIP(a net.Addr) string {
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().String()
	}
	return a.String()
}

func (s *sinkhole) reply(msg []byte, from net.Addr) []byte {
	q, err := parseQuery(msg)
	if err != nil {
		return nil
	}
	s.rec.add(Record{Kind: "dns", From: clientIP(from), Name: q.name, Type: q.qtype})
	return answer(q, s.res.addr(q.name))
}

func (s *sinkhole) serveDNSUDP(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if out := s.reply(buf[:n], from); out != nil {
			_, _ = pc.WriteTo(out, from)
		}
	}
}

func (s *sinkhole) serveDNSTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(s.timeout))
			var l [2]byte
			if _, err := io.ReadFull(c, l[:]); err != nil {
				return
			}
			msg := make([]byte, int(l[0])<<8|int(l[1]))
			if _, err := io.ReadFull(c, msg); err != nil {
				return
			}
			if out := s.reply(msg, c.RemoteAddr()); out != nil {
				var l [2]byte
				binary.BigEndian.PutUint16(l[:], uint16(len(out))) // #nosec G115 -- a DNS answer is far below 64 KiB
				_, _ = c.Write(append(l[:], out...))
			}
		}()
	}
}

func (s *sinkhole) serveTCP(ln net.Listener, port, redirect int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.handle(c, port, redirect)
	}
}

// handle records one connection. The port is the one the client dialed:
// from SO_ORIGINAL_DST behind the redirect, or the listener's own.
func (s *sinkhole) handle(c net.Conn, port, redirect int) {
	defer func() { _ = c.Close() }()
	if redirect != 0 {
		if p := originalPort(c); p != 0 {
			port = p
		}
	}
	_ = c.SetDeadline(time.Now().Add(s.timeout))
	if smtpPorts[port] {
		_, _ = io.WriteString(c, "220 sinkhole ESMTP testbed\r\n")
	}
	buf := make([]byte, firstBytes)
	// Up to 256 bytes or what came before the deadline.
	n, _ := io.ReadAtLeast(c, buf, firstBytes)
	rec := Record{Kind: "tcp", From: clientIP(c.RemoteAddr()), Port: port}
	if n > 0 {
		rec.First = buf[:n]
		rec.SNI = sniFrom(buf[:n])
		rec.Host = hostFrom(buf[:n])
	}
	s.rec.add(rec)
}
