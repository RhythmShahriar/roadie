// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command stubs answers for the third-party APIs a shop calls (Stripe,
// PayPal, Twilio, Zapier hooks, Mailchimp) on the sealed network, where
// the sinkhole's DNS sends those names. It records every request with the
// key it carried (masked) and whether the key is live: a seeded live key
// (runtime/canaries.json) or a live-format one. A test key is allowed; a
// live key reaching a stub means a copy still holds it and used it.
//
//	stubs -init -dir testbed/runtime/stubs     make the CA and the host certificate
//	stubs -dir /runtime/stubs -canaries /runtime/canaries.json -log /runtime/stubs.jsonl
//
// The CA exists only for the testbed: make up installs it in the site
// containers so their TLS clients reach the stubs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "stubs:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("stubs", flag.ContinueOnError)
	dir := fl.String("dir", "/runtime/stubs", "where the CA and the host certificate are")
	initCA := fl.Bool("init", false, "make the CA and the host certificate in -dir and exit")
	canaries := fl.String("canaries", "/runtime/canaries.json", "the seeded keys, to tell live from test")
	owner := fl.String("owner", "", "uid:gid to own the log, so the harness on the host reads it (the container runs as root)")
	logPath := fl.String("log", "/runtime/stubs.jsonl", "where the records go")
	httpAddr := fl.String("http", ":80", "plain HTTP listen address")
	httpsAddr := fl.String("https", ":443", "HTTPS listen address")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *initCA {
		return makeCerts(*dir, stubHosts, time.Now())
	}
	keys, err := loadKeys(*canaries)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- the testbed's own log
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := chownLog(*logPath, *owner); err != nil {
		return err
	}
	h := &handler{keys: keys, rec: &recorder{w: f, now: time.Now}}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	plain := &http.Server{Addr: *httpAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	tlsSrv := &http.Server{Addr: *httpsAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- plain.ListenAndServe() }()
	go func() {
		errs <- tlsSrv.ListenAndServeTLS(filepath.Join(*dir, "host.pem"), filepath.Join(*dir, "host.key"))
	}()
	fmt.Fprintf(os.Stderr, "stubs: answering for %v\n", stubHosts)
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = plain.Shutdown(sctx)
	_ = tlsSrv.Shutdown(sctx)
	return nil
}
