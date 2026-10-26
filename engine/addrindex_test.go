// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import "testing"

// The address index merges scattered writes of one address (a later
// part wins), finds every address after seal, says nothing before seal
// or for an address it never saw and refuses one with a part too long
// to record.
func TestAddrIndex(t *testing.T) {
	a := newAddrIndex()
	k1, k2, k3 := newAddrKey("wp_postmeta", []byte("1"), true), newAddrKey("wp_postmeta", []byte("1"), false), newAddrKey("wp_postmeta", []byte("2"), true)
	a.set(k1, 0, 4)
	a.set(k2, 0, 7)
	a.set(k1, 1, 3)
	if _, ok := a.get(k1); ok {
		t.Error("get before seal")
	}
	// Five other addresses push k1 out of the recent list: its next part
	// becomes a second entry.
	for i := range 5 {
		a.set(newAddrKey("wp_postmeta", []byte{byte('a' + i)}, true), 0, i)
	}
	a.set(k1, 2, 0)
	a.set(k1, 0, 5)
	a.set(k3, 0, 300)
	n := a.len()
	a.seal()
	if a.len() != n-1 {
		t.Errorf("len %d after seal, want %d", a.len(), n-1)
	}
	got, ok := a.get(k1)
	if !ok || got[0] != 5 || got[1] != 3 || got[2] != 0 || got[3] != 0 {
		t.Errorf("k1 %v %v", got, ok)
	}
	if got, ok := a.get(k2); !ok || got[0] != 7 {
		t.Errorf("k2 %v %v", got, ok)
	}
	if _, ok := a.get(k3); ok {
		t.Error("a part of 300 bytes was recorded")
	}
	if _, ok := a.get(newAddrKey("wp_postmeta", []byte("9"), true)); ok {
		t.Error("an address never seen was found")
	}
	var nilIdx *addrIndex
	nilIdx.seal()
	if _, ok := nilIdx.get(k1); ok || nilIdx.len() != 0 {
		t.Error("nil index")
	}
	// 253 bytes is the longest part recorded.
	b := newAddrIndex()
	b.set(k1, 0, 253)
	b.set(k2, 0, 254)
	b.seal()
	if got, ok := b.get(k1); !ok || got[0] != 253 {
		t.Errorf("253: %v %v", got, ok)
	}
	if _, ok := b.get(k2); ok {
		t.Error("254 recorded")
	}
}
