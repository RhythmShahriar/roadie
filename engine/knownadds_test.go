// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// Known filled through parallelAdds holds the same values as Known
// filled in one goroutine: Seal, Contains and Len wait for every add, the
// caller's buffer is copied; an Add after the wait goes straight in.
func TestParallelAdds(t *testing.T) {
	var key pseudo.Key
	key[0] = 7
	direct, par := pseudo.NewKnown(key), newParallelAdds(pseudo.NewKnown(key), 4)
	if _, ok := par.(*parallelAdds); !ok {
		t.Fatalf("%T", par)
	}
	buf := make([]byte, 0, 64)
	for i := range 5000 {
		kind := core.KEmail
		if i%3 == 0 {
			kind = core.KFullName
		}
		buf = fmt.Appendf(buf[:0], "person%05d-name Smith%d@shop.example.com", i, i%97)
		direct.Add(kind, buf)
		par.Add(kind, buf) // buf is reused at once: Add must copy it
	}
	if got, want := par.Len(), direct.Len(); got != want || want == 0 {
		t.Fatalf("len %d, want %d", got, want)
	}
	late := []byte("late-value@shop.example.com")
	direct.Add(core.KEmail, late)
	par.Add(core.KEmail, late)
	direct.Seal()
	par.Seal()
	if par.Len() != direct.Len() {
		t.Errorf("sealed len %d, want %d", par.Len(), direct.Len())
	}
	for _, tok := range []string{"person00042-name smith42@shop.example.com", "late-value@shop.example.com", "nobody@shop.example.com"} {
		k1, e1, ok1 := direct.Contains([]byte(tok))
		k2, e2, ok2 := par.Contains([]byte(tok))
		if k1 != k2 || e1 != e2 || ok1 != ok2 {
			t.Errorf("%s: %v %v %v, want %v %v %v", tok, k2, e2, ok2, k1, e1, ok1)
		}
	}
	// One worker or none: Known itself, no goroutines.
	k := pseudo.NewKnown(key)
	if newParallelAdds(k, 1) != k {
		t.Error("one worker wrapped Known")
	}
}
