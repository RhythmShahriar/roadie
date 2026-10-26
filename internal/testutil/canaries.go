// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package testutil

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// CanaryCount is how many canaries Canaries makes per kind.
const CanaryCount = 5

// Canaries returns, for every fake kind, CanaryCount values that look real
// (non-reserved domains, phone numbers outside the fictional ranges,
// public IPs) and are unique enough that finding one anywhere in output
// means it leaked. They are made at test time from seed and never
// committed, so the repository holds no real-looking personal data.
func Canaries(seed int64) map[core.FakeKind][]string {
	r := rand.New(rand.NewPCG(uint64(seed), 0x726f61646965)) // #nosec G404 G115 -- test data, not secrets; any seed bits will do
	word := func(n int) string {
		const cons, vows = "bcdfghjklmnprstvz", "aeiou"
		var b strings.Builder
		for i := range n {
			if i%2 == 0 {
				b.WriteByte(cons[r.IntN(len(cons))])
			} else {
				b.WriteByte(vows[r.IntN(len(vows))])
			}
		}
		return b.String()
	}
	title := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
	digits := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteByte("0123456789"[r.IntN(10)])
		}
		return b.String()
	}
	out := map[core.FakeKind][]string{}
	for range CanaryCount {
		first, last, dom := title(word(7)), title(word(8)), word(9)
		user := strings.ToLower(first) + "." + strings.ToLower(last)
		out[core.KEmail] = append(out[core.KEmail], user+"@"+dom+".com")
		out[core.KUsername] = append(out[core.KUsername], user+digits(3))
		out[core.KNicename] = append(out[core.KNicename], strings.ToLower(first)+"-"+strings.ToLower(last))
		out[core.KFirstName] = append(out[core.KFirstName], first)
		out[core.KLastName] = append(out[core.KLastName], last)
		out[core.KFullName] = append(out[core.KFullName], first+" "+last)
		// +44 7911 is a real UK mobile range, outside Ofcom's 07700 900 drama block.
		out[core.KPhone] = append(out[core.KPhone], "+44 7911 "+digits(6))
		out[core.KStreet] = append(out[core.KStreet], fmt.Sprintf("%d %s Road", 10+r.IntN(180), title(word(9))))
		out[core.KAddress] = append(out[core.KAddress], fmt.Sprintf("Flat %d, %s House", 1+r.IntN(40), title(word(9))))
		out[core.KCity] = append(out[core.KCity], title(word(10)))
		out[core.KPostcode] = append(out[core.KPostcode], fmt.Sprintf("Z%d %d%s", 1+r.IntN(9), r.IntN(10), strings.ToUpper(word(2))))
		out[core.KCompany] = append(out[core.KCompany], title(word(9))+" Holdings")
		out[core.KIP] = append(out[core.KIP], fmt.Sprintf("81.%d.%d.%d", 1+r.IntN(250), 1+r.IntN(250), 1+r.IntN(250)))
		out[core.KUserAgent] = append(out[core.KUserAgent], "Mozilla/5.0 (X11; Linux x86_64) Canary/"+digits(8))
		out[core.KDOB] = append(out[core.KDOB], fmt.Sprintf("19%02d-%02d-%02d", 50+r.IntN(49), 1+r.IntN(12), 1+r.IntN(28)))
		out[core.KURL] = append(out[core.KURL], "https://"+word(10)+".com/"+word(6))
		out[core.KText] = append(out[core.KText], "Please call "+first+" "+last+" about order "+digits(6))
	}
	return out
}
