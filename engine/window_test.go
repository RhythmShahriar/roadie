// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// The window lets batches in while they fit, a batch larger than the
// window in when nothing else is in flight, waits for give otherwise and
// stops waiting when the run is cancelled.
func TestWindow(t *testing.T) {
	ctx := context.Background()
	w := newWindow(100)
	if err := w.take(ctx, 60); err != nil {
		t.Fatal(err)
	}
	if err := w.take(ctx, 40); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() { got <- w.take(ctx, 30) }()
	select {
	case err := <-got:
		t.Fatalf("took past the window: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	w.give(60)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	w.give(40)
	w.give(30)
	if err := w.take(ctx, 500); err != nil {
		t.Fatalf("a batch larger than the window with nothing in flight: %v", err)
	}
	cctx, cancel := context.WithCancelCause(ctx)
	stop := errors.New("stop")
	go func() { got <- w.take(cctx, 1) }()
	cancel(stop)
	if err := <-got; !errors.Is(err, stop) {
		t.Errorf("cancelled take: %v", err)
	}
}

// batchCost counts the bytes of a batch and its rows' and values' own
// size.
func TestBatchCost(t *testing.T) {
	b := &core.Batch{Head: []byte("INSERT "), Tail: []byte(";\n"), Rows: []core.Row{
		{Src: []byte("(1,'a')"), Lead: []byte(","), Values: []core.Value{{Raw: []byte("1")}, {Raw: []byte("a")}}},
	}}
	if got, want := batchCost(b), int64(7+2+rowOverhead+7+1+2*valueOverhead+2); got != want {
		t.Errorf("cost %d, want %d", got, want)
	}
	if got := batchCost(&core.Batch{Pass: &core.Statement{Text: []byte("DROP TABLE x;")}}); got != 13 {
		t.Errorf("statement cost %d", got)
	}
}
