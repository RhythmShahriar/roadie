// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
)

// A read waiting on a pipe nothing writes to returns once the context
// ends; data that arrives is passed on unchanged.
func TestCtxReader(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	r := newCtxReader(ctx, pr)
	go func() { _, _ = pw.Write([]byte("hello")) }()
	b := make([]byte, 16)
	if n, err := r.Read(b); err != nil || string(b[:n]) != "hello" {
		t.Fatalf("read %q %v", b[:n], err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Read(b); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read didn't return on cancel")
	}
	if _, err := r.Read(b); !errors.Is(err, context.Canceled) {
		t.Errorf("after cancel: %v", err)
	}
}

// A signal while copy waits on stdin stops the run at once as
// interrupted, not as a truncated dump once the writer gives up.
func TestStdinCancelIsInterrupted(t *testing.T) {
	f := useRunFakes(t)
	old1, old2 := stdin, openDump
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	stdin, openDump = pr, dump.Open
	t.Cleanup(func() { stdin, openDump = old1, old2 })
	go func() {
		_, _ = pw.Write([]byte("-- MySQL dump 10.13\nCREATE TABLE `t` (`a` int);\nINSERT INTO `t` VALUES (1);\n"))
	}()
	j := f.job(Copy)
	j.From = core.Endpoint{Kind: core.Stdio, Raw: "-", Display: "-"}
	j.To = core.Endpoint{Kind: core.DumpFile, Raw: "o.sql", Display: "o.sql", Path: "o.sql"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := Run(ctx, j)
	if !errors.Is(err, context.Canceled) || isCode(err, core.ErrDumpTruncated) {
		t.Errorf("err %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
}
