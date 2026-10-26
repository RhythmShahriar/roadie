// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"cmp"
	"slices"
)

// addrLens holds the byte length of each address part pass 1 saw, plus
// one, so 0 is a part it didn't see. Lengths, not values: with them pass
// 2 splits the index back into its parts. One byte a part keeps a big
// shop's index small; a part of addrTooLong bytes or more is marked so
// the fix leaves that index to its own text rule.
type addrLens [11]uint8

// addrTooLong is the stored length (plus one) of a part too long to
// record. WooCommerce's address parts are far shorter.
const addrTooLong = 255

// addrEntry is one order address's lengths: 8 bytes of key and 11 of
// lengths, 24 with padding, where a map entry costs about twice that.
type addrEntry struct {
	k addrKey
	l addrLens
}

// addrIndex holds the part lengths of every order address pass 1 saw.
// Pass 1 appends; the rows of one order come together in a dump, so a
// short list of recent entries catches nearly every update and a
// scattered one becomes a second entry that seal merges. After seal it
// is sorted by key and only read, by any number of workers.
type addrIndex struct {
	es     []addrEntry
	recent [4]int // positions in es of the entries last written, -1 for none
	sealed bool
}

func newAddrIndex() *addrIndex { return &addrIndex{recent: [4]int{-1, -1, -1, -1}} }

// set records the length n of part i of the address k.
func (a *addrIndex) set(k addrKey, i, n int) {
	v := uint8(addrTooLong)
	if n < addrTooLong-1 {
		v = uint8(n + 1) // #nosec G115 -- n+1 < 255
	}
	for _, r := range a.recent {
		if r >= 0 && a.es[r].k == k {
			a.es[r].l[i] = v
			return
		}
	}
	a.es = append(a.es, addrEntry{k: k})
	a.es[len(a.es)-1].l[i] = v
	copy(a.recent[1:], a.recent[:3])
	a.recent[0] = len(a.es) - 1
	a.sealed = false
}

// seal sorts the entries and merges those of one key: a part a later
// entry recorded wins, as a later row of the dump would.
func (a *addrIndex) seal() {
	if a == nil || a.sealed {
		return
	}
	slices.SortStableFunc(a.es, func(x, y addrEntry) int { return cmp.Compare(x.k, y.k) })
	out := a.es[:0]
	for _, e := range a.es {
		if n := len(out); n > 0 && out[n-1].k == e.k {
			for i, v := range e.l {
				if v != 0 {
					out[n-1].l[i] = v
				}
			}
			continue
		}
		out = append(out, e)
	}
	a.es = slices.Clip(out)
	a.recent = [4]int{-1, -1, -1, -1}
	a.sealed = true
}

// get returns the lengths of the address k after seal: each part's byte
// length, 0 for one pass 1 didn't see. ok is false when pass 1 saw no
// part of it or one was too long to record.
func (a *addrIndex) get(k addrKey) (lens [11]int, ok bool) {
	if a == nil || !a.sealed {
		return lens, false
	}
	i, found := slices.BinarySearchFunc(a.es, k, func(e addrEntry, k addrKey) int { return cmp.Compare(e.k, k) })
	if !found {
		return lens, false
	}
	for j, v := range a.es[i].l {
		if v == addrTooLong {
			return lens, false
		}
		if v > 0 {
			lens[j] = int(v) - 1
		}
	}
	return lens, true
}

// len is the number of addresses held.
func (a *addrIndex) len() int {
	if a == nil {
		return 0
	}
	return len(a.es)
}
